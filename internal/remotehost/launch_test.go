package remotehost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeLauncher struct {
	mu    sync.Mutex
	specs []LaunchSpec
	pane  string
	// pending: the fake reports the agent blocked before the prompt could go out.
	pending bool
	err     error
}

func (f *fakeLauncher) Kinds() []LaunchKind {
	return []LaunchKind{{ID: LaunchTerminal, Label: "Terminal"}, {ID: "claude-code", Label: "Claude Code"}}
}

func (f *fakeLauncher) Launch(_ context.Context, _ string, spec LaunchSpec) (LaunchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs = append(f.specs, spec)
	return LaunchResult{PaneID: f.pane, PromptPending: f.pending}, f.err
}

func (f *fakeLauncher) last() (LaunchSpec, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.specs) == 0 {
		return LaunchSpec{}, 0
	}
	return f.specs[len(f.specs)-1], len(f.specs)
}

func (h *harness) launch(path, token, body string) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest("POST", h.http.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func errCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	raw, _ := io.ReadAll(resp.Body)
	var e struct{ Error struct{ Code string } }
	_ = json.Unmarshal(raw, &e)
	if e.Error.Code == "" {
		var flat struct{ Code string }
		_ = json.Unmarshal(raw, &flat)
		return flat.Code
	}
	return e.Error.Code
}

func launcherHarness(t *testing.T) (*harness, *fakeLauncher) {
	t.Helper()
	h := newHarness(t)
	l := &fakeLauncher{pane: "w1:p2"}
	h.srv.opts.Launcher = l
	return h, l
}

const launchID = "req_0123456789"

func TestLaunchUnavailableWithoutLauncher(t *testing.T) {
	h := newHarness(t)
	resp := h.launch("/api/v3/launch", h.token, `{"requestId":"`+launchID+`","workspaceId":"w1","kind":"terminal"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
}

func TestLaunchNeedsAuth(t *testing.T) {
	h, l := launcherHarness(t)
	resp := h.launch("/api/v3/launch", "", `{"requestId":"`+launchID+`","workspaceId":"w1","kind":"terminal"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if _, n := l.last(); n != 0 {
		t.Fatal("an unauthenticated launch reached the launcher")
	}
}

func TestLaunchRejectsBadRequests(t *testing.T) {
	h, l := launcherHarness(t)
	cases := map[string]string{
		"not json":         `{`,
		"short request id": `{"requestId":"x","workspaceId":"w1","kind":"terminal"}`,
		"bad request id":   `{"requestId":"req 0123456789!","workspaceId":"w1","kind":"terminal"}`,
		"unknown kind":     `{"requestId":"` + launchID + `","workspaceId":"w1","kind":"rm -rf"}`,
		"no target":        `{"requestId":"` + launchID + `","kind":"terminal"}`,
		"two targets":      `{"requestId":"` + launchID + `","workspaceId":"w1","path":"/tmp","kind":"terminal"}`,
		"relative path":    `{"requestId":"` + launchID + `","path":"work/solco","kind":"terminal"}`,
		"dotdot path":      `{"requestId":"` + launchID + `","path":"/work/../etc","kind":"terminal"}`,
		"newline in path":  `{"requestId":"` + launchID + `","path":"/work\n/x","kind":"terminal"}`,
		"terminal prompt":  `{"requestId":"` + launchID + `","workspaceId":"w1","kind":"terminal","prompt":"ls"}`,
		"huge prompt":      `{"requestId":"` + launchID + `","workspaceId":"w1","kind":"claude-code","prompt":"` + strings.Repeat("a", maxLaunchPrompt+1) + `"}`,
	}
	for name, body := range cases {
		resp := h.launch("/api/v3/launch", h.token, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, resp.StatusCode)
		}
	}
	if _, n := l.last(); n != 0 {
		t.Fatalf("a refused request reached the launcher (%d calls)", n)
	}
}

func TestLaunchUnknownInstanceAndWorkspace(t *testing.T) {
	h, l := launcherHarness(t)
	resp := h.launch("/api/v3/launch?instance=ghost", h.token, `{"requestId":"`+launchID+`","workspaceId":"w1","kind":"terminal"}`)
	if resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "unknown_instance" {
		t.Fatalf("ghost instance: status %d", resp.StatusCode)
	}
	resp = h.launch("/api/v3/launch", h.token, `{"requestId":"`+launchID+`","workspaceId":"w9","kind":"terminal"}`)
	if resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "unknown_workspace" {
		t.Fatalf("ghost workspace: status %d", resp.StatusCode)
	}
	if _, n := l.last(); n != 0 {
		t.Fatal("an unknown target reached the launcher")
	}
}

func TestLaunchInWorkspaceUsesItsDirectory(t *testing.T) {
	h, l := launcherHarness(t)
	resp := h.launch("/api/v3/launch", h.token, `{"requestId":"`+launchID+`","workspaceId":"w1","kind":"claude-code","prompt":"  fix the build  "}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out launchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.PaneID != "w1:p2" || out.TerminalID != "term_b" || out.WorkspaceID != "w1" || out.Agent {
		t.Fatalf("response %+v", out)
	}
	spec, _ := l.last()
	if spec.CWD != "/work/solco" || spec.WorkspaceID != "w1" || spec.Kind != "claude-code" || spec.Prompt != "fix the build" || spec.RequestID != launchID {
		t.Fatalf("spec %+v", spec)
	}
}

func TestLaunchByPathPassesItThrough(t *testing.T) {
	h, l := launcherHarness(t)
	resp := h.launch("/api/v3/launch", h.token, `{"requestId":"`+launchID+`","path":"/Users/me/new proj","kind":"terminal"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	spec, _ := l.last()
	if spec.CWD != "/Users/me/new proj" || spec.WorkspaceID != "" {
		t.Fatalf("spec %+v", spec)
	}
}

func TestLaunchWorkspaceWithoutDirectory(t *testing.T) {
	h, l := launcherHarness(t)
	proj := fixtureProjection()
	proj.Projects[0].CWD = ""
	for i := range proj.Panes {
		proj.Panes[i].CWD = ""
	}
	h.back.set(proj)
	resp := h.launch("/api/v3/launch", h.token, `{"requestId":"`+launchID+`","workspaceId":"w1","kind":"terminal"}`)
	if resp.StatusCode != http.StatusConflict || errCode(t, resp) != "workspace_no_directory" {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if _, n := l.last(); n != 0 {
		t.Fatal("a workspace without a directory reached the launcher")
	}
}

func TestLaunchRelaysRefusalsAndHidesPlainErrors(t *testing.T) {
	h, l := launcherHarness(t)
	body := `{"requestId":"` + launchID + `","workspaceId":"w1","kind":"terminal"}`

	l.err = &LaunchError{Code: "path_unavailable", Status: http.StatusBadRequest, Detail: "not a directory"}
	resp := h.launch("/api/v3/launch", h.token, body)
	if resp.StatusCode != http.StatusBadRequest || errCode(t, resp) != "path_unavailable" {
		t.Fatalf("refusal: status %d", resp.StatusCode)
	}

	l.err = io.ErrUnexpectedEOF
	resp = h.launch("/api/v3/launch", h.token, body)
	if resp.StatusCode != http.StatusBadGateway || errCode(t, resp) != "launch_failed" {
		t.Fatalf("plain error: status %d", resp.StatusCode)
	}
}

func TestLaunchAnswersUncertainWhenPaneNeverShows(t *testing.T) {
	old := launchVisible
	launchVisible = 300 * time.Millisecond
	t.Cleanup(func() { launchVisible = old })
	h, l := launcherHarness(t)
	l.pane = "w1:p99"
	resp := h.launch("/api/v3/launch", h.token, `{"requestId":"`+launchID+`","workspaceId":"w1","kind":"terminal"}`)
	if resp.StatusCode != http.StatusGatewayTimeout || errCode(t, resp) != "launch_uncertain" {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestBootstrapAdvertisesLaunchOnlyWithALauncher(t *testing.T) {
	h := newHarness(t)
	var plain struct {
		Capabilities map[string]bool `json:"capabilities"`
		LaunchKinds  []LaunchKind    `json:"launchKinds"`
	}
	_ = json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&plain)
	if plain.Capabilities["launch"] || plain.LaunchKinds != nil {
		t.Fatalf("a Host without a launcher advertised launch: %+v", plain)
	}
	h.srv.opts.Launcher = &fakeLauncher{}
	var with struct {
		Capabilities map[string]bool `json:"capabilities"`
		LaunchKinds  []LaunchKind    `json:"launchKinds"`
	}
	_ = json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&with)
	if !with.Capabilities["launch"] || len(with.LaunchKinds) != 2 || with.LaunchKinds[0].ID != LaunchTerminal {
		t.Fatalf("launch not advertised: %+v", with)
	}
}

func TestLaunchReportsPromptPending(t *testing.T) {
	h, l := launcherHarness(t)
	body := `{"requestId":"` + launchID + `","workspaceId":"w1","kind":"claude-code","prompt":"fix it"}`
	var plain, pending launchResponse
	if err := json.NewDecoder(h.launch("/api/v3/launch", h.token, body).Body).Decode(&plain); err != nil {
		t.Fatal(err)
	}
	if plain.PromptPending {
		t.Fatal("a delivered prompt must not be flagged pending")
	}
	l.pending = true
	if err := json.NewDecoder(h.launch("/api/v3/launch", h.token, body).Body).Decode(&pending); err != nil {
		t.Fatal(err)
	}
	if !pending.PromptPending || pending.PaneID != "w1:p2" {
		t.Fatalf("blocked agent: %+v", pending)
	}
}
