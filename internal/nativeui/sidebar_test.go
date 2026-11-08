package nativeui

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

// TestBackFootBelongsToSettingsSessionsAndHosts pins the inner-page chrome
// after 2026-11-08 (U9): Settings, Sessions (list, detail, by-project) and
// Hosts end their sidebar in one Back row, in place of the Session switch and
// Settings button; the Workspace and the legacy Chat page keep the standard
// foot. Settings' own Back sits below its navigation and the page has no
// search row above it.
func TestBackFootBelongsToSettingsSessionsAndHosts(t *testing.T) {
	const height = 800
	s := NewShell()
	s.loading = false
	s.router.Replace(routeWorkspace)
	tester := ui.NewTester(s.View, 1200, height)
	if !tester.HasText("Choose Session") {
		t.Fatalf("workspace sidebar must keep its Workspace switch; texts=%q", tester.Texts())
	}
	// The chrome history cluster (Back beside the sidebar toggle, top-left)
	// exists on every route; a Back FOOT would sit at the sidebar's bottom.
	if back, ok := tester.Find("Back"); ok && back.Y >= height-60 {
		t.Fatalf("workspace sidebar has a Back foot at %+v", back)
	}

	for _, route := range headerRoutes() {
		tester := ui.NewTester(route.setup(t).View, 1200, height)
		settleFrames(tester, 3)
		back, hasBack := tester.Find("Back")
		footBack := hasBack && back.X <= sidebarWidth && back.Y >= height-60
		_, hasSession := tester.Find("Choose Session")
		if route.name == "chat" {
			// The legacy full page keeps the standard foot.
			if footBack || !hasSession {
				t.Errorf("chat: want the standard foot, got Back foot %v, session switch %v; texts=%q", footBack, hasSession, tester.Texts())
			}
			continue
		}
		if !footBack {
			t.Errorf("%s: Back at %+v (found=%v), want the sidebar foot", route.name, back, hasBack)
		}
		if hasSession {
			t.Errorf("%s kept the Session switch beside the Back foot; texts=%q", route.name, tester.Texts())
		}
		if route.name != "settings" {
			continue
		}
		if nav, ok := tester.Find("General"); !ok || nav.Y >= back.Y {
			t.Fatalf("settings: page navigation at %+v must sit above Back %+v", nav, back)
		}
		if _, ok := tester.Find("History view"); ok {
			t.Fatalf("settings kept the search row above its navigation; texts=%q", tester.Texts())
		}
	}

	// A session detail and the by-project page are Sessions pages too.
	for _, path := range []string{"/history-projects", "/history/codex:abc", routeQuota} {
		s := newMultiAgentHistoryShell(t)
		s.router.Replace(path)
		tester := ui.NewTester(s.View, 1200, height)
		settleFrames(tester, 3)
		if back, ok := tester.Find("Back"); !ok || back.Y < height-60 {
			t.Errorf("%s: a Sessions page lost its Back foot (Back at %+v, found=%v)", path, back, ok)
		}
		if tester.HasText("Choose Session") {
			t.Errorf("%s: a Sessions page kept the Session switch; texts=%q", path, tester.Texts())
		}
	}
}

// TestEveryInnerPageLeavesByItsBack pins the way out of the pages with a Back
// foot: it lands the workspace Terminal from Settings, Sessions and Hosts
// (Sessions and Hosts land the selected Pane, which is the Terminal here).
func TestEveryInnerPageLeavesByItsBack(t *testing.T) {
	for _, route := range headerRoutes() {
		if route.name == "chat" {
			continue // the legacy page keeps the standard foot
		}
		s := route.setup(t)
		tester := ui.NewTester(s.View, 1200, 800)
		settleFrames(tester, 3)
		if err := tester.Click("Back"); err != nil {
			t.Fatalf("%s: %v", route.name, err)
		}
		settleFrames(tester, 3)
		if got := s.router.Path(); got != routeWorkspace || s.surface.current() != WorkspaceSurfaceTerminal {
			t.Fatalf("%s: Back landed on %s showing %v, want the workspace Terminal", route.name, got, s.surface.current())
		}
	}
}

// TestSettingsNavLivesInAppSidebar pins the Settings inner-page sidebar: the
// section navigation renders in the app sidebar (no page-internal column)
// and switching sections keeps the nested route.
func TestSettingsNavLivesInAppSidebar(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.router.Replace("/settings/general")

	tester := ui.NewTester(s.View, 1200, 800)
	for _, want := range []string{"Settings", "General", "Terminal", "Agents", "Diagnostics"} {
		if !tester.HasText(want) {
			t.Fatalf("settings sidebar missing %q; texts=%q", want, tester.Texts())
		}
	}
	if err := tester.Click("Terminal"); err != nil {
		t.Fatal(err)
	}
	if got := s.router.Path(); got != "/settings/terminal" {
		t.Fatalf("section click route = %q", got)
	}
	tester.Frame()
	if !tester.HasText("Font family") {
		t.Fatalf("terminal section did not render; texts=%q", tester.Texts())
	}
}

func TestSidebarPinSectionRendersAndRoutes(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.activeInstance = "default"
	s.projection = herdr.Projection{
		Panes: []herdr.Pane{{ID: "p9", TerminalID: "term-9", Label: "Watch tower", TabID: "t1"}},
	}
	s.settings.Workbench.PinnedPanes = map[string][]string{"default": {"p9"}}

	tester := ui.NewTester(s.View, 1200, 800)
	// The Pin section replaces Recent (2026-10-05): it renders only when a
	// live Pane is pinned, with the Pane label as the row.
	for _, want := range []string{"Pin", "Watch tower"} {
		if !tester.HasText(want) {
			t.Fatalf("sidebar missing %q; texts=%q", want, tester.Texts())
		}
	}
	if err := tester.Click("Watch tower"); err != nil {
		t.Fatal(err)
	}
	if s.selectedPaneID != "p9" {
		t.Fatalf("pin click selected pane = %q", s.selectedPaneID)
	}

	// Unpinning through the settings service removes the section.
	s.togglePanePin("p9")
	tester.Frame()
	if tester.HasText("Watch tower") {
		t.Fatalf("pinned row still rendered after unpin; texts=%q", tester.Texts())
	}
	if len(s.settings.Workbench.PinnedPanes["default"]) != 0 {
		t.Fatalf("pins persisted after unpin: %q", s.settings.Workbench.PinnedPanes)
	}
}

func TestProjectAndTabExpansionArePresentationState(t *testing.T) {
	s := NewShell()
	s.settings.General.TabsPlacement = "sidebar" // these assertions read the tree/breadcrumb layout
	s.loading = false
	s.activeInstance = "default"
	s.projection = herdr.Projection{
		FocusedProjectID: "w1",
		FocusedTabID:     "t1",
		FocusedPaneID:    "p1",
		Projects: []herdr.Project{
			{ID: "w1", Label: "Project A", TabCount: 1},
			{ID: "w2", Label: "Project B", TabCount: 1},
		},
		Tabs: []herdr.Tab{
			{ID: "t1", ProjectID: "w1", Label: "Tab A", PaneCount: 2},
			{ID: "t2", ProjectID: "w2", Label: "Tab B", PaneCount: 1},
		},
		Panes: []herdr.Pane{
			{ID: "p1", ProjectID: "w1", TabID: "t1", Label: "Pane A1"},
			{ID: "p2", ProjectID: "w1", TabID: "t1", Label: "Pane A2"},
			{ID: "p3", ProjectID: "w2", TabID: "t2", Label: "Pane B"},
		},
	}
	s.selectedProjectID = "w1"
	s.selectedTabID = "t1"
	s.selectedPaneID = "p1"

	tester := ui.NewTester(s.View, 1200, 800)
	if !tester.HasText("Tab A") || !tester.HasText("Pane A1") {
		t.Fatalf("focused tree should start expanded; texts=%q", tester.Texts())
	}

	s.setTabExpanded("t1", false)
	tester.Frame()
	if tester.HasText("Pane A2") {
		t.Fatalf("focused tab should collapse without changing runtime focus")
	}
	if s.projection.FocusedTabID != "t1" {
		t.Fatalf("collapse changed runtime projection focus: %q", s.projection.FocusedTabID)
	}

	s.setTabExpanded("t1", true)
	tester.Frame()
	if !tester.HasText("Pane A2") {
		t.Fatalf("tab should expand again")
	}

	s.setProjectExpanded("w2", true)
	tester.Frame()
	if !tester.HasText("Tab A") || !tester.HasText("Tab B") {
		t.Fatalf("multiple projects should remain expanded; texts=%q", tester.Texts())
	}
	if s.projection.FocusedProjectID != "w1" {
		t.Fatalf("presentation expansion changed runtime project focus: %q", s.projection.FocusedProjectID)
	}

	s.setProjectExpanded("w1", false)
	tester.Frame()
	if tester.HasText("Pane A2") {
		t.Fatalf("focused project should collapse from presentation state")
	}
	if !tester.HasText("Tab B") {
		t.Fatalf("collapsing Project A should not collapse Project B")
	}
}

// TestSidebarSearchRowSitsAtTheTop pins U9 (2026-11-08): on every route but
// Settings the sidebar's first row under the traffic-light band is the search
// field with the Sessions toggle at its right end — one line, a field wider
// than an icon button and a square button, at the same place whichever page is
// open. The title bar carries no copy of either.
func TestSidebarSearchRowSitsAtTheTop(t *testing.T) {
	setups := []headerRoute{{"terminal", "", func(t *testing.T) *Shell {
		s := NewShell()
		s.loading = false
		return s
	}}}
	for _, route := range headerRoutes() {
		if route.name == "settings" {
			continue
		}
		setups = append(setups, route)
	}
	var baseSearch, baseToggle ui.Rect
	for i, route := range setups {
		tester := ui.NewTester(route.setup(t).View, 1200, 800)
		settleFrames(tester, 3)
		for _, label := range []string{"Search", "History view"} {
			if _, ok := tester.Find(label); !ok {
				t.Fatalf("%s: no %q in the sidebar's top row; texts=%q", route.name, label, tester.Texts())
			}
		}
		field, _ := tester.Find("Search")
		toggle, _ := tester.Find("History view")
		for name, r := range map[string]ui.Rect{"Search": field, "History view": toggle} {
			if r.X+r.W > sidebarWidth {
				t.Errorf("%s: %q at x=%.0f–%.0f is outside the sidebar", route.name, name, r.X, r.X+r.W)
			}
			if r.Y < 38 || r.Y+r.H > 38+gorexIconBtn+16 {
				t.Errorf("%s: %q at y=%.0f–%.0f is not the sidebar's first row under the band", route.name, name, r.Y, r.Y+r.H)
			}
		}
		if field.W <= gorexIconBtn || math.Abs(float64(toggle.W-gorexIconBtn)) > 0.5 {
			t.Errorf("%s: field %.1f wide, toggle %.1f wide; want a field and a %.0f-point button", route.name, field.W, toggle.W, float32(gorexIconBtn))
		}
		if toggle.X < field.X+field.W-0.5 || math.Abs(float64(toggle.Y+toggle.H/2-(field.Y+field.H/2))) > 0.5 {
			t.Errorf("%s: the Sessions toggle (%+v) must sit at the right end of the field's row (%+v)", route.name, toggle, field)
		}
		if i == 0 {
			baseSearch, baseToggle = field, toggle
			continue
		}
		if math.Abs(float64(field.X-baseSearch.X)) > 0.5 || math.Abs(float64(field.Y-baseSearch.Y)) > 0.5 ||
			math.Abs(float64(toggle.X-baseToggle.X)) > 0.5 || math.Abs(float64(toggle.Y-baseToggle.Y)) > 0.5 {
			t.Errorf("%s: the row moved (field %+v, toggle %+v; was %+v, %+v) — entering a page must not move it", route.name, field, toggle, baseSearch, baseToggle)
		}
	}
}

// TestSessionsToggleAndHostsPinOpenAndLeaveTheirPage pins the launchers that
// replaced the strip: the toggle opens Sessions and, lit, leaves for the
// Terminal; Hosts is one keystroke from the Command Center's pinned group and
// leaves through its Back foot.
func TestSessionsToggleAndHostsPinOpenAndLeaveTheirPage(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.hosts.loaded = true // never read the developer's real ~/.ssh/config
	tester := ui.NewTester(s.View, 1200, 800)
	settleFrames(tester, 2)

	if err := tester.Click("History view"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 2)
	if got := s.router.Path(); got != routeHistory {
		t.Fatalf("the Sessions toggle opened %q, want %q", got, routeHistory)
	}
	if err := tester.Click("History view"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 2)
	if got := s.router.Path(); got != routeWorkspace || s.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("the lit toggle went to %q/%v, want the Terminal", got, s.surface.current())
	}

	if err := tester.Click("Search"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 2)
	results := s.commandCenterResults()
	if len(results) == 0 || results[0].Action.ID != "app.hosts" {
		t.Fatalf("the palette's first row is %v, want the pinned Hosts", commandCenterIDs(results[:min(1, len(results))]))
	}
	s.executeCommandCenterResult(results[0])
	settleFrames(tester, 2)
	if got := s.router.Path(); got != routeHosts {
		t.Fatalf("pinned Hosts opened %q, want %q", got, routeHosts)
	}
	if err := tester.Click("Back"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 2)
	if got := s.router.Path(); got != routeWorkspace || s.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("Hosts' Back went to %q/%v, want the Terminal", got, s.surface.current())
	}
}
