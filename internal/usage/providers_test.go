package usage

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func jsonServer(t *testing.T, status int, body string, check func(r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func ctx5(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// ---- Claude ----

const claudeBlob = `{"claudeAiOauth":{"accessToken":"tok-claude","expiresAt":%d,"subscriptionType":"max","rateLimitTier":"default_claude_max_5x"}}`

const claudeBody = `{
 "five_hour": {"utilization": 41.0, "resets_at": "2026-10-30T15:12:00Z"},
 "seven_day": {"utilization": 87.0, "resets_at": "2026-11-01T17:00:00Z"},
 "seven_day_opus": {"utilization": 63.0, "resets_at": "2026-11-01T17:00:00Z"},
 "seven_day_sonnet": null,
 "extra_usage": {"is_enabled": false}
}`

func newTestClaude(t *testing.T, endpoint string, blob string, env map[string]string) *claude {
	t.Helper()
	home := t.TempDir()
	return &claude{
		client: newHTTPClient(), endpoint: endpoint, home: home,
		getenv:   func(k string) string { return env[k] },
		keychain: func(context.Context) ([]byte, error) { return []byte(blob), nil },
	}
}

func TestClaudeFetch(t *testing.T) {
	var gotAuth, gotBeta string
	srv := jsonServer(t, 200, claudeBody, func(r *http.Request) {
		gotAuth, gotBeta = r.Header.Get("Authorization"), r.Header.Get("anthropic-beta")
	})
	blob := fmt.Sprintf(claudeBlob, time.Now().Add(time.Hour).UnixMilli())
	snap, err := newTestClaude(t, srv.URL, blob, nil).Fetch(ctx5(t))
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok-claude" || gotBeta == "" {
		t.Errorf("auth = %q, beta = %q", gotAuth, gotBeta)
	}
	if snap.Plan != "Max 5×" {
		t.Errorf("plan = %q", snap.Plan)
	}
	names := []string{}
	for _, w := range snap.Windows {
		names = append(names, w.Name)
	}
	if strings.Join(names, ",") != "5-hour,Weekly,Weekly · Opus" {
		t.Errorf("windows = %v", names)
	}
	if snap.Windows[1].UsedPct != 87 || snap.Windows[1].Span != 7*24*time.Hour || snap.Windows[1].Resets.IsZero() {
		t.Errorf("weekly = %+v", snap.Windows[1])
	}
	if len(snap.Notes) != 1 || snap.Notes[0] != "Extra usage is off" {
		t.Errorf("notes = %v", snap.Notes)
	}
}

func TestClaudeCustomEndpointNeverCallsOut(t *testing.T) {
	called := false
	srv := jsonServer(t, 200, claudeBody, func(*http.Request) { called = true })
	blob := fmt.Sprintf(claudeBlob, time.Now().Add(time.Hour).UnixMilli())
	for name, c := range map[string]*claude{
		"env": newTestClaude(t, srv.URL, blob, map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:3425"}),
	} {
		if _, err := c.Fetch(ctx5(t)); !errors.Is(err, ErrCustomEndpoint) {
			t.Errorf("%s: err = %v, want ErrCustomEndpoint", name, err)
		}
	}
	// The same through ~/.claude/settings.json.
	c := newTestClaude(t, srv.URL, blob, nil)
	if err := os.MkdirAll(filepath.Join(c.home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:3425"}}`
	if err := os.WriteFile(filepath.Join(c.home, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(ctx5(t)); !errors.Is(err, ErrCustomEndpoint) {
		t.Errorf("settings.json: err = %v, want ErrCustomEndpoint", err)
	}
	if called {
		t.Fatal("a custom endpoint must not send the token to the vendor")
	}
}

func TestClaudeExpiredTokenIsNeverRefreshed(t *testing.T) {
	called := false
	srv := jsonServer(t, 200, claudeBody, func(*http.Request) { called = true })
	blob := fmt.Sprintf(claudeBlob, time.Now().Add(-time.Hour).UnixMilli())
	if _, err := newTestClaude(t, srv.URL, blob, nil).Fetch(ctx5(t)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if called {
		t.Fatal("an expired token must not be sent")
	}
}

func TestClaudeStatusMapping(t *testing.T) {
	blob := fmt.Sprintf(claudeBlob, time.Now().Add(time.Hour).UnixMilli())
	srv := jsonServer(t, 429, `{}`, nil)
	_, err := newTestClaude(t, srv.URL, blob, nil).Fetch(ctx5(t))
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("429: err = %v", err)
	}
	srv = jsonServer(t, 401, `{}`, nil)
	if _, err := newTestClaude(t, srv.URL, blob, nil).Fetch(ctx5(t)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("401: err = %v", err)
	}
	if _, err := parseClaudeCredentials([]byte(`not json`)); !errors.Is(err, ErrSetup) {
		t.Fatalf("garbage credentials: err = %v, want ErrSetup", err)
	}
}

// ---- Cursor ----

func jwt(sub string, exp time.Time) string {
	payload := fmt.Sprintf(`{"sub":%q,"exp":%d}`, sub, exp.Unix())
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".s"
}

func TestCursorCookie(t *testing.T) {
	token := jwt("auth0|user_01ABC", time.Now().Add(time.Hour))
	got, err := cursorCookie(token)
	if err != nil {
		t.Fatal(err)
	}
	if want := "WorkosCursorSessionToken=user_01ABC%3A%3A" + token; got != want {
		t.Errorf("cookie = %q", got)
	}
	// A pasted full cookie value passes through, both spellings.
	if got, _ := cursorCookie("user_1::abc.def.ghi"); got != "WorkosCursorSessionToken=user_1%3A%3Aabc.def.ghi" {
		t.Errorf(":: form = %q", got)
	}
	if got, _ := cursorCookie("WorkosCursorSessionToken=user_1%3A%3Aabc"); got != "WorkosCursorSessionToken=user_1%3A%3Aabc" {
		t.Errorf("encoded form = %q", got)
	}
	if _, err := cursorCookie(jwt("auth0|user_1", time.Now().Add(-time.Hour))); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expired: err = %v", err)
	}
	if _, err := cursorCookie("not-a-token"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("garbage: err = %v", err)
	}
}

const cursorBodyJSON = `{
 "billingCycleStart": "2026-10-01T00:00:00Z", "billingCycleEnd": "2026-11-01T00:00:00Z",
 "membershipType": "pro",
 "individualUsage": {
  "plan": {"enabled": true, "used": 496, "limit": 2000, "autoPercentUsed": 0, "apiPercentUsed": 44.09, "totalPercentUsed": 1.08},
  "onDemand": {"enabled": true, "used": 120, "limit": 1000}
 }
}`

func TestCursorFetch(t *testing.T) {
	var cookie string
	srv := jsonServer(t, 200, cursorBodyJSON, func(r *http.Request) { cookie = r.Header.Get("Cookie") })
	token := jwt("auth0|user_01ABC", time.Now().Add(time.Hour))
	c := &cursor{client: newHTTPClient(), endpoint: srv.URL, manual: func() string { return token },
		auto: func(context.Context) (string, error) { return "", ErrSetup }}
	snap, err := c.Fetch(ctx5(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cookie, "WorkosCursorSessionToken=user_01ABC%3A%3A") {
		t.Errorf("cookie = %q", cookie)
	}
	if snap.Plan != "Pro" || len(snap.Windows) != 3 {
		t.Fatalf("snap = %+v", snap)
	}
	total, api, od := snap.Windows[0], snap.Windows[1], snap.Windows[2]
	if api.Name != "API · monthly" || math.Abs(api.UsedPct-44.09) > 0.001 {
		t.Errorf("api = %+v", api)
	}
	if total.Name != "Total · monthly" || math.Abs(total.UsedPct-1.08) > 0.001 || total.Span != 31*24*time.Hour {
		t.Errorf("total = %+v", total)
	}
	if od.Name != "On-demand" || od.Amount != "$1.20 of $10" || math.Abs(od.UsedPct-12) > 0.001 {
		t.Errorf("on-demand = %+v", od)
	}
}

func TestCursorLegacyPlanUsesRequestCount(t *testing.T) {
	var query string
	legacy := jsonServer(t, 200, `{"gpt-4":{"maxRequestUsage":500,"numRequests":124,"numRequestsTotal":124},"startOfMonth":"2026-10-17T02:00:23.000Z"}`,
		func(r *http.Request) { query = r.URL.RawQuery })
	summary := jsonServer(t, 200, cursorBodyJSON, nil)
	token := jwt("auth0|user_01ABC", time.Now().Add(time.Hour))
	c := &cursor{client: newHTTPClient(), endpoint: summary.URL, legacyEndpoint: legacy.URL, manual: func() string { return token },
		auto: func(context.Context) (string, error) { return "", ErrSetup }}
	snap, err := c.Fetch(ctx5(t))
	if err != nil {
		t.Fatal(err)
	}
	if query != "user=user_01ABC" {
		t.Errorf("legacy query = %q", query)
	}
	if snap.Plan != "Pro · 500 requests" || len(snap.Windows) != 2 {
		t.Fatalf("snap = %+v", snap)
	}
	req, od := snap.Windows[0], snap.Windows[1]
	if req.Name != "Requests · monthly" || req.Amount != "124 of 500" || math.Abs(req.UsedPct-24.8) > 0.001 || req.Resets.IsZero() {
		t.Errorf("requests = %+v", req)
	}
	if od.Name != "On-demand" {
		t.Errorf("on-demand kept? %+v", od)
	}
}

func TestCursorNewPlanIgnoresLegacyEndpoint(t *testing.T) {
	legacy := jsonServer(t, 200, `{"gpt-4":{"maxRequestUsage":null,"numRequests":0},"startOfMonth":"2026-10-17T02:00:23.000Z"}`, nil)
	summary := jsonServer(t, 200, cursorBodyJSON, nil)
	token := jwt("auth0|user_01ABC", time.Now().Add(time.Hour))
	c := &cursor{client: newHTTPClient(), endpoint: summary.URL, legacyEndpoint: legacy.URL, manual: func() string { return token },
		auto: func(context.Context) (string, error) { return "", ErrSetup }}
	snap, err := c.Fetch(ctx5(t))
	if err != nil || snap.Plan != "Pro" || len(snap.Windows) != 3 {
		t.Fatalf("snap = %+v, err = %v", snap, err)
	}
	// A failing legacy call leaves the summary standing too.
	bad := jsonServer(t, 500, `{}`, nil)
	c.legacyEndpoint = bad.URL
	if snap, err = c.Fetch(ctx5(t)); err != nil || len(snap.Windows) != 3 {
		t.Fatalf("legacy failure broke the summary: %+v, %v", snap, err)
	}
}

func TestCursorFallsBackToAutoToken(t *testing.T) {
	srv := jsonServer(t, 200, cursorBodyJSON, nil)
	token := jwt("auth0|user_9", time.Now().Add(time.Hour))
	c := &cursor{client: newHTTPClient(), endpoint: srv.URL, manual: func() string { return "  " },
		auto: func(context.Context) (string, error) { return token, nil }}
	if _, err := c.Fetch(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	c.auto = func(context.Context) (string, error) { return "", fmt.Errorf("%w: none", ErrSetup) }
	if _, err := c.Fetch(ctx5(t)); !errors.Is(err, ErrSetup) {
		t.Fatalf("no token anywhere: err = %v, want ErrSetup", err)
	}
}

// ---- Copilot ----

const copilotBodyJSON = `{
 "copilot_plan": "individual_pro", "access_type_sku": "copilot_pro_subscriber", "quota_reset_date": "2026-11-01",
 "quota_snapshots": {
  "premium_interactions": {"entitlement": 300, "remaining": 114, "percent_remaining": 38.0, "unlimited": false},
  "chat": {"entitlement": 0, "remaining": 0, "percent_remaining": 100, "unlimited": true},
  "completions": {"entitlement": 0, "remaining": 0, "percent_remaining": 100, "unlimited": true}
 }
}`

func TestCopilotFetch(t *testing.T) {
	var auth string
	srv := jsonServer(t, 200, copilotBodyJSON, func(r *http.Request) { auth = r.Header.Get("Authorization") })
	c := &copilot{client: newHTTPClient(), endpoint: srv.URL, manual: func() string { return "" },
		gh: func(context.Context) (string, error) { return "gho_x", nil }}
	snap, err := c.Fetch(ctx5(t))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "token gho_x" {
		t.Errorf("auth = %q", auth)
	}
	if snap.Plan != "Pro" || len(snap.Windows) != 1 {
		t.Fatalf("snap = %+v", snap)
	}
	w := snap.Windows[0]
	if w.Name != "Premium requests · monthly" || w.Amount != "186 of 300" || w.UsedPct != 62 || w.Resets.IsZero() {
		t.Errorf("premium = %+v", w)
	}
	// A PAT saved in Settings wins over gh.
	c.manual = func() string { return "ghp_pat" }
	if _, err := c.Fetch(ctx5(t)); err != nil || auth != "token ghp_pat" {
		t.Errorf("manual token: err = %v, auth = %q", err, auth)
	}
}

func TestCopilotFreePlan(t *testing.T) {
	snap, err := parseCopilot(copilotBody{Plan: "individual", SKU: "free_limited_copilot"})
	if err == nil {
		t.Fatalf("no quotas must be an error, got %+v", snap)
	}
	var b copilotBody
	b.Plan, b.SKU = "individual", "free_limited_copilot"
	b.QuotaSnapshots.Chat = &copilotQuota{Entitlement: 200, Remaining: 200, PercentRemaining: 100}
	if snap, err = parseCopilot(b); err != nil || snap.Plan != "Free" || len(snap.Windows) != 1 {
		t.Fatalf("snap = %+v, err = %v", snap, err)
	}
}

func TestCopilotNeedsSignIn(t *testing.T) {
	c := &copilot{client: newHTTPClient(), endpoint: "http://unused", manual: func() string { return "" },
		gh: func(context.Context) (string, error) { return "", fmt.Errorf("%w: sign in", ErrSetup) }}
	if _, err := c.Fetch(ctx5(t)); !errors.Is(err, ErrSetup) {
		t.Fatalf("err = %v, want ErrSetup", err)
	}
}

// ---- Zhipu ----

const zhipuBodyJSON = `{"code":200,"success":true,"data":{"level":"pro","limits":[
 {"type":"TOKENS_LIMIT","unit":3,"number":5,"usage":1000,"currentValue":230,"percentage":23,"nextResetTime":1793362966000},
 {"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":35,"nextResetTime":1793886543000},
 {"type":"TIME_LIMIT","unit":5,"number":1,"usage":1000,"currentValue":90,"percentage":9,"nextResetTime":1796000000000}
]}}`

func TestZhipuFetch(t *testing.T) {
	var auth string
	srv := jsonServer(t, 200, zhipuBodyJSON, func(r *http.Request) { auth = r.Header.Get("Authorization") })
	z := &zhipu{client: newHTTPClient(), key: func() string { return " k-123 " }, site: func() string { return ZhipuGlobal }, endpoint: srv.URL}
	snap, err := z.Fetch(ctx5(t))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer k-123" {
		t.Errorf("auth = %q", auth)
	}
	var names []string
	for _, w := range snap.Windows {
		names = append(names, w.Name+"="+strconv.Itoa(int(w.UsedPct)))
	}
	if got := strings.Join(names, ","); got != "5-hour=23,Weekly=35,MCP · monthly=9" {
		t.Errorf("windows = %s", got)
	}
	if snap.Plan != "Pro" || snap.Windows[2].Amount != "90 of 1000" {
		t.Errorf("snap = %+v", snap)
	}
}

func TestZhipuNeedsKey(t *testing.T) {
	z := &zhipu{client: newHTTPClient(), key: func() string { return "" }, site: func() string { return ZhipuChina }}
	if _, err := z.Fetch(ctx5(t)); !errors.Is(err, ErrSetup) {
		t.Fatalf("err = %v, want ErrSetup", err)
	}
}

func TestZhipuSitePicksHost(t *testing.T) {
	for site, host := range map[string]string{ZhipuGlobal: "api.z.ai", ZhipuChina: "open.bigmodel.cn"} {
		u, err := url.Parse(zhipuEndpoints[site])
		if err != nil || u.Host != host {
			t.Errorf("%s endpoint = %v", site, zhipuEndpoints[site])
		}
	}
}

// ---- Antigravity ----

func TestParseAntigravityPS(t *testing.T) {
	ps := `  101 /usr/sbin/cfprefsd agent
  4242 /Applications/Antigravity.app/Contents/Resources/app/extensions/antigravity/bin/language_server_macos_arm --enable_lsp --csrf_token abc-123 --extension_server_port 5555 --app_data_dir antigravity
  4300 /Applications/Windsurf.app/language_server_macos_arm --csrf_token other`
	pid, csrf, ok := parseAntigravityPS(ps)
	if !ok || pid != 4242 || csrf != "abc-123" {
		t.Fatalf("pid=%d csrf=%q ok=%v", pid, csrf, ok)
	}
	if _, _, ok := parseAntigravityPS("  1 /sbin/launchd"); ok {
		t.Fatal("no language server must report not running")
	}
}

func TestParseLsofPorts(t *testing.T) {
	out := `COMMAND  PID USER FD TYPE DEVICE SIZE/OFF NODE NAME
language 4242 w 12u IPv4 0x1 0t0 TCP 127.0.0.1:51234 (LISTEN)
language 4242 w 13u IPv4 0x2 0t0 TCP 127.0.0.1:50111 (LISTEN)
language 4242 w 14u IPv6 0x3 0t0 TCP *:50111 (LISTEN)`
	got := parseLsofPorts(out)
	if len(got) != 2 || got[0] != 50111 || got[1] != 51234 {
		t.Fatalf("ports = %v", got)
	}
}

const antigravityBodyJSON = `{"userStatus":{"userTier":{"name":"Pro"},"cascadeModelConfigData":{"clientModelConfigs":[
 {"label":"Gemini 3 Pro (High)","quotaInfo":{"remainingFraction":0.7,"resetTime":"2026-10-30T15:00:00Z"}},
 {"label":"Gemini 3 Pro (Low)","quotaInfo":{"remainingFraction":0.7,"resetTime":"2026-10-30T15:00:00Z"}},
 {"label":"Claude Sonnet 4.5","quotaInfo":{"remainingFraction":1,"resetTime":"2026-10-30T16:00:00Z"}},
 {"label":"No quota model"}
]}}}`

func TestParseAntigravityGroupsSharedQuota(t *testing.T) {
	snap, err := parseAntigravity([]byte(antigravityBodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Plan != "Pro" || len(snap.Windows) != 2 {
		t.Fatalf("snap = %+v", snap)
	}
	g, c := snap.Windows[0], snap.Windows[1]
	if g.Name != "Gemini 3 Pro (High) +1" || g.UsedPct < 29.9 || g.UsedPct > 30.1 {
		t.Errorf("gemini = %+v", g)
	}
	if c.Name != "Claude Sonnet 4.5" || c.UsedPct != 0 {
		t.Errorf("claude = %+v", c)
	}
}

func TestAntigravityFetchSkipsDeadPorts(t *testing.T) {
	var csrf string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csrf = r.Header.Get("X-Codeium-Csrf-Token")
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/GetUserStatus") {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(antigravityBodyJSON))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	live, _ := strconv.Atoi(u.Port())
	a := &antigravity{client: srv.Client(), discover: func(context.Context) (string, []int, error) {
		return "tok", []int{1, live}, nil // port 1 refuses
	}}
	snap, err := a.Fetch(ctx5(t))
	if err != nil {
		t.Fatal(err)
	}
	if csrf != "tok" || len(snap.Windows) != 2 {
		t.Errorf("csrf = %q, snap = %+v", csrf, snap)
	}
}

func TestAntigravityNotRunning(t *testing.T) {
	a := &antigravity{client: http.DefaultClient, discover: func(context.Context) (string, []int, error) {
		return "", nil, fmt.Errorf("%w: Antigravity is not running", ErrUnavailable)
	}}
	if _, err := a.Fetch(ctx5(t)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// Secrets must never reach an error text.
func TestFailuresCarryNoToken(t *testing.T) {
	srv := jsonServer(t, 500, `{}`, nil)
	token := "SECRET-TOKEN-VALUE"
	z := &zhipu{client: newHTTPClient(), key: func() string { return token }, site: func() string { return "" }, endpoint: srv.URL}
	_, err := z.Fetch(ctx5(t))
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("err = %v", err)
	}
	_, detail := classify(err)
	if strings.Contains(detail, token) {
		t.Fatalf("detail leaks the token: %q", detail)
	}
}

// ---- Antigravity cloud ----

const agyQuotaJSON = `{"groups":[{"displayName":"Claude and GPT models","buckets":[
 {"bucketId":"3p-weekly","displayName":"Weekly Limit Remaining","remainingFraction":0.75,"resetTime":"2026-11-06T08:15:53Z","window":"weekly"},
 {"bucketId":"3p-5h","displayName":"Five Hour Limit Remaining","remainingFraction":0.9,"resetTime":"2026-10-30T13:15:53Z","window":"5h"}]}]}`

type agyServers struct {
	cloud                *antigravityCloud
	refreshes            int
	form                 url.Values
	quotaAuth, loadAuth  string
	quotaCalls, loadCall int
}

func newAgyServers(t *testing.T, credJSON string) *agyServers {
	t.Helper()
	s := &agyServers{}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		s.refreshes++
		_ = r.ParseForm()
		s.form = r.PostForm
		_, _ = w.Write([]byte(`{"access_token":"fresh-access","expires_in":3599}`))
	})
	mux.HandleFunc("/quota", func(w http.ResponseWriter, r *http.Request) {
		s.quotaCalls++
		s.quotaAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(agyQuotaJSON))
	})
	mux.HandleFunc("/load", func(w http.ResponseWriter, r *http.Request) {
		s.loadCall++
		s.loadAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"currentTier":{"name":"Antigravity"},"paidTier":{"name":"Google AI Pro"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	file := filepath.Join(t.TempDir(), "token")
	if credJSON != "" {
		if err := os.WriteFile(file, []byte(credJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s.cloud = &antigravityCloud{
		client: srv.Client(), tokenURL: srv.URL + "/token", quotaURLs: []string{srv.URL + "/quota"}, loadURL: srv.URL + "/load",
		files: []string{file}, keychain: func(context.Context) ([]byte, error) { return nil, errors.New("none") },
		clientID: "cid", clientSec: "csec", userAgent: "ua", now: time.Now,
	}
	return s
}

func agyCred(access string, expiresIn time.Duration) string {
	return fmt.Sprintf(`{"token":{"access_token":%q,"refresh_token":"refresh-1","expiry":%q}}`, access, time.Now().Add(expiresIn).Format(time.RFC3339Nano))
}

func TestAntigravityCloudUsesSavedTokenWhileValid(t *testing.T) {
	s := newAgyServers(t, agyCred("saved-access", time.Hour))
	snap, err := s.cloud.Fetch(ctx5(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.refreshes != 0 || s.quotaAuth != "Bearer saved-access" || s.loadAuth != "Bearer saved-access" {
		t.Fatalf("refreshes=%d quotaAuth=%q", s.refreshes, s.quotaAuth)
	}
	if snap.Plan != "Google AI Pro" || len(snap.Windows) != 2 {
		t.Fatalf("snap = %+v", snap)
	}
	five, week := snap.Windows[0], snap.Windows[1]
	if five.Name != "5-hour" || five.Group != "Claude and GPT models" || math.Abs(five.UsedPct-10) > 0.001 || five.Span != 5*time.Hour {
		t.Errorf("five = %+v", five)
	}
	if week.Name != "Weekly" || math.Abs(week.UsedPct-25) > 0.001 || week.Span != 7*24*time.Hour || week.Resets.IsZero() {
		t.Errorf("week = %+v", week)
	}
}

func TestAntigravityCloudRefreshesAnExpiredTokenInMemory(t *testing.T) {
	s := newAgyServers(t, agyCred("old-access", -time.Hour))
	if _, err := s.cloud.Fetch(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	if s.refreshes != 1 || s.quotaAuth != "Bearer fresh-access" {
		t.Fatalf("refreshes=%d quotaAuth=%q", s.refreshes, s.quotaAuth)
	}
	if s.form.Get("refresh_token") != "refresh-1" || s.form.Get("client_id") != "cid" || s.form.Get("grant_type") != "refresh_token" {
		t.Errorf("refresh form = %v", s.form)
	}
	// The refreshed token is reused; the saved file is never written back.
	if _, err := s.cloud.Fetch(ctx5(t)); err != nil || s.refreshes != 1 {
		t.Fatalf("second read refreshed again: %d, %v", s.refreshes, err)
	}
	if raw, _ := os.ReadFile(s.cloud.files[0]); strings.Contains(string(raw), "fresh-access") {
		t.Fatal("refreshed token was written back to Antigravity's file")
	}
}

func TestAntigravityCloudWithoutASavedSignIn(t *testing.T) {
	s := newAgyServers(t, "")
	if _, err := s.cloud.Fetch(ctx5(t)); !errors.Is(err, ErrSetup) {
		t.Fatalf("err = %v, want ErrSetup", err)
	}
}

func TestParseAgyCredentialsKeyringWrapper(t *testing.T) {
	raw := agyKeyringB64 + base64.StdEncoding.EncodeToString([]byte(agyCred("kc-access", time.Hour)))
	cred, ok := parseAgyCredentials([]byte(raw))
	if !ok || cred.Token.Access != "kc-access" {
		t.Fatalf("cred = %+v ok=%v", cred, ok)
	}
	if _, ok := parseAgyCredentials([]byte("garbage")); ok {
		t.Fatal("garbage parsed as credentials")
	}
}

func TestAntigravityNeedsPermissionForTheSavedSignIn(t *testing.T) {
	s := newAgyServers(t, agyCred("saved-access", time.Hour))
	down := &antigravity{client: http.DefaultClient, discover: func(context.Context) (string, []int, error) {
		return "", nil, fmt.Errorf("%w: Antigravity is not running", ErrUnavailable)
	}}
	allowed := false
	both := &antigravityBoth{cloud: s.cloud, local: down, allowed: func() bool { return allowed }}
	if _, err := both.Fetch(ctx5(t)); !errors.Is(err, ErrSetup) || s.quotaCalls != 0 {
		t.Fatalf("without permission: err=%v quotaCalls=%d (must not touch the sign-in)", err, s.quotaCalls)
	}
	allowed = true
	snap, err := both.Fetch(ctx5(t))
	if err != nil || len(snap.Windows) != 2 {
		t.Fatalf("with permission: %+v, %v", snap, err)
	}
	// No saved sign-in and no running app.
	empty := newAgyServers(t, "")
	both = &antigravityBoth{cloud: empty.cloud, local: down, allowed: func() bool { return true }}
	if _, err := both.Fetch(ctx5(t)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nothing available: err = %v", err)
	}
}
