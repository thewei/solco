package nativeui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/personalagent"
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

const assistantsInboxJSON = `{"schema":1,"inbox":{"items":[{"id":"task:t_1","kind":"stalled","assistantId":"a_1","taskId":"t_1","title":"Check it","detail":"Has not started","priority":3,"at":1}],"needsYou":1,"seq":4}}`

// assistantsOverviewWithGroup is the overview when a Group exists beside Ada.
const assistantsOverviewWithGroup = `{"schema":1,"needsYou":0,"assistants":[{"id":"a_1","name":"Ada","status":"active","revision":1,"tasks":0,"queued":0,"running":0,"needsYou":0,"done":0,"goals":0,"approvals":0},{"id":"a_2","name":"Bo","status":"active","revision":1,"tasks":0,"queued":0,"running":0,"needsYou":0,"done":0,"goals":0,"approvals":0}],"groups":[{"id":"gr_1","title":"Launch crew","members":[{"assistantId":"a_1","name":"Ada","status":"active"},{"assistantId":"a_2","name":"Bo","status":"active"}],"lastSeq":3,"open":0,"updatedAt":1}]}`

// assistantsOverviewOverride, when set, replaces the scripted overview.
var assistantsOverviewOverride string

// assistantsReportOverride scripts the opt-in Coordinator reporting outbox.
var assistantsReportOverride string

// assistantsCoordinators scripts `coordinator show <id>` for a test.
var assistantsCoordinators = map[string]string{}

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
			if strings.HasPrefix(strings.Join(args, " "), "inbox") {
				return []byte(assistantsInboxJSON), nil
			}
			if id, ok := strings.CutPrefix(strings.Join(args, " "), "coordinator start "); ok {
				return []byte(coordinatorJSON(id, "starting", "")), nil
			}
			if id, ok := strings.CutPrefix(strings.Join(args, " "), "coordinator show "); ok {
				if raw, found := assistantsCoordinators[id]; found {
					return []byte(raw), nil
				}
				return []byte(`{"schema":1,"coordinator":{"assistantId":"` + id + `","state":"none","revision":0}}`), nil
			}
			switch strings.Join(args, " ") {
			case "report show a_1":
				if assistantsReportOverride != "" {
					return []byte(assistantsReportOverride), nil
				}
				return []byte(`{"schema":1,"report":{"assistantId":"a_1","state":"idle"}}`), nil
			case "routine list --assistant a_1":
				return []byte(`{"schema":1,"routines":[{"name":"morning-check","assistantId":"a_1","requestedBy":"assistant","source":"chat","state":"draft","revision":1,"approved":false,"createdAt":1,"updatedAt":1,"cron":"0 9 * * *","repo":"/tmp","agent":"claude","headless":true,"paused":false}]}`), nil
			case "memory review a_1":
				return []byte(`{"schema":1,"review":{"assistantId":"a_1","approved":1,"candidates":1,"max":500,"duplicates":[],"staleCandidates":[],"conflicts":[],"items":[{"id":"m_1","assistantId":"a_1","content":"short reports","state":"approved","kind":"fact","revision":2},{"id":"m_2","assistantId":"a_1","content":"maybe tea","state":"candidate","kind":"fact","revision":1}]}}`), nil
			case "memory history m_1":
				return []byte(`{"schema":1,"versions":[{"memoryId":"m_1","revision":2,"author":"files","state":"approved","content":"short reports","at":2},{"memoryId":"m_1","revision":1,"author":"user","state":"approved","content":"reports","at":1}]}`), nil
			case "memory folder a_1":
				return []byte(`{"schema":1,"path":"/tmp/solco-memory/a_1/memory","sync":{}}`), nil
			case "proactivity show a_1":
				return []byte(`{"schema":1,"proactivity":{"assistantId":"a_1","level":"follow_up","quietStart":"","quietEnd":"","tz":"","reportEveryMin":0,"dailyBudget":20,"dispatchToday":0,"quietNow":false,"revision":0,"updatedAt":0}}`), nil
			case "memory add", "memory correct m_1", "memory approve m_2", "memory forget m_1":
				return []byte(`{"schema":1,"replayed":false,"memory":{"id":"m_1","assistantId":"a_1","content":"x","state":"approved","revision":3}}`), nil
			case "proactivity set a_1":
				return []byte(`{"schema":1,"replayed":false,"proactivity":{"assistantId":"a_1","level":"suggest","dailyBudget":10,"revision":1}}`), nil
			case "goal plan g_1":
				return []byte(`{"schema":1,"plan":{"goalId":"g_1","assistantId":"a_1","successCriteria":"Merged","milestones":[{"id":"m1","title":"Review","state":"working","task":{"kind":"research","title":"Review","brief":"b"},"taskId":"t_1"}],"nextAction":"","planRevision":1,"replans":0,"maxTasks":12,"staleAfterMs":2700000,"verified":0,"total":1,"state":"working","revisions":[],"updatedAt":1}}`), nil
			case "facts a_1 --limit 6", "facts a_1 --limit 40":
				return []byte(`{"schema":1,"facts":[{"seq":2,"id":"f_2","assistantId":"a_1","goalId":"g_1","kind":"delegated","text":"Delegated: Review","createdAt":2},{"seq":1,"id":"f_1","assistantId":"a_1","goalId":"g_1","kind":"accepted","text":"Accepted: Watch PR","createdAt":1}]}`), nil
			case "overview":
				if assistantsOverviewOverride != "" {
					return []byte(assistantsOverviewOverride), nil
				}
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
		assistantsCoordinators = map[string]string{}
		assistantsOverviewOverride = ""
		assistantsReportOverride = ""
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

func TestAssistantsSectionAppearsAndOpensThePage(t *testing.T) {
	stubAssistants(t, false)
	s := openAssistantsShell(t)
	tt := ui.NewTester(s.View, 1200, 800)
	if !tt.HasText("Assistants") || !tt.HasText("Ada") {
		t.Fatalf("sidebar missing the Assistants section: %q", tt.Texts())
	}
	if err := tt.Click("Ada"); err != nil {
		t.Fatal(err)
	}
	if s.router.Path() != routeAssistants || s.assistants.selected != "a_1" {
		t.Fatalf("path=%q selected=%q, want /assistants on a_1", s.router.Path(), s.assistants.selected)
	}
}

func TestAssistantsBridgeSnapshotReadsTheSelectedAssistant(t *testing.T) {
	calls := stubAssistants(t, false)
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
	if view.Detail.Assistant.Name != "Ada" || len(view.Detail.Approvals) != 0 {
		t.Fatalf("detail = %+v", view.Detail)
	}
	// The ledger rides the light poll: the newest sentence the core wrote.
	if len(view.Detail.Facts) != 2 || view.Detail.Facts[0].Kind != "delegated" {
		t.Fatalf("facts = %+v", view.Detail.Facts)
	}
	// The poll stays light: the long lists are the drawer's, read on demand.
	for _, c := range *calls {
		if strings.HasPrefix(c[0], "goal list") || strings.HasPrefix(c[0], "goal plan") || strings.HasPrefix(c[0], "routine") || strings.HasPrefix(c[0], "task") || strings.HasPrefix(c[0], "memory") {
			t.Fatalf("the light snapshot ran %q", c[0])
		}
	}
	d, err := b.Drawer(context.Background(), "a_1")
	if err != nil || len(d.Goals) != 1 || len(d.Tasks) != 1 {
		t.Fatalf("drawer = %+v err=%v", d, err)
	}
	if len(d.Routines) != 1 || d.Routines[0].Name != "morning-check" || d.Routines[0].State != "draft" {
		t.Fatalf("routines = %+v", d.Routines)
	}
	if len(d.Plans) != 1 || d.Plans[0].GoalID != "g_1" || d.Plans[0].Milestones[0].State != "working" || len(d.Facts) != 2 {
		t.Fatalf("plans/facts = %+v / %+v", d.Plans, d.Facts)
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

func coordinatorJSON(id, state, pane string) string {
	return fmt.Sprintf(`{"schema":1,"coordinator":{"assistantId":%q,"state":%q,"taskId":"t_c","herdrSession":"default","paneId":%q,"revision":2}}`, id, state, pane)
}

// SOLCO-A03 / SOLCO-R04: with two Assistants in one Workspace, each Chat binds
// only to its own Coordinator's pane, and a pane without a reported session is
// never guessed at.
func TestAssistantsChatBindsOnlyToItsOwnCoordinator(t *testing.T) {
	dir := t.TempDir()
	write := func(name, secret string) string {
		path := filepath.Join(dir, name+".jsonl")
		line := `{"type":"user","cwd":"/work","message":{"content":"` + secret + `"}}` + "\n"
		if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	alpha, beta := write("alpha", "alpha-private-plan"), write("beta", "beta-private-plan")
	stubAssistants(t, false)
	assistantsCoordinators["a_1"] = coordinatorJSON("a_1", "ready", "p1")
	assistantsCoordinators["a_2"] = coordinatorJSON("a_2", "ready", "p2")
	assistantsCoordinators["a_3"] = coordinatorJSON("a_3", "ready", "p3")
	assistantsCoordinators["a_4"] = coordinatorJSON("a_4", "starting", "")
	s := openAssistantsShell(t)
	identity := func(path string) *herdr.AgentSessionIdentity {
		return &herdr.AgentSessionIdentity{Agent: "claude-code", Kind: "path", Source: "transcript", Value: path}
	}
	s.projection = herdr.Projection{Agents: []herdr.Agent{
		{TerminalID: "t1", PaneID: "p1", Status: "idle", AgentSession: identity(alpha)},
		{TerminalID: "t2", PaneID: "p2", Status: "idle", AgentSession: identity(beta)},
		// A Claude pane whose title and folder look like Assistant three's, but
		// that never reported a session: nothing may be guessed from them.
		{TerminalID: "t3", PaneID: "p3", Status: "idle", Name: "a_3 coordinator"},
	}}
	b := NewAssistantsBridge(s)

	got, err := b.BindCoordinator(context.Background(), "a_1")
	if err != nil || !got.Bound || s.chatPaneID != "p1" {
		t.Fatalf("a_1 = %+v pane=%q err=%v", got, s.chatPaneID, err)
	}
	if turns := fmt.Sprintf("%+v", s.chatTurns); !strings.Contains(turns, "alpha-private-plan") || strings.Contains(turns, "beta-private-plan") {
		t.Fatalf("a_1's Chat shows the wrong conversation: %s", turns)
	}
	got, err = b.BindCoordinator(context.Background(), "a_2")
	if err != nil || !got.Bound || s.chatPaneID != "p2" {
		t.Fatalf("a_2 = %+v pane=%q err=%v", got, s.chatPaneID, err)
	}
	if turns := fmt.Sprintf("%+v", s.chatTurns); !strings.Contains(turns, "beta-private-plan") || strings.Contains(turns, "alpha-private-plan") {
		t.Fatalf("a_2's Chat leaks a_1's conversation: %s", turns)
	}

	// A pane with no reported session is never guessed at (no title/folder
	// match): the plugin's own record names p3, so the Chat lands on the
	// new-session invitation for exactly that pane and shows no conversation.
	if got, err = b.BindCoordinator(context.Background(), "a_3"); err != nil || !got.Bound || !got.Invitation || s.chatPaneID != "p3" {
		t.Fatalf("a pane with no reported session lands on the invitation: %+v pane=%q %v", got, s.chatPaneID, err)
	}
	if turns := fmt.Sprintf("%+v", s.chatTurns); strings.Contains(turns, "alpha-private-plan") || strings.Contains(turns, "beta-private-plan") || s.chatConversationID != "" {
		t.Fatalf("a_3's invitation inherits a conversation: %s %q", turns, s.chatConversationID)
	}
	if got, err = b.BindCoordinator(context.Background(), "a_4"); err != nil || got.Bound || got.State != "starting" {
		t.Fatalf("a starting coordinator has no Chat yet: %+v %v", got, err)
	}
	if got, err = b.BindCoordinator(context.Background(), "a_9"); err != nil || got.Bound || got.State != "none" {
		t.Fatalf("an Assistant with no Coordinator: %+v %v", got, err)
	}
}

func TestAssistantsBridgeSnapshotCarriesTheCoordinator(t *testing.T) {
	stubAssistants(t, false)
	assistantsCoordinators["a_1"] = coordinatorJSON("a_1", "ready", "w1:p1")
	s := openAssistantsShell(t)
	s.projection = herdr.Projection{Panes: []herdr.Pane{{ID: "w1:p1"}}}
	view, err := NewAssistantsBridge(s).Snapshot(context.Background())
	if err != nil || view.Detail == nil || view.Detail.Coordinator.State != "ready" || !view.Detail.CoordinatorOpen {
		t.Fatalf("view = %+v err=%v", view.Detail, err)
	}
	if in := view.Detail.Inbox; in.NeedsYou != 1 || len(in.Items) != 1 || in.Items[0].Kind != "stalled" {
		t.Fatalf("the page carries the plugin's own inbox: %+v", in)
	}
	s.projection = herdr.Projection{}
	view, _ = NewAssistantsBridge(s).Snapshot(context.Background())
	if view.Detail.CoordinatorOpen {
		t.Fatal("a Coordinator whose pane left the tree must not be openable")
	}
}

// TestBindChatToCardKeepsWorkspaceSelection pins the Assistants chat's
// no-navigation rule (2026-11-04): binding the embedded Chat to a Coordinator's
// pane must not move the Mac user's Workspace selection, and a fresh agent with
// no transcript lands on the new-session invitation (bound, sourceless).
func TestBindChatToCardKeepsWorkspaceSelection(t *testing.T) {
	shell := NewShell()
	shell.loading = false
	shell.selectedPaneID = "p-work"
	shell.selectedTabID = "tab-work"
	shell.projection = herdr.Projection{
		Agents: []herdr.Agent{
			{TerminalID: "term-work", PaneID: "p-work", Status: "idle"},
			{TerminalID: "term-co", PaneID: "p-co", Status: "idle"},
		},
	}
	card, ok := shell.agentCardForPane("p-co")
	if !ok {
		t.Fatal("coordinator card missing from the projection")
	}
	if !shell.bindChatToCard(card) {
		t.Fatal("a card with no transcript yet must land on the invitation (bound)")
	}
	if shell.selectedPaneID != "p-work" || shell.selectedTabID != "tab-work" {
		t.Fatalf("selection moved to pane=%q tab=%q", shell.selectedPaneID, shell.selectedTabID)
	}
	if shell.chatPaneID != "p-co" || shell.chatSourcelessPaneID != "p-co" {
		t.Fatalf("chat pane=%q latch=%q, want the coordinator", shell.chatPaneID, shell.chatSourcelessPaneID)
	}
	if got := shell.chatBridgeState(); !got.Waiting {
		t.Fatalf("state = %+v, want the invitation (Waiting)", got)
	}
}

func coordinatorStartCalls(calls *[][2]string) (n int, stdin string) {
	for _, c := range *calls {
		if c[0] == "coordinator start a_1" {
			n++
			stdin = c[1]
		}
	}
	return
}

// The first Send starts a Coordinator that is not there, carrying the chosen
// agent and the caller's requestId; a Coordinator that is already starting or
// ready is left alone (2026-11-04 ensure).
func TestAssistantsEnsureChatStartsOnlyWhatIsMissing(t *testing.T) {
	calls := stubAssistants(t, false)
	s := openAssistantsShell(t)
	s.launchKinds = []app.SessionKind{{ID: app.TerminalKind, Label: "Terminal"}, {ID: "omp", Label: "OMP"}, {ID: "claude-code", Label: "Claude Code"}, {ID: "droid", Label: "Droid"}}
	b := NewAssistantsBridge(s)

	got, err := b.EnsureChat(context.Background(), "req-start", "a_1", "omp")
	if err != nil || got.State != "starting" {
		t.Fatalf("ensure = %+v err=%v", got, err)
	}
	n, stdin := coordinatorStartCalls(calls)
	if n != 1 || !strings.Contains(stdin, `"requestId":"req-start"`) || !strings.Contains(stdin, `"agent":"omp"`) {
		t.Fatalf("start calls = %d stdin=%q", n, stdin)
	}

	assistantsCoordinators["a_1"] = coordinatorJSON("a_1", "starting", "")
	if _, err := b.EnsureChat(context.Background(), "req-2", "a_1", "omp"); err != nil {
		t.Fatal(err)
	}
	assistantsCoordinators["a_1"] = coordinatorJSON("a_1", "ready", "p9")
	if _, err := b.EnsureChat(context.Background(), "req-3", "a_1", "omp"); err != nil {
		t.Fatal(err)
	}
	if n, _ := coordinatorStartCalls(calls); n != 1 {
		t.Fatalf("a starting or ready Coordinator must not be started again (%d starts)", n)
	}

	assistantsCoordinators["a_1"] = coordinatorJSON("a_1", "failed", "")
	if _, err := b.EnsureChat(context.Background(), "req-4", "a_1", "omp"); err != nil {
		t.Fatal(err)
	}
	if n, _ := coordinatorStartCalls(calls); n != 2 {
		t.Fatalf("a failed Coordinator is started again by the next Send (%d starts)", n)
	}
}

// The chip's choice is fenced to the agents Settings offers and the Chat can
// read; nothing reaches the plugin for any other kind.
func TestAssistantsEnsureChatRefusesAnAgentTheChatCannotRead(t *testing.T) {
	calls := stubAssistants(t, false)
	s := openAssistantsShell(t)
	s.launchKinds = []app.SessionKind{{ID: "omp", Label: "OMP"}, {ID: "droid", Label: "Droid"}}
	b := NewAssistantsBridge(s)
	for _, kind := range []string{"droid", "claude", "pi", "omp; rm -rf /"} {
		if _, err := b.EnsureChat(context.Background(), "r", "a_1", kind); err == nil {
			t.Fatalf("agent %q must be refused", kind)
		}
	}
	if n, _ := coordinatorStartCalls(calls); n != 0 {
		t.Fatalf("a refused agent started %d Coordinators", n)
	}
	view, _ := b.Snapshot(context.Background())
	if len(view.Agents) != 1 || view.Agents[0].ID != "omp" || view.DefaultAgent != "omp" {
		t.Fatalf("agents = %+v default=%q, want omp only (other Agents are locked)", view.Agents, view.DefaultAgent)
	}
}

// The Coordinator's standing brief is the first user row of its transcript;
// the Assistants page's Chat does not show it as something the person said.
func TestAssistantsChatHidesTheCoordinatorBrief(t *testing.T) {
	shell := NewShell()
	shell.chatPaneID = "p9"
	shell.chatBriefPane = "p9"
	shell.chatTurns = []conversation.TimelineTurn{
		{UserRow: true, Text: "hi"},
		{UserRow: true, Text: assistantBriefPrefix + ` "Ada" (id a_1). …`},
		{UserRow: false, Narration: "Ready."},
	}
	turns := shell.chatBridgeState().Turns
	if len(turns) != 2 || !turns[0].User || turns[0].Text != "hi" || turns[1].User {
		t.Fatalf("turns = %+v", turns)
	}
	shell.chatBriefPane = "other"
	if got := shell.chatBridgeState().Turns; len(got) != 3 {
		t.Fatalf("a pane the page did not bind keeps its first row: %+v", got)
	}
}

// The daemon prefixes every prompt it types with a `Solco-Run-Id:` marker line
// (2026-11-05 isolated Mac.app run: the 132-line brief showed up as the person's
// own message). The brief still leaves the user rows, and its text rides on the
// next row as a collapsed "standing instructions" note instead of vanishing.
func TestAssistantsChatFoldsTheMarkedCoordinatorBrief(t *testing.T) {
	shell := NewShell()
	shell.chatPaneID = "p9"
	shell.chatBriefPane = "p9"
	brief := "Solco-Run-Id: r_5e69acd65e25cd40\n\n" + assistantBriefPrefix + ` "Ada" (id a_1). …`
	shell.chatTurns = []conversation.TimelineTurn{
		{UserRow: true, Text: brief},
		{UserRow: false, Narration: "Ready."},
		{UserRow: true, Text: "hi"},
	}
	turns := shell.chatBridgeState().Turns
	if len(turns) != 2 || turns[0].User || turns[0].Narration != "Ready." || !turns[1].User {
		t.Fatalf("turns = %+v, want the brief row folded away", turns)
	}
	if !strings.Contains(turns[0].NoteBefore, `Ada" (id a_1)`) || strings.Contains(turns[0].NoteBefore, "Solco-Run-Id") {
		t.Fatalf("note = %q, want the brief without its run marker", turns[0].NoteBefore)
	}
}

// Real-device AC-02 (F3): Solco's own automated status request / wake is typed
// into the Coordinator like the brief and showed up as the person's message.
func TestAssistantsChatFoldsAutomatedStatusRequests(t *testing.T) {
	shell := NewShell()
	shell.chatPaneID = "p9"
	shell.chatBriefPane = "p9"
	shell.chatTurns = []conversation.TimelineTurn{
		{UserRow: true, Text: "hi"},
		{UserRow: false, Narration: "Hello."},
		{UserRow: true, Text: "Solco-Run-Id: r_1\n\n[Solco automated status request — NOT a message from the human]\nSolco-Report-Id: x"},
		{UserRow: false, Narration: "Progress: routine finished."},
		{UserRow: true, Text: "[Solco automated wake — NOT a message from the human]\nreason"},
		{UserRow: false, Narration: "Noted."},
	}
	turns := shell.chatBridgeState().Turns
	if len(turns) != 4 || !turns[0].User || turns[0].Text != "hi" {
		t.Fatalf("turns = %+v", turns)
	}
	if !strings.Contains(turns[2].NoteBefore, "automated status request") || strings.Contains(turns[2].NoteBefore, "Solco-Run-Id") {
		t.Fatalf("status request note = %q", turns[2].NoteBefore)
	}
	if !strings.Contains(turns[3].NoteBefore, "automated wake") {
		t.Fatalf("wake note = %q", turns[3].NoteBefore)
	}
}

func TestAssistantsGroupRowOpensTheGroupConversation(t *testing.T) {
	assistantsOverviewOverride = assistantsOverviewWithGroup
	stubAssistants(t, false)
	s := openAssistantsShell(t)
	tt := ui.NewTester(s.View, 1200, 800)
	if !tt.HasText("Launch crew") {
		t.Fatalf("sidebar missing the Group row: %q", tt.Texts())
	}
	if err := tt.Click("Launch crew"); err != nil {
		t.Fatal(err)
	}
	if s.router.Path() != routeAssistants || s.assistants.selectedGroup != "gr_1" {
		t.Fatalf("path=%q group=%q, want /assistants on gr_1", s.router.Path(), s.assistants.selectedGroup)
	}
	b := NewAssistantsBridge(s)
	view, err := b.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.SelectedGroup != "gr_1" || view.Detail != nil || len(view.Groups) != 1 {
		t.Fatalf("a Group snapshot is the Group alone: %+v", view)
	}
	// Choosing an Assistant leaves the Group; a vanished Group clears itself.
	b.Select("a_2")
	if s.assistants.selectedGroup != "" {
		t.Fatalf("Select kept the Group: %q", s.assistants.selectedGroup)
	}
	s.assistants.selectedGroup = "gr_gone"
	s.applyAssistantsOverview(personalagent.Overview{Assistants: view.Assistants, Groups: view.Groups}, nil)
	if s.assistants.selectedGroup != "" {
		t.Fatalf("an unknown Group stayed selected: %q", s.assistants.selectedGroup)
	}
}

func TestAssistantsMemoryAndBehaviorBridge(t *testing.T) {
	calls := stubAssistants(t, false)
	var opened string
	old := openFolder
	openFolder = func(p string) error { opened = p; return nil }
	t.Cleanup(func() { openFolder = old })
	s := openAssistantsShell(t)
	b := NewAssistantsBridge(s)
	ctx := context.Background()

	review, err := b.Memory(ctx, "a_1")
	if err != nil || len(review.Items) != 2 || review.Candidates != 1 || review.Items[1].State != "candidate" {
		t.Fatalf("memory = %+v %v", review, err)
	}
	if hist, err := b.MemoryHistory(ctx, "m_1"); err != nil || len(hist) != 2 || hist[0].Author != "files" {
		t.Fatalf("history = %+v %v", hist, err)
	}
	if err := b.AddMemory(ctx, "r1", "a_1", "likes tea"); err != nil {
		t.Fatal(err)
	}
	if err := b.EditMemory(ctx, "r2", "m_1", 2, "new text"); err != nil {
		t.Fatal(err)
	}
	if err := b.ApproveMemory(ctx, "r3", "m_2", 1); err != nil {
		t.Fatal(err)
	}
	if err := b.ForgetMemory(ctx, "r4", "m_1", 3); err != nil {
		t.Fatal(err)
	}
	if err := b.OpenMemoryFolder(ctx, "a_1"); err != nil || opened != "/tmp/solco-memory/a_1/memory" {
		t.Fatalf("open = %q %v", opened, err)
	}
	p, err := b.Behavior(ctx, "a_1")
	if err != nil || p.Level != "follow_up" || p.DailyBudget != 20 || p.Revision != 0 {
		t.Fatalf("behavior = %+v %v", p, err)
	}
	if err := b.SaveBehavior(ctx, "r5", "a_1", personalagent.ProactivityInput{Level: "suggest", DailyBudget: 10}, 0); err != nil {
		t.Fatal(err)
	}
	var sent []string
	for _, c := range *calls {
		sent = append(sent, c[0]+"|"+c[1])
	}
	all := strings.Join(sent, "\n")
	for _, want := range []string{`memory add|{"requestId":"r1"`, `"assistantId":"a_1"`, `memory correct m_1|{"requestId":"r2","expectedRevision":2,"content":"new text"}`,
		`memory approve m_2|{"requestId":"r3","expectedRevision":1}`, `memory forget m_1|{"requestId":"r4","expectedRevision":3}`,
		`proactivity set a_1|{"requestId":"r5"`, `"level":"suggest"`} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q in\n%s", want, all)
		}
	}
}

// A Workspace row is the way out of /assistants even when it is the Workspace
// that was selected before the page opened (2026-11-04 user report: the click
// only toggled the row and the page stayed).
func TestAssistantsPageLeavesThroughAWorkspaceRow(t *testing.T) {
	stubAssistants(t, false)
	s := openAssistantsShell(t)
	s.projection = herdr.Projection{
		FocusedProjectID: "w1",
		Projects:         []herdr.Project{{ID: "w1", Label: "Project A", TabCount: 1}},
		Tabs:             []herdr.Tab{{ID: "t1", ProjectID: "w1", Label: "Tab 1", PaneCount: 1}},
	}
	s.selectedProjectID = "w1"
	tt := ui.NewTester(s.View, 1200, 800)
	if err := tt.Click("Ada"); err != nil {
		t.Fatal(err)
	}
	if s.router.Path() != routeAssistants {
		t.Fatalf("path=%q, want /assistants", s.router.Path())
	}
	tt.Frame()
	if err := tt.Click("Project A"); err != nil {
		t.Fatal(err)
	}
	if s.router.Path() != routeWorkspace {
		t.Fatalf("path=%q after clicking the selected Workspace row, want /workspace", s.router.Path())
	}
}

func TestAssistantsBridgeReportsUncertainProgressAndFencesOwnerResolution(t *testing.T) {
	calls := stubAssistants(t, false)
	assistantsReportOverride = `{"schema":1,"report":{"assistantId":"a_1","state":"uncertain","fromSeq":4,"throughSeq":9,"submittedSeq":4,"attempts":1,"lastError":"Herdr submission outcome is unknown"}}`
	s := openAssistantsShell(t)
	bridge := NewAssistantsBridge(s)
	view, err := bridge.Snapshot(t.Context())
	if err != nil || view.Detail == nil || view.Detail.Report == nil || view.Detail.Report.State != "uncertain" || view.Detail.Report.ThroughSeq != 9 {
		t.Fatalf("uncertain report missing from light snapshot: %+v %v", view.Detail, err)
	}
	if err := bridge.ResolveCoordinatorReport(t.Context(), "owner-checked", "a_1", 9, "skip", true); err != nil {
		t.Fatalf("bridge resolve: %v", err)
	}
	var found bool
	for _, c := range *calls {
		if c[0] == "report resolve a_1" {
			found = true
			if !strings.Contains(c[1], `"requestId":"owner-checked"`) ||
				!strings.Contains(c[1], `"throughSeq":9`) ||
				!strings.Contains(c[1], `"action":"skip"`) ||
				!strings.Contains(c[1], `"confirmed":true`) {
				t.Fatalf("owner decision lost its exact sequence/action: %q", c[1])
			}
		}
	}
	if !found {
		t.Fatal("owner report decision did not reach the plugin")
	}
}
