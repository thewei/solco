package nativeui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/personalagent"
)

/**
 * [INPUT]: 依赖 assistants_state/section/bridge 与 personalagent.NewClientWithExec（插件 CLI 的 fake exec，绝不跑真二进制）、ui.Tester 无头渲染
 * [OUTPUT]: 门接线回归（插件门+探测才开、门关零痕迹且 Projects 区照旧、探测失败保持关闭）、侧栏 Assistants 区取代 Projects 区并可点击进页、桥 Snapshot 读视图（概览 + 选中 Assistant 的 Goal/Task/Approval/Run，窗格存在才可打开）、PR Watch 的 Goal 请求体、门关拒绝
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
			case "memory read a_1":
				return []byte(`{"schema":1,"notes":{"assistantId":"a_1","user":{"text":"I like short reports.\n","rev":"u1"},"assistant":{"text":"- Reports go to the team channel\n","rev":"s1"}}}`), nil
			case "memory folder a_1":
				return []byte(`{"schema":1,"path":"/tmp/solco-memory/a_1"}`), nil
			case "proactivity show a_1":
				return []byte(`{"schema":1,"proactivity":{"assistantId":"a_1","level":"follow_up","quietStart":"","quietEnd":"","tz":"","reportEveryMin":0,"dailyBudget":20,"dispatchToday":0,"quietNow":false,"revision":0,"updatedAt":0}}`), nil
			case "memory write a_1", "memory write-user a_1":
				return []byte(`{"schema":1,"notes":{"assistantId":"a_1","user":{"text":"x","rev":"u2"},"assistant":{"text":"y","rev":"s2"}}}`), nil
			case "proactivity set a_1":
				return []byte(`{"schema":1,"replayed":false,"proactivity":{"assistantId":"a_1","level":"suggest","dailyBudget":10,"revision":1}}`), nil
			case "goal plan g_1":
				return []byte(`{"schema":1,"plan":{"goalId":"g_1","assistantId":"a_1","successCriteria":"Merged","milestones":[{"id":"m1","title":"Review","state":"working","task":{"kind":"research","title":"Review","brief":"b"},"taskId":"t_1"}],"nextAction":"","planRevision":1,"replans":0,"maxTasks":12,"staleAfterMs":2700000,"verified":0,"total":1,"state":"working","revisions":[],"updatedAt":1}}`), nil
			case "facts a_1 --limit 6", "facts a_1 --limit 40":
				return []byte(`{"schema":1,"facts":[{"seq":2,"id":"f_2","assistantId":"a_1","goalId":"g_1","kind":"delegated","text":"Delegated: Review","createdAt":2},{"seq":1,"id":"f_1","assistantId":"a_1","goalId":"g_1","kind":"accepted","text":"Accepted: Watch PR","createdAt":1}]}`), nil
			case "results a_1 --limit 4":
				return []byte(`{"schema":1,"results":[{"taskId":"t_2","title":"Ship it","state":"failed","at":9,"herdrSession":"default","paneId":"w9:p9"},{"taskId":"t_1","title":"Check it","state":"done","at":8,"herdrSession":"default","paneId":"w1:p1"}]}`), nil
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
			case "goal create":
				return []byte(`{"schema":1,"goal":{"id":"g_2"}}`), nil
			case "group archived":
				return []byte(`{"schema":1,"groups":[{"id":"gr_1","title":"Launch crew","status":"archived","members":[{"assistantId":"a_1","name":"Ada","status":"active"},{"assistantId":"a_2","name":"Bo","status":"active"}],"lastSeq":3,"revision":4}]}`), nil
			case "group archive gr_1", "group restore gr_1":
				return []byte(`{"schema":1,"replayed":false,"group":{"id":"gr_1","title":"Launch crew","status":"archived","members":[],"revision":5}}`), nil
			case "group delete-forever gr_1":
				return []byte(`{"schema":1,"deleted":true}`), nil
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
	// Settled work is a system card above the composer; a pane opens only while it is in this window's tree.
	if r := view.Detail.Results; len(r) != 2 || r[0].State != "failed" || r[0].PaneID != "" || r[1].State != "done" || r[1].PaneID != "w1:p1" || r[1].Title != "Check it" {
		t.Fatalf("results = %+v", view.Detail.Results)
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
	s.router.Push(routeAssistants) // the bridge only acts while the page is on screen
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
	// A ready Coordinator the Chat reaches is left alone.
	assistantsCoordinators["a_1"] = coordinatorJSON("a_1", "ready", "p9")
	s.projection = herdr.Projection{Agents: []herdr.Agent{{TerminalID: "t9", PaneID: "p9", Status: "idle"}}}
	if got, err := b.EnsureChat(context.Background(), "req-3", "a_1", "omp"); err != nil || !got.Bound {
		t.Fatalf("a reachable ready Coordinator binds: %+v %v", got, err)
	}
	if n, _ := coordinatorStartCalls(calls); n != 1 {
		t.Fatalf("a starting or reachable ready Coordinator must not be started again (%d starts)", n)
	}

	assistantsCoordinators["a_1"] = coordinatorJSON("a_1", "failed", "")
	if _, err := b.EnsureChat(context.Background(), "req-4", "a_1", "omp"); err != nil {
		t.Fatal(err)
	}
	if n, _ := coordinatorStartCalls(calls); n != 2 {
		t.Fatalf("a failed Coordinator is started again by the next Send (%d starts)", n)
	}
}

// 2026-11-07: a Coordinator Solco lists as ready, uncertain or blocked but the
// Chat cannot reach (its pane left the tree) is offered to the plugin as a
// start, so a dead one never traps the person's message; the plugin alone
// decides from Herdr whether it is really gone. A reachable one is untouched.
func TestAssistantsEnsureChatOffersAnUnreachableCoordinatorToThePlugin(t *testing.T) {
	calls := stubAssistants(t, false)
	s := openAssistantsShell(t)
	s.launchKinds = []app.SessionKind{{ID: "omp", Label: "OMP"}}
	b := NewAssistantsBridge(s)
	for i, state := range []string{"ready", "uncertain", "blocked"} {
		assistantsCoordinators["a_1"] = coordinatorJSON("a_1", state, "w82:p1")
		got, err := b.EnsureChat(context.Background(), fmt.Sprintf("req-%d", i), "a_1", "omp")
		if err != nil || got.Bound {
			t.Fatalf("%s: ensure = %+v err=%v", state, got, err)
		}
		if n, _ := coordinatorStartCalls(calls); n != i+1 {
			t.Fatalf("%s Coordinator that the Chat cannot reach must be offered to the plugin (%d starts after %d)", state, n, i+1)
		}
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
	if len(turns) != 1 || !turns[0].User || turns[0].Text != "hi" {
		t.Fatalf("turns = %+v, want the brief and the greeting it got left out", turns)
	}
	shell.chatBriefPane = "other"
	if got := shell.chatBridgeState().Turns; len(got) != 3 {
		t.Fatalf("a pane the page did not bind keeps its first row: %+v", got)
	}
}

// The daemon prefixes every prompt it types with a `Solco-Run-Id:` marker line
// (2026-11-05 isolated Mac.app run: the 132-line brief showed up as the person's
// own message). The marker does not hide the brief, and the Agent's greeting of
// it goes with it — a restart types the brief again (2026-11-07 real-device run:
// "No new request came through…" sat between two of the person's messages).
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
	if len(turns) != 1 || !turns[0].User || turns[0].Text != "hi" || turns[0].NoteBefore != "" {
		t.Fatalf("turns = %+v, want the marked brief and its greeting gone", turns)
	}
}

// A restart types the brief again in the middle of the transcript: only that
// row and the answer up to the next user row go; the answers around it stay, and
// so does the answer to an automated prompt.
func TestAssistantsChatHidesTheGreetingOfARepeatedBrief(t *testing.T) {
	brief := "Solco-Run-Id: r_2\n\n" + assistantBriefPrefix + ` "Ada" (id a_1). …`
	turns := withoutAssistantBrief([]chatsession.ChatTurn{
		{User: true, Text: "first"},
		{Narration: "Answer one."},
		{User: true, Text: brief},
		{Narration: "No new request came through."},
		{Narration: "(its second part)"},
		{User: true, Text: "second"},
		{Narration: "Answer two."},
		{User: true, Text: "[Solco automated status request — NOT a message from the human]"},
		{Narration: "Progress: done."},
	}, true)
	var got []string
	for _, turn := range turns {
		got = append(got, turn.Text+turn.Narration)
	}
	want := []string{"first", "Answer one.", "second", "Answer two.", "Progress: done."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("turns = %v, want %v", got, want)
	}
	if !strings.Contains(turns[4].NoteBefore, "automated status request") {
		t.Fatalf("the answer to an automated prompt keeps its note: %q", turns[4].NoteBefore)
	}
	if bare := withoutAssistantBrief([]chatsession.ChatTurn{{User: true, Text: "[Solco automated wake — x]"}, {Narration: "Noted."}}, false); len(bare) != 1 || bare[0].NoteBefore != "" {
		t.Fatalf("without notes the answer stays bare: %+v", bare)
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

// Groups live under the Primary's page, not in the sidebar (2026-11-07): the
// sidebar lists Assistants only, and the page opens a Group through the bridge.
func TestAssistantsGroupsLiveUnderThePrimaryNotTheSidebar(t *testing.T) {
	assistantsOverviewOverride = assistantsOverviewWithGroup
	stubAssistants(t, false)
	s := openAssistantsShell(t)
	tt := ui.NewTester(s.View, 1200, 800)
	if tt.HasText("Launch crew") {
		t.Fatalf("the sidebar must not list a Group row: %q", tt.Texts())
	}
	if !tt.HasText("Ada") {
		t.Fatalf("the sidebar still lists the Assistants: %q", tt.Texts())
	}
	s.router.Push(routeAssistants)
	b := NewAssistantsBridge(s)
	b.SelectGroup("gr_1")
	if s.assistants.selectedGroup != "gr_1" {
		t.Fatalf("group=%q, want gr_1", s.assistants.selectedGroup)
	}
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
	var editor string
	oldFile := openMemoryFile
	openMemoryFile = func(p string) error { editor = p; return nil }
	t.Cleanup(func() { openMemoryFile = oldFile })
	s := openAssistantsShell(t)
	b := NewAssistantsBridge(s)
	ctx := context.Background()

	notes, err := b.Notes(ctx, "a_1")
	if err != nil || notes.User.Rev != "u1" || notes.Assistant.Text != "- Reports go to the team channel\n" {
		t.Fatalf("notes = %+v %v", notes, err)
	}
	if err := b.OpenMemoryFolder(ctx, "a_1"); err != nil || opened != "/tmp/solco-memory/a_1" {
		t.Fatalf("open = %q %v", opened, err)
	}
	if err := b.OpenMemoryFile(ctx, "a_1", "assistant"); err != nil || editor != "/tmp/solco-memory/a_1/ASSISTANT.md" {
		t.Fatalf("editor = %q %v", editor, err)
	}
	if err := b.OpenMemoryFile(ctx, "a_1", "user"); err != nil || editor != "/tmp/solco-memory/a_1/USER.md" {
		t.Fatalf("editor = %q %v", editor, err)
	}
	if err := b.OpenMemoryFile(ctx, "a_1", "../x"); err == nil {
		t.Fatal("an unknown file must be refused")
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
	// The desktop never writes memory itself: no write call goes out.
	if strings.Contains(all, "memory write") {
		t.Fatalf("the desktop must not write memory files:\n%s", all)
	}
	for _, want := range []string{`proactivity set a_1|{"requestId":"r5"`, `"level":"suggest"`} {
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

// Gap L06: a change that did not answer in time is not reported as a plain
// failure — it may have gone through, and the page must say so.
func TestAssistantsWriteTimeoutSaysItMayHaveGoneThrough(t *testing.T) {
	stubAssistants(t, false)
	s := openAssistantsShell(t)
	b := NewAssistantsBridge(s)
	err := b.writeWithin(context.Background(), time.Millisecond, func(ctx context.Context, _ *personalagent.Client) error {
		<-ctx.Done()
		return fmt.Errorf("personalagent: add: %w", ctx.Err())
	})
	if err == nil || !strings.Contains(err.Error(), "may have gone through") {
		t.Fatalf("timeout text = %v", err)
	}
	// A refusal keeps the plugin's own words.
	err = b.write(context.Background(), func(context.Context, *personalagent.Client) error {
		return &personalagent.Error{Code: "conflict", Message: "changed elsewhere"}
	})
	if !personalagent.IsCode(err, "conflict") {
		t.Fatalf("refusal lost its code: %v", err)
	}
	// A missing service says so.
	err = b.write(context.Background(), func(context.Context, *personalagent.Client) error { return personalagent.ErrUnavailable })
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("unavailable text = %v", err)
	}
}

func TestAssistantsBridgeArchiveGroupSendsTheRevisionAndInvalidatesTheArchiveCache(t *testing.T) {
	calls := stubAssistants(t, false)
	s := openAssistantsShell(t)
	s.assistants.archiveLoaded = true
	b := NewAssistantsBridge(s)
	if err := b.ArchiveGroup(context.Background(), "req-ga", "gr_1", 3); err != nil {
		t.Fatal(err)
	}
	var stdin string
	for _, c := range *calls {
		if c[0] == "group archive gr_1" {
			stdin = c[1]
		}
	}
	if !strings.Contains(stdin, `"requestId":"req-ga"`) || !strings.Contains(stdin, `"expectedRevision":3`) {
		t.Fatalf("archive body = %q", stdin)
	}
	if s.assistants.archiveLoaded {
		t.Fatal("the Settings archive must be re-read after a group joins it")
	}
}

func TestSettingsArchiveListsGroupsAndRestoresOrDeletesThemWithTheirRevision(t *testing.T) {
	calls := stubAssistants(t, false)
	s := openAssistantsShell(t)
	s.loadArchivedAssistants(true)
	if len(s.assistants.archivedGroups) != 1 || s.assistants.archivedGroups[0].ID != "gr_1" {
		t.Fatalf("archived groups = %+v", s.assistants.archivedGroups)
	}
	s.runGroupArchiveAction("restore", "gr_1")
	s.runGroupArchiveAction("delete", "gr_1")
	var restore, del string
	for _, c := range *calls {
		switch c[0] {
		case "group restore gr_1":
			restore = c[1]
		case "group delete-forever gr_1":
			del = c[1]
		}
	}
	if !strings.Contains(restore, `"expectedRevision":4`) || !strings.Contains(del, `"expectedRevision":4`) {
		t.Fatalf("restore=%q delete=%q", restore, del)
	}
	s.runGroupArchiveAction("delete", "gr_unknown") // a stale row never reaches the core
	for _, c := range *calls {
		if strings.Contains(c[0], "gr_unknown") {
			t.Fatalf("unknown group reached the core: %v", c)
		}
	}
}

// The Primary is named in its row's tooltip; nobody else is.
func TestAssistantTipNamesThePrimary(t *testing.T) {
	if got := assistantTip(personalagent.OverviewRow{Name: "Ada", Status: "active", Primary: true}); got != "Ada · Primary" {
		t.Fatalf("tip = %q", got)
	}
	if got := assistantTip(personalagent.OverviewRow{Name: "Bo", Status: "active"}); got != "Bo" {
		t.Fatalf("tip = %q", got)
	}
}

// A fresh core (no Assistant at all) gets one Primary, "Solco", made once with
// a fixed requestId; a core that already has Assistants is left exactly as it
// is.
func TestAssistantsEmptyCoreGetsADefaultPrimaryOnce(t *testing.T) {
	calls := stubAssistants(t, false)
	assistantsOverviewOverride = `{"schema":1,"assistants":[],"groups":[],"needsYou":0,"activitySeq":0}`
	s := openAssistantsShell(t)
	s.refreshAssistants(true)

	creates := 0
	for _, c := range *calls {
		if c[0] == "create" {
			creates++
			if !strings.Contains(c[1], `"requestId":"default-primary"`) || !strings.Contains(c[1], `"name":"Solco"`) {
				t.Fatalf("the default Primary must carry its fixed requestId and name: %s", c[1])
			}
		}
	}
	if creates != 1 {
		t.Fatalf("an empty core must get exactly one default Primary, got %d (%v)", creates, *calls)
	}

	// A populated core is untouched.
	assistantsOverviewOverride = ""
	populated := stubAssistants(t, false)
	s2 := openAssistantsShell(t)
	s2.refreshAssistants(true)
	for _, c := range *populated {
		if c[0] == "create" {
			t.Fatalf("a core with Assistants must not get a default one: %v", *populated)
		}
	}
}
