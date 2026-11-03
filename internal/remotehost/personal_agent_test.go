package remotehost

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/internal/personalagent"
)

/**
 * [INPUT]: server_test.go 的 harness、personal_agent.go 与 personalagent.NewClientWithExec（假插件 CLI，绝不跑真二进制）
 * [OUTPUT]: 对外提供 /api/v3/personal-agent/* 的行为回归与 golden（bootstrap-personal-agent/personal-agent-*）：无缝/门关 404、需配对、写入需 manage、坏请求在触达核心前被拒、插件拒绝码→HTTP、requestId/expectedRevision/actionHash 原样转发、events 帧只带计数
 * [POS]: personal_agent.go 的守卫测试；核心由 fakeCore 以 CLI 同一套 JSON 回答
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	paAssistantA = "a_0000000000000001"
	paGoalG      = "g_0000000000000002"
	paTaskT      = "t_0000000000000004"
	paApprovalP  = "p_0000000000000007"
	paHash       = "0000000000000000000000000000000000000000000000000000000000000000"
)

// fakeCore answers the plugin's `assistant …` commands the way the real CLI
// does: one JSON object per call, refusals as {"error":{code,message}}.
type fakeCore struct {
	mu    sync.Mutex
	calls []string // "<args joined>|<stdin>"
	fail  *personalagent.Error
}

func (f *fakeCore) exec(_ context.Context, stdin string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " ")+"|"+stdin)
	if f.fail != nil && len(args) > 1 && args[1] != "list" && args[1] != "overview" {
		raw, _ := json.Marshal(map[string]any{"schema": 1, "error": f.fail})
		return raw, nil
	}
	key := strings.Join(args[1:], " ")
	switch {
	case strings.HasPrefix(key, "inbox"):
		return []byte(`{"schema":1,"inbox":{"items":[{"id":"task:` + paTaskT + `","kind":"stalled","assistantId":"` + paAssistantA + `","taskId":"` + paTaskT + `","title":"Check it","detail":"Has not started","priority":3,"at":1800000000000}],"needsYou":1,"seq":4}}`), nil
	case key == "overview":
		return []byte(`{"schema":1,"needsYou":1,"assistants":[{"id":"` + paAssistantA + `","name":"Ada","status":"active","revision":1,"goals":1,"tasks":1,"running":0,"queued":0,"done":1,"needsYou":0,"approvals":1}]}`), nil
	case key == "list":
		return []byte(`{"schema":1,"assistants":[` + paAssistant + `]}`), nil
	case key == "create", strings.HasPrefix(key, "rename "), strings.HasPrefix(key, "pause "), strings.HasPrefix(key, "resume "), strings.HasPrefix(key, "enable "), strings.HasPrefix(key, "disable "):
		return []byte(`{"schema":1,"assistant":` + paAssistant + `}`), nil
	case strings.HasPrefix(key, "goal list"):
		return []byte(`{"schema":1,"goals":[` + paGoal + `]}`), nil
	case strings.HasPrefix(key, "goal "):
		return []byte(`{"schema":1,"replayed":false,"goal":` + paGoal + `}`), nil
	case strings.HasPrefix(key, "task list"):
		return []byte(`{"schema":1,"tasks":[` + paTask + `]}`), nil
	case strings.HasPrefix(key, "task show"):
		return []byte(`{"schema":1,"task":` + paTask + `,"runs":[` + paRun + `]}`), nil
	case strings.HasPrefix(key, "task "):
		return []byte(`{"schema":1,"replayed":false,"task":` + paTask + `}`), nil
	case strings.HasPrefix(key, "approval list"):
		return []byte(`{"schema":1,"approvals":[` + paApproval + `]}`), nil
	case strings.HasPrefix(key, "approval decide"):
		return []byte(`{"schema":1,"replayed":false,"approval":` + paApproval + `}`), nil
	case strings.HasPrefix(key, "memory visible"):
		return []byte(`{"schema":1,"memory":[` + paMemory + `]}`), nil
	}
	return []byte(`{"schema":1,"error":{"code":"invalid","message":"unknown command"}}`), nil
}

func (f *fakeCore) lastCall() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeCore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

const (
	paAssistant = `{"id":"` + paAssistantA + `","name":"Ada","description":"Follows my pull requests","scope":"personal","status":"active","defaultPolicy":"review","coordinatorSession":"","rolePrompt":"","workspaceRefs":[],"revision":1,"createdAt":1800000000000,"updatedAt":1800000000000}`
	paGoal      = `{"id":"` + paGoalG + `","assistantId":"` + paAssistantA + `","title":"Watch PR 12","objective":"Tell me when it changes","state":"active","evidence":"","revision":2,"createdAt":1800000000000,"updatedAt":1800000000000,"commitments":[{"id":"c_0000000000000003","goalId":"` + paGoalG + `","assistantId":"` + paAssistantA + `","kind":"pr_watch","spec":"{\"repo\":\"o/r\",\"number\":12}","successCriteria":"merged","exitCondition":"merged or closed","state":"active","cursor":"","lastError":"","lastCheckedAt":0,"nextCheckAt":0,"revision":2,"createdAt":1800000000000,"updatedAt":1800000000000}]}`
	paTask      = `{"id":"` + paTaskT + `","assistantId":"` + paAssistantA + `","goalId":"` + paGoalG + `","parentTaskId":"","ruleId":"","kind":"research","title":"Check the PR","brief":"Read it.","agent":"claude","model":"","repoPath":"","state":"running","reviewState":"","needsYou":false,"attempt":1,"revision":5,"createdAt":1800000000000,"updatedAt":1800000000000}`
	paRun       = `{"id":"r_0000000000000005","taskId":"` + paTaskT + `","ruleId":"","eventId":"","idempotencyKey":"task:` + paTaskT + `:1","claimant":"daemon","status":"running","herdrSession":"default","workspaceId":"w1","paneId":"w1:p1","agentSessionId":"","label":"","result":"","error":"","startedAt":1800000000000,"finishedAt":0,"createdAt":1800000000000}`
	paApproval  = `{"id":"` + paApprovalP + `","assistantId":"` + paAssistantA + `","subjectKind":"task","subjectId":"` + paTaskT + `","action":"merge work/x","actionHash":"` + paHash + `","expectedRevision":5,"state":"pending","createdAt":1800000000000,"expiresAt":1800003600000,"decidedAt":0}`
	paMemory    = `{"id":"m_0000000000000006","assistantId":"` + paAssistantA + `","kind":"fact","content":"Prefers short reports","sourceRef":"run:r_1","scope":"assistant","sharedWith":[],"state":"approved","revision":2,"createdAt":1800000000000,"updatedAt":1800000000000}`
)

func paHarness(t *testing.T, open bool) (*harness, *fakeCore) {
	t.Helper()
	h := newHarness(t)
	core := &fakeCore{}
	client := personalagent.NewClientWithExec(core.exec)
	h.srv.opts.PersonalAgent = func(context.Context) (*personalagent.Client, bool) { return client, open }
	return h, core
}

func TestPersonalAgentAbsentWithoutSeamOrWithClosedGate(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/overview", "/assistants", "/assistants/" + paAssistantA, "/goals?assistant=" + paAssistantA, "/tasks/" + paTaskT, "/approvals"} {
		if resp := h.get("/api/v3/personal-agent"+path, h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "personal_agent_unavailable" {
			t.Fatalf("no seam %s: status %d", path, resp.StatusCode)
		}
	}
	h, core := paHarness(t, false)
	if resp := h.get("/api/v3/personal-agent/overview", h.token, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("closed gate: status %d", resp.StatusCode)
	}
	if status, _ := h.send("POST", "/api/v3/personal-agent/assistants", h.token, `{"requestId":"r1","name":"Ada"}`); status != http.StatusNotFound {
		t.Fatalf("closed gate write: status %d", status)
	}
	if core.callCount() != 0 {
		t.Fatalf("closed gate reached the core: %v", core.calls)
	}
	var boot struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&boot); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"personalAgent", "personalAgentManage", "personalAgentApprove"} {
		if boot.Capabilities[k] {
			t.Errorf("closed gate advertises %s", k)
		}
	}
}

func TestPersonalAgentNeedsAuth(t *testing.T) {
	h, core := paHarness(t, true)
	if resp := h.get("/api/v3/personal-agent/overview", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("read without token: %d", resp.StatusCode)
	}
	if status, _ := h.send("POST", "/api/v3/personal-agent/assistants", "", `{"requestId":"r1","name":"Ada"}`); status != http.StatusUnauthorized {
		t.Fatalf("write without token: %d", status)
	}
	if core.callCount() != 0 {
		t.Fatalf("unauthenticated request reached the core: %v", core.calls)
	}
}

func TestPersonalAgentReadsForwardTheCore(t *testing.T) {
	h, _ := paHarness(t, true)
	var ov personalagent.Overview
	if err := json.NewDecoder(h.get("/api/v3/personal-agent/overview", h.token, nil).Body).Decode(&ov); err != nil || ov.NeedsYou != 1 || len(ov.Assistants) != 1 {
		t.Fatalf("overview %v %+v", err, ov)
	}
	var detail PersonalAssistantDetail
	if err := json.NewDecoder(h.get("/api/v3/personal-agent/assistants/"+paAssistantA, h.token, nil).Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Assistant.Name != "Ada" || len(detail.Goals) != 1 || len(detail.Tasks) != 1 || len(detail.Tasks[0].Runs) != 1 || len(detail.Approvals) != 1 || len(detail.Memory) != 1 {
		t.Fatalf("detail %+v", detail)
	}
	if resp := h.get("/api/v3/personal-agent/assistants/a_ffffffffffffffff", h.token, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown assistant: %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/personal-agent/assistants/not%20an%20id", h.token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad id: %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/personal-agent/goals", h.token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("goals without assistant: %d", resp.StatusCode)
	}
	var approvals struct {
		Approvals []personalagent.Approval `json:"approvals"`
	}
	if err := json.NewDecoder(h.get("/api/v3/personal-agent/approvals", h.token, nil).Body).Decode(&approvals); err != nil || len(approvals.Approvals) != 1 {
		t.Fatalf("approvals %v %+v", err, approvals)
	}
}

func TestPersonalAgentCapabilitiesFollowTheGate(t *testing.T) {
	h, _ := paHarness(t, true)
	var boot struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&boot); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"personalAgent", "personalAgentManage", "personalAgentApprove"} {
		if !boot.Capabilities[k] {
			t.Errorf("open gate misses %s: %v", k, boot.Capabilities)
		}
	}
}

func TestPersonalAgentWritesForwardIdsAndRevisions(t *testing.T) {
	h, core := paHarness(t, true)
	post := func(path, body string) int {
		status, _ := h.send("POST", "/api/v3/personal-agent"+path, h.token, body)
		return status
	}
	if got := post("/assistants/"+paAssistantA+"/rename", `{"requestId":"req-1","name":"Grace","expectedRevision":1}`); got != 200 {
		t.Fatalf("rename: %d", got)
	}
	if call := core.lastCall(); !strings.Contains(call, `"requestId":"req-1"`) || !strings.Contains(call, `"expectedRevision":1`) || !strings.Contains(call, "Grace") {
		t.Fatalf("rename call %q", call)
	}
	if got := post("/tasks/"+paTaskT+"/publish", `{"requestId":"req-p","expectedRevision":3}`); got != 200 {
		t.Fatalf("publish: %d", got)
	}
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant task publish "+paTaskT) || !strings.Contains(call, `"expectedRevision":3`) {
		t.Fatalf("publish call %q", call)
	}
	if got := post("/goals/"+paGoalG+"/approve", `{"requestId":"req-2","expectedRevision":2}`); got != 200 {
		t.Fatalf("approve: %d", got)
	}
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant goal approve "+paGoalG) {
		t.Fatalf("goal approve call %q", call)
	}
	if got := post("/approvals/"+paApprovalP+"/decision", `{"requestId":"req-3","actionHash":"`+paHash+`","approve":true}`); got != 200 {
		t.Fatalf("decision: %d", got)
	}
	if call := core.lastCall(); !strings.Contains(call, `"actionHash":"`+paHash+`"`) || !strings.Contains(call, `"approve":true`) {
		t.Fatalf("decision call %q", call)
	}
	if got := post("/tasks/"+paTaskT+"/retry", `{"requestId":"req-4","expectedRevision":5}`); got != 200 {
		t.Fatalf("retry: %d", got)
	}
	if got := post("/goals/draft", `{"requestId":"req-5","assistantId":"`+paAssistantA+`","title":"Watch PR 12","commitments":[{"kind":"pr_watch","spec":"{}"}]}`); got != 200 {
		t.Fatalf("draft: %d", got)
	}
}

func TestPersonalAgentRejectsMalformedRequestsBeforeTheCore(t *testing.T) {
	h, core := paHarness(t, true)
	long := strings.Repeat("x", 200)
	bad := []struct{ path, body string }{
		{"/assistants", `{"name":"Ada"}`},                                             // no requestId
		{"/assistants", `{"requestId":"r","name":""}`},                                // empty name
		{"/assistants/" + paAssistantA + "/rename", `{"requestId":"r","name":"Ada"}`}, // no expectedRevision
		{"/assistants/" + paAssistantA + "/rename", `{"requestId":"r","name":"a\nb","expectedRevision":1}`},
		{"/assistants/" + paAssistantA + "/pause", `{"requestId":"r"}`},
		{"/assistants/not-an-id/rename", `{"requestId":"r","name":"Ada","expectedRevision":1}`},
		{"/goals/draft", `{"requestId":"r","assistantId":"nope","title":"x"}`},
		{"/goals/draft", `{"requestId":"r","assistantId":"` + paAssistantA + `","title":"x","commitments":[{},{},{},{},{}]}`},
		{"/goals/" + paGoalG + "/approve", `not json`},
		{"/goals/" + paGoalG + "/cancel", `{"requestId":"r"}`},
		{"/approvals/" + paApprovalP + "/decision", `{"requestId":"r","actionHash":"abc","approve":true}`},
		{"/approvals/" + paApprovalP + "/decision", `{"requestId":"r","actionHash":"` + strings.Repeat("G", 64) + `"}`},
		{"/tasks/" + paTaskT + "/ack", `{"requestId":"` + long + `","expectedRevision":1}`},
	}
	for _, c := range bad {
		if status, _ := h.send("POST", "/api/v3/personal-agent"+c.path, h.token, c.body); status != http.StatusBadRequest {
			t.Errorf("%s %q: status %d, want 400", c.path, c.body, status)
		}
	}
	if core.callCount() != 0 {
		t.Fatalf("a malformed request reached the core: %v", core.calls)
	}
}

func TestPersonalAgentMapsTheCoresRefusals(t *testing.T) {
	cases := map[string]int{
		"stale_revision":     http.StatusConflict,
		"request_conflict":   http.StatusConflict,
		"not_found":          http.StatusNotFound,
		"bad_state":          http.StatusConflict,
		"assistant_disabled": http.StatusConflict,
		"uncertain":          http.StatusConflict,
		"invalid":            http.StatusBadRequest,
		"internal":           http.StatusBadGateway,
	}
	for code, want := range cases {
		h, core := paHarness(t, true)
		core.fail = &personalagent.Error{Code: code, Message: "the core's own words"}
		status, body := h.send("POST", "/api/v3/personal-agent/goals/"+paGoalG+"/pause", h.token, `{"requestId":"r","expectedRevision":2}`)
		if status != want {
			t.Errorf("%s: status %d, want %d", code, status, want)
		}
		var env struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if err := json.Unmarshal(body, &env); err != nil || env.Error.Code != code || env.Error.Message != "the core's own words" {
			t.Errorf("%s: body %s", code, body)
		}
	}
}

func TestPersonalAgentEventCarriesCountsOnly(t *testing.T) {
	h, _ := paHarness(t, true)
	events, _, err := h.dial("/api/v3/events", true)
	if err != nil {
		t.Fatal(err)
	}
	readRaw(t, events) // hello
	h.srv.NotifyPersonalAgent(2, 3)
	raw := readRaw(t, events)
	golden(t, "events-personal-agent.json", raw)
	var frame map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	if frame["t"] != "personal-agent.changed" || frame["assistants"] != float64(2) || frame["needsYou"] != float64(3) || frame["revision"] == nil || len(frame) != 4 {
		t.Fatalf("frame %v", frame)
	}
}

func TestPersonalAgentGolden(t *testing.T) {
	h, _ := paHarness(t, true)
	golden(t, "bootstrap-personal-agent.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "personal-agent-overview.json", bodyOf(t, h.get("/api/v3/personal-agent/overview", h.token, nil)))
	golden(t, "personal-agent-assistants.json", bodyOf(t, h.get("/api/v3/personal-agent/assistants", h.token, nil)))
	golden(t, "personal-agent-assistant.json", bodyOf(t, h.get("/api/v3/personal-agent/assistants/"+paAssistantA, h.token, nil)))
	golden(t, "personal-agent-goals.json", bodyOf(t, h.get("/api/v3/personal-agent/goals?assistant="+paAssistantA, h.token, nil)))
	golden(t, "personal-agent-task.json", bodyOf(t, h.get("/api/v3/personal-agent/tasks/"+paTaskT, h.token, nil)))
	golden(t, "personal-agent-approvals.json", bodyOf(t, h.get("/api/v3/personal-agent/approvals", h.token, nil)))
	golden(t, "personal-agent-inbox.json", bodyOf(t, h.get("/api/v3/personal-agent/inbox", h.token, nil)))
	if resp := h.get("/api/v3/personal-agent/inbox?assistant=not%20an%20id", h.token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a malformed assistant filter = %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/personal-agent/inbox", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("inbox without a token = %d", resp.StatusCode)
	}
	golden(t, "personal-agent-decision-request.json", []byte(`{"requestId":"7c9e6679-7425-40de-944b-e07fc1f90ae7","actionHash":"`+paHash+`","approve":true}`))
	_, decided := h.send("POST", "/api/v3/personal-agent/approvals/"+paApprovalP+"/decision", h.token, `{"requestId":"7c9e6679-7425-40de-944b-e07fc1f90ae7","actionHash":"`+paHash+`","approve":true}`)
	golden(t, "personal-agent-decision.json", decided)
	h2, core := paHarness(t, true)
	core.fail = &personalagent.Error{Code: "stale_revision", Message: "revision is stale"}
	_, stale := h2.send("POST", "/api/v3/personal-agent/goals/"+paGoalG+"/approve", h2.token, `{"requestId":"r","expectedRevision":1}`)
	golden(t, "error-personal-agent-stale.json", stale)
	h3 := newHarness(t)
	golden(t, "error-personal-agent-unavailable.json", bodyOf(t, h3.get("/api/v3/personal-agent/overview", h3.token, nil)))
}

// SOLCO-R03: a phone that lost its connection and sends the same decision again forwards the same requestId (the
// core answers from its receipt, never deciding twice); a revoked phone is refused over HTTP and its socket is
// closed, and nothing reaches the core afterwards.
func TestReconnectReplaysTheRequestIdAndARevokedPhoneIsRefused(t *testing.T) {
	h, core := paHarness(t, true)
	decide := `{"requestId":"retry-9","actionHash":"` + paHash + `","approve":true}`
	path := "/api/v3/personal-agent/approvals/" + paApprovalP + "/decision"
	for i := 0; i < 2; i++ { // the second send is the retry after a dropped connection
		if status, _ := h.send("POST", path, h.token, decide); status != 200 {
			t.Fatalf("decision #%d: %d", i, status)
		}
		if call := core.lastCall(); !strings.Contains(call, `"requestId":"retry-9"`) || !strings.Contains(call, "approval decide "+paApprovalP) {
			t.Fatalf("decision #%d call %q", i, call)
		}
	}
	conn, _, err := h.dial("/api/v3/events", true)
	if err != nil {
		t.Fatal(err)
	}
	readJSON(t, conn) // hello

	if err := h.srv.Devices().Revoke(h.devID); err != nil {
		t.Fatal(err)
	}
	before := core.callCount()
	if status, _ := h.send("POST", path, h.token, decide); status != http.StatusUnauthorized {
		t.Fatalf("revoked phone decision: %d", status)
	}
	if resp := h.get("/api/v3/personal-agent/overview", h.token, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked phone read: %d", resp.StatusCode)
	}
	if core.callCount() != before {
		t.Fatal("a revoked phone reached the Assistants core")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			break // closed by the revoke
		}
	}
	if ctx.Err() != nil {
		t.Fatal("the revoked phone's events socket stayed open")
	}
}
