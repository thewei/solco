package nativeui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/personalagent"
	"github.com/thewei/solco/internal/workboard"
)

/**
 * [INPUT]: 依赖 assistants_state/section/bridge 与 personalagent.NewClientWithExec（插件 CLI 的 fake exec，绝不跑真二进制）、ui.Tester 无头渲染
 * [OUTPUT]: 门接线回归（插件门+探测才开、门关零痕迹且 Projects 区照旧、探测失败保持关闭）、侧栏 Assistants 区取代 Projects 区并可点击进页、桥 Snapshot 读视图（概览 + 选中 Assistant 的 Goal/Task/Approval/Memory/Run，窗格存在才可打开）、PR Watch 的 Goal 请求体、门关拒绝
 * [POS]: Assistants 桌面表面的守卫测试；与 assistants_*.go 同生共死
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	assistantsOverviewJSON  = `{"schema":1,"needsYou":1,"assistants":[{"id":"a_1","name":"Ada","status":"active","revision":1,"tasks":1,"queued":0,"running":0,"needsYou":1,"done":0,"goals":1,"approvals":0}]}`
	assistantsListJSON      = `{"schema":1,"assistants":[{"id":"a_1","name":"Ada","description":"Follows PRs","status":"active","revision":1,"workspaceRefs":[]}]}`
	assistantsGoalsJSON     = `{"schema":1,"goals":[{"id":"g_1","assistantId":"a_1","title":"Watch PR","state":"active","revision":2,"commitments":[]}]}`
	assistantsTasksJSON     = `{"schema":1,"tasks":[{"id":"t_1","assistantId":"a_1","title":"Check it","kind":"research","state":"done","needsYou":true,"revision":5}]}`
	assistantsTaskShowJSON  = `{"schema":1,"task":{"id":"t_1"},"runs":[{"id":"r_1","taskId":"t_1","status":"done","herdrSession":"default","paneId":"w1:p1"},{"id":"r_2","taskId":"t_1","status":"done","herdrSession":"default","paneId":"w9:p9"}]}`
	assistantsApprovalsJSON = `{"schema":1,"approvals":[]}`
	assistantsMemoryJSON    = `{"schema":1,"memory":[{"id":"m_1","assistantId":"a_1","content":"short reports","state":"approved"}]}`
)

// stubAssistants routes every CLI call through one fake, recording stdin and
// argv, and runs the background lane inline.
func stubAssistants(t *testing.T, fail bool) *[][2]string {
	t.Helper()
	var calls [][2]string
	newAssistantsClient = func(string, string) *personalagent.Client {
		return personalagent.NewClientWithExec(func(_ context.Context, stdin string, args ...string) ([]byte, error) {
			args = args[1:] // the leading "assistant"
			calls = append(calls, [2]string{strings.Join(args, " "), stdin})
			if fail {
				return []byte(`{"schema":1,"error":{"code":"internal","message":"no"}}`), context.Canceled
			}
			switch strings.Join(args, " ") {
			case "overview":
				return []byte(assistantsOverviewJSON), nil
			case "list":
				return []byte(assistantsListJSON), nil
			case "goal list --assistant a_1":
				return []byte(assistantsGoalsJSON), nil
			case "task list --assistant a_1":
				return []byte(assistantsTasksJSON), nil
			case "task show t_1":
				return []byte(assistantsTaskShowJSON), nil
			case "approval list --assistant a_1":
				return []byte(assistantsApprovalsJSON), nil
			case "memory visible a_1":
				return []byte(assistantsMemoryJSON), nil
			case "goal create":
				return []byte(`{"schema":1,"goal":{"id":"g_2"}}`), nil
			}
			return []byte(`{"schema":1}`), nil
		})
	}
	assistantsGo = func(f func()) { f() }
	t.Cleanup(func() {
		newAssistantsClient = func(root, session string) *personalagent.Client {
			return personalagent.NewClient(root, []string{"HERDR_SOCKET_PATH=" + herdr.SocketPath(session)})
		}
		assistantsGo = func(func()) {}
	})
	return &calls
}

func openAssistantsShell(t *testing.T) *Shell {
	t.Helper()
	s := NewShell()
	s.loading = false
	s.activeInstance = "default"
	s.automations.avail = automations.Availability{Installed: true, Enabled: true, Version: "0.12.0", Supported: true, Root: "/plugin"}
	s.refreshAssistants(true)
	return s
}

func TestAssistantsGateNeedsThePluginAndItsProbe(t *testing.T) {
	stubAssistants(t, false)
	s := NewShell()
	s.refreshAssistants(true)
	if s.assistantsAvailable() {
		t.Fatal("no plugin gate: the Assistants gate must stay closed")
	}
	s = openAssistantsShell(t)
	if !s.assistantsAvailable() || len(s.assistants.overview.Assistants) != 1 {
		t.Fatalf("gate and overview not open: %+v", s.assistants)
	}
	s.applyAutomationsAvailability(automations.Availability{})
	if s.assistantsAvailable() || len(s.assistants.overview.Assistants) != 0 {
		t.Fatal("a plugin that went away takes the Assistants surface with it")
	}
}

func TestAssistantsProbeFailureLeavesNoTrace(t *testing.T) {
	stubAssistants(t, true)
	s := openAssistantsShell(t)
	if s.assistantsAvailable() {
		t.Fatal("an older plugin (refused overview) must not open the gate")
	}
	tt := ui.NewTester(s.View, 1200, 800)
	if tt.HasText("Assistants") {
		t.Fatalf("Assistants visible without a working core: %q", tt.Texts())
	}
}

func TestAssistantsSectionReplacesProjectsAndOpensThePage(t *testing.T) {
	stubAssistants(t, false)
	s := openAssistantsShell(t)
	s.workboard.avail = workboard.Availability{Installed: true, Enabled: true, Version: "1.2.26", Root: "/plugin", Supported: true}
	tt := ui.NewTester(s.View, 1200, 800)
	if !tt.HasText("Assistants") || !tt.HasText("Ada") {
		t.Fatalf("sidebar missing the Assistants section: %q", tt.Texts())
	}
	if tt.HasText("Projects") {
		t.Fatalf("Projects section must give way while Assistants are on: %q", tt.Texts())
	}
	if err := tt.Click("Ada"); err != nil {
		t.Fatal(err)
	}
	if s.router.Path() != routeAssistants || s.assistants.selected != "a_1" {
		t.Fatalf("path=%q selected=%q, want /assistants on a_1", s.router.Path(), s.assistants.selected)
	}
}

func TestAssistantsBridgeSnapshotReadsTheSelectedAssistant(t *testing.T) {
	stubAssistants(t, false)
	s := openAssistantsShell(t)
	s.projection = herdr.Projection{Panes: []herdr.Pane{{ID: "w1:p1"}}}
	b := NewAssistantsBridge(s)
	view, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !view.Available || view.Selected != "a_1" || view.Detail == nil {
		t.Fatalf("view = %+v", view)
	}
	d := view.Detail
	if len(d.Goals) != 1 || len(d.Memory) != 1 || len(d.Tasks) != 1 || len(d.Approvals) != 0 {
		t.Fatalf("detail = %+v", d)
	}
	runs := d.Tasks[0].Runs
	if len(runs) != 2 || !runs[0].CanOpen || runs[1].CanOpen {
		t.Fatalf("only the run whose pane is still listed can be opened: %+v", runs)
	}
	if _, err := json.Marshal(view); err != nil {
		t.Fatal(err)
	}
}

func TestAssistantsBridgeCreateGoalSendsAPRWatchSpec(t *testing.T) {
	calls := stubAssistants(t, false)
	s := openAssistantsShell(t)
	b := NewAssistantsBridge(s)
	err := b.CreateGoal(context.Background(), "req-1", AssistantsGoalInput{
		AssistantID: "a_1", Title: "Watch PR 12", Objective: "tell me",
		Watch: &AssistantsWatch{Repo: "o/r", Number: 12, IntervalSeconds: 60, Agent: "pi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdin string
	for _, c := range *calls {
		if c[0] == "goal create" {
			stdin = c[1]
		}
	}
	var body struct {
		RequestID   string `json:"requestId"`
		Commitments []struct {
			Kind string `json:"kind"`
			Spec string `json:"spec"`
		} `json:"commitments"`
	}
	if err := json.Unmarshal([]byte(stdin), &body); err != nil || body.RequestID != "req-1" || len(body.Commitments) != 1 {
		t.Fatalf("body %q: %v", stdin, err)
	}
	spec := body.Commitments[0]
	if spec.Kind != "pr_watch" || !strings.Contains(spec.Spec, `"repo":"o/r"`) || !strings.Contains(spec.Spec, `"number":12`) {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestAssistantsBridgeRefusesWhileTheGateIsClosed(t *testing.T) {
	stubAssistants(t, false)
	s := NewShell()
	b := NewAssistantsBridge(s)
	if _, err := b.CreateAssistant(context.Background(), "r", "Ada", ""); err == nil {
		t.Fatal("a closed gate must refuse writes")
	}
	view, err := b.Snapshot(context.Background())
	if err != nil || view.Available {
		t.Fatalf("closed gate snapshot = %+v, %v", view, err)
	}
}

func TestHostPersonalAgentFollowsTheGate(t *testing.T) {
	stubAssistants(t, false)
	s := NewShell()
	if _, ok := s.hostPersonalAgent(context.Background()); ok {
		t.Fatal("closed gate must give the phones no client")
	}
	s = openAssistantsShell(t)
	c, ok := s.hostPersonalAgent(context.Background())
	if !ok || c == nil {
		t.Fatal("open gate must give the phones the desktop's client")
	}
	if ov, err := c.Overview(context.Background()); err != nil || len(ov.Assistants) != 1 {
		t.Fatalf("phone client overview: %v %+v", err, ov)
	}
	s.applyAutomationsAvailability(automations.Availability{})
	if _, ok := s.hostPersonalAgent(context.Background()); ok {
		t.Fatal("a plugin that went away must withdraw the phones' client")
	}
}
