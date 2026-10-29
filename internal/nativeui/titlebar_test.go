package nativeui

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/thewei/solco/internal/commandcenter"
)

// TestTitlebarPinTogglesAlwaysOnTop pins the titlebar pin (2026-10-07): the
// button renders on every route's trailing edge and its click flips the
// Shell's always-on-top mirror. Headless sessions keep win nil, so the
// platform call is a guarded no-op and the click stays a pure state flip.
func TestTitlebarPinTogglesAlwaysOnTop(t *testing.T) {
	s := NewShell()
	s.loading = false
	tester := ui.NewTester(s.View, 1200, 800)
	if !tester.HasText(windowPinLabel) {
		t.Fatalf("titlebar missing pin button; texts=%q", tester.Texts())
	}
	if s.windowPinned {
		t.Fatal("window must start unpinned")
	}
	if err := tester.Click(windowPinLabel); err != nil {
		t.Fatal(err)
	}
	if !s.windowPinned {
		t.Fatal("pin click did not enable always-on-top")
	}
	if err := tester.Click(windowPinLabel); err != nil {
		t.Fatal(err)
	}
	if s.windowPinned {
		t.Fatal("repeat pin click did not disable always-on-top")
	}
}

// The sidebar band's floating chrome is one left/right split (2026-10-29
// user request): the Search button joins the sidebar toggle in the left
// pair, and a click opens the Command Center palette in the All scope — the
// destination ⌘K fires.
func TestTitlebarSearchSitsBesideTheSidebarToggle(t *testing.T) {
	s := NewShell()
	s.loading = false
	tester := ui.NewTester(s.View, 1200, 800)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Frame()
	toggle, ok := tester.Find("Hide Sidebar")
	if !ok {
		t.Fatalf("no sidebar toggle; texts=%q", tester.Texts())
	}
	search, ok := tester.Find("Search")
	if !ok {
		t.Fatalf("no Search button in the sidebar band; texts=%q", tester.Texts())
	}
	if math.Abs(float64(search.Y-toggle.Y)) > 0.5 || search.X < toggle.X+toggle.W-0.5 {
		t.Fatalf("Search at %.1f,%.1f is not right of the toggle at %.1f,%.1f on one row", search.X, search.Y, toggle.X, toggle.Y)
	}
	if err := tester.Click("Search"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !s.commandCenterOpen || s.commandCenterScope != commandcenter.ScopeAll {
		t.Fatalf("Search click: open=%v scope=%v, want the palette in the All scope", s.commandCenterOpen, s.commandCenterScope)
	}
}

// The history pair holds the sidebar band's right edge while the sidebar is
// open, and once the band is gone it falls in beside the left pair — never
// on top of it.
func TestChromeHistoryPairHoldsTheSidebarBandEdge(t *testing.T) {
	s := NewShell()
	s.loading = false
	tester := ui.NewTester(s.View, 1200, 800)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Frame()
	search, ok := tester.Find("Search")
	if !ok {
		t.Fatalf("no Search button; texts=%q", tester.Texts())
	}
	// Open: the pair right-aligns inside the sidebar column.
	back, okBack := tester.Find("Back")
	forward, okForward := tester.Find("Forward")
	if !okBack || !okForward {
		t.Fatalf("no history pair; texts=%q", tester.Texts())
	}
	wantRight := s.sidebarSlideW - sidebarResizeHit - 12
	if back.X >= forward.X || math.Abs(float64(forward.X+forward.W-wantRight)) > 0.5 {
		t.Fatalf("open sidebar: pair spans %.1f–%.1f, want the right edge at %.1f (slide %.1f)", back.X, forward.X+forward.W, wantRight, s.sidebarSlideW)
	}
	// Shut: no band edge to hold — the pair settles one gap after Search.
	s.sidebarCollapsed = true
	tester.Frame()
	back, okBack = tester.Find("Back")
	if !okBack {
		t.Fatalf("shut sidebar: no Back button; texts=%q", tester.Texts())
	}
	if math.Abs(float64(back.X-(search.X+search.W+sidebarNavGap))) > 0.5 {
		t.Fatalf("shut sidebar: Back at %.1f, want it one gap after Search ending at %.1f", back.X, search.X+search.W+sidebarNavGap)
	}
}
