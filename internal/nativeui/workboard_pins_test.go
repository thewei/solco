package nativeui

import (
	"strings"
	"testing"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/workboard"
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

func TestProjectViewCarriesPinsAndCoordinator(t *testing.T) {
	var ran []string
	s := extrasShell(t, &ran, pinnedR4, `{"name":"r coordinator","state":"working","pane":"p9"}`)
	loadBoard(t, s)
	s.openWorkboardProjectPage("r")
	p := s.projectsView().Project
	if p.Coordinator.State != "working" || !p.Coordinator.Running {
		t.Fatalf("coordinator = %+v", p.Coordinator)
	}
	pinned := 0
	for _, g := range p.Groups {
		for _, task := range g.Tasks {
			if task.Pinned {
				pinned++
				if task.ID != "r/4" {
					t.Fatalf("pinned %s, want r/4", task.ID)
				}
			}
		}
	}
	if pinned != 1 {
		t.Fatalf("pinned tasks = %d, want 1", pinned)
	}
	s.workboard.board.extras.pinnedTasks = map[string]bool{}
	for _, g := range s.projectsView().Project.Groups {
		for _, task := range g.Tasks {
			if task.Pinned {
				t.Fatalf("an emptied pin set marks nothing: %s", task.ID)
			}
		}
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
