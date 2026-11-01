package nativeui

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

func tabBarShell(tabCount int, selected string) *Shell {
	s := NewShell()
	s.loading = false
	s.activeInstance = "default"
	projection := herdr.Projection{Projects: []herdr.Project{{ID: "w1", Label: "Project A", TabCount: uint(tabCount)}}}
	for i := 1; i <= tabCount; i++ {
		id := fmt.Sprintf("t%d", i)
		projection.Tabs = append(projection.Tabs, herdr.Tab{ID: id, ProjectID: "w1", Label: fmt.Sprintf("Tab number %d", i), PaneCount: 1})
		projection.Panes = append(projection.Panes, herdr.Pane{ID: "p" + id, ProjectID: "w1", TabID: id, Label: "pane " + id})
	}
	projection.Agents = []herdr.Agent{{PaneID: "pt2", Kind: "omp", Status: "working"}}
	s.projection = projection
	s.selectedProjectID = "w1"
	s.selectedTabID = selected
	s.selectedPaneID = "p" + selected
	return s
}

func TestTitlebarTabsModel(t *testing.T) {
	s := tabBarShell(3, "t1")
	tabs := s.titlebarTabs(false)
	if len(tabs) != 3 {
		t.Fatalf("tabs = %d, want 3", len(tabs))
	}
	if !tabs[0].Selected || tabs[1].Selected {
		t.Fatalf("selection flags wrong: %+v", tabs)
	}
	if !tabs[1].HasState || tabs[1].Tone != ToneWorking {
		t.Fatalf("working agent tab carries no state: %+v", tabs[1])
	}
	if tabs[0].HasState {
		t.Fatalf("a Tab without an agent shows no state: %+v", tabs[0])
	}
}

func TestTabBarReplacesBreadcrumbAndTreeTabs(t *testing.T) {
	s := tabBarShell(3, "t1")
	tester := ui.NewTester(s.View, 1400, 800)
	tester.Frame()
	if _, ok := tester.Find("New Tab"); !ok {
		t.Fatalf("title-bar mode shows no New Tab button; texts=%q", tester.Texts())
	}
	// Every chip is reachable by its title, the unselected ones included.
	for _, label := range []string{"Tab number 1", "Tab number 2", "Tab number 3"} {
		if _, ok := tester.Find(label); !ok {
			t.Fatalf("chip %q missing; texts=%q", label, tester.Texts())
		}
	}

	s.settings.General.TabsPlacement = "sidebar"
	tester = ui.NewTester(s.View, 1400, 800)
	tester.Frame()
	if _, ok := tester.Find("New Tab"); ok {
		t.Fatal("sidebar mode must not draw the Tab bar")
	}
}

func TestTabBarScrollsToTheSelectedTab(t *testing.T) {
	s := tabBarShell(40, "t40")
	tester := ui.NewTester(s.View, 1100, 800)
	tester.Frame()
	tester.Frame()
	if s.tabBarScroll.MaxX <= 0 {
		t.Fatalf("40 tabs must overflow the strip; MaxX=%v", s.tabBarScroll.MaxX)
	}
	if s.tabBarScroll.X <= 0 {
		t.Fatalf("the selected last tab must be scrolled into view; X=%v", s.tabBarScroll.X)
	}
}

// A Tab opened later grows into its width instead of appearing whole.
func TestNewTabChipGrowsIn(t *testing.T) {
	s := tabBarShell(3, "t1")
	tester := ui.NewTester(s.View, 1100, 800)
	tester.Frame()
	tester.Advance(2 * tabChipMotion.Duration)
	s.projection.Tabs = append(s.projection.Tabs, herdr.Tab{ID: "t4", ProjectID: "w1", Label: "Fresh tab", PaneCount: 1})
	s.projection.Panes = append(s.projection.Panes, herdr.Pane{ID: "pt4", ProjectID: "w1", TabID: "t4", Label: "pane t4"})
	s.projection.Projects[0].TabCount = 4
	tester.Frame()
	early, _ := tester.Find("Fresh tab")
	tester.Advance(2 * tabChipMotion.Duration)
	settled, ok := tester.Find("Fresh tab")
	if !ok || settled.W <= 0 {
		t.Fatalf("the new chip never reached its width: %+v ok=%v", settled, ok)
	}
	if early.W >= settled.W {
		t.Fatalf("the new chip appeared whole (%.1f wide at once, %.1f settled)", early.W, settled.W)
	}
}
