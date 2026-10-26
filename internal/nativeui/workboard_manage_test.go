package nativeui

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

// archivedRows are shaped like `list --archived --json` of 1.2.26
// (a plain task row plus archivedAt).
const archivedRows = `[
 {"id":"r/7","state":"idle","title":"Old one","msg":"","stage":"todo","archivedAt":1792994257229},
 {"id":"r/8","state":"idle","title":"Older","msg":"","stage":"todo","archivedAt":1792994257300},
 {"id":"q/1","state":"idle","title":"Other repo","msg":"","stage":"todo","archivedAt":1792994257400}
]`

// automationRows is 1.2.26's `automations --json` shape: one on, one off.
const automationRows = `[
 {"id":"a1","name":"nightly check","on":true,"trigger":"daily 09:00","repo":"r","research":false,"chat":false,"prompt":"p","settings":{},"last":null,"next":1793062800000},
 {"id":"a2","name":"weekly sweep","on":false,"offWhy":"its project is archived","trigger":"mon 09:00","repo":"r","research":true,"chat":false,"next":null},
 {"id":"a3","name":"elsewhere","on":true,"trigger":"daily 10:00","repo":"q","next":null}
]`

// manageShell answers the reads of the board and the archive, records writes.
func manageShell(t *testing.T, ran *[]string, autopilot string) *Shell {
	t.Helper()
	newWorkboardClient = func(string, string) *workboard.Client {
		return workboard.NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
			switch {
			case args[0] == "list" && len(args) > 1 && args[1] == "--pinned":
				return `{"machine":null,"projects":[],"tasks":[]}`, nil
			case args[0] == "list" && len(args) > 1 && args[1] == "--archived":
				return archivedRows, nil
			case args[0] == "list":
				return boardList, nil
			case args[0] == "details":
				return `{"task":{"id":"` + args[1] + `"}}`, nil
			case args[0] == "automations" && len(args) == 2:
				return automationRows, nil
			case args[0] == "autopilot" && len(args) == 2:
				return autopilot, nil
			case args[0] == "coordinator" && args[len(args)-2] == "status":
				return `{"name":"r coordinator","state":"idle","pane":"p1","said":""}`, nil
			case args[0] == "archive" && len(args) > 1 && args[1] == "--done":
				*ran = append(*ran, strings.Join(args, " "))
				return `{"archived":["r/5"],"left":{},"failed":[]}`, nil
			}
			*ran = append(*ran, strings.Join(args, " "))
			return "", nil
		})
	}
	workboardGo = func(f func()) { f() }
	t.Cleanup(func() { newWorkboardClient, workboardGo = workboard.NewClient, func(f func()) { go f() } })
	s := supportedWorkboardShell()
	loadBoard(t, s)
	return s
}

func projectMenu(t *testing.T, s *Shell, project workboardProject) (*ui.Tester, string) {
	t.Helper()
	view := func(c *ui.Context) {
		row := ui.ButtonBase(c).Label("Row")
		row.Children(func() { ui.Text(c, "Row") })
		row.ContextMenu(func(m *ui.Menu) { s.boardManageItems(m, project) })
	}
	tt := ui.NewTester(view, 300, 200)
	if err := tt.RightClick("Row"); err != nil {
		t.Fatal(err)
	}
	return tt, strings.Join(tt.Menu(), "|")
}

func TestProjectMenuOffersManagementWithTheRightVerbs(t *testing.T) {
	var ran []string
	s := manageShell(t, &ran, `[{"on":false,"coordinator":"project:r","name":"r coordinator"}]`)
	project, _ := s.workboardProjectByName("r")
	_, menu := projectMenu(t, s, project)
	for _, want := range []string{"Archive Done Tasks…", "Archive Project…", "Restart Coordinator", "New Coordinator Conversation…", "Stop Coordinator", "Turn Autopilot On…"} {
		if !strings.Contains(menu, want) {
			t.Fatalf("menu %q missing %q", menu, want)
		}
	}
	if strings.Contains(menu, "Turn Autopilot Off") {
		t.Fatalf("autopilot is off: %q", menu)
	}
	s.workboard.board.extras.autopilot["r"] = true
	_, menu = projectMenu(t, s, project)
	if !strings.Contains(menu, "Turn Autopilot Off") || strings.Contains(menu, "Turn Autopilot On") {
		t.Fatalf("autopilot is on: %q", menu)
	}
	// No coordinator pane, nothing done: those verbs are not offered.
	project.Coord = workboard.Coordinator{State: "none"}
	project.Done = nil
	_, menu = projectMenu(t, s, project)
	for _, bad := range []string{"Restart Coordinator", "Stop Coordinator", "Archive Done Tasks"} {
		if strings.Contains(menu, bad) {
			t.Fatalf("menu %q offers %q", menu, bad)
		}
	}
	_, menu = projectMenu(t, s, workboardProject{Name: workboardOtherProject})
	if menu != "" {
		t.Fatalf("the catch-all Project has no management: %q", menu)
	}
}

func TestAutopilotStateComesFromTheListNotFromGuessing(t *testing.T) {
	var ran []string
	s := manageShell(t, &ran, `[{"on":true,"coordinator":"project:r","name":"r coordinator"},{"on":false,"coordinator":"main","name":"Coordinator"}]`)
	if !s.workboard.board.extras.autopilot["r"] || s.workboard.board.extras.autopilot["main"] {
		t.Fatalf("autopilot = %v", s.workboard.board.extras.autopilot)
	}
}

func TestManagementAsksWithConsequencesThenRunsExactlyOneVerb(t *testing.T) {
	var ran []string
	s := manageShell(t, &ran, `[]`)
	cases := []struct {
		kind, target string
		n            int
		message      []string
		argv         string
	}{
		{boardArchiveDoneConfirm, "r", 1, []string{"1 done task of r", "Restore brings them back"}, "archive --done --repo r --json"},
		{boardArchiveProjectConfirm, "r", 5, []string{"5 tasks", "stopped", "automations are turned off"}, "archive --repo r"},
		{boardRestoreProjectConfirm, "r", 2, []string{"2 archived tasks"}, "restore --repo r"},
		{boardClearConfirm, "r/7", 0, []string{"cannot be restored"}, "clear r/7"},
		{boardCoordinatorNewConfirm, "r", 0, []string{"fresh conversation"}, "coordinator --project r new"},
		{boardAutopilotOnConfirm, "r", 0, []string{"spending tokens", "saved goal"}, "autopilot on --project r"},
	}
	for _, tc := range cases {
		ran = nil
		s.confirmOpen = false
		s.startBoardManage(tc.kind, tc.target, tc.n)
		if !s.confirmOpen || s.confirmKind != tc.kind || s.confirmTarget != tc.target || len(ran) != 0 {
			t.Fatalf("%s: confirm=%v kind=%q ran=%q (asking must not run anything)", tc.kind, s.confirmOpen, s.confirmKind, ran)
		}
		for _, want := range tc.message {
			if !strings.Contains(s.confirmMessage, want) {
				t.Fatalf("%s: message %q lacks %q", tc.kind, s.confirmMessage, want)
			}
		}
		s.submitConfirm(s.confirmKind, s.confirmTarget)
		if got := strings.Join(ran, "|"); got != tc.argv {
			t.Fatalf("%s: ran %q, want %q", tc.kind, got, tc.argv)
		}
	}
}

func TestUnconfirmedVerbsAndTheirToasts(t *testing.T) {
	var ran []string
	s := manageShell(t, &ran, `[]`)
	for op, want := range map[string][2]string{
		"coordinator-restart": {"coordinator --project r restart", "Restarting r's coordinator"},
		"coordinator-stop":    {"coordinator --project r stop", "Stopped r's coordinator"},
		"autopilot-off":       {"autopilot off --project r", "Autopilot off for r"},
	} {
		ran, s.pendingToast = nil, ""
		s.runBoardManage(op, "r")
		if strings.Join(ran, "|") != want[0] || s.pendingToast != want[1] {
			t.Fatalf("%s: ran %q toast %q", op, ran, s.pendingToast)
		}
	}
	ran, s.pendingToast = nil, ""
	s.runBoardManage("archive-done", "r")
	if s.pendingToast != "Archived 1 done task of r" {
		t.Fatalf("toast = %q", s.pendingToast)
	}
}

func TestArchivedPageListsByProjectAndRestoresOrRemoves(t *testing.T) {
	var ran []string
	s := manageShell(t, &ran, `[]`)
	s.openWorkboardArchived()
	if s.router.Path() != routeArchived || !s.workboard.archived.loaded {
		t.Fatalf("path=%q archived=%+v", s.router.Path(), s.workboard.archived)
	}
	tt := ui.NewTester(s.workboardArchivedPage, 900, 700)
	for _, want := range []string{"Old one", "Older", "Other repo", "r/7", "Restore Project", "Remove…"} {
		if !tt.HasText(want) {
			t.Fatalf("archived page missing %q: %q", want, tt.Texts())
		}
	}
	// Groups sort by name, so the first buttons are q/1's.
	if err := tt.Click("Restore"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ran, "|"); got != "restore q/1" || s.pendingToast != "Restored q/1" {
		t.Fatalf("ran=%q toast=%q", ran, s.pendingToast)
	}
	ran = nil
	if err := tt.Click("Remove…"); err != nil {
		t.Fatal(err)
	}
	if !s.confirmOpen || s.confirmKind != boardClearConfirm || s.confirmTarget != "q/1" || len(ran) != 0 {
		t.Fatalf("Remove must ask first: open=%v kind=%q target=%q ran=%q", s.confirmOpen, s.confirmKind, s.confirmTarget, ran)
	}
	s.submitConfirm(s.confirmKind, s.confirmTarget)
	if got := strings.Join(ran, "|"); got != "clear q/1" {
		t.Fatalf("ran = %q", got)
	}
	ran, s.confirmOpen = nil, false
	if err := tt.Click("Restore Project"); err != nil {
		t.Fatal(err)
	}
	if s.confirmKind != boardRestoreProjectConfirm || s.confirmTarget != "q" || !strings.Contains(s.confirmMessage, "1 archived task") || len(ran) != 0 {
		t.Fatalf("kind=%q target=%q message=%q ran=%q", s.confirmKind, s.confirmTarget, s.confirmMessage, ran)
	}
}

func TestArchivedPageStates(t *testing.T) {
	var ran []string
	s := manageShell(t, &ran, `[]`)
	if persistableRoute(routeArchived) != routeWorkspace {
		t.Fatal("the archived route is never restored on relaunch")
	}
	tt := ui.NewTester(s.workboardArchivedPage, 900, 500)
	if tt.HasText("Nothing archived") {
		t.Fatal("not loaded yet: it must not claim the archive is empty")
	}
	s.workboard.archived = workboardArchived{loaded: true}
	tt = ui.NewTester(s.workboardArchivedPage, 900, 500)
	if !tt.HasText("Nothing archived") {
		t.Fatalf("empty archive: %q", tt.Texts())
	}
	s.workboard.archived = workboardArchived{failed: true}
	tt = ui.NewTester(s.workboardArchivedPage, 900, 500)
	if !tt.HasText("Couldn't read the archive") || !tt.HasText("Retry") {
		t.Fatalf("failed read: %q", tt.Texts())
	}
}

func TestNewTaskDialogCarriesEveryOptionToTheAddVerb(t *testing.T) {
	var ran []string
	s := manageShell(t, &ran, `[]`)
	s.openWorkboardAddIn("r", false)
	tt := ui.NewTester(func(c *ui.Context) { s.newTaskDialog(c) }, 700, 600)
	for _, want := range []string{"New Task", "Task", "Research only", "Chat only", "Start an agent now", "Add"} {
		if !tt.HasText(want) {
			t.Fatalf("dialog missing %q: %q", want, tt.Texts())
		}
	}
	d := &s.workboard.newTask
	d.title, d.desc, d.after, d.kind, d.delegate = "Look into it", "Some words", "r/1, r/2", newTaskResearch, true
	s.submitNewTask()
	want := "add Look into it --repo r --desc Some words --research --after r/1,r/2 --delegate"
	if got := strings.Join(ran, "|"); got != want {
		t.Fatalf("ran = %q, want %q", got, want)
	}
	ran = nil
	s.openWorkboardAddIn("r", false)
	d = &s.workboard.newTask
	d.title, d.kind = "Talk", newTaskChat
	s.submitNewTask()
	if got := strings.Join(ran, "|"); got != "add Talk --repo r --chat" {
		t.Fatalf("ran = %q", got)
	}
	// The plugin's refusal of an unknown --after comes back as the toast; nothing is added.
	if s.workboard.newTask.open {
		t.Fatal("the dialog closes on submit")
	}
}

func TestAfterStageOffersStartNowAfterAConfirm(t *testing.T) {
	var ran []string
	s := manageShell(t, &ran, `[]`)
	task := workboard.Task{ID: "r/6", Title: "Waits", Stage: "after", State: "blocked", Kind: "question"}
	if got := kinds(workboardActionsForTask(task, workboard.GroupNeedsYou, nil)); got != "start-now,archive" {
		t.Fatalf("after-stage actions = %q", got)
	}
	s.startWorkboardAction(boardWrite{"Start Now…", "start-now"}, task)
	if !s.confirmOpen || s.confirmKind != workboardStartNowConfirm || len(ran) != 0 {
		t.Fatalf("confirm=%v kind=%q ran=%q", s.confirmOpen, s.confirmKind, ran)
	}
	s.submitConfirm(s.confirmKind, s.confirmTarget)
	if got := strings.Join(ran, "|"); got != "delegate r/6 --now" {
		t.Fatalf("ran = %q", got)
	}
}

func TestProjectPageAutomationsManageNotCreate(t *testing.T) {
	var ran []string
	s := manageShell(t, &ran, `[]`)
	s.openWorkboardProjectPage("r")
	tt := ui.NewTester(s.workboardProjectPage, 900, 900)
	for _, want := range []string{"Automations", "nightly check", "weekly sweep", "Turn Off", "Turn On", "Run…", "Remove…", "off: its project is archived · research only"} {
		if !tt.HasText(want) {
			t.Fatalf("project page missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("elsewhere") {
		t.Fatalf("another repo's automation: %q", tt.Texts())
	}
	if err := tt.Click("Turn Off"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ran, "|"); got != "automations off a1" || s.pendingToast != "“nightly check” is off" {
		t.Fatalf("ran=%q toast=%q", ran, s.pendingToast)
	}
	ran = nil
	s.startBoardManage(boardAutomationRunConfirm, "a1", 0)
	if !strings.Contains(s.confirmMessage, "spending tokens") || len(ran) != 0 {
		t.Fatalf("message=%q ran=%q: Run must ask first", s.confirmMessage, ran)
	}
	s.submitConfirm(s.confirmKind, s.confirmTarget)
	s.startBoardManage(boardAutomationRemoveConfirm, "a2", 0)
	if !strings.Contains(s.confirmMessage, "The tasks it made stay") {
		t.Fatalf("message=%q", s.confirmMessage)
	}
	s.submitConfirm(s.confirmKind, s.confirmTarget)
	if got := strings.Join(ran, "|"); got != "automations run a1|automations remove a2" {
		t.Fatalf("ran = %q", got)
	}
	// A Project with none draws no group.
	s.workboard.board.extras.automations = nil
	if tt = ui.NewTester(s.workboardProjectPage, 900, 900); tt.HasText("Automations") {
		t.Fatalf("no automations, no group: %q", tt.Texts())
	}
}
