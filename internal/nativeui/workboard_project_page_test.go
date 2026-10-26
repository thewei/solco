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
