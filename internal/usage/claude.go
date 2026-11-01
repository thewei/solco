package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

/**
 * [INPUT]: Claude Code 自己的登录凭据（登录 Keychain 条目 `Claude Code-credentials`，或 ~/.claude/.credentials.json）与 ANTHROPIC_BASE_URL（环境变量/~/.claude/settings.json）
 * [OUTPUT]: Claude Fetcher（GET api.anthropic.com/api/oauth/usage；NewClaudeCached 把取到的登录副本存进 SigninCache）、parseClaude/parseClaudeCredentials 纯函数、自定义端点识别
 * [POS]: 只读借用 Claude Code 的 OAuth access token，仅发往 api.anthropic.com；从不刷新 token（刷新会让 Claude Code 自己的登录失效），过期就如实报“请打开 Claude Code 续期”；走自定义端点的 Claude 没有套餐额度，返回 ErrCustomEndpoint；有 SigninCache 时先读副本，Claude Code 的 Keychain 条目只在副本缺失/过期/被拒时重读一次；过期的来源或被拒的 Keychain 读取之后休息 claudeSourceRetry（10 分钟）不再去问（errClaudeExpired / blockedUntil），手动 Refresh 之外不会每次打开 Quota 都弹授权
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	claudeHost       = "api.anthropic.com"
	claudeUsageURL   = "https://" + claudeHost + "/api/oauth/usage"
	claudeKeychainSV = "Claude Code-credentials"
)

type claude struct {
	client   *http.Client
	endpoint string
	home     string
	getenv   func(string) string
	// keychain returns the credential blob Claude Code stored, or ErrSetup.
	keychain func(ctx context.Context) ([]byte, error)
	// cache keeps the sourced sign-in in Solco's own store, so Claude Code's
	// Keychain item — whose read makes macOS ask — is read once per copy.
	cache    SigninCache
	cacheKey string
	// A sourced sign-in that is expired, or a Keychain read the user denied,
	// is not read again before blockedUntil: every read of that item makes
	// macOS ask, and the answer cannot change until Claude Code renews it.
	blockedUntil time.Time
	blockedErr   error
}

// claudeSourceRetry is how long an expired or denied Keychain source rests.
const claudeSourceRetry = 10 * time.Minute

var errClaudeExpired = fmt.Errorf("%w: Claude Code's sign-in expired, open Claude Code to renew it", ErrUnauthorized)

// NewClaude reads Claude Code's plan limits with its own stored sign-in.
func NewClaude() Fetcher {
	home, _ := os.UserHomeDir()
	return &claude{client: newHTTPClient(), endpoint: claudeUsageURL, home: home, getenv: os.Getenv, keychain: claudeKeychain}
}

// NewClaudeCached is NewClaude keeping the sign-in it sources in cache under
// key: Claude Code's Keychain item is read once, and again only after the
// copy expired, was rejected or was dropped.
func NewClaudeCached(cache SigninCache, key string) Fetcher {
	c := NewClaude().(*claude)
	c.cache, c.cacheKey = cache, key
	return c
}

func (*claude) ID() ID { return Claude }

func (c *claude) Fetch(ctx context.Context) (Snapshot, error) {
	if base := c.baseURL(); base != "" {
		if u, err := url.Parse(base); err != nil || u.Hostname() != claudeHost {
			return Snapshot{}, fmt.Errorf("%w: Claude Code uses its own endpoint, so it has no plan limits", ErrCustomEndpoint)
		}
	}
	cred, cached, err := c.credentials(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if cred.expired() {
		return Snapshot{}, errClaudeExpired
	}
	var body claudeUsageBody
	err = getJSON(ctx, c.client, c.endpoint, map[string]string{
		"Authorization":  "Bearer " + cred.AccessToken,
		"anthropic-beta": "oauth-2025-04-20",
		"User-Agent":     "claude-code/2.0",
	}, &body)
	if err != nil {
		if cached && errors.Is(err, ErrUnauthorized) {
			c.dropCached() // the copy is no longer accepted; re-source next read
		}
		return Snapshot{}, err
	}
	snap := parseClaude(body)
	if len(snap.Windows) == 0 {
		return Snapshot{}, fmt.Errorf("%w: Claude reported no limits", ErrUnavailable)
	}
	snap.Plan = cred.plan()
	snap.At = time.Now()
	return snap, nil
}

// baseURL is the endpoint Claude Code is pointed at: the process
// environment first, then the env block of ~/.claude/settings.json.
func (c *claude) baseURL() string {
	if v := strings.TrimSpace(c.getenv("ANTHROPIC_BASE_URL")); v != "" {
		return v
	}
	data, err := os.ReadFile(filepath.Join(c.home, ".claude", "settings.json"))
	if err != nil {
		return ""
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return ""
	}
	return strings.TrimSpace(settings.Env["ANTHROPIC_BASE_URL"])
}

type claudeCred struct {
	AccessToken      string `json:"accessToken"`
	ExpiresAt        int64  `json:"expiresAt"` // ms since epoch
	SubscriptionType string `json:"subscriptionType"`
	RateLimitTier    string `json:"rateLimitTier"`
}

func (c claudeCred) expired() bool {
	return c.ExpiresAt > 0 && time.UnixMilli(c.ExpiresAt).Before(time.Now())
}

// plan is the subscription as Claude names it: "Max 5×", "Pro".
func (c claudeCred) plan() string {
	tier := strings.ToLower(c.RateLimitTier)
	switch {
	case strings.Contains(tier, "max_20x"):
		return "Max 20×"
	case strings.Contains(tier, "max_5x"):
		return "Max 5×"
	}
	return planTitle(c.SubscriptionType)
}

// parseClaudeCredentials reads the blob Claude Code keeps: the OAuth block.
func parseClaudeCredentials(blob []byte) (claudeCred, error) {
	var file struct {
		OAuth claudeCred `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(blob, &file); err != nil || file.OAuth.AccessToken == "" {
		return claudeCred{}, fmt.Errorf("%w: Claude Code is not signed in", ErrSetup)
	}
	return file.OAuth, nil
}

// credentials returns the sign-in to read with and whether it came from the
// cache. The cache is Solco's own credential entry: Claude Code's Keychain
// item — whose read makes macOS ask — is touched only while no usable copy
// exists.
func (c *claude) credentials(ctx context.Context) (claudeCred, bool, error) {
	if c.cache != nil && c.cacheKey != "" {
		if blob, ok := c.cache.Get(c.cacheKey); ok {
			var cred claudeCred
			if json.Unmarshal([]byte(blob), &cred) == nil && cred.AccessToken != "" && !cred.expired() {
				return cred, true, nil
			}
			c.dropCached() // corrupt or expired: re-source below
		}
	}
	if time.Now().Before(c.blockedUntil) {
		return claudeCred{}, false, c.blockedErr
	}
	cred, err := c.sourceCredentials(ctx)
	switch {
	case err == nil && cred.expired():
		c.blockedUntil, c.blockedErr = time.Now().Add(claudeSourceRetry), errClaudeExpired
	case err != nil && errors.Is(err, ErrUnauthorized):
		c.blockedUntil, c.blockedErr = time.Now().Add(claudeSourceRetry), err
	}
	if err == nil && c.cache != nil && !cred.expired() {
		if blob, merr := json.Marshal(cred); merr == nil {
			c.cache.Set(c.cacheKey, string(blob)) // best effort; a failed copy only means macOS asks again
		}
	}
	return cred, false, err
}

// sourceCredentials reads where Claude Code keeps its sign-in.
func (c *claude) sourceCredentials(ctx context.Context) (claudeCred, error) {
	blob, err := c.keychain(ctx)
	if err != nil && errors.Is(err, ErrSetup) {
		// Older installs and Linux-style setups keep the same JSON in a file.
		if data, ferr := os.ReadFile(filepath.Join(c.home, ".claude", ".credentials.json")); ferr == nil {
			blob, err = data, nil
		}
	}
	if err != nil {
		return claudeCred{}, err
	}
	return parseClaudeCredentials(blob)
}

// dropCached forgets the stored copy, so the next read sources the sign-in
// again.
func (c *claude) dropCached() {
	if c.cache != nil && c.cacheKey != "" {
		c.cache.Delete(c.cacheKey)
	}
}

// claudeKeychain reads Claude Code's Keychain item through /usr/bin/security;
// macOS asks the user to allow it (the confirmation the Settings consent
// dialog announces).
func claudeKeychain(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", claudeKeychainSV, "-w")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 { // errSecItemNotFound
			return nil, fmt.Errorf("%w: Claude Code is not signed in", ErrSetup)
		}
		return nil, fmt.Errorf("%w: the Keychain did not allow reading Claude Code's sign-in", ErrUnauthorized)
	}
	return []byte(strings.TrimSpace(string(out))), nil
}

type claudeUsageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

type claudeUsageBody struct {
	FiveHour       *claudeUsageWindow `json:"five_hour"`
	SevenDay       *claudeUsageWindow `json:"seven_day"`
	SevenDayOpus   *claudeUsageWindow `json:"seven_day_opus"`
	SevenDaySonnet *claudeUsageWindow `json:"seven_day_sonnet"`
	ExtraUsage     *struct {
		Enabled     bool     `json:"is_enabled"`
		Utilization *float64 `json:"utilization"`
	} `json:"extra_usage"`
}

// parseClaude maps the usage body onto windows: the 5-hour and weekly
// limits, plus the per-model weekly ones Anthropic reports when they apply.
func parseClaude(b claudeUsageBody) Snapshot {
	var snap Snapshot
	add := func(w *claudeUsageWindow, name string, span time.Duration) {
		if w == nil || w.Utilization == nil {
			return
		}
		win := Window{Name: name, UsedPct: clampPct(*w.Utilization), Span: span, Minor: strings.Contains(name, " · ")}
		if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
			win.Resets = t
		}
		snap.Windows = append(snap.Windows, win)
	}
	add(b.FiveHour, "5-hour", 5*time.Hour)
	add(b.SevenDay, "Weekly", 7*24*time.Hour)
	add(b.SevenDayOpus, "Weekly · Opus", 7*24*time.Hour)
	add(b.SevenDaySonnet, "Weekly · Sonnet", 7*24*time.Hour)
	if e := b.ExtraUsage; e != nil {
		switch {
		case !e.Enabled:
			snap.Notes = append(snap.Notes, "Extra usage is off")
		case e.Utilization != nil:
			snap.Notes = append(snap.Notes, fmt.Sprintf("Extra usage · %.0f%% of its monthly limit", *e.Utilization))
		}
	}
	return snap
}
