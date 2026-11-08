package usage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

/**
 * [INPUT]: Antigravity 自己保存的 Google OAuth 登录（~/.gemini/jetski-standalone-oauth-token 或旧版 antigravity-cli/antigravity-oauth-token，其次 Keychain gemini/antigravity）；调用方给出的“已允许读取该登录”开关
 * [OUTPUT]: NewAntigravity（云端优先、本机 language server 兜底的 Fetcher）、antigravityCloud（oauth2.googleapis.com 刷新 + cloudcode-pa.googleapis.com retrieveUserQuotaSummary/loadCodeAssist）、parseAntigravityQuota 纯函数
 * [POS]: App 未运行也能读额度；只读他人登录（用户 2026-10-30 在知悉 Google 条款风险后选定为默认来源，且须在 Usage 里逐次明确同意）；令牌只在内存、刷新后的令牌不写回、日志只含状态；Google 官方不支持第三方使用此接口，接口或客户端密钥一旦被轮换，仅 Antigravity 一家失效并退回本机 server
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	agyTokenURL     = "https://oauth2.googleapis.com/token"
	agyQuotaURL     = "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"
	agyQuotaURLProd = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"
	agyLoadURL      = "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	agyKeychainSvc  = "gemini"
	agyKeychainAcct = "antigravity"
	agyKeyringB64   = "go-keyring-base64:"
	// Antigravity's installed-app OAuth client authenticates the refresh, but
	// an installed app cannot keep a client secret confidential (RFC 8252) and
	// GitHub push protection forbids embedding one in source, so the client id
	// and secret come from ANTIGRAVITY_OAUTH_CLIENT_ID and
	// ANTIGRAVITY_OAUTH_CLIENT_SECRET. They identify the client app, never the
	// user; the authorization itself comes from the sign-in already on this Mac.
)

type antigravityCloud struct {
	client     *http.Client
	tokenURL   string
	quotaURLs  []string
	loadURL    string
	files      []string
	keychain   func(ctx context.Context) ([]byte, error)
	clientID   string
	clientSec  string
	userAgent  string
	now        func() time.Time
	mu         sync.Mutex
	memAccess  string
	memExpires time.Time
}

func newAntigravityCloud() *antigravityCloud {
	home, _ := os.UserHomeDir()
	c := &antigravityCloud{
		client:    newHTTPClient(),
		tokenURL:  agyTokenURL,
		quotaURLs: []string{agyQuotaURL, agyQuotaURLProd},
		loadURL:   agyLoadURL,
		files: []string{
			filepath.Join(home, ".gemini", "jetski-standalone-oauth-token"),
			filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token"),
		},
		keychain:  agyKeychain,
		userAgent: "antigravity/1.11.3 " + runtime.GOOS + "/" + runtime.GOARCH,
		now:       time.Now,
	}
	if v := strings.TrimSpace(os.Getenv("ANTIGRAVITY_OAUTH_CLIENT_ID")); v != "" {
		c.clientID = v
	}
	if v := strings.TrimSpace(os.Getenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET")); v != "" {
		c.clientSec = v
	}
	return c
}

// agyKeychain reads Antigravity's Keychain item (gemini / antigravity).
func agyKeychain(ctx context.Context) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-a", agyKeychainAcct, "-s", agyKeychainSvc, "-w").Output()
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(string(out))), nil
}

type agyCredentials struct {
	Token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expiry  string `json:"expiry"`
	} `json:"token"`
}

func parseAgyCredentials(raw []byte) (agyCredentials, bool) {
	raw = bytes.TrimSpace(raw)
	if rest, ok := bytes.CutPrefix(raw, []byte(agyKeyringB64)); ok {
		decoded, err := base64.StdEncoding.DecodeString(string(rest))
		if err != nil {
			return agyCredentials{}, false
		}
		raw = decoded
	}
	var cred agyCredentials
	if json.Unmarshal(raw, &cred) != nil || (cred.Token.Access == "" && cred.Token.Refresh == "") {
		return agyCredentials{}, false
	}
	return cred, true
}

// credentials finds Antigravity's saved sign-in: its files first (no prompt),
// then its Keychain item.
func (c *antigravityCloud) credentials(ctx context.Context) (agyCredentials, bool) {
	for _, path := range c.files {
		if raw, err := os.ReadFile(path); err == nil {
			if cred, ok := parseAgyCredentials(raw); ok {
				return cred, true
			}
		}
	}
	if c.keychain != nil {
		if raw, err := c.keychain(ctx); err == nil {
			return parseAgyCredentials(raw)
		}
	}
	return agyCredentials{}, false
}

// accessToken is a usable access token: the saved one while it still has a
// minute left, else one refreshed in memory (never written back).
func (c *antigravityCloud) accessToken(ctx context.Context, forceRefresh bool) (string, error) {
	c.mu.Lock()
	if !forceRefresh && c.memAccess != "" && c.now().Add(time.Minute).Before(c.memExpires) {
		token := c.memAccess
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	cred, ok := c.credentials(ctx)
	if !ok {
		return "", fmt.Errorf("%w: no saved Antigravity sign-in", ErrSetup)
	}
	if exp, err := time.Parse(time.RFC3339Nano, cred.Token.Expiry); err == nil && cred.Token.Access != "" && !forceRefresh && c.now().Add(time.Minute).Before(exp) {
		return cred.Token.Access, nil
	}
	if cred.Token.Refresh == "" {
		return "", fmt.Errorf("%w: Antigravity's sign-in expired, open Antigravity to renew it", ErrUnauthorized)
	}

	if c.clientID == "" || c.clientSec == "" {
		return "", fmt.Errorf("%w: set ANTIGRAVITY_OAUTH_CLIENT_ID and ANTIGRAVITY_OAUTH_CLIENT_SECRET to read Antigravity's cloud quota", ErrSetup)
	}
	form := url.Values{
		"client_id": {c.clientID}, "client_secret": {c.clientSec},
		"refresh_token": {cred.Token.Refresh}, "grant_type": {"refresh_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: oauth2.googleapis.com unreachable", ErrUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("%w: Antigravity's sign-in expired, open Antigravity to renew it", ErrUnauthorized)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: oauth2.googleapis.com answered %d", ErrUnavailable, resp.StatusCode)
	}
	var tok struct {
		Access  string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&tok) != nil || tok.Access == "" {
		return "", fmt.Errorf("%w: unexpected response from oauth2.googleapis.com", ErrUnavailable)
	}
	c.mu.Lock()
	c.memAccess = tok.Access
	c.memExpires = c.now().Add(time.Duration(tok.Expires) * time.Second)
	c.mu.Unlock()
	return tok.Access, nil
}

func (c *antigravityCloud) headers(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token, "User-Agent": c.userAgent}
}

// Fetch reads the quota summary and the plan name.
func (c *antigravityCloud) Fetch(ctx context.Context) (Snapshot, error) {
	token, err := c.accessToken(ctx, false)
	if err != nil {
		return Snapshot{}, err
	}
	var body agyQuotaBody
	err = c.quota(ctx, token, &body)
	if errors.Is(err, ErrUnauthorized) {
		// The saved token may have been revoked early: refresh once.
		if token, err = c.accessToken(ctx, true); err == nil {
			err = c.quota(ctx, token, &body)
		}
	}
	if err != nil {
		return Snapshot{}, err
	}
	snap, err := parseAntigravityQuota(body, c.now())
	if err != nil {
		return Snapshot{}, err
	}
	snap.Plan = c.plan(ctx, token)
	snap.At = c.now()
	return snap, nil
}

// quota tries the daily host, then production.
func (c *antigravityCloud) quota(ctx context.Context, token string, out *agyQuotaBody) error {
	var last error
	for _, u := range c.quotaURLs {
		err := postJSON(ctx, c.client, u, c.headers(token), []byte(`{}`), out)
		if err == nil {
			return nil
		}
		last = err
		if errors.Is(err, ErrUnauthorized) || ctx.Err() != nil {
			return err
		}
	}
	return last
}

// plan is the paid tier's name ("Google AI Pro"), else the current tier's;
// empty when the call fails (the plan is a label, not a limit).
func (c *antigravityCloud) plan(ctx context.Context, token string) string {
	var body struct {
		PaidTier    *struct{ Name string } `json:"paidTier"`
		CurrentTier *struct{ Name string } `json:"currentTier"`
	}
	req := []byte(`{"metadata":{"ideType":"ANTIGRAVITY","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}}`)
	if postJSON(ctx, c.client, c.loadURL, c.headers(token), req, &body) != nil {
		return ""
	}
	switch {
	case body.PaidTier != nil && body.PaidTier.Name != "":
		return body.PaidTier.Name
	case body.CurrentTier != nil && body.CurrentTier.Name != "Antigravity":
		return body.CurrentTier.Name
	}
	return ""
}

type agyQuotaBody struct {
	Groups []struct {
		DisplayName string `json:"displayName"`
		Buckets     []struct {
			BucketID          string   `json:"bucketId"`
			Window            string   `json:"window"`
			DisplayName       string   `json:"displayName"`
			RemainingFraction *float64 `json:"remainingFraction"`
			ResetTime         string   `json:"resetTime"`
			Disabled          bool     `json:"disabled"`
		} `json:"buckets"`
	} `json:"groups"`
}

// parseAntigravityQuota maps each model group's session (5-hour) and weekly
// buckets onto windows that render as one block per group.
func parseAntigravityQuota(b agyQuotaBody, now time.Time) (Snapshot, error) {
	var snap Snapshot
	for _, g := range b.Groups {
		name := strings.TrimSpace(g.DisplayName)
		if name == "" {
			name = "Models"
		}
		var five, week *Window
		for _, bk := range g.Buckets {
			if bk.Disabled || bk.RemainingFraction == nil {
				continue
			}
			label := strings.ToLower(bk.Window + " " + bk.BucketID + " " + bk.DisplayName)
			w := Window{UsedPct: clampPct((1 - *bk.RemainingFraction) * 100), Group: name}
			if t, err := time.Parse(time.RFC3339, bk.ResetTime); err == nil {
				w.Resets = t
			}
			switch {
			case strings.Contains(label, "week") || strings.Contains(label, "7d"):
				w.Name, w.Span = "Weekly", 7*24*time.Hour
				if week == nil {
					week = &w
				}
			case strings.Contains(label, "5h") || strings.Contains(label, "session") || strings.Contains(label, "hour"):
				w.Name, w.Span = "5-hour", 5*time.Hour
				if five == nil {
					five = &w
				}
			}
		}
		if five != nil {
			snap.Windows = append(snap.Windows, *five)
		}
		if week != nil {
			snap.Windows = append(snap.Windows, *week)
		}
	}
	if len(snap.Windows) == 0 {
		return Snapshot{}, fmt.Errorf("%w: Antigravity reported no quotas", ErrUnavailable)
	}
	return snap, nil
}

// antigravityBoth reads the cloud quota first — it needs no running app —
// and falls back to the running app's local server. The cloud read happens
// only when the user allowed Solco to use Antigravity's saved sign-in.
type antigravityBoth struct {
	cloud   *antigravityCloud
	local   *antigravity
	allowed func() bool
}

// NewAntigravity reads Antigravity's quotas. allowed says whether the user let
// Solco use Antigravity's saved sign-in; without it only the running app's
// local server is asked.
func NewAntigravity(allowed func() bool) Fetcher {
	return &antigravityBoth{cloud: newAntigravityCloud(), local: newAntigravityLocal(), allowed: allowed}
}

func (*antigravityBoth) ID() ID { return Antigravity }

func (a *antigravityBoth) Fetch(ctx context.Context) (Snapshot, error) {
	allowed := a.allowed != nil && a.allowed()
	var cloudErr error
	if allowed {
		snap, err := a.cloud.Fetch(ctx)
		if err == nil {
			// Gemini models are not in every plan's summary; the running app
			// lists them.
			if !hasGroup(snap, "gemini") {
				if local, lerr := a.local.Fetch(ctx); lerr == nil {
					snap = mergeGemini(snap, local)
				}
			}
			return snap, nil
		}
		cloudErr = err
	}
	snap, err := a.local.Fetch(ctx)
	if err == nil {
		return snap, nil
	}
	switch {
	case !allowed:
		return Snapshot{}, fmt.Errorf("%w: Antigravity isn't running. Allow its saved sign-in to read limits while it is closed", ErrSetup)
	case errors.Is(cloudErr, ErrSetup):
		return Snapshot{}, fmt.Errorf("%w: Antigravity isn't running and has no saved sign-in", ErrUnavailable)
	}
	return Snapshot{}, cloudErr
}

func hasGroup(s Snapshot, word string) bool {
	for _, w := range s.Windows {
		if strings.Contains(strings.ToLower(w.Group), word) {
			return true
		}
	}
	return false
}

// mergeGemini appends the running app's Gemini model quotas as one group.
func mergeGemini(cloud, local Snapshot) Snapshot {
	for _, w := range local.Windows {
		if strings.Contains(strings.ToLower(w.Name), "gemini") {
			w.Group = "Gemini models"
			cloud.Windows = append(cloud.Windows, w)
		}
	}
	if cloud.Plan == "" {
		cloud.Plan = local.Plan
	}
	return cloud
}
