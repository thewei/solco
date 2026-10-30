package nativeui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/settings"
)

// TestFilesStartListsRecentFilesAndReadme pins the Files empty state B
// (2026-10-30): files opened before come back under their root, vanished or
// foreign ones do not, the README is offered, and a click opens the file.
func TestFilesStartListsRecentFilesAndReadme(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"README.md": "# Hi\n", "a.go": "package a\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := rightPanelTestShell(t, root)
	s.noteRecentFile(filepath.Join(root, "a.go"))
	s.noteRecentFile(filepath.Join(root, "gone.go"))                         // deleted since
	s.noteRecentFile(filepath.Join(filepath.Dir(root), "elsewhere", "x.go")) // another root
	s.showViewFiles()
	tester := ui.NewTester(s.View, 1200, 800)
	settleFrames(tester, 3)
	for _, want := range []string{"PICK UP WHERE YOU LEFT OFF", "a.go", "README.md"} {
		if !tester.HasText(want) {
			t.Fatalf("Files start lacks %q; texts=%q", want, tester.Texts())
		}
	}
	if tester.HasText("gone.go") || tester.HasText("x.go") {
		t.Fatalf("Files start lists a vanished or foreign file; texts=%q", tester.Texts())
	}
	if err := tester.Click("Open a.go"); err != nil {
		t.Fatal(err)
	}
	if got := s.rightPanel.selectedFilePath; got != filepath.Join(root, "a.go") {
		t.Fatalf("clicked recent file opened %q", got)
	}
}

func TestRecentFilesAreMRUBoundedAndPersist(t *testing.T) {
	s := NewShell()
	for i := 0; i < recentFilesKept+3; i++ {
		s.noteRecentFile("/r/f" + itoa(i))
	}
	s.noteRecentFile("/r/f5")
	if len(s.recentFiles) != recentFilesKept || s.recentFiles[0] != "/r/f5" {
		t.Fatalf("recent files = %v", s.recentFiles)
	}
	seen := map[string]bool{}
	for _, p := range s.recentFiles {
		if seen[p] {
			t.Fatalf("duplicate %q in %v", p, s.recentFiles)
		}
		seen[p] = true
	}
	s.recentHosts = []string{"dev-box"}
	snap := s.captureSession()
	if len(snap.RecentFiles) != len(s.recentFiles) || snap.RecentHosts[0] != "dev-box" {
		t.Fatalf("session = %+v", snap)
	}
	restored := NewShell()
	restored.settings.Session = settings.SessionSettings{Route: routeWorkspace, RecentFiles: snap.RecentFiles, RecentHosts: snap.RecentHosts}
	restored.restoreSession()
	if restored.recentFiles[0] != "/r/f5" || restored.recentHosts[0] != "dev-box" {
		t.Fatalf("restored %v %v", restored.recentFiles, restored.recentHosts)
	}
}

// TestHostsStartAndFilter pins the Hosts empty state B: recently connected
// cards, user@host rows that tell same-named blocks apart, and the filter.
func TestHostsStartAndFilter(t *testing.T) {
	s, tester := hostsTestShell(t, "Host dev-box\n  HostName 10.0.0.12\n  User deploy\n\nHost nas\n  HostName nas.lan\n", nil)
	if tester.HasText("RECENTLY CONNECTED") {
		t.Fatal("no connection recorded yet, so no recent cards")
	}
	if !tester.HasText("deploy@10.0.0.12") {
		t.Fatalf("rows lack the user@host subtitle; texts=%q", tester.Texts())
	}
	s.noteRecentHost("dev-box")
	s.noteRecentHost("gone-host") // no longer in the config
	settleFrames(tester, 3)
	if !tester.HasText("RECENTLY CONNECTED") {
		t.Fatalf("no recent card; texts=%q", tester.Texts())
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
