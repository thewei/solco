package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/herdr-client/internal/app"
	"github.com/wh-studio/herdr-client/internal/herdr"
	"github.com/wh-studio/herdr-client/internal/settings"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

// projectsBoardList: app has a task that needs you and one ready to approve,
// site only has work to do, docs only finished work.
const projectsBoardList = `[
 {"id":"site/1","title":"Later work","state":"idle","stage":"todo","msg":""},
 {"id":"app/1","title":"Finished work","state":"blocked","kind":"permission","stage":"approval","msg":"Approve to merge? Did v1."},
 {"id":"app/2","title":"Asks a question","state":"blocked","kind":"question","stage":"working","msg":"which db?"},
 {"id":"app/3","title":"Busy","state":"working","stage":"working","msg":""},
 {"id":"docs/1","title":"Shipped","state":"idle","stage":"done","msg":""}
]`

func projectsSectionShell(t *testing.T, opts ...ShellOption) *Shell {
	t.Helper()
	s := NewShell(opts...)
	s.workboard.avail = workboard.Availability{Installed: true, Enabled: true, Version: "1.2.26", Root: "/plugin", Supported: true}
	s.loading = false
	s.activeInstance = "default"
	s.projection = herdr.Projection{Projects: []herdr.Project{{ID: "w1", Label: "herdr-client", CWD: "/work/app"}}}
	tasks, err := workboard.ParseTasks(projectsBoardList)
	if err != nil {
		t.Fatal(err)
	}
	s.workboard.board = workboardBoard{
		loadedAt: timeNow(),
		tasks:    tasks,
		details: map[string]workboardDetail{
			"app/1": {key: detailKey(tasks[1]), detail: workboard.Detail{}},
		},
	}
	return s
}

func TestWorkboardProjectsGroupByRepoAndOrderByUrgency(t *testing.T) {
	s := projectsSectionShell(t)
	got := workboardProjects(s.workboard.board)
	if len(got) != 3 {
		t.Fatalf("projects = %+v, want app, site, docs", got)
	}
	want := []struct {
		name string
		open int
	}{{"app", 3}, {"site", 1}, {"docs", 0}}
	for i, w := range want {
		if got[i].Name != w.name || got[i].Open != w.open {
			t.Fatalf("project %d = %q open %d, want %q open %d", i, got[i].Name, got[i].Open, w.name, w.open)
		}
	}
	if top, ok := got[0].Top(); !ok || top != workboard.GroupNeedsYou {
		t.Fatalf("app top = %v/%v, want Needs you", top, ok)
	}
	if _, ok := got[2].Top(); ok {
		t.Fatal("a project with only finished tasks has no top group")
	}
}

func TestProjectsSectionHasNoTraceWithoutThePlugin(t *testing.T) {
	s := projectsSectionShell(t)
	s.workboard.avail.Supported = false
	tt := ui.NewTester(s.View, 1200, 800)
	if tt.HasText("Projects") || tt.HasText("Finished work") || tt.HasText("site") {
		t.Fatalf("Projects section visible without the plugin: %q", tt.Texts())
	}
	if !tt.HasText("Workspace") {
		t.Fatalf("the rest of the sidebar must still render: %q", tt.Texts())
	}
}

func TestProjectsSectionNamesEachProjectWithItsOpenTasks(t *testing.T) {
	s := projectsSectionShell(t)
	tt := ui.NewTester(s.View, 1200, 800)
	for _, want := range []string{"Projects", "app", "site", "docs",
		"Finished work", "Asks a question", "Busy", "Later work"} {
		if !tt.HasText(want) {
			t.Fatalf("sidebar missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("Shipped") {
		t.Fatal("finished tasks stay out of the sidebar")
	}
	if err := tt.Click("Busy"); err != nil {
		t.Fatal(err)
	}
	if s.router.Path() != routeTask || s.workboard.page.id != "app/3" {
		t.Fatalf("path=%q page=%q, want the task page for app/3", s.router.Path(), s.workboard.page.id)
	}
}

func TestProjectRowOpensItsPageThenFoldsAndTheFoldPersists(t *testing.T) {
	store := settings.NewMemoryStore(settings.Default())
	s := projectsSectionShell(t, WithSettings(app.NewSettingsService(store)))
	tt := ui.NewTester(s.View, 1200, 800)
	if err := tt.Click("app"); err != nil {
		t.Fatal(err)
	}
	if s.router.Path() != routeProject || s.workboard.project.name != "app" || s.foldedBoards["app"] {
		t.Fatalf("first click must open the project page: path=%q project=%q folded=%v", s.router.Path(), s.workboard.project.name, s.foldedBoards)
	}
	tt.Frame()
	if err := tt.Click("app"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !s.foldedBoards["app"] || !tt.HasText("Later work") {
		t.Fatalf("a second click on the open project must fold it and leave the others: folded=%v texts=%q", s.foldedBoards, tt.Texts())
	}
	s.SaveSession()
	second := NewShell(WithSettings(app.NewSettingsService(store)))
	if !second.foldedBoards["app"] || second.foldedBoards["site"] {
		t.Fatalf("restored folds = %v, want only app", second.foldedBoards)
	}
}

func TestProjectsSectionFoldsThroughThePersistedFlag(t *testing.T) {
	s := projectsSectionShell(t)
	tt := ui.NewTester(s.View, 1200, 800)
	if err := tt.Click("Projects"); err != nil {
		t.Fatal(err)
	}
	if s.tasksOpen {
		t.Fatal("header click did not fold the section")
	}
	tt.Frame()
	if tt.HasText("Busy") || tt.HasText("site") || !tt.HasText("Projects") {
		t.Fatalf("folded section must keep its header and drop its rows: %q", tt.Texts())
	}
	if err := tt.Click("Projects"); err != nil {
		t.Fatal(err)
	}
	if !s.tasksOpen {
		t.Fatal("second click did not reopen the section")
	}
}

func TestProjectsSectionEmptyAndFailedStates(t *testing.T) {
	s := projectsSectionShell(t)
	s.workboard.board = workboardBoard{loadedAt: timeNow()}
	tt := ui.NewTester(s.View, 1200, 800)
	if !tt.HasText("Projects") || !tt.HasText("No projects yet") {
		t.Fatalf("empty board: %q", tt.Texts())
	}
	s.workboard.board = workboardBoard{loadedAt: timeNow(), failed: true}
	tt.Frame()
	if !tt.HasText("Couldn't read the board") || tt.HasText("No projects yet") {
		t.Fatalf("failed board: %q", tt.Texts())
	}
	s.workboard.board = workboardBoard{} // first read still in flight
	tt.Frame()
	if tt.HasText("No projects yet") || tt.HasText("Couldn't read the board") {
		t.Fatalf("an unread board says nothing yet: %q", tt.Texts())
	}
}

func TestFoldedProjectHidesItsTasksInTheSidebar(t *testing.T) {
	s := projectsSectionShell(t)
	s.foldedBoards["site"] = true // only the sidebar shows site's task
	tt := ui.NewTester(s.View, 1200, 800)
	if tt.HasText("Later work") || !tt.HasText("site") || !tt.HasText("Busy") {
		t.Fatalf("a folded project keeps its row and drops its tasks, others stay: %q", tt.Texts())
	}
}
