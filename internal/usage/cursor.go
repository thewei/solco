package usage

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
 * [INPUT]: Cursor 的 access token——手填覆盖值，否则 Cursor.app 自己的 state.vscdb（cursorAuth/accessToken），再否则 cursor-agent 的 Keychain 条目 cursor-access-token
 * [OUTPUT]: Cursor Fetcher（GET cursor.com/api/usage-summary；旧请求制套餐再 GET /api/usage?user=<sub> 取 gpt-4 请求数，以其为准）、parseCursor/applyCursorLegacy/cursorCookie 纯函数
 * [POS]: 只读借用 Cursor 自己的 user token，仅发往 cursor.com；用户 2026-10-30 批准读取 Cursor.app / cursor-agent 的 token；state.vscdb 通过只读 sqlite3 单键查询，不拷贝数据库
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	cursorHost       = "cursor.com"
	cursorUsageURL   = "https://" + cursorHost + "/api/usage-summary"
	cursorLegacyURL  = "https://" + cursorHost + "/api/usage"
	cursorAgentKCSvc = "cursor-access-token"
)

type cursor struct {
	client   *http.Client
	endpoint string
	// legacyEndpoint is the request-count usage of old plans; empty skips it.
	legacyEndpoint string
	// manual is the token typed into Settings ("" when none).
	manual func() string
	// auto finds the token Cursor itself keeps; ErrSetup when there is none.
	auto func(ctx context.Context) (string, error)
}

// NewCursor reads Cursor's plan usage; manual returns the token saved in
// Settings, if any, which wins over the one Cursor keeps.
func NewCursor(manual func() string) Fetcher {
	return &cursor{client: newHTTPClient(), endpoint: cursorUsageURL, legacyEndpoint: cursorLegacyURL, manual: manual, auto: cursorAutoToken}
}

func (*cursor) ID() ID { return Cursor }

func (c *cursor) Fetch(ctx context.Context) (Snapshot, error) {
	token := ""
	if c.manual != nil {
		token = strings.TrimSpace(c.manual())
	}
	if token == "" {
		var err error
		if token, err = c.auto(ctx); err != nil {
			return Snapshot{}, err
		}
	}
	cookie, err := cursorCookie(token)
	if err != nil {
		return Snapshot{}, err
	}
	var body cursorBody
	if err := getJSON(ctx, c.client, c.endpoint, map[string]string{"Cookie": cookie}, &body); err != nil {
		return Snapshot{}, err
	}
	snap, err := parseCursor(body)
	if legacy := c.fetchLegacy(ctx, cookie); legacy != nil {
		// An old request-based plan: its request count is the real allowance,
		// whatever percentages the dollar summary also states.
		snap, err = applyCursorLegacy(snap, body, *legacy), nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	snap.At = time.Now()
	return snap, nil
}

// fetchLegacy asks for the request counters of the old plans; nil when the
// account is not on one (or the call fails — the summary then stands alone).
func (c *cursor) fetchLegacy(ctx context.Context, cookie string) *cursorLegacy {
	if c.legacyEndpoint == "" {
		return nil
	}
	user := strings.TrimPrefix(cookie, "WorkosCursorSessionToken=")
	if i := strings.Index(user, "%3A%3A"); i > 0 {
		user = user[:i]
	}
	var body cursorLegacyBody
	if err := getJSON(ctx, c.client, c.legacyEndpoint+"?user="+url.QueryEscape(user), map[string]string{"Cookie": cookie}, &body); err != nil {
		return nil
	}
	if g := body.GPT4; g != nil && g.MaxRequestUsage != nil && *g.MaxRequestUsage > 0 {
		return &cursorLegacy{used: g.NumRequests, max: *g.MaxRequestUsage, start: body.StartOfMonth}
	}
	return nil
}

// cursorCookie is the session cookie cursor.com reads: the user id from the
// token's subject, then the token itself. A pasted full cookie value
// (user_…::jwt, or URL-encoded) is accepted as it is.
func cursorCookie(token string) (string, error) {
	token = strings.TrimPrefix(strings.TrimSpace(token), "WorkosCursorSessionToken=")
	if strings.Contains(token, "%3A%3A") {
		return "WorkosCursorSessionToken=" + token, nil
	}
	if i := strings.Index(token, "::"); i > 0 {
		return "WorkosCursorSessionToken=" + token[:i] + "%3A%3A" + token[i+2:], nil
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("%w: the Cursor token is not a sign-in token", ErrUnauthorized)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("%w: the Cursor token is not a sign-in token", ErrUnauthorized)
	}
	var claims struct {
		Sub string `json:"sub"`
		Exp int64  `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Sub == "" {
		return "", fmt.Errorf("%w: the Cursor token is not a sign-in token", ErrUnauthorized)
	}
	if claims.Exp > 0 && time.Unix(claims.Exp, 0).Before(time.Now()) {
		return "", fmt.Errorf("%w: Cursor's sign-in expired, open Cursor to renew it", ErrUnauthorized)
	}
	user := claims.Sub
	if i := strings.LastIndex(user, "|"); i >= 0 {
		user = user[i+1:]
	}
	return "WorkosCursorSessionToken=" + user + "%3A%3A" + token, nil
}

// cursorAutoToken looks where Cursor keeps its own sign-in: cursor-agent's
// Keychain item first, then the editor's state database. Current Cursor
// builds keep the editor's token encrypted with Electron's safeStorage
// ("cursor-safe-storage:…"); Solco never decrypts that, so only a plain token
// from an older build is used from there.
func cursorAutoToken(ctx context.Context) (string, error) {
	if token := cursorAgentToken(ctx); token != "" {
		return token, nil
	}
	home, _ := os.UserHomeDir()
	db := filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	if _, err := os.Stat(db); err == nil {
		if token := cursorStateToken(ctx, db); token != "" && !strings.HasPrefix(token, "cursor-safe-storage:") {
			return token, nil
		}
	}
	return "", fmt.Errorf("%w: sign in with Cursor's agent (cursor-agent login) or paste a token", ErrSetup)
}

// cursorStateToken reads the one key from the editor's SQLite state through
// the system sqlite3, read-only: the database is large and Cursor keeps it
// open, so it is never copied or locked.
func cursorStateToken(ctx context.Context, db string) string {
	cmd := exec.CommandContext(ctx, "/usr/bin/sqlite3", "-readonly", "-cmd", ".timeout 2000", db,
		"SELECT value FROM ItemTable WHERE key = 'cursorAuth/accessToken' LIMIT 1;")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(string(out)), `"`)
}

// cursorAgentToken reads cursor-agent's Keychain item.
func cursorAgentToken(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", cursorAgentKCSvc, "-w")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type cursorBucket struct {
	Enabled          bool     `json:"enabled"`
	Used             float64  `json:"used"`
	Limit            float64  `json:"limit"`
	AutoPercentUsed  *float64 `json:"autoPercentUsed"`
	APIPercentUsed   *float64 `json:"apiPercentUsed"`
	TotalPercentUsed *float64 `json:"totalPercentUsed"`
}

type cursorBody struct {
	BillingCycleStart string `json:"billingCycleStart"`
	BillingCycleEnd   string `json:"billingCycleEnd"`
	MembershipType    string `json:"membershipType"`
	IndividualUsage   struct {
		Plan     *cursorBucket `json:"plan"`
		OnDemand *cursorBucket `json:"onDemand"`
	} `json:"individualUsage"`
}

// cents formats a Cursor amount (cents) as dollars: 1160 → "$11.60", 2000 → "$20".
func cents(v float64) string {
	if v == float64(int64(v/100))*100 {
		return fmt.Sprintf("$%d", int64(v/100))
	}
	return fmt.Sprintf("$%.2f", v/100)
}

type cursorLegacyBody struct {
	GPT4 *struct {
		MaxRequestUsage *float64 `json:"maxRequestUsage"`
		NumRequests     float64  `json:"numRequests"`
	} `json:"gpt-4"`
	StartOfMonth string `json:"startOfMonth"`
}

type cursorLegacy struct {
	used, max float64
	start     string
}

// cursorCycle is the billing cycle of a summary.
func cursorCycle(b cursorBody) (start, end time.Time, span time.Duration) {
	if t, err := time.Parse(time.RFC3339, b.BillingCycleEnd); err == nil {
		end = t
	}
	if t, err := time.Parse(time.RFC3339, b.BillingCycleStart); err == nil {
		start = t
	}
	if !start.IsZero() && !end.IsZero() && end.After(start) {
		span = end.Sub(start)
	}
	return start, end, span
}

// applyCursorLegacy replaces the summary's plan windows with the request
// count (e.g. 124 of 500 requests); the on-demand window, if any, stays.
func applyCursorLegacy(snap Snapshot, b cursorBody, legacy cursorLegacy) Snapshot {
	start, end, span := cursorCycle(b)
	if end.IsZero() {
		if t, err := time.Parse(time.RFC3339, legacy.start); err == nil {
			start = t
			end = t.AddDate(0, 1, 0)
			span = end.Sub(start)
		}
	}
	out := Snapshot{Plan: planTitle(b.MembershipType), Notes: snap.Notes}
	if out.Plan != "" {
		out.Plan += " · "
	}
	out.Plan += fmt.Sprintf("%d requests", int(legacy.max))
	out.Windows = append(out.Windows, Window{
		Name: "Requests · monthly", UsedPct: clampPct(legacy.used / legacy.max * 100),
		Span: span, Resets: end, Amount: fmt.Sprintf("%d of %d", int(legacy.used), int(legacy.max)),
		Used: legacy.used, Max: legacy.max, Unit: "requests",
	})
	for _, w := range snap.Windows {
		if w.Name == "On-demand" {
			out.Windows = append(out.Windows, w)
		}
	}
	return out
}

// parseCursor maps the usage summary onto the included allowance and the
// optional on-demand one, both over the billing cycle.
func parseCursor(b cursorBody) (Snapshot, error) {
	snap := Snapshot{Plan: planTitle(b.MembershipType)}
	_, end, span := cursorCycle(b)
	window := func(name string, pct float64, amount string) {
		snap.Windows = append(snap.Windows, Window{Name: name, UsedPct: clampPct(pct), Span: span, Resets: end, Amount: amount})
	}
	if plan := b.IndividualUsage.Plan; plan != nil {
		switch {
		case plan.TotalPercentUsed != nil:
			// The shares Cursor's own dashboard states: total, API and Auto.
			window("Total · monthly", *plan.TotalPercentUsed, "")
			if plan.APIPercentUsed != nil {
				window("API · monthly", *plan.APIPercentUsed, "")
				snap.Windows[len(snap.Windows)-1].Minor = true
			}
			if plan.AutoPercentUsed != nil && *plan.AutoPercentUsed > 0 {
				window("Auto · monthly", *plan.AutoPercentUsed, "")
				snap.Windows[len(snap.Windows)-1].Minor = true
			}
		case plan.Limit > 0:
			window("Included · monthly", plan.Used/plan.Limit*100, cents(plan.Used)+" of "+cents(plan.Limit))
		}
	}
	if od := b.IndividualUsage.OnDemand; od != nil && od.Enabled && od.Limit > 0 {
		window("On-demand", od.Used/od.Limit*100, cents(od.Used)+" of "+cents(od.Limit))
		snap.Windows[len(snap.Windows)-1].Minor = true
	}
	if len(snap.Windows) == 0 {
		return Snapshot{}, fmt.Errorf("%w: Cursor reported no usage", ErrUnavailable)
	}
	return snap, nil
}
