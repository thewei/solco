package remotehost

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

/**
 * [INPUT]: 依赖 crypto/rand、crypto/sha256、encoding/base64、net/url、os 标准库与 tls.go 的 writeFileAtomic
 * [OUTPUT]: 对外提供 RelayConfig（Enabled/URL/RegistrationKey + Validate）与 LoadOrCreateRelaySecret（256-bit、URL 安全字母表、0600 原子写）、RelayTokenHash
 * [POS]: 自托管 alma-relay（docs/PROTOCOL.md）的接入数据层：URL 只许 https:// 或 wss:// 的裸基址（有路径/查询即拒绝，隧道自带 /c/<secret>/ 前缀）；relaySecret 与证书同一套「缺失/损坏即重建」语义，重建只意味着手机要重新配对；令牌哈希是 relay 唯一保存的东西
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// relaySecretFile is the Mac's relay connect code, stored next to the TLS key.
const relaySecretFile = "relay-secret"

// RelayConfig is the user's opt-in choice to reach this Mac through a
// self-hosted alma-relay (https://github.com/yetone/alma-relay) when no
// direct network path exists. URL is the bare relay base address: tunnelled
// requests already carry their own /c/<secret>/… path, so a path here could
// only be a mistake. The loopback bridge the client needs is an
// implementation detail of relayclient.go and never a user setting.
type RelayConfig struct {
	Enabled         bool
	URL             string
	RegistrationKey string
}

// Validate reports whether the relay config may dial. Only an enabled relay
// is validated: a half-remembered address must not block the page while the
// relay is switched off.
func (c RelayConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil {
		return fmt.Errorf("relay address %q: %w", c.URL, err)
	}
	switch {
	case u.Scheme != "https" && u.Scheme != "wss":
		return fmt.Errorf("relay address %q must be an https:// or wss:// address", c.URL)
	case u.Host == "":
		return fmt.Errorf("relay address %q needs a host", c.URL)
	case (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("relay address %q must be a bare base address (no path, query or fragment)", c.URL)
	}
	return nil
}

// LoadOrCreateRelaySecret returns the Mac's relay connect code: 256 bits
// encoded in the relay's URL-safe token alphabet ([A-Za-z0-9_-], 43 chars).
// It lives in <dir>/relay-secret with mode 0600 and is regenerated whenever
// it is missing or unreadable, exactly like the TLS certificate — a
// regenerated secret only means phones must pair again.
func LoadOrCreateRelaySecret(dir string) (string, error) {
	path := filepath.Join(dir, relaySecretFile)
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(b)); validRelaySecret(s) {
			return s, nil
		}
	}
	// Missing, unreadable or corrupt: rebuild, like the TLS certificate does.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create remote host dir: %w", err)
	}
	secret, err := newRelaySecret()
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, []byte(secret+"\n"), 0o600); err != nil {
		return "", err
	}
	return secret, nil
}

// newRelaySecret is 32 random bytes through RawURLEncoding: exactly the
// relay's [A-Za-z0-9_-] alphabet, and always ≥16 chars.
func newRelaySecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// validRelaySecret mirrors the relay's token rule ([A-Za-z0-9_-]{16,128}).
func validRelaySecret(s string) bool {
	if len(s) < 16 || len(s) > 128 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// RelayTokenHash is what the relay keeps in memory: the lower-case hex
// SHA-256 of the secret. The secret itself never leaves this Mac (except
// inside the pairing QR, which pairs a phone with this Mac only).
func RelayTokenHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
