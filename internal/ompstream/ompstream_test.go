package ompstream

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

/**
 * [INPUT]: tiny embedded-like OMP Extension fixture and temporary HOME.
 * [OUTPUT]: safe install/update/diagnosis/uninstall with ownership/symlink/idempotence guards.
 * [POS]: SC-05 OMP companion installer independent of native UI and Herdr runtime.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const exampleExtension = "// Managed by Solco, NOT by Herdr.\nexport default function register() {}\n"

func TestPiManagerSharesOneCanonicalSourceAndKeepsExtensionsSeparate(t *testing.T) {
	home := t.TempDir()
	canonical := []byte(exampleExtension + ompAnchor + "\nexport default function pi() {}\n")
	omp := NewManager(home, canonical)
	pi := NewPiManager(home, canonical)
	if !bytes.Contains(pi.source, []byte(piAnchor)) || bytes.Contains(pi.source, []byte(ompAnchor)) {
		t.Fatalf("Pi compiled with wrong agent label: %s", pi.source)
	}
	if pi.path() == omp.path() || pi.dir() == omp.dir() {
		t.Fatal("Pi and OMP accidentally share the extensions folder")
	}
	for _, manager := range []*Manager{pi, omp} {
		if _, err := manager.Install(); err != nil {
			t.Fatal(err)
		}
		if manager.Diagnose().State != Current {
			t.Fatalf("not current: %s", manager.path())
		}
	}
	if bytes.Equal(read(t, pi.path()), read(t, omp.path())) {
		t.Fatal("same Agent identity sent to both runtimes")
	}
	if _, err := pi.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if omp.Diagnose().State != Current {
		t.Fatal("Pi uninstall touched OMP")
	}
	if got := NewPiManager(home, []byte(exampleExtension)).Diagnose().State; got != Error {
		t.Fatalf("missing canonical Pi anchor installed as OMP: %s", got)
	}
}

func TestPiManagerHonorsCustomAgentDirectoryAndRejectsRelativePaths(t *testing.T) {
	home := t.TempDir()
	canonical := []byte(exampleExtension + ompAnchor)
	custom := filepath.Join(home, "isolated-pi-agent")
	m := NewPiManagerWithDir(home, custom, canonical)
	if _, err := m.Install(); err != nil {
		t.Fatal(err)
	}
	if m.path() != filepath.Join(custom, "extensions", fileName) {
		t.Fatalf("Pi ignored custom agent dir: %s", m.path())
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "extensions", fileName)); !os.IsNotExist(err) {
		t.Fatalf("custom Pi installation accidentally used default directory: %v", err)
	}
	bad := NewPiManagerWithDir(home, "relative/agent", canonical)
	if bad.Diagnose().State != Error {
		t.Fatal("relative Pi agent dir accepted")
	}
	if _, err := bad.Install(); err == nil {
		t.Fatal("installed to a relative directory")
	}
}

func TestManagerLifecycleAndHerdrCoexistence(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home, []byte(exampleExtension))
	if got := m.Diagnose().State; got != Missing {
		t.Fatalf("initial state = %s", got)
	}
	herdrPath := filepath.Join(home, ".omp", "agent", "extensions", "herdr-omp-agent-state.ts")
	if err := os.MkdirAll(filepath.Dir(herdrPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(herdrPath, []byte("Herdr owns this!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(); err != nil {
		t.Fatal(err)
	}
	if got := m.Diagnose().State; got != Current {
		t.Fatalf("installed state = %s", got)
	}
	if _, err := m.Install(); err != nil {
		t.Fatalf("idempotent installation failed: %v", err)
	}
	updated := NewManager(home, append([]byte(exampleExtension), []byte("// revision2\n")...))
	if got := updated.Diagnose().State; got != Outdated {
		t.Fatalf("outdated state = %s", got)
	}
	if _, err := updated.Install(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(updated.source, read(t, m.path())) {
		t.Fatal("installed extension differs from embedded source")
	}
	if _, err := updated.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if got := updated.Diagnose().State; got != Missing {
		t.Fatalf("uninstalled state = %s", got)
	}
	if !bytes.Equal(read(t, herdrPath), []byte("Herdr owns this!")) {
		t.Fatal("modified Herdr extension")
	}
}

func TestManagerNeverOverwritesForeignFilesOrSymlinks(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home, []byte(exampleExtension))
	if err := os.MkdirAll(m.dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.path(), []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := m.Diagnose().State; got != Foreign {
		t.Fatalf("foreign state = %s", got)
	}
	if _, err := m.Install(); err == nil {
		t.Fatal("overwrote foreign extension")
	}
	if _, err := m.Uninstall(); err == nil {
		t.Fatal("removed foreign extension")
	}
	if got := string(read(t, m.path())); got != "foreign" {
		t.Fatal("changed foreign bytes")
	}
	if err := os.Remove(m.path()); err != nil {
		t.Fatal(err)
	}
	foreignTarget := filepath.Join(home, "protected")
	if err := os.WriteFile(foreignTarget, []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreignTarget, m.path()); err != nil {
		t.Fatal(err)
	}
	if got := m.Diagnose().State; got != Foreign {
		t.Fatalf("symlink state = %s", got)
	}
	if _, err := m.Install(); err == nil {
		t.Fatal("followed symlink")
	}
	if string(read(t, foreignTarget)) != "protected" {
		t.Fatal("modified symlink target")
	}
}

func TestManagerRefusesSymlinkedExtensionDirectoryAndInvalidSource(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home, []byte(exampleExtension))
	target := filepath.Join(home, "other")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(m.dir()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, m.dir()); err != nil {
		t.Fatal(err)
	}
	if got := m.Diagnose().State; got != Foreign {
		t.Fatalf("symlink dir state = %s", got)
	}
	if _, err := m.Install(); err == nil {
		t.Fatal("used foreign folder")
	}
	if err := os.Remove(m.dir()); err != nil {
		t.Fatal(err)
	}
	broken := NewManager(home, []byte("unmarked"))
	if got := broken.Diagnose().State; got != Error {
		t.Fatalf("invalid source state = %s", got)
	}
	if _, err := broken.Install(); err == nil {
		t.Fatal("installed unmarked source")
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
