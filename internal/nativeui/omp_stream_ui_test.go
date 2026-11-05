package nativeui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/ompstream"
)

/**
 * [INPUT]: OMP provider row with injectable Manager and hermetic test HOME.
 * [OUTPUT]: no implicit installation during render/toggle; explicit install, status refresh and foreign-file protection.
 * [POS]: SC-05 Settings→Agents provider UI regression; no Herdr CLI or user HOME mutations.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const ompDemoSource = "// Managed by Solco, NOT by Herdr.\nexport default function extension() {}\n"

func testOMPStreamShell(t *testing.T, home string) (*Shell, *ui.Tester, *ompstream.Manager) {
	t.Helper()
	s := NewShell()
	s.newTabStarter = toggleKindsStarter{kinds: []app.SessionKind{
		{ID: app.TerminalKind, Label: "Terminal"},
		{ID: "omp", Label: "OMP"},
	}}
	s.providers.loaded = true
	s.providers.rows = []providerRow{{provider: history.AgentOMP, name: "OMP"}}
	m := ompstream.NewManager(home, []byte(ompDemoSource))
	s.ompStream.manager = m
	s.ompStream.diagnosis = m.Diagnose()
	s.ompStream.loaded = true
	s.router.Replace("/settings/providers")
	return s, ui.NewTester(s.View, 1200, 800), m
}

func TestPiStreamRequiresExplicitInstallInItsOwnDirectory(t *testing.T) {
	home := t.TempDir()
	s := NewShell()
	s.newTabStarter = toggleKindsStarter{kinds: []app.SessionKind{{ID: app.TerminalKind, Label: "Terminal"}, {ID: "pi", Label: "Pi"}}}
	s.providers.loaded = true
	s.providers.rows = []providerRow{{provider: history.AgentPi, name: "Pi"}}
	m := ompstream.NewPiManager(home, []byte(ompDemoSource+`const streamAgent = "omp";`))
	s.piStream.manager, s.piStream.diagnosis, s.piStream.loaded = m, m.Diagnose(), true
	s.router.Replace("/settings/providers")
	tester := ui.NewTester(s.View, 1200, 800)
	if !tester.HasText("Pi live text") || !tester.HasText("Install Live Text") {
		t.Fatalf("Pi install action missing: %q", tester.Texts())
	}
	if m.Diagnose().State != ompstream.Missing {
		t.Fatal("Pi installed without clicking")
	}
	if err := tester.Click("Install Live Text"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if m.Diagnose().State != ompstream.Current || !tester.HasText("Installed") {
		t.Fatalf("Pi install did not complete: %q", tester.Texts())
	}
	if _, err := os.Stat(filepath.Join(home, ".omp", "agent", "extensions", "solco-chat-stream.ts")); !os.IsNotExist(err) {
		t.Fatalf("Pi installation touched OMP extension: %v", err)
	}
}

func TestOMPStreamRequiresExplicitInstallAndShowsHealthyState(t *testing.T) {
	home := t.TempDir()
	s, tester, m := testOMPStreamShell(t, home)
	if !tester.HasText("OMP live text") || !tester.HasText("Install Live Text") {
		t.Fatalf("OMP installation control missing: %q", tester.Texts())
	}
	if m.Diagnose().State != ompstream.Missing {
		t.Fatal("render installed OMP Extension without user action")
	}
	if err := tester.Click("Install Live Text"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if m.Diagnose().State != ompstream.Current {
		t.Fatal("button click failed to install owned extension")
	}
	if tester.HasText("Install Live Text") {
		t.Fatalf("install button remained after success: %q", tester.Texts())
	}
	if !tester.HasText("Installed") {
		t.Fatalf("current state not shown after install: %q", tester.Texts())
	}
	if s.ompStream.installing {
		t.Fatal("install spinner remained busy")
	}
}

func TestOMPStreamRefusesForeignAndCanUpdate(t *testing.T) {
	home := t.TempDir()
	s, tester, m := testOMPStreamShell(t, home)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(home, ".omp", "agent", "extensions", "solco-chat-stream.ts")), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".omp", "agent", "extensions", "solco-chat-stream.ts")
	if err := os.WriteFile(path, []byte("foreign-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	// UI read must never offer an overwrite of foreign files.
	s.requestOMPStreamDiagnosis()
	tester.Frame()
	if got := m.Diagnose().State; got != ompstream.Foreign {
		t.Fatalf("foreign state %s", got)
	}
	if !tester.HasText("Protected") || tester.HasText("Install Live Text") {
		t.Fatalf("foreign extension shown as installable: %q", tester.Texts())
	}
	if err := os.WriteFile(path, []byte(ompDemoSource+"// older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.requestOMPStreamDiagnosis()
	tester.Frame()
	if got := m.Diagnose().State; got != ompstream.Outdated {
		t.Fatalf("outdated state %s", got)
	}
	if !tester.HasText("Update Live Text") {
		t.Fatalf("outdated update action missing: %q", tester.Texts())
	}
	if err := tester.Click("Update Live Text"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if got := m.Diagnose().State; got != ompstream.Current {
		t.Fatalf("current state %s", got)
	}
	if tester.HasText("Update Live Text") {
		t.Fatalf("stale update button remained: %q", tester.Texts())
	}
}
