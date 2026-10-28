package nativeui

import "testing"

func TestParseRevealURL(t *testing.T) {
	cases := []struct {
		raw  string
		want revealTarget
		ok   bool
	}{
		{"solco://reveal?session=default&workspace=w2&tab=t2", revealTarget{Session: "default", ProjectID: "w2", TabID: "t2"}, true},
		{"solco://reveal?tab=t2", revealTarget{TabID: "t2"}, true},
		{"solco://reveal?workspace=w2", revealTarget{ProjectID: "w2"}, true},
		{"solco://reveal?session=work&tab=t%2F9", revealTarget{Session: "work", TabID: "t/9"}, true},
		{"solco://reveal?session=default", revealTarget{}, false},
		{"solco://other?tab=t2", revealTarget{}, false},
		{"https://reveal?tab=t2", revealTarget{}, false},
		{"::not a url", revealTarget{}, false},
	}
	for _, tc := range cases {
		got, ok := parseRevealURL(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseRevealURL(%q) = %+v, %v; want %+v, %v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func revealShell() *Shell {
	s := NewShell()
	s.activeInstance = "default"
	s.router.Replace(routeHistory)
	s.projection = selectionFixture("w1", "t1", "p1")
	s.adoptRuntimeSelection(s.projection)
	return s
}

func TestRevealExternalSelectsTabInActiveInstance(t *testing.T) {
	s := revealShell()

	s.revealExternal(revealTarget{Session: "default", ProjectID: "w2", TabID: "t2"})

	if s.router.Path() != routeWorkspace {
		t.Fatalf("route = %q, want workspace", s.router.Path())
	}
	if s.selectedProjectID != "w2" || s.selectedTabID != "t2" {
		t.Fatalf("selection = %q/%q, want w2/t2", s.selectedProjectID, s.selectedTabID)
	}
	if s.pendingReveal != nil {
		t.Fatalf("pending reveal left behind: %+v", s.pendingReveal)
	}
}

func TestRevealExternalWithoutSessionTargetsActiveInstance(t *testing.T) {
	s := revealShell()
	s.revealExternal(revealTarget{TabID: "t2"})
	if s.selectedTabID != "t2" {
		t.Fatalf("selectedTabID = %q, want t2", s.selectedTabID)
	}
}

func TestRevealExternalWaitsForTabMissingFromProjection(t *testing.T) {
	s := revealShell()

	s.revealExternal(revealTarget{TabID: "t3"})
	if s.selectedTabID != "t1" || s.router.Path() == routeWorkspace {
		t.Fatalf("navigated to an unknown tab: %q route %q", s.selectedTabID, s.router.Path())
	}
	if s.pendingReveal == nil {
		t.Fatal("unknown tab must stay pending for the next projection")
	}

	next := selectionFixture("w1", "t1", "p1")
	next.Tabs = append(next.Tabs, next.Tabs[1])
	next.Tabs[2].ID, next.Tabs[2].Label = "t3", "Tab C"
	s.applyProjection(next, false)

	if s.selectedTabID != "t3" || s.selectedProjectID != "w2" {
		t.Fatalf("pending reveal not applied: %q/%q", s.selectedProjectID, s.selectedTabID)
	}
	if s.pendingReveal != nil {
		t.Fatal("pending reveal must clear once applied")
	}
}

func TestRevealExternalOtherSessionStaysPendingUntilThatInstanceIsActive(t *testing.T) {
	s := revealShell()

	s.revealExternal(revealTarget{Session: "work", TabID: "t2"})
	if s.selectedTabID != "t1" {
		t.Fatalf("revealed across sessions: %q", s.selectedTabID)
	}
	if s.pendingReveal == nil {
		t.Fatal("other-session reveal must stay pending")
	}

	s.activeInstance = "work"
	s.applyProjection(selectionFixture("w1", "t1", "p1"), false)
	if s.selectedTabID != "t2" {
		t.Fatalf("pending reveal not applied after switching instance: %q", s.selectedTabID)
	}
}

func TestPendingRevealExpires(t *testing.T) {
	s := revealShell()
	s.revealExternal(revealTarget{TabID: "t3"})
	s.pendingReveal.at = s.pendingReveal.at.Add(-2 * revealPendingTTL)

	next := selectionFixture("w1", "t1", "p1")
	s.applyProjection(next, false)
	if s.pendingReveal != nil {
		t.Fatal("expired pending reveal must be dropped")
	}
}
