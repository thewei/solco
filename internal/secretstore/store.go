package secretstore

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

/**
 * [INPUT]: 应用数据目录里的一个文件路径、可选的旧 Keychain 适配器（Legacy）、本机硬件 UUID（ioreg 的 IOPlatformUUID，取不到则为空）
 * [OUTPUT]: 对外提供 Store（Get/Set/Delete，字符串凭据）、Legacy 接口、Use（启用文件）、New
 * [POS]: Solco 自己的凭据存放处（2026-11-01 用户批准，替代 mygo.Secrets/Keychain）：AES-256-GCM 加密的单个 0600 文件；密钥由「本机硬件 UUID + 文件内随机盐」经 HKDF 派生，拷到别的机器或只拿到备份无法解密，但这不是对本机同用户恶意进程的防线（与 ~/.codex/auth.json 同级）。macOS 钥匙串条目的访问权限绑定代码签名，重装/重编译就会弹「输入登录钥匙串密码」，所以 Solco 自己的条目不再放钥匙串；旧条目在第一次读取时迁入并删除，每个 key 最多碰一次钥匙串（命中或未命中都记账）。别的应用的钥匙串条目（Claude Code、cursor-agent）不在此列，仍由 internal/usage 经用户同意读取
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	version = 1
	hkdfTag = "solco.secretstore.v1"
)

// Legacy is where Solco kept its credentials before this store: the Keychain.
// It is asked at most once per key, to move an old item over.
type Legacy interface {
	Get(key string) (string, bool)
	Delete(key string) error
}

// Store is an encrypted file of string credentials. Until Use gives it a
// path it is memory only and never asks the Legacy store, so headless tests
// touch neither the disk nor the Keychain.
type Store struct {
	legacy Legacy
	hwid   func() string
	hwOnce sync.Once
	hwVal  string

	// legacyMu serialises the (possibly prompting) Keychain look-ups so one
	// key is never asked twice at once; mu guards the data and is never held
	// across a Keychain call.
	legacyMu sync.Mutex

	mu      sync.Mutex
	path    string
	loaded  bool
	salt    []byte
	items   map[string]string
	checked map[string]bool
}

// New returns a memory-only store. legacy may be nil.
func New(legacy Legacy) *Store {
	return &Store{legacy: legacy, hwid: hardwareID, items: map[string]string{}, checked: map[string]bool{}}
}

// Use points the store at its file. Call once at startup, before reads.
func (s *Store) Use(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.path, s.loaded = path, false
}

// Get returns the credential, moving an old Keychain item into the file the
// first time a key is asked for.
func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	s.load()
	value, ok := s.items[key]
	look := !ok && s.path != "" && s.legacy != nil && !s.checked[key]
	s.mu.Unlock()
	if ok || !look {
		return value, ok
	}

	s.legacyMu.Lock()
	defer s.legacyMu.Unlock()
	s.mu.Lock()
	if value, ok := s.items[key]; ok { // written while this look-up waited
		s.mu.Unlock()
		return value, true
	}
	if s.checked[key] {
		s.mu.Unlock()
		return "", false
	}
	s.mu.Unlock()

	old, found := s.legacy.Get(key)
	s.mu.Lock()
	if cur, ok := s.items[key]; ok {
		s.mu.Unlock()
		return cur, true
	}
	s.checked[key] = true
	if found {
		s.items[key] = old
	}
	s.saveLocked()
	s.mu.Unlock()
	if found {
		_ = s.legacy.Delete(key) // best effort: the copy now lives in the file
	}
	return old, found
}

// Set stores the credential and retires any old Keychain item of that key.
func (s *Store) Set(key, value string) error {
	s.mu.Lock()
	s.load()
	s.items[key] = value
	retire := s.markLocked(key)
	err := s.saveLocked()
	s.mu.Unlock()
	if retire {
		s.retire(key)
	}
	return err
}

// Delete removes the credential, here and in the old Keychain.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	s.load()
	delete(s.items, key)
	retire := s.markLocked(key)
	err := s.saveLocked()
	s.mu.Unlock()
	if retire {
		s.retire(key)
	}
	return err
}

// markLocked records that the Keychain has nothing left to say about key and
// reports whether an old item may still need deleting.
func (s *Store) markLocked(key string) bool {
	retire := s.path != "" && s.legacy != nil && !s.checked[key]
	s.checked[key] = true
	return retire
}

func (s *Store) retire(key string) {
	s.legacyMu.Lock()
	defer s.legacyMu.Unlock()
	_ = s.legacy.Delete(key)
}

// ---- file ----

type document struct {
	V       int               `json:"v"`
	Salt    string            `json:"salt"`
	Items   map[string]string `json:"items"`
	Checked []string          `json:"checked,omitempty"`
}

// load reads the file once per Use. A missing, unreadable or foreign file is
// an empty store; an item that does not decrypt (another machine, damage) is
// dropped on its own. Callers hold mu.
func (s *Store) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.items, s.checked, s.salt = map[string]string{}, map[string]bool{}, nil
	if s.path == "" {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var doc document
	if json.Unmarshal(raw, &doc) != nil || doc.V != version {
		return
	}
	salt, err := base64.StdEncoding.DecodeString(doc.Salt)
	if err != nil || len(salt) == 0 {
		return
	}
	s.salt = salt
	aead := s.aead()
	for key, sealed := range doc.Items {
		blob, err := base64.StdEncoding.DecodeString(sealed)
		if err != nil || len(blob) < aead.NonceSize() {
			continue
		}
		plain, err := aead.Open(nil, blob[:aead.NonceSize()], blob[aead.NonceSize():], []byte(key))
		if err != nil {
			continue
		}
		s.items[key] = string(plain)
	}
	for _, key := range doc.Checked {
		s.checked[key] = true
	}
}

// saveLocked writes the whole file through a temporary and a rename, 0600.
// A memory-only store has nothing to write. Callers hold mu.
func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if s.salt == nil {
		s.salt = make([]byte, 16)
		if _, err := rand.Read(s.salt); err != nil {
			return err
		}
	}
	aead := s.aead()
	doc := document{V: version, Salt: base64.StdEncoding.EncodeToString(s.salt), Items: map[string]string{}}
	for key, value := range s.items {
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		doc.Items[key] = base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(value), []byte(key)))
	}
	for key := range s.checked {
		doc.Checked = append(doc.Checked, key)
	}
	slices.Sort(doc.Checked)
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".secrets-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// aead derives the file key from this Mac's hardware UUID and the file's
// salt. Callers hold mu and have a salt.
func (s *Store) aead() cipher.AEAD {
	s.hwOnce.Do(func() {
		if s.hwid != nil {
			s.hwVal = s.hwid()
		}
	})
	key, err := hkdf.Key(sha256.New, []byte(s.hwVal), s.salt, hkdfTag, 32)
	if err != nil {
		panic(err) // 32 bytes from SHA-256 is always within HKDF's limit
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return aead
}

// hardwareID is the Mac's IOPlatformUUID; "" when ioreg cannot say, which
// leaves the salt as the only key material (the file still is not plaintext).
func hardwareID() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	const tag = `"IOPlatformUUID" = "`
	i := bytes.Index(out, []byte(tag))
	if i < 0 {
		return ""
	}
	rest := out[i+len(tag):]
	if j := bytes.IndexByte(rest, '"'); j > 0 {
		return string(rest[:j])
	}
	return ""
}
