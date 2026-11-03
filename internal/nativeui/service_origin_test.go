package nativeui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/scripts"
)

// writeOriginRoot builds a project root with the given package.json and a
// pnpm lockfile, so Detect picks the pnpm runner.
func writeOriginRoot(t *testing.T, packageJSON string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(packageJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pnpm-lock.yaml"), []byte("# lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestResolveServiceOrigins pins the scan-lane inference end to end on
// real argv shapes captured 2026-11-03: the wrapper explains a pane even
// though the listening child has a different name; a cwd-fallback daemon
// (no argv) and a remote root stay uninferred.
func TestResolveServiceOrigins(t *testing.T) {
	root := writeOriginRoot(t, `{"scripts":{"start":"node dist/cli.js serve","dev":"tsx scripts/dev.ts"}}`)
	s := NewShell()
	panes := []herdr.Pane{
		{ID: "p1", ProjectID: "w1", CWD: root},
		{ID: "p2", ProjectID: "w1", CWD: root},
		{ID: "p3", ProjectID: "w2", CWD: "/mnt/remote-box/repo"}, // remote: Detect refuses
		{ID: "p4", ProjectID: "w1", CWD: root},                   // daemon: no argv
	}
	acts := map[string]paneServiceActivity{
		"p1": {Proc: "node", Ports: []uint16{7676}, Argvs: [][]string{
			{"node", "/pm/pnpm/11/pnpm/bin/pnpm.cjs", "run", "start"},
			{"node", filepath.Join(root, "dist/cli.js"), "serve"},
		}},
		"p2": {Proc: "node", Ports: []uint16{3000}, Argvs: [][]string{{"tsx", "scripts/dev.ts"}}},
		"p3": {Proc: "node", Ports: []uint16{8081}, Argvs: [][]string{{"node", "/pm/pnpm/11/pnpm/bin/pnpm.cjs", "run", "start"}}},
		"p4": {Proc: "postgres", Ports: []uint16{5432}},
	}
	out := s.resolveServiceOrigins(panes, acts)
	if out["p1"] != "start" || out["p2"] != "dev" {
		t.Fatalf("origins = %v, want p1=start p2=dev", out)
	}
	if got, ok := out["p3"]; ok {
		t.Fatalf("remote pane inferred %q; local Detect must refuse remote roots", got)
	}
	if got, ok := out["p4"]; ok {
		t.Fatalf("argv-less daemon inferred %q; nothing explains it", got)
	}
}

// A user script definition scoped to the project explains the pane from a
// subdirectory cwd, exactly like scriptTitleFor's containment rule.
func TestResolveServiceOriginsUserDefinition(t *testing.T) {
	root := t.TempDir()
	store, err := scripts.NewStore(filepath.Join(root, "scripts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(scripts.ScriptDefinition{ID: "1", ProjectPath: root, Name: "dev.up", Command: "bun run dev"}); err != nil {
		t.Fatal(err)
	}
	s := NewShell()
	s.scriptStore = store
	panes := []herdr.Pane{{ID: "p1", ProjectID: "w1", CWD: filepath.Join(root, "sub")}}
	acts := map[string]paneServiceActivity{
		"p1": {Proc: "bun.native", Ports: []uint16{3636}, Argvs: [][]string{
			{"/pm/bun/1.3/bun/bin/bun.native", "run", "dev"},
		}},
	}
	if got := s.resolveServiceOrigins(panes, acts); got["p1"] != "dev.up" {
		t.Fatalf("origins = %v, want p1=dev.up (user definition through containment)", got)
	}
}

// The per-root cache holds for the TTL: a rewritten package.json inside
// the window does not change the inference until the cache expires.
func TestOriginEntriesCacheTTL(t *testing.T) {
	root := writeOriginRoot(t, `{"scripts":{"start":"node dist/cli.js serve"}}`)
	s := NewShell()
	if entries := s.originEntriesFor(root); len(entries) != 1 {
		t.Fatalf("entries = %+v, want the single script", entries)
	}
	newer := `{"scripts":{"start":"node dist/cli.js serve","dev":"vite"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	if entries := s.originEntriesFor(root); len(entries) != 1 {
		t.Fatalf("cached entries = %+v, want the stale single script within the TTL", entries)
	}
	// A different key is a different cache entry: Detect is root-exact
	// (the same rule as the run menu), so a plain subdirectory has no
	// commands of its own.
	if entries := s.originEntriesFor(filepath.Join(root, "sub")); len(entries) != 0 {
		t.Fatalf("subdir entries = %+v, want none (root-exact Detect)", entries)
	}
}

func TestBoundedArgv(t *testing.T) {
	long := strings.Repeat("x", maxArgvTokenChars+50)
	got := boundedArgv([]string{"node", long, "extra1", "extra2"})
	if len(got) != 4 || len(got[1]) != maxArgvTokenChars {
		t.Fatalf("boundedArgv = %q (len %d)", got[1], len(got))
	}
	var nilIn []string
	if out := boundedArgv(nilIn); out != nil {
		t.Fatalf("nil argv → %v, want nil", out)
	}
	many := make([]string, maxArgvTokens+5)
	for i := range many {
		many[i] = "t"
	}
	if out := boundedArgv(many); len(out) != maxArgvTokens {
		t.Fatalf("argv capped to %d, want %d", len(out), maxArgvTokens)
	}
}

// The inferred name reaches the row title through scriptTitleFor's
// fallback: without definitions the detected script name is the title.
func TestServiceRowModelsInferredTitle(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "devspace"}},
		Panes:    []herdr.Pane{{ID: "p1", ProjectID: "w1", CWD: "/repo-a"}},
	}
	s.serviceIndex = sidebarServiceIndex{
		Panes: map[string]paneServiceActivity{
			"p1": {Proc: "node", Ports: []uint16{7676}},
		},
		Projects: map[string]paneServiceActivity{},
	}
	s.serviceOrigins = map[string]string{"p1": "start"}

	rows := s.serviceRowModels()
	if len(rows) != 1 {
		t.Fatalf("rows = %d (%+v)", len(rows), rows)
	}
	row := rows[0]
	if row.Origin != serviceOriginScript || row.OriginLabel != "start" || row.Title != "start" {
		t.Fatalf("inferred row = %+v, want script origin titled start", row)
	}
}
