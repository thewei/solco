package remotehost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

/**
 * [INPUT]: server_test.go 的 harness、sessions.go 的 handler 与 Options.Sessions 缝
 * [OUTPUT]: /api/v3/sessions 的行为回归：无缝 404、需配对、q/limit 传递与上限、坏参数 400、sessions 永不为 null、能力位
 * [POS]: sessions.go 的守卫测试；缝由测试注入，绝不读真实会话目录
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type fakeSessions struct {
	mu      sync.Mutex
	queries []SessionsQuery
	out     Sessions
}

func (f *fakeSessions) serve(_ context.Context, q SessionsQuery) (Sessions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	return f.out, nil
}

func (f *fakeSessions) seen() []SessionsQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SessionsQuery(nil), f.queries...)
}

func sessionsHarness(t *testing.T) (*harness, *fakeSessions) {
	t.Helper()
	h := newHarness(t)
	f := &fakeSessions{}
	h.srv.opts.Sessions = f.serve
	return h, f
}

func TestSessionsAbsentWithoutSource(t *testing.T) {
	h := newHarness(t)
	if resp := h.get("/api/v3/sessions", h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "sessions_unavailable" {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestSessionsNeedAuth(t *testing.T) {
	h, f := sessionsHarness(t)
	if resp := h.get("/api/v3/sessions", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if len(f.seen()) != 0 {
		t.Fatal("an unauthenticated read reached Sessions")
	}
}

func TestSessionsForwardSearchAndBoundTheLimit(t *testing.T) {
	h, f := sessionsHarness(t)
	for _, path := range []string{"/api/v3/sessions", "/api/v3/sessions?q=%20tests%20&limit=5", "/api/v3/sessions?limit=9999"} {
		if resp := h.get(path, h.token, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
	}
	got := f.seen()
	want := []SessionsQuery{{Limit: sessionsDefaultLimit}, {Search: "tests", Limit: 5}, {Limit: sessionsMaxLimit}}
	if len(got) != len(want) {
		t.Fatalf("queries %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("query %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSessionsRefuseBadParameters(t *testing.T) {
	h, f := sessionsHarness(t)
	for _, path := range []string{"/api/v3/sessions?limit=0", "/api/v3/sessions?limit=x", "/api/v3/sessions?q=" + strings.Repeat("a", sessionsMaxQuery+1)} {
		if resp := h.get(path, h.token, nil); resp.StatusCode != http.StatusBadRequest || errCode(t, resp) != "bad_request" {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
	}
	if len(f.seen()) != 0 {
		t.Fatal("a refused request reached Sessions")
	}
}

func TestSessionsNeverNullAndAdvertised(t *testing.T) {
	h, _ := sessionsHarness(t)
	var out struct {
		Sessions []SessionRow `json:"sessions"`
	}
	if err := json.NewDecoder(h.get("/api/v3/sessions", h.token, nil).Body).Decode(&out); err != nil || out.Sessions == nil {
		t.Fatalf("sessions decode %v, body %#v", err, out.Sessions)
	}
	var body struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Capabilities["sessions"] {
		t.Fatalf("capabilities %v", body.Capabilities)
	}
}

// sampleSessions is the golden file's desktop: one session an Agent is running
// (waiting on the user), one finished one, and a search that narrows the list.
func sampleSessions(q SessionsQuery) Sessions {
	all := []SessionRow{
		{ID: "claude-code:7f3a", Agent: "claude-code", Title: "Fix the failing tests", Project: "solco", UpdatedMs: 1785549600000, Messages: 42,
			Running: &SessionRunning{Instance: "default", PaneID: "w1:p1", State: "attention"}},
		{ID: "codex:19bd", Agent: "codex", Title: "Refactor the tab bar", Project: "solco", UpdatedMs: 1785463200000, Messages: 18},
	}
	if q.Search == "" {
		return Sessions{Sessions: all}
	}
	var hits []SessionRow
	for _, row := range all {
		if strings.Contains(strings.ToLower(row.Title), strings.ToLower(q.Search)) {
			hits = append(hits, row)
		}
	}
	return Sessions{Sessions: hits}
}

// fakeContinuer answers one scripted result and records what it was asked.
type fakeContinuer struct {
	mu       sync.Mutex
	asked    []string
	instance string
	result   ContinueResult
	err      error
}

func (f *fakeContinuer) Continue(_ context.Context, instance, sessionID string) (ContinueResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, sessionID)
	f.instance = instance
	return f.result, f.err
}

func (f *fakeContinuer) sessions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

func continueHarness(t *testing.T) (*harness, *fakeContinuer) {
	t.Helper()
	h := newHarness(t)
	f := &fakeContinuer{result: ContinueResult{PaneID: "w1:p2"}}
	h.srv.opts.Sessions = (&fakeSessions{}).serve
	h.srv.opts.Continuer = f
	return h, f
}

func TestSessionContinueAbsentWithoutSource(t *testing.T) {
	h := newHarness(t)
	if resp := h.launch("/api/v3/sessions/codex:19bd/continue", h.token, ""); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "continue_unavailable" {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestSessionContinueNeedsManageScope(t *testing.T) {
	h, f := continueHarness(t)
	if resp := h.launch("/api/v3/sessions/codex:19bd/continue", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if len(f.sessions()) != 0 {
		t.Fatal("an unauthenticated request reached the Continuer")
	}
}

func TestSessionContinueReturnsTheVisiblePane(t *testing.T) {
	h, f := continueHarness(t)
	resp := h.launch("/api/v3/sessions/codex:19bd/continue", h.token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		PaneID      string `json:"paneId"`
		TerminalID  string `json:"terminalId"`
		AlreadyLive bool   `json:"alreadyLive"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.PaneID != "w1:p2" || out.TerminalID == "" || out.AlreadyLive {
		t.Fatalf("answer %+v, %v", out, err)
	}
	if got := f.sessions(); len(got) != 1 || got[0] != "codex:19bd" || f.instance != "default" {
		t.Fatalf("asked %v on %q", got, f.instance)
	}
}

func TestSessionContinueRelaysRefusalsWithTheirCodes(t *testing.T) {
	h, f := continueHarness(t)
	f.err = &LaunchError{Code: "needs_project", Status: http.StatusConflict, Detail: "no folder"}
	if resp := h.launch("/api/v3/sessions/codex:19bd/continue", h.token, ""); resp.StatusCode != http.StatusConflict || errCode(t, resp) != "needs_project" {
		t.Fatalf("status %d", resp.StatusCode)
	}
	f.err = errors.New("boom")
	if resp := h.launch("/api/v3/sessions/codex:19bd/continue", h.token, ""); resp.StatusCode != http.StatusBadGateway || errCode(t, resp) != "launch_failed" {
		t.Fatalf("status %d", resp.StatusCode)
	}
	f.err = nil
	f.result = ContinueResult{PaneID: "w9:gone"}
	prev := launchVisible
	launchVisible = 150 * time.Millisecond
	defer func() { launchVisible = prev }()
	if resp := h.launch("/api/v3/sessions/codex:19bd/continue", h.token, ""); resp.StatusCode != http.StatusGatewayTimeout || errCode(t, resp) != "launch_uncertain" {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestSessionContinueIsAdvertisedOnlyWithSessions(t *testing.T) {
	h, _ := continueHarness(t)
	var body struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Capabilities["sessions"] || !body.Capabilities["sessionContinue"] {
		t.Fatalf("capabilities %v", body.Capabilities)
	}
	h.srv.opts.Sessions = nil
	body.Capabilities = nil
	if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Capabilities["sessionContinue"] {
		t.Fatalf("Continue advertised without the Sessions view: %v", body.Capabilities)
	}
}
