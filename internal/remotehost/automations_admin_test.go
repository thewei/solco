package remotehost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

/**
 * [INPUT]: server_test.go 的 harness、automations_admin.go 的 handler 与 Options.AutomationsAdmin 缝（fakeAdmin）
 * [OUTPUT]: 手机 Automations 管理端点的行为回归：无缝/门关 404、需配对且写需 manage、请求形状（名字/长度/控制字符/未知字段值）在触达缝之前就被拒、每个动作转发到缝且答新列表、拒绝带插件原文、capability 随缝与门
 * [POS]: automations_admin.go 的守卫测试；缝由测试注入，绝不读真实插件
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type fakeAdmin struct {
	open  bool
	calls []string
	fail  error
	last  AutomationFields
}

func (f *fakeAdmin) Open() bool { return f.open }
func (f *fakeAdmin) Detail(_ context.Context, name string) (AutomationDetail, error) {
	f.calls = append(f.calls, "detail "+name)
	if f.fail != nil {
		return AutomationDetail{}, f.fail
	}
	return sampleAutomationDetail(), nil
}
func (f *fakeAdmin) Create(_ context.Context, name string, fl AutomationFields) (Automations, error) {
	f.calls, f.last = append(f.calls, "create "+name), fl
	return sampleAutomations(), f.fail
}
func (f *fakeAdmin) Update(_ context.Context, name string, fl AutomationFields) (Automations, error) {
	f.calls, f.last = append(f.calls, "update "+name), fl
	return sampleAutomations(), f.fail
}
func (f *fakeAdmin) Do(_ context.Context, name string, op AutomationOp) (Automations, error) {
	f.calls = append(f.calls, string(op)+" "+name)
	return sampleAutomations(), f.fail
}
func (f *fakeAdmin) StartScheduler(context.Context) (Automations, error) {
	f.calls = append(f.calls, "scheduler")
	return sampleAutomations(), f.fail
}
func (f *fakeAdmin) MarkSeen(_ context.Context, runID string, all bool) (Automations, error) {
	f.calls = append(f.calls, "seen "+runID+map[bool]string{true: " all", false: ""}[all])
	return sampleAutomations(), f.fail
}

func sampleAutomationDetail() AutomationDetail {
	return AutomationDetail{
		Name: "nightly-ci-triage", State: "active", Schedule: "Daily 03:00", NextRunMs: 1786129200000,
		AutomationFields: AutomationFields{
			Cron: "0 3 * * *", Repo: "/Users/me/work/api", Workspace: "worktree", Agent: "claude", Model: "sonnet",
			Prompt: "Triage the failing CI jobs and open one issue per root cause.", Rationale: "Goal: keep main green. Guardrail: read-only except issues.",
			OnOverlap: "skip", Headless: true, TimeoutMinutes: 45, CatchUpMinutes: 30, CleanupTTLHours: 72,
		},
		Runs: []AutomationRunInfo{
			{ID: "run-7", Status: "failed", Trigger: "schedule", AtMs: 1786042800000, DurationSec: 31, Agent: "claude", WorkspaceID: "w_1", PaneID: "p_9", Unread: true},
			{ID: "run-6", Status: "done", Trigger: "manual", AtMs: 1785956400000, DurationSec: 412, Agent: "claude"},
		},
	}
}

func adminHarness(t *testing.T, open bool) (*harness, *fakeAdmin) {
	t.Helper()
	h := automationsHarness(t, sampleAutomations(), true)
	fa := &fakeAdmin{open: open}
	h.srv.opts.AutomationsAdmin = fa
	return h, fa
}

func (h *harness) send(method, path, token, body string) (int, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.http.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func codeOf(raw []byte) string {
	var out struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.Error.Code
}

func TestAutomationsAdminAbsentOrClosed(t *testing.T) {
	h := automationsHarness(t, sampleAutomations(), true)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v3/automations/x"}, {"POST", "/api/v3/automations"}, {"POST", "/api/v3/automations/x/save"},
		{"POST", "/api/v3/automations/x/approve"}, {"POST", "/api/v3/automations/scheduler/start"}, {"POST", "/api/v3/automations/seen"},
	} {
		if st, raw := h.send(c.method, c.path, h.token, `{}`); st != http.StatusNotFound || codeOf(raw) != "automation_unavailable" {
			t.Errorf("no seam %s %s: %d %s", c.method, c.path, st, raw)
		}
	}
	h, fa := adminHarness(t, false)
	if st, _ := h.send("POST", "/api/v3/automations/x/run", h.token, ""); st != http.StatusNotFound || len(fa.calls) != 0 {
		t.Fatalf("closed gate: %d %v", st, fa.calls)
	}
}

func TestAutomationsAdminNeedsAuthAndForwardsEveryVerb(t *testing.T) {
	h, fa := adminHarness(t, true)
	if st, _ := h.send("POST", "/api/v3/automations/x/approve", "", ""); st != http.StatusUnauthorized {
		t.Fatalf("no token: %d", st)
	}
	for op, want := range map[string]string{
		"approve": "approve nightly", "run": "run nightly", "pause": "pause nightly", "resume": "resume nightly", "delete": "delete nightly",
	} {
		fa.calls = nil
		st, raw := h.send("POST", "/api/v3/automations/nightly/"+op, h.token, "")
		if st != http.StatusOK || len(fa.calls) != 1 || fa.calls[0] != want {
			t.Fatalf("%s: %d %v %s", op, st, fa.calls, raw)
		}
		var out Automations
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Automations) == 0 {
			t.Fatalf("%s answered no fresh list: %v %s", op, err, raw)
		}
	}
	fa.calls = nil
	h.send("POST", "/api/v3/automations/scheduler/start", h.token, "")
	h.send("POST", "/api/v3/automations/seen", h.token, `{"runId":"run-7"}`)
	h.send("POST", "/api/v3/automations/seen", h.token, `{"all":true}`)
	if strings.Join(fa.calls, "|") != "scheduler|seen run-7|seen  all" {
		t.Fatalf("calls %v", fa.calls)
	}
}

func TestAutomationsAdminBodiesAreCheckedBeforeTheSeam(t *testing.T) {
	h, fa := adminHarness(t, true)
	long := strings.Repeat("a", maxAutomationName+1)
	for name, c := range map[string]struct{ method, path, body string }{
		"create bad json":   {"POST", "/api/v3/automations", `{`},
		"create no name":    {"POST", "/api/v3/automations", `{"cron":"* * * * *"}`},
		"create long name":  {"POST", "/api/v3/automations", `{"name":"` + long + `"}`},
		"create newline":    {"POST", "/api/v3/automations", `{"name":"a\nb"}`},
		"create long cron":  {"POST", "/api/v3/automations", `{"name":"a","cron":"` + strings.Repeat("1", maxAutomationShort+1) + `"}`},
		"create neg number": {"POST", "/api/v3/automations", `{"name":"a","timeoutMinutes":-1}`},
		"update bad json":   {"POST", "/api/v3/automations/a/save", `[]`},
		"seen neither":      {"POST", "/api/v3/automations/seen", `{}`},
		"seen both":         {"POST", "/api/v3/automations/seen", `{"runId":"r","all":true}`},
		"seen flag-like":    {"POST", "/api/v3/automations/seen", `{"runId":"--all"}`},
	} {
		if st, raw := h.send(c.method, c.path, h.token, c.body); st != http.StatusBadRequest || codeOf(raw) != "bad_request" {
			t.Errorf("%s: %d %s", name, st, raw)
		}
	}
	if len(fa.calls) != 0 {
		t.Fatalf("a bad request reached the seam: %v", fa.calls)
	}
}

func TestAutomationsAdminCreateAndUpdatePassTheFields(t *testing.T) {
	h, fa := adminHarness(t, true)
	body := `{"name":"weekly","cron":"0 9 * * 1","repo":"/r","workspace":"root","agent":"codex","prompt":"- check the logs","rationale":"why","onOverlap":"queue","headless":true,"timeoutMinutes":20}`
	if st, raw := h.send("POST", "/api/v3/automations", h.token, body); st != http.StatusOK {
		t.Fatalf("create: %d %s", st, raw)
	}
	if fa.calls[0] != "create weekly" || fa.last.Prompt != "- check the logs" || !fa.last.Headless || fa.last.OnOverlap != "queue" || fa.last.TimeoutMinutes != 20 {
		t.Fatalf("create forwarded %v %+v", fa.calls, fa.last)
	}
	if st, raw := h.send("POST", "/api/v3/automations/weekly/save", h.token, `{"cron":"0 10 * * 1"}`); st != http.StatusOK || fa.calls[1] != "update weekly" || fa.last.Cron != "0 10 * * 1" {
		t.Fatalf("update: %d %s %v", st, raw, fa.calls)
	}
}

func TestAutomationsAdminRefusalsCarryStableCodes(t *testing.T) {
	h, fa := adminHarness(t, true)
	fa.fail = &AutomationError{Code: "automation_refused", Status: http.StatusConflict, Detail: "cron: expected 5 fields"}
	st, raw := h.send("POST", "/api/v3/automations/x/approve", h.token, "")
	if st != http.StatusConflict || codeOf(raw) != "automation_refused" || !strings.Contains(string(raw), "expected 5 fields") {
		t.Fatalf("refusal: %d %s", st, raw)
	}
	fa.fail = io.ErrUnexpectedEOF
	if st, raw := h.send("POST", "/api/v3/automations/x/run", h.token, ""); st != http.StatusBadGateway || codeOf(raw) != "automation_failed" || strings.Contains(string(raw), "EOF") {
		t.Fatalf("plain failure leaked: %d %s", st, raw)
	}
	fa.fail = &AutomationError{Code: "unknown_automation", Status: http.StatusNotFound}
	if st, raw := h.send("GET", "/api/v3/automations/ghost", h.token, ""); st != http.StatusNotFound || codeOf(raw) != "unknown_automation" {
		t.Fatalf("unknown: %d %s", st, raw)
	}
}

func TestAutomationsManageCapabilityFollowsTheSeamAndTheGate(t *testing.T) {
	for _, c := range []struct {
		admin bool
		open  bool
		want  bool
	}{{false, false, false}, {true, false, false}, {true, true, true}} {
		h := automationsHarness(t, sampleAutomations(), true)
		if c.admin {
			h.srv.opts.AutomationsAdmin = &fakeAdmin{open: c.open}
		}
		var boot struct {
			Capabilities map[string]bool `json:"capabilities"`
		}
		if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&boot); err != nil {
			t.Fatal(err)
		}
		if boot.Capabilities["automationsManage"] != c.want {
			t.Errorf("%+v: capabilities %v", c, boot.Capabilities)
		}
	}
}

func TestAutomationDetailNeverHasNullRuns(t *testing.T) {
	h, fa := adminHarness(t, true)
	_ = fa
	h.srv.opts.AutomationsAdmin = emptyRunsAdmin{fa}
	st, raw := h.send("GET", "/api/v3/automations/x", h.token, "")
	if st != http.StatusOK || !strings.Contains(string(raw), `"runs":[]`) {
		t.Fatalf("%d %s", st, raw)
	}
}

type emptyRunsAdmin struct{ *fakeAdmin }

func (emptyRunsAdmin) Detail(context.Context, string) (AutomationDetail, error) {
	return AutomationDetail{Name: "x", State: "draft"}, nil
}
