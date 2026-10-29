package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: 依赖 Router、WorkspacePrimarySurface、Settings/History 导航与 MyGo headless Tester
 * [OUTPUT]: 锁定内页返回、历史详情归属、重复点击、顶栏互斥、快捷键 Back/Forward 的统一语义，以及侧栏顶带右缘 Router Back/Forward 按钮（2026-10-29 用户要求：顶带左右分布——左组侧栏钮+搜索钮居左，右组历史钮居右贴侧栏缘，侧栏收起后并入左组右侧）的历史遍历与首末灭活（灭活 = 字形半强度、无悬停脸，TestChromeHistoryButtonAtItsEndIsHalfStrength）
 * [POS]: UI-009 顶栏/侧栏导航回归，不触碰真实 Herdr 会话
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestHistoryRouteBoundaryDoesNotIncludeSimilarNames(t *testing.T) {
	for _, tc := range []struct {
		path    string
		history bool
	}{
		{routeHistory, true},
		{"/history-projects", true},
		{"/history/abc", true},
		{"/history-old", false},
		{"/historyless", false},
		{"/settings/general", false},
	} {
		if got := isHistoryRoute(tc.path); got != tc.history {
			t.Errorf("%s history=%v, want %v", tc.path, got, tc.history)
		}
		s := NewShell()
		s.router.Replace(tc.path)
		_, _, _, active := s.viewSwitchActive()
		if active != tc.history {
			t.Errorf("%s titlebar history=%v, want %v", tc.path, active, tc.history)
		}
	}
}

func TestHistoryViewNestedFirstReturnsIndexThenTerminal(t *testing.T) {
	for _, nested := range []string{"/history-projects", "/history/session-direct"} {
		t.Run(nested, func(t *testing.T) {
			s := NewShell()
			s.loading = false
			s.router.Replace(nested)
			s.showViewHistory()
			if got := s.router.Path(); got != routeHistory {
				t.Fatalf("nested History click went to %q, want History index", got)
			}
			_, _, _, active := s.viewSwitchActive()
			if !active {
				t.Fatal("History index is no longer marked active")
			}
			s.showViewHistory()
			if got := s.router.Path(); got != routeWorkspace {
				t.Fatalf("repeat History index click went to %q, want Workspace", got)
			}
			if s.surface.current() != WorkspaceSurfaceTerminal {
				t.Fatalf("leaving History index should reach Terminal, got %q", s.surface.current())
			}
		})
	}
}

// The sidebar band's floating chrome splits left/right (2026-10-29 user
// request): the sidebar toggle and Search sit left after the traffic
// lights, the Router's Back/Forward hold the band's right edge — Back
// returns through the pushed pages, Forward re-enters them, and at the
// history's ends the buttons are inert. The Router owns the walk; the
// buttons are its face.
func TestChromeBackForwardWalkRouterHistory(t *testing.T) {
	s := NewShell()
	s.loading = false
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	for _, want := range []string{"Back", "Forward"} {
		if _, ok := tester.Find(want); !ok {
			t.Fatalf("no %q on the sidebar band; texts=%q", want, tester.Texts())
		}
	}
	// At the start of the history Back cannot move; clicking stays put.
	if err := tester.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if got := s.router.Path(); got != routeWorkspace {
		t.Fatalf("inert Back moved to %q", got)
	}
	// From a strip page Back returns to the workspace and Forward re-enters
	// the page. Sessions wears no other "Back" label, so the click is the
	// chrome button's.
	s.router.Push(routeHistory)
	tester.Frame()
	if err := tester.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if got := s.router.Path(); got != routeWorkspace {
		t.Fatalf("Back went to %q, want the workspace", got)
	}
	if err := tester.Click("Forward"); err != nil {
		t.Fatal(err)
	}
	if got := s.router.Path(); got != routeHistory {
		t.Fatalf("Forward went to %q, want Sessions again", got)
	}
}

// A history button with nowhere to go reads as disabled (2026-10-29,
// shadcn's disabled:opacity-50): at the end of the history its glyph is half
// the strength of one that can move, and it lights no hover face. Both were
// the same muted glyph before, so Back at the start looked like a live one.
func TestChromeHistoryButtonAtItsEndIsHalfStrength(t *testing.T) {
	for _, dark := range []bool{false, true} {
		s := NewShell()
		s.loading = false
		tester := ui.NewTester(s.View, 1200, 800)
		tester.SetDark(dark)
		tester.SetPreferences(ui.Preferences{ReduceMotion: true})
		tester.Frame()
		s.router.Push(routeHistory) // Back has a page behind it; Forward has none ahead
		settleFrames(tester, 3)
		back, ok1 := tester.Find("Back")
		forward, ok2 := tester.Find("Forward")
		if !ok1 || !ok2 {
			t.Fatalf("dark=%v: Back/Forward missing; texts=%q", dark, tester.Texts())
		}
		// The strongest pixel in the button against its own corner: the glyph's ink.
		ink := func(r ui.Rect) int {
			img := tester.Image()
			at := func(x, y int) [4]uint8 {
				i := img.PixOffset(x, y)
				return [4]uint8{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}
			}
			ground, best := at(int(r.X)+1, int(r.Y)+1), 0
			for y := int(r.Y); y < int(r.Y+r.H); y++ {
				for x := int(r.X); x < int(r.X+r.W); x++ {
					best = max(best, rgbDiff(at(x, y), ground))
				}
			}
			return best
		}
		live, inert := ink(back), ink(forward)
		if live == 0 || float64(inert) > 0.75*float64(live) {
			t.Errorf("dark=%v: the inert Forward glyph has ink %d against the live Back's %d; it should read at about half", dark, inert, live)
		}
		// No hover face on the inert one.
		ground := pixelAt(tester, int(forward.X)+1, int(forward.Y)+1)
		tester.Move(forward.X+forward.W/2, forward.Y+forward.H/2)
		settleHover(tester)
		if d := rgbDiff(pixelAt(tester, int(forward.X)+2, int(forward.Y+forward.H/2)), ground); d > 2 {
			t.Errorf("dark=%v: the inert Forward lights a hover face (diff %d)", dark, d)
		}
	}
}

// Selecting panes rides the same history the chrome Back/Forward walk:
// each switch pushes one workspace location carrying the selection, Back
// restores the previous pane, Forward re-enters it, and a tree-row click is
// one entry — never a bare /workspace push beside the selection push.
func TestPaneSelectionRidesRouterHistory(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.activeInstance = "default"
	s.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "demo"}},
		Tabs:     []herdr.Tab{{ID: "t1", ProjectID: "w1", Label: "main"}},
		Panes: []herdr.Pane{
			{ID: "p1", ProjectID: "w1", TabID: "t1", TerminalID: "term-1", Label: "one"},
			{ID: "p2", ProjectID: "w1", TabID: "t1", TerminalID: "term-2", Label: "two"},
			{ID: "p3", ProjectID: "w1", TabID: "t1", TerminalID: "term-3", Label: "three"},
		},
	}
	s.setProjectExpanded("w1", true)
	s.setTabExpanded("t1", true)
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()

	// Two switches, two entries: Back walks them pane by pane, Forward
	// re-enters, and the page path never leaves the workspace.
	s.selectPane("p1")
	tester.Frame()
	if got := s.router.Query("pane"); got != "p1" {
		t.Fatalf("pane selection location = %q, want p1; loc=%q", got, s.router.Location())
	}
	if err := tester.Click("two"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if s.selectedPaneID != "p2" || s.router.Path() != routeWorkspace {
		t.Fatalf("tree click landed on %q showing pane %q", s.router.Path(), s.selectedPaneID)
	}
	s.router.Back()
	tester.Frame()
	if s.selectedPaneID != "p1" {
		t.Fatalf("Back restored pane %q, want p1 — a bare page push must not sit beside the selection entry", s.selectedPaneID)
	}
	s.router.Forward()
	tester.Frame()
	if s.selectedPaneID != "p2" {
		t.Fatalf("Forward restored pane %q, want p2", s.selectedPaneID)
	}
	// The mirror is frame-stable: sitting on an entry re-applies nothing.
	tester.Frame()
	if s.selectedPaneID != "p2" || s.router.Query("pane") != "p2" {
		t.Fatalf("idle frame drifted: pane %q query %q", s.selectedPaneID, s.router.Query("pane"))
	}
}

// Settings' Back and every other page's Terminal tab land the Terminal, not
// the Diff/Chat/Commit surface that was open before the page.
func TestSidebarWayHomeDoesNotRestoreDiff(t *testing.T) {
	for _, tc := range []struct{ route, label string }{
		{"/settings/general", "Back"},
		{routeHistory, "Terminal view"},
	} {
		t.Run(tc.route, func(t *testing.T) {
			s := NewShell()
			s.loading = false
			s.surface.openDiff(s.workspaceContext(), "example.go")
			s.router.Replace(tc.route)
			tester := ui.NewTester(s.View, 1200, 800)
			if err := tester.Click(tc.label); err != nil {
				t.Fatal(err)
			}
			if got := s.router.Path(); got != routeWorkspace {
				t.Fatalf("%s went to %q", tc.label, got)
			}
			if got := s.surface.current(); got != WorkspaceSurfaceTerminal {
				t.Fatalf("%s restored %q instead of the Terminal", tc.label, got)
			}
		})
	}
}

// A session detail has no Back foot; the Sessions tab is its parent step and
// goes to the index, not to whatever page was open before.
func TestHistoryDeepLinkSessionsTabTargetsParentNotPreviousPage(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.router.Replace("/settings/general")
	s.router.Push("/history/session-direct")
	tester := ui.NewTester(s.View, 1200, 800)
	if err := tester.Click("History view"); err != nil {
		t.Fatal(err)
	}
	if got := s.router.Path(); got != routeHistory {
		t.Fatalf("History detail's Sessions tab went to %q, want History index", got)
	}
}

func TestActiveSettingsAndHistorySidebarSelectionDoesNotGrowHistory(t *testing.T) {
	for _, tc := range []struct{ route, label string }{
		{routeHistory, "All sessions"},
		{"/settings/general", "General"},
	} {
		t.Run(tc.route, func(t *testing.T) {
			s := NewShell()
			s.loading = false
			s.router.Push(tc.route)
			tester := ui.NewTester(s.View, 1200, 800)
			if err := tester.Click(tc.label); err != nil {
				t.Fatal(err)
			}
			if got := s.router.Path(); got != tc.route {
				t.Fatalf("selected nav changed route to %q", got)
			}
			s.router.Back()
			if got := s.router.Path(); got != routeWorkspace {
				t.Fatalf("selected nav added a duplicate route to history: Back=%q", got)
			}
		})
	}
}

// TestChromeHistoryClusterWalksPages pins the top-left cluster (2026-10-15
// user request): the sidebar toggle carries the Router's Back/Forward beside
// it. At the history's start Back is inert; from a pushed page Back returns
// and Forward re-enters. The click goes through the chrome buttons — on the
// Sessions route the only Back/Forward labels are theirs (the Sessions list
// has no foot Back), so the Tester's first-match click cannot be ambiguous.
func TestChromeHistoryClusterWalksPages(t *testing.T) {
	s := NewShell()
	s.loading = false
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	if _, ok := tester.Find("Back"); !ok {
		t.Fatalf("no Back control beside the sidebar toggle; texts=%q", tester.Texts())
	}
	if _, ok := tester.Find("Forward"); !ok {
		t.Fatalf("no Forward control beside the sidebar toggle; texts=%q", tester.Texts())
	}
	// At the start of the history Back cannot move; clicking stays put.
	if err := tester.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if got := s.router.Path(); got != routeWorkspace {
		t.Fatalf("inert Back moved to %q", got)
	}

	s.router.Push(routeHistory)
	tester.Frame()
	if err := tester.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if got := s.router.Path(); got != routeWorkspace {
		t.Fatalf("Back went to %q, want the workspace", got)
	}
	if err := tester.Click("Forward"); err != nil {
		t.Fatal(err)
	}
	if got := s.router.Path(); got != routeHistory {
		t.Fatalf("Forward went to %q, want the Sessions page again", got)
	}
}
