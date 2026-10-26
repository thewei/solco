package nativeui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/solco/internal/herdr"
	"github.com/wh-studio/solco/internal/workboard"
)

const pinnedR4 = `{"machine":null,"projects":["r","empty"],"tasks":[{"id":"r/4","state":"idle","title":"Later","msg":"","stage":"todo"}]}`

func loadBoard(t *testing.T, s *Shell) {
	t.Helper()
	s.refreshWorkboardTasks(true)
	if s.workboard.board.failed || len(s.workboard.board.tasks) == 0 {
		t.Fatalf("board did not load: %+v", s.workboard.board)
	}
}

func TestBoardRefreshReadsPinsAndCoordinators(t *testing.T) {
	var ran []string
	s := extrasShell(t, &ran, pinnedR4, `{"name":"r coordinator","state":"blocked","pane":"p9"}`)
	loadBoard(t, s)
	x := s.workboard.board.extras
	if !x.pinnedProjects["r"] || !x.pinnedTasks["r/4"] || x.coordinators["r"].State != "blocked" {
		t.Fatalf("extras = %+v", x)
	}
	projects := workboardProjects(s.workboard.board)
	var names []string
	for _, p := range projects {
		names = append(names, p.Name)
	}
	if got := strings.Join(names, ","); got != "r,empty" {
		t.Fatalf("a pinned project with no tasks stays listed, after the one with open tasks: %q", got)
	}
	if projects[0].Dot() != opNeedsAttention {
		t.Fatal("a blocked coordinator raises the project dot")
	}
	if len(ran) != 0 {
		t.Fatalf("reads must not write: %q", ran)
	}
}

func TestExtrasFailuresLeaveTheBoardIntact(t *testing.T) {
	var ran []string
	s := extrasShell(t, &ran, `not json`, `also not json`)
	loadBoard(t, s)
	x := s.workboard.board.extras
	if len(x.pinnedProjects) != 0 || len(x.coordinators) != 0 || s.workboard.board.failed {
		t.Fatalf("a broken extra must only be absent: %+v", s.workboard.board)
	}
}

func TestPinWritesTheTargetAndRefreshes(t *testing.T) {
	var ran []string
	s := extrasShell(t, &ran, pinnedR4, `{"state":"none","pane":""}`)
	loadBoard(t, s)
	s.setWorkboardPin("r/3", true)
	s.setWorkboardPin("r", false) // r is pinned in the canned answer
	if got := strings.Join(ran, "|"); got != "pin r/3|unpin r" {
		t.Fatalf("ran = %q", got)
	}
	s.workboard.avail.Supported = false
	ran = nil
	s.setWorkboardPin("r", true)
	if len(ran) != 0 {
		t.Fatalf("gate closed must write nothing: %q", ran)
	}
}

func TestProjectPageShowsPinnedFilterAndCoordinator(t *testing.T) {
	var ran []string
	s := extrasShell(t, &ran, pinnedR4, `{"name":"r coordinator","state":"working","pane":"p9"}`)
	loadBoard(t, s)
	s.openWorkboardProjectPage("r")
	tt := ui.NewTester(s.workboardProjectPage, 900, 700)
	for _, want := range []string{"Pinned 1", "Message r's coordinator · working", "Open Coordinator", "★ r/4"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Pinned 1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Later") || tt.HasText("Asks a question") {
		t.Fatalf("Pinned filter shows only starred tasks: %q", tt.Texts())
	}
	// With nothing starred the filter disappears and falls back to All.
	s.workboard.board.extras.pinnedTasks = map[string]bool{}
	tt.Frame()
	if tt.HasText("Pinned 0") || s.workboard.project.filter != filterAll {
		t.Fatalf("an emptied Pinned filter must fall back: filter=%d %q", s.workboard.project.filter, tt.Texts())
	}
}

func TestOpenCoordinatorNeedsARunningPaneThisSessionShows(t *testing.T) {
	var ran []string
	s := extrasShell(t, &ran, pinnedR4, `{"state":"idle","pane":"not-in-this-session"}`)
	loadBoard(t, s)
	s.openProjectCoordinator("r")
	if s.pendingToast != "r's coordinator isn't open in this session" {
		t.Fatalf("toast = %q", s.pendingToast)
	}
	s.openProjectCoordinator("unknown")
	if s.pendingToast != "unknown's coordinator isn't running" {
		t.Fatalf("toast = %q", s.pendingToast)
	}
	pane := "p1"
	s.projection.Panes = []herdr.Pane{{ID: pane}}
	s.workboard.board.extras.coordinators["r"] = workboard.Coordinator{State: "idle", Pane: pane}
	s.router.Push(routeProject)
	s.openProjectCoordinator("r")
	if s.router.Path() != routeWorkspace || s.selectedPaneID != pane {
		t.Fatalf("path=%q selected=%q want %q", s.router.Path(), s.selectedPaneID, pane)
	}
}

func TestSidebarProjectRowMarksPinnedAndLeadsTheList(t *testing.T) {
	var ran []string
	s := extrasShell(t, &ran, `{"machine":null,"projects":["r"],"tasks":[]}`, `{"state":"none","pane":""}`)
	loadBoard(t, s)
	rows := workboardProjects(s.workboard.board)
	if len(rows) != 1 || !rows[0].Pinned {
		t.Fatalf("rows = %+v", rows)
	}
}
