package nativeui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/workboard"
)

func supportedWorkboardShell() *Shell {
	s := NewShell()
	s.workboard.avail = workboard.Availability{Installed: true, Enabled: true, Version: "1.2.26", Root: "/plugin", Supported: true}
	s.settings.General.WorkboardView = "native" // these tests exercise the native pages
	return s
}

func TestProjectForWorktree(t *testing.T) {
	p := herdr.Projection{
		Projects: []herdr.Project{
			{ID: "w1", CWD: "/work/app"},
			{ID: "w2", CWD: "/work/app-wt/r-1/src"}, // shell cd'd into a subfolder
			{ID: "w3", CWD: "/work/app-wt/r-10"},
			{ID: "w5", CWD: "/private/tmp/acc/wt/r-7"},
			{ID: "w6", CWD: "/var/folders/x/r-8"},
			{ID: "w7", CWD: "/home/r-9"},
		},
		Panes: []herdr.Pane{{ID: "p9", ProjectID: "w4", CWD: "/work/app-wt/r-4/deep"}},
	}
	cases := []struct {
		name, worktree, want string
	}{
		{"project cwd inside the worktree", "/work/app-wt/r-1", "w2"},
		{"project cwd is the worktree", "/work/app", "w1"},
		{"trailing slash", "/work/app-wt/r-1/", "w2"},
		{"sibling with a shared prefix does not match", "/work/app-wt/r-", ""},
		{"r-1 must not claim r-10", "/work/app-wt/r-10", "w3"},
		{"matched through a pane cwd", "/work/app-wt/r-4", "w4"},
		{"no worktree yet", "", ""},
		{"/tmp worktree against a /private/tmp cwd", "/tmp/acc/wt/r-7", "w5"},
		{"/private/var worktree against a /var cwd", "/private/var/folders/x/r-8", "w6"},
		{"/private elsewhere is not folded", "/private/home/r-9", ""},
		{"unknown worktree", "/elsewhere", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := projectForWorktree(p, tc.worktree); got != tc.want {
				t.Fatalf("projectForWorktree(%q) = %q, want %q", tc.worktree, got, tc.want)
			}
		})
	}
}

func fakeBoardClient(list string, details map[string]string, calls *[]string) *workboard.Client {
	return workboard.NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
		*calls = append(*calls, strings.Join(args, " "))
		if args[0] == "list" {
			return list, nil
		}
		if raw, ok := details[args[1]]; ok {
			return raw, nil
		}
		return "", errors.New("no such task")
	})
}

const boardList = `[
 {"id":"r/1","title":"Finished work","state":"blocked","kind":"permission","stage":"approval","msg":"Approve to merge? Did v1."},
 {"id":"r/2","title":"Asks a question","state":"blocked","kind":"question","stage":"working","msg":"which db?"},
 {"id":"r/3","title":"Busy","state":"working","stage":"working","msg":""},
 {"id":"r/4","title":"Later","state":"idle","stage":"todo","msg":""},
 {"id":"r/5","title":"Shipped","state":"idle","stage":"done","msg":""}
]`

func TestReadWorkboardReadsOnlyUsefulDetailsAndCachesThem(t *testing.T) {
	var calls []string
	client := fakeBoardClient(boardList, map[string]string{
		"r/1": `{"worktree":"/wt/r-1"}`,
		"r/2": `{"worktree":"/wt/r-2"}`,
		"r/3": `{"worktree":"/wt/r-3"}`,
	}, &calls)
	tasks, details, err := readWorkboard(context.Background(), client, nil)
	if err != nil || len(tasks) != 5 {
		t.Fatalf("tasks=%d err=%v", len(tasks), err)
	}
	// todo and done rows never cost a details call.
	for _, call := range calls {
		if strings.Contains(call, "r/4") || strings.Contains(call, "r/5") {
			t.Fatalf("unneeded details call %q", call)
		}
	}
	if len(details) != 3 {
		t.Fatalf("details cached for %d tasks, want 3", len(details))
	}

	// Unchanged rows are served from the cache: only the list is re-read.
	calls = nil
	if _, _, err := readWorkboard(context.Background(), client, details); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "list --json" {
		t.Fatalf("a warm cache must cost one list call, got %v", calls)
	}

	// A changed row fingerprint invalidates just that task's details.
	changed := strings.Replace(boardList, `"title":"Busy","state":"working","stage":"working","msg":""`, `"title":"Busy","state":"working","stage":"working","msg":"step 2"`, 1)
	calls = nil
	client = fakeBoardClient(changed, map[string]string{"r/3": `{"worktree":"/wt/r-3"}`}, &calls)
	if _, _, err := readWorkboard(context.Background(), client, details); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1] != "details r/3 --json" {
		t.Fatalf("calls = %v, want list + details r/3", calls)
	}
}

func TestReadWorkboardCapsDetailReads(t *testing.T) {
	var rows []string
	for i := 0; i < workboardDetailCap+9; i++ {
		rows = append(rows, `{"id":"r/`+itoa(i+1)+`","title":"t","state":"working","stage":"working","msg":""}`)
	}
	var calls []string
	client := fakeBoardClient("["+strings.Join(rows, ",")+"]", nil, &calls)
	if _, _, err := readWorkboard(context.Background(), client, nil); err != nil {
		t.Fatal(err)
	}
	if got := len(calls) - 1; got != workboardDetailCap {
		t.Fatalf("details reads = %d, want the cap %d", got, workboardDetailCap)
	}
}

func TestReadWorkboardListFailureIsAnError(t *testing.T) {
	client := workboard.NewClientWithExec(func(context.Context, ...string) (string, error) { return "", errors.New("down") })
	if _, _, err := readWorkboard(context.Background(), client, nil); err == nil {
		t.Fatal("a failed list must be an error")
	}
}

// The grouping is the Option-A contract: a finished task is only "Ready to
// approve" with its details in hand; without them it is "Needs you".
func TestWorkboardGroupsOrderAndSafety(t *testing.T) {
	tasks, err := workboard.ParseTasks(boardList)
	if err != nil {
		t.Fatal(err)
	}
	board := workboardBoard{tasks: tasks}
	labels := func() []string {
		var out []string
		for _, g := range workboardGroups(board) {
			out = append(out, g.Group.Label()+":"+itoa(len(g.Tasks)))
		}
		return out
	}
	// No details yet: r/1 is doubt, so it sits with r/2 under Needs you; Done is hidden.
	if got := strings.Join(labels(), ","); got != "Needs you:2,Working:1,To do:1" {
		t.Fatalf("without details: %s", got)
	}
	board.details = map[string]workboardDetail{"r/1": {key: detailKey(tasks[0]), detail: workboard.Detail{}}}
	if got := strings.Join(labels(), ","); got != "Needs you:1,Ready to approve:1,Working:1,To do:1" {
		t.Fatalf("with details: %s", got)
	}
	board.details["r/1"] = workboardDetail{detail: workboard.Detail{Failure: true}}
	if got := strings.Join(labels(), ","); got != "Needs you:2,Working:1,To do:1" {
		t.Fatalf("recorded failure must fall back to Needs you: %s", got)
	}
}

func TestApplyWorkboardTasksAfterGateClosedLeavesNothing(t *testing.T) {
	s := supportedWorkboardShell()
	tasks, _ := workboard.ParseTasks(boardList)
	s.workboard.board.loading = true
	s.applyWorkboardTasks(tasks, nil, nil)
	if len(s.workboard.board.tasks) != 5 || s.workboard.board.loading {
		t.Fatalf("supported refresh not stored: %+v", s.workboard.board)
	}
	s.applyWorkboardAvailability(workboard.Availability{Reason: workboard.ReasonNotInstalled})
	if len(s.workboard.board.tasks) != 0 {
		t.Fatal("a removed plugin must take its rows with it")
	}
	s.workboard.board.loading = true
	s.applyWorkboardTasks(tasks, nil, nil) // a refresh that was in flight lands late
	if len(s.workboard.board.tasks) != 0 || s.workboard.board.loading {
		t.Fatalf("a late refresh must be dropped: %+v", s.workboard.board)
	}
}

func TestFailedRefreshKeepsPreviousRows(t *testing.T) {
	s := supportedWorkboardShell()
	tasks, _ := workboard.ParseTasks(boardList)
	s.applyWorkboardTasks(tasks, nil, nil)
	s.applyWorkboardTasks(nil, nil, errors.New("down"))
	if len(s.workboard.board.tasks) != 5 || !s.workboard.board.failed {
		t.Fatalf("board = %+v, want old rows marked failed", s.workboard.board)
	}
}

func TestRefreshDoesNothingWhileGateClosed(t *testing.T) {
	s := NewShell()
	newWorkboardClient = func(string, string) *workboard.Client {
		t.Fatal("a closed gate must not build a client")
		return nil
	}
	defer func() { newWorkboardClient = workboard.NewClient }()
	s.refreshWorkboardTasks(true)
	if s.workboard.board.loading {
		t.Fatal("a closed gate must not start a refresh")
	}
}

func TestTasksTabHasNoTraceWithoutThePlugin(t *testing.T) {
	s := NewShell()
	s.quickPanelTab = quickPanelTasksTab // stale selection from before the plugin went away
	tt := ui.NewTester(s.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	if tt.HasText("Tasks") {
		t.Fatalf("Tasks tab visible without the plugin: %q", tt.Texts())
	}
	if !tt.HasText("No active agents") {
		t.Fatalf("a stale Tasks selection must fall back to Agents: %q", tt.Texts())
	}
}

func TestTasksTabRendersGroupsAndOpensTheTasksProject(t *testing.T) {
	s := supportedWorkboardShell()
	tasks, _ := workboard.ParseTasks(boardList)
	s.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "app", CWD: "/work/app"}, {ID: "w2", Label: "task", CWD: "/wt/r-3/src"}},
	}
	s.workboard.board = workboardBoard{
		loadedAt: timeNow(),
		tasks:    tasks,
		details: map[string]workboardDetail{
			"r/1": {key: detailKey(tasks[0]), detail: workboard.Detail{}},
			"r/3": {key: detailKey(tasks[2]), detail: workboard.Detail{Worktree: "/wt/r-3"}},
		},
	}
	s.quickPanelTab = quickPanelTasksTab
	tt := ui.NewTester(s.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	for _, want := range []string{"Agents", "Services", "Tasks", "Needs you", "Ready to approve", "Working", "To do",
		"Finished work", "Asks a question", "Busy", "Later", "r/2 · which db?"} {
		if !tt.HasText(want) {
			t.Fatalf("Tasks tab missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("Shipped") {
		t.Fatal("finished tasks do not belong in the panel")
	}
	if err := tt.Click("Busy"); err != nil {
		t.Fatal(err)
	}
	if s.router.Path() != routeTask || s.workboard.page.id != "r/3" {
		t.Fatalf("path=%q page=%q, want the task page for r/3", s.router.Path(), s.workboard.page.id)
	}
	page := ui.NewTester(s.workboardTaskPage, 900, 700)
	if err := page.Click("Open Project"); err != nil {
		t.Fatal(err)
	}
	if s.selectedProjectID != "w2" {
		t.Fatalf("selectedProjectID = %q, want the task's own Project w2", s.selectedProjectID)
	}
}

func TestTasksTabEmptyAndFailedStates(t *testing.T) {
	s := supportedWorkboardShell()
	s.quickPanelTab = quickPanelTasksTab
	s.workboard.board = workboardBoard{loadedAt: timeNow()}
	tt := ui.NewTester(s.QuickPanelView, QuickPanelWidth, QuickPanelHeight)
	if !tt.HasText("No open tasks") {
		t.Fatalf("empty board: %q", tt.Texts())
	}
	s.workboard.board = workboardBoard{loadedAt: timeNow(), failed: true}
	tt.Frame()
	if !tt.HasText("Couldn't read the board") || !tt.HasText("Retry") || tt.HasText("No open tasks") {
		t.Fatalf("failed board: %q", tt.Texts())
	}
}

func actionableBoard(t *testing.T) (workboardBoard, herdr.Projection) {
	t.Helper()
	tasks, err := workboard.ParseTasks(boardList)
	if err != nil {
		t.Fatal(err)
	}
	board := workboardBoard{
		tasks: tasks,
		details: map[string]workboardDetail{
			"r/1": {detail: workboard.Detail{Worktree: "/wt/r-1"}}, // ready, joined to w1
			"r/2": {detail: workboard.Detail{Worktree: "/wt/r-2"}}, // needs you, joined to w2
			"r/3": {detail: workboard.Detail{Worktree: "/wt/r-3"}}, // working: never actionable
		},
	}
	p := herdr.Projection{Projects: []herdr.Project{{ID: "w1", CWD: "/wt/r-1"}, {ID: "w2", CWD: "/wt/r-2"}, {ID: "w3", CWD: "/wt/r-3"}}}
	return board, p
}

func TestActionableTasksAreOnlyNeedsYouAndReady(t *testing.T) {
	board, p := actionableBoard(t)
	got := actionableTasksOf(board, p)
	want := []actionableTask{{workboard.GroupNeedsYou, "w2"}, {workboard.GroupReady, "w1"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("actionable = %+v, want %+v", got, want)
	}
	s := NewShell() // gate closed
	s.workboard.board = board
	s.projection = p
	if s.actionableTasks() != nil {
		t.Fatal("a closed gate must expose no actionable tasks")
	}
}

// The Header badge counts a task once: when its Project already has an Agent
// in the activity snapshot, the Agent carries it.
func TestHeaderTaskCountsDoNotDoubleCountAgents(t *testing.T) {
	tasks := []actionableTask{
		{workboard.GroupNeedsYou, "w2"}, // w2 has an agent -> skipped
		{workboard.GroupNeedsYou, ""},   // unjoined -> counted
		{workboard.GroupReady, "w1"},    // no agent -> counted
		{workboard.GroupReady, "w2"},    // w2 has an agent -> skipped
	}
	entries := []StatusCenterEntry{{Card: agent.AgentCardModel{ProjectID: "w2"}}}
	attention, review := headerTaskCounts(tasks, entries)
	if attention != 1 || review != 1 {
		t.Fatalf("counts = %d attention, %d review; want 1, 1", attention, review)
	}
	if a, r := headerTaskCounts(nil, entries); a != 0 || r != 0 {
		t.Fatal("no tasks, no counts")
	}
}

func TestMergeTaskActivityOnProjectRows(t *testing.T) {
	index := map[string]sidebarActivity{
		"w2": {On: true, Tone: ToneWorking, Detail: "1 agent running"},
		"w3": {On: true, Tone: ToneMuted, Detail: "1 agent running"},
		"w4": {On: true, Tone: ToneSuccess, Detail: "node on :3000"},
		"w5": {On: true, Tone: ToneAttention, Detail: "1 agent running"},
	}
	mergeTaskActivity(index, []actionableTask{
		{workboard.GroupNeedsYou, "w1"}, // no dot yet -> attention
		{workboard.GroupNeedsYou, "w2"}, // outranks working
		{workboard.GroupReady, "w3"},    // fills an idle dot
		{workboard.GroupReady, "w4"},    // never hides a service
		{workboard.GroupNeedsYou, "w5"}, // already attention: detail joins
		{workboard.GroupReady, "w6"},    // no dot yet -> ready
		{workboard.GroupNeedsYou, ""},   // unjoined: no row
	})
	check := func(id string, tone StatusTone, detail string) {
		t.Helper()
		if got := index[id]; !got.On || got.Tone != tone || got.Detail != detail {
			t.Fatalf("%s = %+v, want tone %v detail %q", id, got, tone, detail)
		}
	}
	check("w1", ToneAttention, "1 task needs you")
	check("w2", ToneAttention, "1 agent running · 1 task needs you")
	check("w3", ToneSuccess, "1 agent running · 1 task ready to approve")
	check("w4", ToneSuccess, "node on :3000")
	check("w5", ToneAttention, "1 agent running · 1 task needs you")
	check("w6", ToneSuccess, "1 task ready to approve")
	if len(index) != 6 {
		t.Fatalf("an unjoined task must not light a row: %+v", index)
	}
}

func TestOpeningTheGateReadsTheBoardOnce(t *testing.T) {
	s := NewShell()
	calls := 0
	newWorkboardClient = func(root, _ string) *workboard.Client {
		calls++
		if root != "/plugin" {
			t.Errorf("root = %q, want the detected plugin root", root)
		}
		return workboard.NewClientWithExec(func(context.Context, ...string) (string, error) { return "[]", nil })
	}
	workboardGo = func(f func()) { f() }
	defer func() { newWorkboardClient, workboardGo = workboard.NewClient, func(f func()) { go f() } }()
	s.applyWorkboardAvailability(workboard.Availability{Supported: true, Root: "/plugin"})
	s.applyWorkboardAvailability(workboard.Availability{Supported: true, Root: "/plugin"}) // no second read on an unchanged gate
	if calls != 1 {
		t.Fatalf("board reads on gate open = %d, want 1", calls)
	}
}
