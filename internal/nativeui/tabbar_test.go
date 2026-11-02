package nativeui

import (
	"fmt"
	"testing"
	"time"

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

func planTabs(n, selected int) []titlebarTab {
	tabs := make([]titlebarTab, n)
	for i := range tabs {
		tabs[i] = titlebarTab{ID: fmt.Sprintf("t%d", i), Label: "Tab", Selected: i == selected}
	}
	return tabs
}

func TestPlanTabStripKeepsNaturalWidthsWhenTheyFit(t *testing.T) {
	plan := planTabStrip(1000, planTabs(3, 0))
	if plan.Shrunk || plan.Width[1] != 0 {
		t.Fatalf("three tabs in 1000pt must stay natural: %+v", plan)
	}
	if plan := planTabStrip(0, planTabs(30, 0)); plan.Shrunk {
		t.Fatalf("an unknown width must not shrink: %+v", plan)
	}
}

func TestPlanTabStripSharesWidthEquallyAndKeepsTheSelectedTitle(t *testing.T) {
	plan := planTabStrip(800, planTabs(8, 2))
	if !plan.Shrunk || plan.Overflow {
		t.Fatalf("8 tabs in 800pt shrink without scrolling: %+v", plan)
	}
	var sum float32
	for i, w := range plan.Width {
		sum += w
		if i != 2 && w != plan.Width[0] && i != 0 {
			t.Fatalf("unselected chips must share one width: %v", plan.Width)
		}
	}
	if plan.Width[2] < tabChipSelectedMinWidth {
		t.Fatalf("selected chip %v below its title floor", plan.Width[2])
	}
	if sum+tabStripGap*7 > 800+0.5 {
		t.Fatalf("plan %v overruns the strip", plan.Width)
	}
}

func TestPlanTabStripGoesCompactThenScrolls(t *testing.T) {
	plan := planTabStrip(520, planTabs(8, 0))
	if !plan.Compact[1] || plan.Compact[0] {
		t.Fatalf("narrow chips must be icon-only, the selected never: %+v", plan)
	}
	plan = planTabStrip(520, planTabs(30, 0))
	if !plan.Overflow || plan.Width[1] != tabChipCompactWidth || plan.Width[0] != tabChipSelectedMinWidth {
		t.Fatalf("past the floor the strip scrolls at 48/160: %+v", plan)
	}
}

// Under pressure the bar offers the All Tabs menu and every chip keeps its
// title in the hover card.
func TestTabBarShrinksAndOffersAllTabs(t *testing.T) {
	s := tabBarShell(12, "t1")
	tester := ui.NewTester(s.View, 1000, 800)
	tester.Frame()
	tester.Frame()
	if _, ok := tester.Find("All Tabs"); !ok {
		t.Fatalf("12 tabs in 1000pt must show the All Tabs menu; texts=%q", tester.Texts())
	}
	tabs := s.titlebarTabs(false)
	card := tabTipSpec(tabs[1], "Project A")
	if card.Title != "Tab number 2" || card.Meta != "Project A · 1 pane" || card.State == "" || !card.HasTone {
		t.Fatalf("hover card = %+v, want title / workspace · panes / state", card)
	}
	if card := tabTipSpec(tabs[0], "Project A"); card.State != "" || card.HasTone || card.Meta == "" {
		t.Fatalf("a Tab without a state says only title and meta: %+v", card)
	}
}

// The compact strip fits its room: no scrolling, and no ‹ › that would take
// room back and push it into overflow again.
func TestTabBarCompactStripFitsWithoutScrollButtons(t *testing.T) {
	s := tabBarShell(8, "t2")
	tester := ui.NewTester(s.View, 1183, 921)
	for i := 0; i < 4; i++ {
		tester.Frame()
	}
	if s.tabBarScroll.MaxX > 1 {
		t.Fatalf("8 shrunk tabs must fit without scrolling; MaxX=%v", s.tabBarScroll.MaxX)
	}
	if _, ok := tester.Find("Scroll Tabs Right"); ok {
		t.Fatal("scroll buttons must not appear while the strip fits")
	}
}

func TestAllTabsMenuWaitsForIconOnlyChips(t *testing.T) {
	if planTabStrip(700, planTabs(4, 0)).needsMenu() {
		t.Fatal("four truncated-but-readable chips must not need the menu")
	}
	if !planTabStrip(520, planTabs(8, 0)).needsMenu() {
		t.Fatal("icon-only chips need the menu")
	}
	if !planTabStrip(520, planTabs(30, 0)).needsMenu() {
		t.Fatal("a scrolling strip needs the menu")
	}
}

// Dragging a chip over another opens a gap on the half it hovers and drops
// there (the drop itself is Herdr's tab.move through reorderTree, covered by
// TestReorderOnlyWithinAuthoritativeParentAndSession).
func TestTabChipDragOpensAGapAndDrops(t *testing.T) {
	s := tabBarShell(3, "t1")
	tester := ui.NewTester(s.View, 1400, 800)
	for i := 0; i < 3; i++ {
		tester.Frame()
	}
	from, ok := tester.Find("Tab number 1")
	if !ok {
		t.Fatal("missing chip 1")
	}
	to, _ := tester.Find("Tab number 3")
	tester.Press(from.X+20, from.Y+from.H/2)
	tester.Move(from.X+40, from.Y+from.H/2+2)
	tester.Move(to.X+8, to.Y+to.H/2)
	for i := 0; i < 4; i++ {
		tester.Frame()
	}
	if s.tabDrop.id != "t3" || !s.tabDrop.before {
		t.Fatalf("hovering chip 3's left half must target it before: %+v", s.tabDrop)
	}
	tester.Advance(2 * motionRow)
	moved, _ := tester.Find("Tab number 3")
	if moved.X <= to.X+tabChipGap/2 {
		t.Fatalf("chip 3 did not make way: x %.0f -> %.0f", to.X, moved.X)
	}
	var got []string
	s.onReorder = func(from, to treeNodeDrag, before bool) {
		got = append(got, fmt.Sprintf("%s>%s:%v", from.ID, to.ID, before))
	}
	tester.Release(to.X+8, to.Y+to.H/2)
	tester.Frame()
	if len(got) != 1 || got[0] != "t1>t3:true" {
		t.Fatalf("the drop must reach reorderTree once, before chip 3: %v", got)
	}
}

func TestTabChipDragInAShrunkStrip(t *testing.T) {
	s := tabBarShell(8, "t2")
	tester := ui.NewTester(s.View, 1183, 921)
	for i := 0; i < 4; i++ {
		tester.Frame()
	}
	from, ok := tester.Find("Tab number 4")
	if !ok {
		t.Fatal("missing chip 4")
	}
	to, _ := tester.Find("Tab number 6")
	tester.Press(from.X+10, from.Y+from.H/2)
	tester.Move(from.X+25, from.Y+from.H/2+2)
	tester.Move(to.X+10, to.Y+to.H/2)
	for i := 0; i < 4; i++ {
		tester.Frame()
	}
	if s.tabDrop.id != "t6" {
		t.Fatalf("hovering chip 6 must target it: %+v (from %+v to %+v)", s.tabDrop, from, to)
	}
}

// Resting on a chip shows the card with its facts, in the theme's surface.
func TestTabChipHoverCardShowsItsFacts(t *testing.T) {
	s := tabBarShell(3, "t1")
	tester := ui.NewTester(s.View, 1400, 800)
	for i := 0; i < 3; i++ {
		tester.Frame()
	}
	chip, _ := tester.Find("Tab number 2")
	tester.Move(chip.X+chip.W/2, chip.Y+chip.H/2)
	tester.Advance(2 * time.Second)
	if _, ok := tester.Find("Project A · 1 pane"); !ok {
		t.Fatalf("hover card missing its Workspace · panes line; texts=%q", tester.Texts())
	}
}

// Dragging right over a neighbour and letting go past its middle lands after
// it: the gap that opens before a chip slides it away from the pointer, and
// the side is judged against where the chip rests, so the reach does not grow.
func TestTabChipDragRightLandsAfterTheNeighbour(t *testing.T) {
	s := tabBarShell(4, "t2")
	tester := ui.NewTester(s.View, 1400, 921)
	for i := 0; i < 5; i++ {
		tester.Advance(50 * time.Millisecond)
	}
	var got []string
	s.onReorder = func(from, to treeNodeDrag, before bool) {
		got = append(got, fmt.Sprintf("%s>%s:%v", from.ID, to.ID, before))
	}
	from, _ := tester.Find("Tab number 1")
	to, _ := tester.Find("Tab number 2")
	y := from.Y + from.H/2
	startX, endX := from.X+from.W/2, to.X+to.W*0.75
	tester.Press(startX, y)
	tester.Move(startX+6, y)
	for i := 1; i <= 12; i++ {
		tester.Move(startX+6+(endX-startX-6)*float32(i)/12, y)
		tester.Advance(16 * time.Millisecond)
	}
	for i := 0; i < 6; i++ {
		tester.Advance(16 * time.Millisecond)
	}
	tester.Release(endX, y)
	tester.Advance(16 * time.Millisecond)
	if len(got) != 1 || got[0] != "t1>t2:false" {
		t.Fatalf("a drop past chip 2's middle must land after it: %v", got)
	}
}
