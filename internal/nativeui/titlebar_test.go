package nativeui

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/thewei/solco/internal/commandcenter"
)

// The sidebar band's floating chrome is one left/right split (2026-10-29
// user request): the Search button joins the sidebar toggle in the left
// pair, and a click opens the Command Center palette in the All scope — the
// destination ⌘K fires.
//
// Since 2026-11-08 (U9) the button draws only where the sidebar's own search
// row does not: with the sidebar shut, and on Settings.
func TestTitlebarSearchSitsBesideTheSidebarToggle(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.sidebarCollapsed = true
	tester := ui.NewTester(s.View, 1200, 800)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Frame()
	toggle, ok := tester.Find("Show Sidebar")
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

// TestSearchLivesInTheSidebarRowWhileItIsOnScreen pins U9's hand-over: with
// the sidebar open the search is the field in its first row (wider than an
// icon button, under the band) and the band carries no second button; shut,
// or on Settings, the band's 28-point button is the way in.
func TestSearchLivesInTheSidebarRowWhileItIsOnScreen(t *testing.T) {
	open := NewShell()
	open.loading = false
	tester := ui.NewTester(open.View, 1200, 800)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Frame()
	field, ok := tester.Find("Search")
	if !ok || field.W <= gorexIconBtn || field.Y < 38 || field.X+field.W > sidebarWidth {
		t.Fatalf("open sidebar: Search = %+v (found=%v), want the sidebar's field under the band", field, ok)
	}
	if !tester.HasText("⌘K") {
		t.Errorf("the field must show the Search shortcut; texts=%q", tester.Texts())
	}
	if err := tester.Click("Search"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !open.commandCenterOpen || open.commandCenterScope != commandcenter.ScopeAll {
		t.Fatalf("the field opened open=%v scope=%v, want the palette in the All scope", open.commandCenterOpen, open.commandCenterScope)
	}

	settings := NewShell()
	settings.loading = false
	settings.router.Replace("/settings/general")
	tester = ui.NewTester(settings.View, 1200, 800)
	tester.Frame()
	button, ok := tester.Find("Search")
	if !ok || math.Abs(float64(button.W-gorexIconBtn)) > 0.5 || button.Y >= 38 {
		t.Fatalf("Settings: Search = %+v (found=%v), want the band's icon button", button, ok)
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
	// Shut: no band edge to hold — the pair settles one gap after Search
	// (the band's button, there being no sidebar row then).
	s.sidebarCollapsed = true
	tester.Frame()
	search, ok := tester.Find("Search")
	if !ok {
		t.Fatalf("shut sidebar: no Search button; texts=%q", tester.Texts())
	}
	back, okBack = tester.Find("Back")
	if !okBack {
		t.Fatalf("shut sidebar: no Back button; texts=%q", tester.Texts())
	}
	if math.Abs(float64(back.X-(search.X+search.W+sidebarNavGap))) > 0.5 {
		t.Fatalf("shut sidebar: Back at %.1f, want it one gap after Search ending at %.1f", back.X, search.X+search.W+sidebarNavGap)
	}
}
