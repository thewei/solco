package nativeui

import (
	"testing"

	"github.com/thewei/solco/internal/settings"
)

// TestRecentHostsPersistInTheSession pins that the Hosts start page's recent
// connections survive a restart through the session snapshot.
func TestRecentHostsPersistInTheSession(t *testing.T) {
	s := NewShell()
	s.recentHosts = []string{"dev-box"}
	snap := s.captureSession()
	if len(snap.RecentHosts) != 1 || snap.RecentHosts[0] != "dev-box" {
		t.Fatalf("session = %+v", snap)
	}
	restored := NewShell()
	restored.settings.Session = settings.SessionSettings{Route: routeWorkspace, RecentHosts: snap.RecentHosts}
	restored.restoreSession()
	if len(restored.recentHosts) != 1 || restored.recentHosts[0] != "dev-box" {
		t.Fatalf("restored %v", restored.recentHosts)
	}
}

// TestHostsStartAndFilter pins the Hosts empty state B: recently connected
// cards, user@host rows that tell same-named blocks apart, and the filter.
func TestHostsStartAndFilter(t *testing.T) {
	s, tester := hostsTestShell(t, "Host dev-box\n  HostName 10.0.0.12\n  User deploy\n\nHost nas\n  HostName nas.lan\n", nil)
	if tester.HasText("RECENTLY CONNECTED") {
		t.Fatal("no connection recorded yet, so no recent cards")
	}
	// 2026-10-30 UX audit 3: a list row gives its name the whole width; user@host
	// shows on the selected or hovered row (TestHostEndpointShowsOnlyOnTheSelectedRow).
	if tester.HasText("deploy@10.0.0.12") {
		t.Fatalf("an unselected row drew its user@host; texts=%q", tester.Texts())
	}
	s.noteRecentHost("dev-box")
	s.noteRecentHost("gone-host") // no longer in the config
	settleFrames(tester, 3)
	if !tester.HasText("RECENTLY CONNECTED") {
		t.Fatalf("no recent card; texts=%q", tester.Texts())
	}
	if !tester.HasText("deploy@10.0.0.12") {
		t.Fatalf("the recent card lacks the user@host subtitle; texts=%q", tester.Texts())
	}
	if tester.HasText("gone-host") {
		t.Fatal("a host missing from the config must not get a card")
	}
	s.hosts.filter = "nas"
	settleFrames(tester, 3)
	if onScreen(tester, "deploy@10.0.0.12") && len(textsWithPrefix(tester, "dev-box")) > 1 {
		t.Fatalf("the filter left dev-box in the list; texts=%q", tester.Texts())
	}
	s.hosts.filter = "zzz"
	settleFrames(tester, 3)
	if !tester.HasText("No matching hosts") {
		t.Fatalf("an empty filter result says nothing; texts=%q", tester.Texts())
	}
}
