package remotehost

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
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
	case strings.HasPrefix(key, "persona "):
		name := map[string]string{"instructions": "persona-instructions", "set-instructions": "persona-instructions", "restore-instructions": "persona-instructions",
			"runtime": "persona-runtime", "set-runtime": "persona-runtime", "mcp": "persona-mcp", "mcp-grant": "persona-mcp", "mcp-verify": "persona-mcp-verified"}[strings.Fields(key)[1]]
		raw, err := os.ReadFile("../../plugins/solco-automations/internal/personalagent/testdata/" + name + ".json")
		if err != nil {
			return nil, err
		}
		return raw, nil
	case key == "group list":
		return []byte(`{"schema":1,"groups":[` + paGroup + `]}`), nil
	case strings.HasPrefix(key, "group show"):
		return []byte(`{"schema":1,"feed":{"group":` + paGroup + `,"messages":[` + paGroupMsg + `],"deliveries":[{"id":"d_0000000000000011","messageId":"m_0000000000000012","assistantId":"` + paAssistantB + `","state":"queued","attempts":0}],"tasks":[],"agents":[{"assistantId":"` + paAssistantA + `","state":"none"},{"assistantId":"` + paAssistantB + `","state":"starting","paneId":"w3:p1"}],"seq":1}}`), nil
	case strings.HasPrefix(key, "group post"):
		return []byte(`{"schema":1,"replayed":false,"result":{"message":` + paGroupMsg + `,"deliveries":[{"id":"d_0000000000000011","messageId":"m_0000000000000012","assistantId":"` + paAssistantB + `","state":"queued","attempts":0}]}}`), nil
	case strings.HasPrefix(key, "group "):
		return []byte(`{"schema":1,"replayed":false,"group":` + paGroup + `}`), nil
	case strings.HasPrefix(key, "coordinator show"):
		return []byte(`{"schema":1,"coordinator":` + paCoordinator("ready") + `}`), nil
	case strings.HasPrefix(key, "coordinator start"):
		return []byte(`{"schema":1,"replayed":false,"coordinator":` + paCoordinator("starting") + `}`), nil
	case strings.HasPrefix(key, "goal plan"), strings.HasPrefix(key, "goal facts"), strings.HasPrefix(key, "facts "):
		name := "goal-plan"
		if strings.HasPrefix(key, "goal facts") {
			name = "goal-facts"
		} else if strings.HasPrefix(key, "facts ") {
			name = "assistant-facts"
		}
		raw, err := os.ReadFile("../../plugins/solco-automations/internal/personalagent/testdata/" + name + ".json")
		if err != nil {
			return nil, err
		}
		return raw, nil
	case strings.HasPrefix(key, "routine list"):
		raw, err := os.ReadFile("../../plugins/solco-automations/internal/personalagent/testdata/routine-list.json")
		if err != nil {
			return nil, err
		}
		return raw, nil
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
	paAssistantB = "a_0000000000000009"
	paGroupID    = "g_0000000000000010"
	paGroup      = `{"id":"` + paGroupID + `","title":"Launch crew","status":"active","members":[{"assistantId":"` + paAssistantA + `","name":"Ada","status":"active"},{"assistantId":"` + paAssistantB + `","name":"Bo","status":"active"}],"lastSeq":1,"revision":1,"createdAt":1800000000000,"updatedAt":1800000000000}`
	paGroupMsg   = `{"seq":1,"id":"m_0000000000000012","groupId":"` + paGroupID + `","authorKind":"user","kind":"text","body":"@Bo can you check the launch plan?","mentions":["` + paAssistantB + `"],"createdAt":1800000000000}`
)

const (
	paAssistant = `{"id":"` + paAssistantA + `","name":"Ada","description":"Follows my pull requests","scope":"personal","status":"active","defaultPolicy":"review","coordinatorSession":"","rolePrompt":"","workspaceRefs":[],"revision":1,"createdAt":1800000000000,"updatedAt":1800000000000}`
	paGoal      = `{"id":"` + paGoalG + `","assistantId":"` + paAssistantA + `","title":"Watch PR 12","objective":"Tell me when it changes","state":"active","evidence":"","revision":2,"createdAt":1800000000000,"updatedAt":1800000000000,"commitments":[{"id":"c_0000000000000003","goalId":"` + paGoalG + `","assistantId":"` + paAssistantA + `","kind":"pr_watch","spec":"{\"repo\":\"o/r\",\"number\":12}","successCriteria":"merged","exitCondition":"merged or closed","state":"active","cursor":"","lastError":"","lastCheckedAt":0,"nextCheckAt":0,"revision":2,"createdAt":1800000000000,"updatedAt":1800000000000}]}`
	paTask      = `{"id":"` + paTaskT + `","assistantId":"` + paAssistantA + `","goalId":"` + paGoalG + `","parentTaskId":"","ruleId":"","kind":"research","title":"Check the PR","brief":"Read it.","agent":"claude","model":"","repoPath":"","state":"running","reviewState":"","needsYou":false,"attempt":1,"revision":5,"createdAt":1800000000000,"updatedAt":1800000000000}`
	paRun       = `{"id":"r_0000000000000005","taskId":"` + paTaskT + `","ruleId":"","eventId":"","idempotencyKey":"task:` + paTaskT + `:1","claimant":"daemon","status":"running","herdrSession":"default","workspaceId":"w1","paneId":"w1:p1","agentSessionId":"","label":"","result":"","error":"","startedAt":1800000000000,"finishedAt":0,"createdAt":1800000000000}`
	paApproval  = `{"id":"` + paApprovalP + `","assistantId":"` + paAssistantA + `","subjectKind":"task","subjectId":"` + paTaskT + `","action":"merge work/x","actionHash":"` + paHash + `","expectedRevision":5,"state":"pending","createdAt":1800000000000,"expiresAt":1800003600000,"decidedAt":0}`
	paMemory    = `{"id":"m_0000000000000006","assistantId":"` + paAssistantA + `","kind":"fact","content":"Prefers short reports","sourceRef":"run:r_1","scope":"assistant","sharedWith":[],"state":"approved","revision":2,"createdAt":1800000000000,"updatedAt":1800000000000}`
)

// paCoordinator is the core's Coordinator row in one state.
func paCoordinator(state string) string {
	row := `{"assistantId":"` + paAssistantA + `","state":"` + state + `","taskId":"` + paTaskT + `","revision":2}`
	if state == "ready" {
		row = `{"assistantId":"` + paAssistantA + `","state":"ready","taskId":"` + paTaskT + `","herdrSession":"default","workspaceId":"w1","paneId":"w1:p1","revision":3}`
	}
	return row
}

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

// The phone starts the Coordinator on an Agent the desktop offers, once per
// requestId; an Agent outside that list never reaches the core.
func TestPersonalAgentStartsTheCoordinatorOnAnOfferedAgent(t *testing.T) {
	h, core := paHarness(t, true)
	h.srv.opts.AssistantAgents = func() []PersonalAgentKind {
		return []PersonalAgentKind{{ID: "pi", Label: "Pi"}, {ID: "claude", Label: "Claude Code"}}
	}
	path := "/api/v3/personal-agent/assistants/" + paAssistantA + "/coordinator/start"
	if status, _ := h.send("POST", path, h.token, `{"requestId":"s1","agent":"codex"}`); status != http.StatusBadRequest {
		t.Fatalf("an Agent the desktop does not offer = %d", status)
	}
	if core.callCount() != 0 {
		t.Fatalf("a refused Agent reached the core: %v", core.calls)
	}
	status, body := h.send("POST", path, h.token, `{"requestId":"s1","agent":"pi"}`)
	if status != http.StatusOK || !strings.Contains(string(body), `"state":"starting"`) {
		t.Fatalf("start = %d %s", status, body)
	}
	if last := core.lastCall(); !strings.HasPrefix(last, "assistant coordinator start "+paAssistantA) || !strings.Contains(last, `"agent":"pi"`) || !strings.Contains(last, `"requestId":"s1"`) {
		t.Fatalf("core call %q", last)
	}
	if status, _ := h.send("POST", path, h.token, `{"requestId":"s2"}`); status != http.StatusOK {
		t.Fatalf("the default Agent (empty) = %d", status)
	}
	if status, _ := h.send("POST", path, h.token, `{"agent":"pi"}`); status != http.StatusBadRequest {
		t.Fatalf("no requestId = %d", status)
	}
	detail := bodyOf(t, h.get("/api/v3/personal-agent/assistants/"+paAssistantA, h.token, nil))
	if !strings.Contains(string(detail), `"agents":[{"id":"pi","label":"Pi"}`) || !strings.Contains(string(detail), `"coordinator":{"assistantId":"`+paAssistantA+`","state":"ready"`) {
		t.Fatalf("the Assistant page carries its Coordinator and the Agent choice: %s", detail)
	}
}

func TestPersonalAgentGroupRoutes(t *testing.T) {
	h, core := paHarness(t, true)
	base := "/api/v3/personal-agent/groups"
	var list struct {
		Groups []personalagent.Group `json:"groups"`
	}
	if err := json.NewDecoder(h.get(base, h.token, nil).Body).Decode(&list); err != nil || len(list.Groups) != 1 || len(list.Groups[0].Members) != 2 {
		t.Fatalf("groups %v %+v", err, list)
	}
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant group list") {
		t.Fatalf("core call %q", call)
	}
	h.get(base+"/"+paGroupID+"?since=4&limit=50", h.token, nil)
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant group show "+paGroupID+" --since 4 --limit 50") {
		t.Fatalf("feed call %q", call)
	}
	for _, bad := range []string{"?since=-1", "?since=x", "?limit=0", "?limit=999"} {
		if resp := h.get(base+"/"+paGroupID+bad, h.token, nil); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("feed%s = %d", bad, resp.StatusCode)
		}
	}
	if resp := h.get(base+"/not%20an%20id", h.token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed id = %d", resp.StatusCode)
	}

	members := `["` + paAssistantA + `","` + paAssistantB + `"]`
	if status, _ := h.send("POST", base, h.token, `{"requestId":"g1","title":"Launch crew","members":`+members+`}`); status != http.StatusOK {
		t.Fatalf("create = %d", status)
	}
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant group create") || !strings.Contains(call, `"requestId":"g1"`) {
		t.Fatalf("create call %q", call)
	}
	n := core.callCount()
	for name, body := range map[string]string{
		"one member":   `{"requestId":"g2","members":["` + paAssistantA + `"]}`,
		"duplicate":    `{"requestId":"g2","members":["` + paAssistantA + `","` + paAssistantA + `"]}`,
		"bad id":       `{"requestId":"g2","members":["x","y"]}`,
		"no requestId": `{"members":` + members + `}`,
		"too many":     `{"requestId":"g2","members":["a_1","a_2","a_3","a_4","a_5","a_6","a_7","a_8","a_9"]}`,
	} {
		if status, _ := h.send("POST", base, h.token, body); status != http.StatusBadRequest {
			t.Fatalf("create %s = %d", name, status)
		}
	}
	if core.callCount() != n {
		t.Fatalf("a refused create reached the core: %v", core.calls[n:])
	}

	post := base + "/" + paGroupID + "/messages"
	if status, _ := h.send("POST", post, h.token, `{"requestId":"m1","body":"@Bo can you check the launch plan?","mentions":["`+paAssistantB+`"],"replyTo":"m_0000000000000012"}`); status != http.StatusOK {
		t.Fatalf("post = %d", status)
	}
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant group post") || !strings.Contains(call, `"groupId":"`+paGroupID+`"`) || !strings.Contains(call, `"mentions":["`+paAssistantB+`"]`) {
		t.Fatalf("post call %q", call)
	}
	n = core.callCount()
	for name, body := range map[string]string{
		"empty":        `{"requestId":"m2","body":"  "}`,
		"bad mention":  `{"requestId":"m2","body":"hi","mentions":["nope"]}`,
		"bad reply":    `{"requestId":"m2","body":"hi","replyTo":"nope"}`,
		"no requestId": `{"body":"hi"}`,
	} {
		if status, _ := h.send("POST", post, h.token, body); status != http.StatusBadRequest {
			t.Fatalf("post %s = %d", name, status)
		}
	}
	// The phone has no way to speak as an Assistant.
	if status, _ := h.send("POST", base+"/"+paGroupID+"/say", h.token, `{"requestId":"s1","as":"`+paAssistantB+`","body":"hi"}`); status == http.StatusOK {
		t.Fatalf("a say route exists")
	}
	if core.callCount() != n {
		t.Fatalf("a refused post reached the core: %v", core.calls[n:])
	}

	if status, _ := h.send("POST", base+"/"+paGroupID+"/rename", h.token, `{"requestId":"n1","title":"Crew","expectedRevision":1}`); status != http.StatusOK {
		t.Fatalf("rename = %d", status)
	}
	if status, _ := h.send("POST", base+"/"+paGroupID+"/rename", h.token, `{"requestId":"n2","title":"Crew"}`); status != http.StatusBadRequest {
		t.Fatalf("rename without revision = %d", status)
	}
	if status, _ := h.send("POST", base+"/"+paGroupID+"/members/add", h.token, `{"requestId":"a1","assistantId":"`+paAssistantB+`"}`); status != http.StatusOK {
		t.Fatalf("add member = %d", status)
	}
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant group add "+paGroupID) {
		t.Fatalf("add call %q", call)
	}
	if status, _ := h.send("POST", base+"/"+paGroupID+"/members/remove", h.token, `{"requestId":"a2","assistantId":"`+paAssistantB+`"}`); status != http.StatusOK {
		t.Fatalf("remove member = %d", status)
	}
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant group remove "+paGroupID) {
		t.Fatalf("remove call %q", call)
	}
	if status, _ := h.send("POST", post, "", `{"requestId":"m9","body":"hi"}`); status != http.StatusUnauthorized {
		t.Fatalf("post without token = %d", status)
	}
}

func TestPersonalAgentGolden(t *testing.T) {
	h, _ := paHarness(t, true)
	h.srv.opts.AssistantAgents = func() []PersonalAgentKind {
		return []PersonalAgentKind{{ID: "pi", Label: "Pi"}, {ID: "claude", Label: "Claude Code"}}
	}
	golden(t, "personal-agent-coordinator-start-request.json", []byte(`{"requestId":"4d9a1f0e-6c1b-4e52-9a58-1b6f3a2c7d10","agent":"pi"}`))
	_, started := h.send("POST", "/api/v3/personal-agent/assistants/"+paAssistantA+"/coordinator/start", h.token, `{"requestId":"4d9a1f0e-6c1b-4e52-9a58-1b6f3a2c7d10","agent":"pi"}`)
	golden(t, "personal-agent-coordinator-start.json", started)
	golden(t, "bootstrap-personal-agent.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "personal-agent-overview.json", bodyOf(t, h.get("/api/v3/personal-agent/overview", h.token, nil)))
	golden(t, "personal-agent-assistants.json", bodyOf(t, h.get("/api/v3/personal-agent/assistants", h.token, nil)))
	golden(t, "personal-agent-assistant.json", bodyOf(t, h.get("/api/v3/personal-agent/assistants/"+paAssistantA, h.token, nil)))
	golden(t, "personal-agent-goals.json", bodyOf(t, h.get("/api/v3/personal-agent/goals?assistant="+paAssistantA, h.token, nil)))
	golden(t, "personal-agent-task.json", bodyOf(t, h.get("/api/v3/personal-agent/tasks/"+paTaskT, h.token, nil)))
	golden(t, "personal-agent-groups.json", bodyOf(t, h.get("/api/v3/personal-agent/groups", h.token, nil)))
	golden(t, "personal-agent-group-feed.json", bodyOf(t, h.get("/api/v3/personal-agent/groups/"+paGroupID, h.token, nil)))
	golden(t, "personal-agent-group-message-request.json", []byte(`{"requestId":"8c1d5e2a-3b7f-4f60-9a11-2d4c6e8f0a13","body":"@Bo can you check the launch plan?","mentions":["`+paAssistantB+`"]}`))
	_, posted := h.send("POST", "/api/v3/personal-agent/groups/"+paGroupID+"/messages", h.token, `{"requestId":"8c1d5e2a-3b7f-4f60-9a11-2d4c6e8f0a13","body":"@Bo can you check the launch plan?","mentions":["`+paAssistantB+`"]}`)
	golden(t, "personal-agent-group-message.json", posted)
	pbase := "/api/v3/personal-agent/assistants/" + paAssistantB
	golden(t, "personal-agent-instructions.json", bodyOf(t, h.get(pbase+"/instructions", h.token, nil)))
	golden(t, "personal-agent-instructions-request.json", []byte(`{"requestId":"2f6c1a90-5d3e-4b78-8c21-7a9e0b4d6f15","body":"Be brief and precise.","expectedRevision":1}`))
	_, setIns := h.send("POST", pbase+"/instructions", h.token, `{"requestId":"2f6c1a90-5d3e-4b78-8c21-7a9e0b4d6f15","body":"Be brief and precise.","expectedRevision":1}`)
	golden(t, "personal-agent-instructions-saved.json", setIns)
	golden(t, "personal-agent-runtime.json", bodyOf(t, h.get(pbase+"/runtime", h.token, nil)))
	golden(t, "personal-agent-runtime-request.json", []byte(`{"requestId":"9b4e7d21-0c3a-4f58-a6d9-3e1f8c2b5a70","agent":"claude","model":"sonnet","effort":"high","expectedRevision":0}`))
	_, setRt := h.send("POST", pbase+"/runtime", h.token, `{"requestId":"9b4e7d21-0c3a-4f58-a6d9-3e1f8c2b5a70","agent":"claude","model":"sonnet","effort":"high","expectedRevision":0}`)
	golden(t, "personal-agent-runtime-saved.json", setRt)
	golden(t, "personal-agent-mcp.json", bodyOf(t, h.get(pbase+"/mcp", h.token, nil)))
	golden(t, "personal-agent-mcp-grant-request.json", []byte(`{"requestId":"c7a3f5e8-1b2d-4960-8e4a-5d7f9a1c3b62","serverId":"alpha","granted":true}`))
	_, grant := h.send("POST", pbase+"/mcp/grant", h.token, `{"requestId":"c7a3f5e8-1b2d-4960-8e4a-5d7f9a1c3b62","serverId":"alpha","granted":true}`)
	golden(t, "personal-agent-mcp-granted.json", grant)
	_, verified := h.send("POST", pbase+"/mcp/verify", h.token, `{}`)
	golden(t, "personal-agent-mcp-verified.json", verified)
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

func TestPersonalAgentPlanRoutes(t *testing.T) {
	h, core := paHarness(t, true)
	pbase := "/api/v3/personal-agent"
	resp := h.get(pbase+"/goals/"+paGoalG+"/plan", h.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("plan = %d", resp.StatusCode)
	}
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant goal facts "+paGoalG) {
		t.Fatalf("plan call %q", call)
	}
	golden(t, "personal-agent-plan.json", bodyOf(t, h.get(pbase+"/goals/"+paGoalG+"/plan", h.token, nil)))
	golden(t, "personal-agent-facts.json", bodyOf(t, h.get(pbase+"/assistants/"+paAssistantA+"/facts?limit=20", h.token, nil)))
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant facts "+paAssistantA+" --limit 20") {
		t.Fatalf("facts call %q", call)
	}
	for _, bad := range []string{pbase + "/assistants/" + paAssistantA + "/facts?limit=0", pbase + "/assistants/" + paAssistantA + "/facts?limit=101", pbase + "/assistants/" + paAssistantA + "/facts?limit=x", pbase + "/goals/not%20an%20id/plan"} {
		if r := h.get(bad, h.token, nil); r.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, r.StatusCode)
		}
	}
	if r := h.get(pbase+"/goals/"+paGoalG+"/plan", "", nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("plan without a token = %d", r.StatusCode)
	}
	h2, _ := paHarness(t, false)
	if r := h2.get(pbase+"/goals/"+paGoalG+"/plan", h2.token, nil); r.StatusCode == http.StatusOK {
		t.Fatal("plan served without the seam")
	}
}

func TestPersonalAgentRoutineRoutes(t *testing.T) {
	h, core := paHarness(t, true)
	pbase := "/api/v3/personal-agent"
	golden(t, "personal-agent-routines.json", bodyOf(t, h.get(pbase+"/assistants/"+paAssistantA+"/routines", h.token, nil)))
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant routine list --assistant "+paAssistantA) {
		t.Fatalf("routines call %q", call)
	}
	if r := h.get(pbase+"/assistants/not%20an%20id/routines", h.token, nil); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad id = %d", r.StatusCode)
	}
	if r := h.get(pbase+"/assistants/"+paAssistantA+"/routines", "", nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without a token = %d", r.StatusCode)
	}
	h2, _ := paHarness(t, false)
	if r := h2.get(pbase+"/assistants/"+paAssistantA+"/routines", h2.token, nil); r.StatusCode == http.StatusOK {
		t.Fatal("routines served without the seam")
	}
}

func TestPersonalAgentPersonaRoutes(t *testing.T) {
	h, core := paHarness(t, true)
	base := "/api/v3/personal-agent/assistants/" + paAssistantA

	h.get(base+"/instructions", h.token, nil)
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant persona instructions "+paAssistantA) {
		t.Fatalf("instructions call %q", call)
	}
	h.get(base+"/runtime", h.token, nil)
	h.get(base+"/mcp", h.token, nil)
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant persona mcp "+paAssistantA) {
		t.Fatalf("mcp call %q", call)
	}
	if resp := h.get("/api/v3/personal-agent/assistants/not%20an%20id/mcp", h.token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed id = %d", resp.StatusCode)
	}
	if resp := h.get(base+"/mcp", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("mcp without a token = %d", resp.StatusCode)
	}

	if status, _ := h.send("POST", base+"/instructions", h.token, `{"requestId":"i1","body":"Be kind.","expectedRevision":2}`); status != http.StatusOK {
		t.Fatalf("set instructions = %d", status)
	}
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant persona set-instructions "+paAssistantA) || !strings.Contains(call, `"expectedRevision":2`) || !strings.Contains(call, `"requestId":"i1"`) {
		t.Fatalf("set call %q", call)
	}
	if status, _ := h.send("POST", base+"/instructions/restore", h.token, `{"requestId":"i2","revision":1,"expectedRevision":2}`); status != http.StatusOK {
		t.Fatalf("restore = %d", status)
	}
	if status, _ := h.send("POST", base+"/runtime", h.token, `{"requestId":"r1","agent":"codex","model":"gpt 5","effort":"","expectedRevision":0}`); status != http.StatusOK {
		t.Fatalf("set runtime = %d", status)
	}
	if status, _ := h.send("POST", base+"/mcp/grant", h.token, `{"requestId":"g1","serverId":"alpha","granted":true}`); status != http.StatusOK {
		t.Fatalf("grant = %d", status)
	}
	if status, _ := h.send("POST", base+"/mcp/verify", h.token, `{}`); status != http.StatusOK {
		t.Fatalf("verify = %d", status)
	}
	if call := core.lastCall(); !strings.HasPrefix(call, "assistant persona mcp-verify "+paAssistantA) {
		t.Fatalf("verify call %q", call)
	}

	n := core.callCount()
	for name, tc := range map[string][2]string{
		"empty body":      {"/instructions", `{"requestId":"x","body":"  ","expectedRevision":1}`},
		"no revision":     {"/instructions", `{"requestId":"x","body":"ok"}`},
		"too long":        {"/instructions", `{"requestId":"x","body":"` + strings.Repeat("a", maxPAInstructions+1) + `","expectedRevision":1}`},
		"no requestId":    {"/instructions", `{"body":"ok","expectedRevision":1}`},
		"restore zero":    {"/instructions/restore", `{"requestId":"x","revision":0,"expectedRevision":1}`},
		"agent has space": {"/runtime", `{"requestId":"x","agent":"my agent","expectedRevision":0}`},
		"effort newline":  {"/runtime", `{"requestId":"x","effort":"a\nb","expectedRevision":0}`},
		"negative rev":    {"/runtime", `{"requestId":"x","expectedRevision":-1}`},
		"bad server id":   {"/mcp/grant", `{"requestId":"x","serverId":"a b","granted":true}`},
		"empty server id": {"/mcp/grant", `{"requestId":"x","serverId":"","granted":true}`},
	} {
		if status, _ := h.send("POST", base+tc[0], h.token, tc[1]); status != http.StatusBadRequest {
			t.Fatalf("%s = %d", name, status)
		}
	}
	if core.callCount() != n {
		t.Fatalf("a refused write reached the core: %v", core.calls[n:])
	}
	// The core's own refusals keep their codes.
	core.fail = &personalagent.Error{Code: "mcp_unsupported", Message: "not for this Agent"}
	status, body := h.send("POST", base+"/mcp/verify", h.token, `{}`)
	if status != http.StatusConflict || !strings.Contains(string(body), "mcp_unsupported") {
		t.Fatalf("unsupported = %d %s", status, body)
	}
	core.fail = &personalagent.Error{Code: "stale_revision", Message: "edited elsewhere"}
	if status, _ := h.send("POST", base+"/instructions", h.token, `{"requestId":"i9","body":"x","expectedRevision":1}`); status != http.StatusConflict {
		t.Fatalf("stale = %d", status)
	}
}
