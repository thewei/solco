package nativeui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

// projectPageShell is a supported shell with the canned board loaded; every
// task of boardList lives in the Project "r".
func projectPageShell(t *testing.T, ran *[]string) *Shell {
	t.Helper()
	s := writeShell(t, ran)
	tasks, err := workboard.ParseTasks(boardList)
	if err != nil {
		t.Fatal(err)
	}
	s.workboard.board = workboardBoard{
		loadedAt: timeNow(),
		tasks:    tasks,
		details:  map[string]workboardDetail{"r/1": {key: detailKey(tasks[0]), detail: workboard.Detail{}}},
	}
	s.openWorkboardProjectPage("r")
	return s
}

func TestProjectPageIsTheTUIListInOneProject(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	tt := ui.NewTester(s.workboardProjectPage, 900, 700)
	for _, want := range []string{"All 4", "Needs you 1", "Working 1", "Ready 1", "To do 1", "Done 1",
		"Ready to approve", "Finished work", "Asks a question", "Busy", "Later", "↓ 1 done",
		"Message r's coordinator", "New Task", "Add and Delegate", "Send"} {
		if !tt.HasText(want) {
			t.Fatalf("project page missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("Shipped") {
		t.Fatal("finished work stays out of the All list")
	}
	if s.router.Path() != routeProject {
		t.Fatalf("path = %q", s.router.Path())
	}
}

func TestProjectPageFiltersAndFoldsGroups(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	tt := ui.NewTester(s.workboardProjectPage, 900, 700)
	if err := tt.Click("Needs you 1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Asks a question") || tt.HasText("Busy") || tt.HasText("Later") {
		t.Fatalf("Needs you filter must show only that group: %q", tt.Texts())
	}
	if err := tt.Click("Done 1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Shipped") || tt.HasText("Asks a question") {
		t.Fatalf("Done filter: %q", tt.Texts())
	}
	if err := tt.Click("All 4"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("↓ 1 done"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.workboard.project.filter != filterDone || !tt.HasText("Shipped") {
		t.Fatalf("the done line must open the Done filter: filter=%d %q", s.workboard.project.filter, tt.Texts())
	}
	s.workboard.project.filter = filterAll
	tt.Frame()
	if err := tt.Click("Toggle Working"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Busy") || !tt.HasText("Asks a question") {
		t.Fatalf("folding Working must hide only its tasks: %q", tt.Texts())
	}
}

func TestProjectPageMessagesTheCoordinator(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	tt := ui.NewTester(s.workboardProjectPage, 900, 700)
	_ = tt.Click("Send") // an empty draft sends nothing
	if len(ran) != 0 {
		t.Fatalf("empty draft ran %q", ran)
	}
	s.workboard.project.draft = "  add a dark mode "
	tt.Frame()
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ran, "|"); got != "coordinator --project r tell add a dark mode" {
		t.Fatalf("ran = %q", got)
	}
	if s.workboard.project.draft != "" || s.pendingToast != "Sent to r's coordinator" {
		t.Fatalf("draft=%q toast=%q after a sent message", s.workboard.project.draft, s.pendingToast)
	}
	ran = nil
	s.workboard.project.draft, s.workboard.project.delegate = "do it now", true
	tt.Frame()
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ran, "|"); got != "coordinator --project r tell do it now --delegate" {
		t.Fatalf("ran = %q", got)
	}
}

func TestProjectMessageKeepsTheDraftWhenThePluginRefuses(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	s.workboard.project.draft = "--machine"
	s.sendProjectMessage("r", "--machine", false)
	if s.workboard.project.draft != "--machine" || len(ran) != 0 || s.pendingToast == "" {
		t.Fatalf("draft=%q ran=%q toast=%q: refused text must stay and say why", s.workboard.project.draft, ran, s.pendingToast)
	}
}

func TestProjectPageNewTaskTargetsTheProject(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	s.openWorkboardAddIn("r", true)
	d := s.workboard.newTask
	if !d.open || d.repo != "r" || d.byRoot || !d.delegate {
		t.Fatalf("draft = %+v", d)
	}
	s.workboard.newTask.title = "Ship it"
	s.submitNewTask()
	s.openWorkboardAddIn("r", false)
	s.workboard.newTask.title = "Plain"
	s.submitNewTask()
	want := "add Ship it --repo r --delegate|add Plain --repo r"
	if got := strings.Join(ran, "|"); got != want {
		t.Fatalf("ran = %q, want %q", got, want)
	}
}

func TestProjectPageVanishedAndGateClosed(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	s.workboard.project.name = "gone"
	tt := ui.NewTester(s.workboardProjectPage, 900, 700)
	if !tt.HasText("This project is no longer on the board") || !tt.HasText("Back") {
		t.Fatalf("vanished project: %q", tt.Texts())
	}
	s.workboard.avail.Supported = false
	s.sendProjectMessage("r", "hello", false)
	s.workboard.newTask = newTaskDraft{open: true, repo: "r", title: "x"}
	s.submitNewTask()
	if len(ran) != 0 {
		t.Fatalf("gate closed must write nothing: %q", ran)
	}
}

func TestProjectRouteHeaderCountsOpenAndDone(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	spec := s.projectRouteHeader()
	if spec.title != "r" || spec.facts != "4 open · 1 done" || spec.actions == nil {
		t.Fatalf("header = %+v", spec)
	}
	if persistableRoute(routeProject) != routeWorkspace {
		t.Fatal("the project route is never restored on relaunch")
	}
}

func TestTaskPageMessagesTheAgentOnlyWhenOneRuns(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	s.openWorkboardTaskPage("r/4") // to do: nobody to tell
	tt := ui.NewTester(s.workboardTaskPage, 900, 700)
	if tt.HasText("Message r/4's agent") {
		t.Fatalf("a task nobody is on has no agent box: %q", tt.Texts())
	}
	s.openWorkboardTaskPage("r/3") // working
	tt = ui.NewTester(s.workboardTaskPage, 900, 700)
	if !tt.HasText("Message r/3's agent") {
		t.Fatalf("working task has an agent box: %q", tt.Texts())
	}
	s.workboard.page.draft = "keep going"
	tt.Frame()
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ran, "|"); got != "tell r/3 keep going" {
		t.Fatalf("ran = %q", got)
	}
	if s.workboard.page.draft != "" || s.pendingToast != "Sent to r/3's agent" {
		t.Fatalf("draft=%q toast=%q", s.workboard.page.draft, s.pendingToast)
	}
}

// projectNavTester renders the Project page through the Shell's View, as the
// keyboard lists need the per-View pass counter.
func projectNavTester(t *testing.T) (*Shell, *ui.Tester) {
	t.Helper()
	var ran []string
	s := projectPageShell(t, &ran)
	tt := ui.NewTester(s.View, 1200, 800)
	settleFrames(tt, 4)
	return s, tt
}

func TestNarrowGroupsMatchesTitleIDMessageAndApp(t *testing.T) {
	tasks, err := workboard.ParseTasks(`[
 {"id":"r/1","title":"Alpha","state":"working","stage":"working","msg":"building Parser","app":"codex"},
 {"id":"r/2","title":"Beta","state":"idle","stage":"todo"}]`)
	if err != nil {
		t.Fatal(err)
	}
	groups := []workboardGroup{{Group: workboard.GroupWorking, Tasks: tasks[:1]}, {Group: workboard.GroupTodo, Tasks: tasks[1:]}}
	for needle, want := range map[string]int{"": 2, "alpha": 1, "r/2": 1, "parser": 1, "codex": 1, "zzz": 0} {
		got := 0
		for _, g := range narrowGroups(groups, needle) {
			got += len(g.Tasks)
		}
		if got != want {
			t.Errorf("needle %q: %d tasks, want %d", needle, got, want)
		}
	}
}

func TestProjectPageFindNarrowsAndSaysWhenNothingMatches(t *testing.T) {
	s, tt := projectNavTester(t)
	if err := tt.Click("Find tasks"); err != nil {
		t.Fatal(err)
	}
	tt.Type("busy")
	settleFrames(tt, 2)
	if s.workboard.project.find != "busy" || !tt.HasText("r/3") || tt.HasText("r/4") || tt.HasText("r/2 · which db?") {
		t.Fatalf("find %q: %q", s.workboard.project.find, tt.Texts())
	}
	tt.Key(ui.Super, ui.KeyA)
	tt.Type("zzz")
	settleFrames(tt, 2)
	if !tt.HasText("No matching tasks") {
		t.Fatalf("an empty match must say so: %q", tt.Texts())
	}
}

func TestProjectPageCmdFFocusesFind(t *testing.T) {
	_, tt := projectNavTester(t)
	tt.Key(ui.Cmd, ui.KeyF)
	settleFrames(tt, 2)
	if !tt.Focused("Find tasks") {
		t.Fatal("⌘F must focus the Find field on the Project page")
	}
}

func TestProjectPageKeyboardWalksTasksAndEnterOpens(t *testing.T) {
	s, tt := projectNavTester(t)
	// ↓ from the Find field enters the list on its first task.
	tt.Key(ui.Cmd, ui.KeyF)
	settleFrames(tt, 2)
	pressKey(tt, 0, ui.KeyDown)
	settleFrames(tt, 2)
	wantCursor(t, tt, "Asks a question")
	pressKey(tt, 0, ui.KeyDown)
	wantCursor(t, tt, "Toggle Ready to approve")
	pressKey(tt, 0, ui.KeyDown)
	wantCursor(t, tt, "Finished work")
	pressKey(tt, 0, ui.KeyEnd)
	wantCursor(t, tt, "Later")
	pressKey(tt, 0, ui.KeyHome)
	wantCursor(t, tt, "Toggle Needs you")
	pressKey(tt, 0, ui.KeyDown)
	pressKey(tt, 0, ui.KeyEnter)
	if s.router.Path() != routeTask {
		t.Fatalf("Enter on a task must open its page, path = %q", s.router.Path())
	}
}

func TestProjectPageArrowsFoldAHeading(t *testing.T) {
	s, tt := projectNavTester(t)
	if err := tt.Click("Toggle Working"); err != nil { // fold it, then walk back onto it
		t.Fatal(err)
	}
	settleFrames(tt, 2)
	if !s.workboard.project.foldedBy[workboard.GroupWorking] {
		t.Fatal("control: click folds")
	}
	pressKey(tt, 0, ui.KeyRight) // the cursor is on the clicked heading: → unfolds
	if s.workboard.project.foldedBy[workboard.GroupWorking] {
		t.Fatal("→ on a folded heading must unfold it")
	}
	pressKey(tt, 0, ui.KeyLeft)
	if !s.workboard.project.foldedBy[workboard.GroupWorking] {
		t.Fatal("← on an open heading must fold it")
	}
}

func TestProjectPageNewAutomationOpensFromTheGroupAndSubmits(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	tt := ui.NewTester(s.workboardProjectPage, 900, 700)
	if err := tt.Click("New Automation…"); err != nil {
		t.Fatalf("the Automations group must offer creation even with none yet: %v (%q)", err, tt.Texts())
	}
	if d := s.workboard.newAutomation; !d.open || d.project != "r" || d.event != "added" {
		t.Fatalf("draft = %+v", d)
	}
	s.workboard.newAutomation.name, s.workboard.newAutomation.prompt = "Nightly", "Audit"
	if s.newAutomationReady() {
		t.Fatal("a schedule trigger without a schedule is not ready")
	}
	s.workboard.newAutomation.schedule = "daily 03:00"
	if !s.newAutomationReady() {
		t.Fatal("name + prompt + schedule is ready")
	}
	s.submitNewAutomation()

	s.openWorkboardAutomation("r")
	d := &s.workboard.newAutomation
	d.name, d.prompt, d.trigger, d.event, d.everyRepo, d.cond, d.kind = "Tidy", "Tidy up", automationOnEvent, "merged", true, "branch = x-*", newTaskResearch
	if !s.newAutomationReady() {
		t.Fatal("an event trigger needs no schedule")
	}
	s.submitNewAutomation()
	want := "automations add Nightly --prompt Audit --schedule daily 03:00 --repo r|" +
		"automations add Tidy --prompt Tidy up --when merged --every-repo --if branch = x-* --repo r --research"
	if got := strings.Join(ran, "|"); got != want {
		t.Fatalf("ran = %q, want %q", got, want)
	}
	if s.workboard.newAutomation.open {
		t.Fatal("submitting closes the dialog")
	}
}

func pickShell(t *testing.T, ran *[]string, attempts string) (*Shell, workboard.Task) {
	t.Helper()
	s := writeShell(t, ran)
	detail, err := workboard.ParseDetail(`{"attempts":` + attempts + `}`)
	if err != nil {
		t.Fatal(err)
	}
	task := workboard.Task{ID: "r/9", Title: "Pick me", Stage: "pick"}
	s.workboard.board = workboardBoard{
		tasks:   []workboard.Task{task},
		details: map[string]workboardDetail{"r/9": {key: detailKey(task), detail: detail}},
	}
	return s, task
}

const pickAttempts = `[
 {"letter":"A","agent":"claude","standing":"passed","stat":"+12 -3"},
 {"letter":"B","agent":"codex","standing":"out","stat":""},
 {"letter":"C","agent":"codex/gpt-6","standing":"passed","stat":"+40 -9"}]`

func TestPickKeepsOnlyThePassedAttemptYouChoose(t *testing.T) {
	var ran []string
	s, task := pickShell(t, &ran, pickAttempts)
	if got := kindsOf(workboardActionsForTask(task, workboard.GroupNeedsYou, nil)); got != "pick,stop,archive" {
		t.Fatalf("a pick-stage task offers Pick first: %q", got)
	}
	s.startWorkboardAction(boardWrite{"Pick…", "pick"}, task)
	tt := ui.NewTester(s.pickDialog, 900, 700)
	for _, want := range []string{"Keep which attempt?", "A claude · +12 -3", "C codex/gpt-6 · +40 -9"} {
		if !tt.HasText(want) {
			t.Fatalf("dialog missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("B codex") {
		t.Fatalf("an attempt that did not pass cannot be kept: %q", tt.Texts())
	}
	if err := tt.Click("Keep"); err == nil && len(ran) != 0 {
		t.Fatalf("nothing is chosen: Keep must not run (%q)", ran)
	}
	if err := tt.Click("C codex/gpt-6 · +40 -9"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Keep C"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ran, "|"); got != "pick r/9 C" {
		t.Fatalf("ran = %q", got)
	}
	if s.workboard.pick.open {
		t.Fatal("keeping closes the dialog")
	}
}

func TestPickWithoutAPassedAttemptSaysSo(t *testing.T) {
	var ran []string
	s, task := pickShell(t, &ran, `[{"letter":"A","agent":"claude","standing":"works"}]`)
	s.openWorkboardPick(task)
	if s.workboard.pick.open || !strings.Contains(s.pendingToast, "No attempt of r/9") || len(ran) != 0 {
		t.Fatalf("toast=%q open=%v ran=%q", s.pendingToast, s.workboard.pick.open, ran)
	}
}

func kindsOf(actions []boardWrite) string {
	var out []string
	for _, a := range actions {
		out = append(out, a.Kind)
	}
	return strings.Join(out, ",")
}
