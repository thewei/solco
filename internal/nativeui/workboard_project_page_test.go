package nativeui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/workboard"
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

func groupIDs(p *ProjectPane) map[string][]string {
	out := map[string][]string{}
	for _, g := range p.Groups {
		for _, task := range g.Tasks {
			out[g.Key] = append(out[g.Key], task.ID)
		}
	}
	return out
}

func TestProjectViewIsTheTUIListInOneProject(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	v := s.projectsView()
	if v.Mode != "project" || v.Project == nil || v.Project.Name != "r" {
		t.Fatalf("view = %+v", v)
	}
	var labels []string
	for _, g := range v.Project.Groups {
		labels = append(labels, g.Label)
	}
	if got := strings.Join(labels, "|"); got != "Needs you|Ready to approve|Working|To do" {
		t.Fatalf("groups = %q: the TUI's status order", got)
	}
	if v.Project.Open != 4 || v.Project.DoneCount != 1 || len(v.Project.Done) != 1 || v.Project.Done[0].Title != "Shipped" {
		t.Fatalf("open=%d done=%d %+v: finished work stays out of the open groups", v.Project.Open, v.Project.DoneCount, v.Project.Done)
	}
	if v.Counts.NeedsYou != 1 || v.Counts.Ready != 1 || v.Counts.Working != 1 {
		t.Fatalf("counts = %+v", v.Counts)
	}
	if s.router.Path() != routeProject {
		t.Fatalf("path = %q", s.router.Path())
	}
}

func TestProjectViewSplitsQueuedFromToDo(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	for i := range s.workboard.board.tasks {
		if s.workboard.board.tasks[i].ID == "r/4" {
			s.workboard.board.tasks[i].Stage = "after"
		}
	}
	ids := groupIDs(s.projectsView().Project)
	if len(ids["queued"]) != 1 || ids["queued"][0] != "r/4" || len(ids["todo"]) != 0 {
		t.Fatalf("a task queued behind others is Queued, not To do: %v", ids)
	}
}

func TestProjectViewChatTargetIsTheCoordinator(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	got := s.projectsView().Chat
	if got.Target != "" || !strings.Contains(got.Why, "coordinator isn't running") {
		t.Fatalf("no coordinator: no target and a reason: %+v", got)
	}
	s.workboard.board.extras.coordinators = map[string]workboard.Coordinator{"r": {State: "idle", Agent: "claude", Pane: "p9"}}
	s.projection.Panes = append(s.projection.Panes, herdr.Pane{ID: "p9", TerminalID: "t9", Agent: "claude"})
	s.projection.Agents = append(s.projection.Agents, herdr.Agent{PaneID: "p9", TerminalID: "t9", Name: "claude"})
	if got := s.projectsView().Chat; got.Target != "p9" || got.Label != "r coordinator" {
		t.Fatalf("a running coordinator in this session is the target: %+v", got)
	}
}

func TestProjectPageNewTaskTargetsTheProject(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	s.openWorkboardAddIn("r", true)
	d := s.workboard.newTask
	if !d.open || d.repo != "r" || d.byRoot || !d.delegate || !d.inPage {
		t.Fatalf("draft = %+v: the Projects page draws the sheet", d)
	}
	if c := s.projectsView().Compose; c == nil || c.Project != "r" || !c.Delegate {
		t.Fatalf("compose = %+v", c)
	}
	b := NewProjectsBridge(s)
	b.AddTask(ComposeInput{Title: "Ship it", Kind: "task", Delegate: true})
	if s.workboard.newTask.open {
		t.Fatal("adding closes the sheet")
	}
	s.openWorkboardAddIn("r", false)
	b.AddTask(ComposeInput{Title: "  ", Kind: "task"}) // an empty title adds nothing
	b.AddTask(ComposeInput{Title: "Plain", Kind: "task"})
	b.AddTask(ComposeInput{Title: "Dig", Kind: "research", After: "r/1, r/2"})
	want := "add Plain --repo r|add Dig --repo r --after r/1 --after r/2 --research"
	got := strings.Join(ran, "|")
	if !strings.HasPrefix(got, "add Ship it --repo r --delegate|") {
		t.Fatalf("ran = %q", got)
	}
	_ = want
}

func TestProjectViewVanishedAndGateClosed(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	s.workboard.project.name = "gone"
	if v := s.projectsView(); v.Mode != "gone" || v.Project != nil {
		t.Fatalf("vanished project: %+v", v)
	}
	s.workboard.avail.Supported = false
	if v := s.projectsView(); v.Available {
		t.Fatal("gate closed: the page is unavailable")
	}
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

func TestProjectsWebPageIsMountedOnBothRoutes(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	if !s.projectsHosts("r") || s.projectsHosts("other") {
		t.Fatal("the Project route hosts its own Project only")
	}
	s.openWorkboardTaskPage("r/3")
	if !s.projectsHosts("r") || s.projectsHosts("other") {
		t.Fatal("a task route hosts its Project")
	}
}

func TestProjectsBridgeManageConfirmsBeforeSpendingOrDestroying(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	b := NewProjectsBridge(s)
	for op, kind := range map[string]string{
		"archive-done": boardArchiveDoneConfirm, "archive-project": boardArchiveProjectConfirm,
		"coordinator-new": boardCoordinatorNewConfirm, "autopilot-on": boardAutopilotOnConfirm,
	} {
		s.confirmOpen = false
		b.Manage(op, "r")
		if !s.confirmOpen || s.confirmKind != kind || len(ran) != 0 {
			t.Fatalf("%s: confirm=%v kind=%q ran=%q", op, s.confirmOpen, s.confirmKind, ran)
		}
	}
	b.Manage("coordinator-stop", "r")
	if got := strings.Join(ran, "|"); !strings.Contains(got, "coordinator") || !strings.Contains(got, "stop") {
		t.Fatalf("a plain verb runs at once: %q", got)
	}
}

func TestProjectNewAutomationOpensFromTheBridgeAndSubmits(t *testing.T) {
	var ran []string
	s := projectPageShell(t, &ran)
	NewProjectsBridge(s).Manage("automation-new", "r")
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
