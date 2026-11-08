package personalagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// treeDigest lists every entry of a folder with size and mtime, so a test can prove nothing changed.
func treeDigest(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, p)
		out[rel] = info.Mode().String() + "|" + info.ModTime().String() + "|" + string(rune(info.Size()))
		return nil
	})
	return out
}

// SOLCO-D03: the old herdr-projects data is only ever read, and only when the user exports it.
func TestLegacyExportIsReadOnlyAndNeverMovesTheSource(t *testing.T) {
	src := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(src, "tasks", "p1"), 0o700))
	must(os.WriteFile(filepath.Join(src, "tasks", "p1", "t1.json"), []byte(`{"title":"keep me"}`), 0o600))
	must(os.WriteFile(filepath.Join(src, "settings.json"), []byte(`{}`), 0o600))
	outside := filepath.Join(t.TempDir(), "secret.txt")
	must(os.WriteFile(outside, []byte("do not follow"), 0o600))
	must(os.Symlink(outside, filepath.Join(src, "link")))
	before := treeDigest(t, src)

	dst := t.TempDir()
	res, err := ExportLegacy(src, dst, time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 || res.Skipped != 1 {
		t.Fatalf("export = %+v, want 2 files and the symlink skipped", res)
	}
	got, err := os.ReadFile(filepath.Join(res.To, "tasks", "p1", "t1.json"))
	if err != nil || string(got) != `{"title":"keep me"}` {
		t.Fatalf("copy: %q %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(res.To, "link")); err == nil {
		t.Fatal("a symlink must not be copied or followed")
	}
	after := treeDigest(t, src)
	if len(after) != len(before) {
		t.Fatalf("the source changed: %d entries before, %d after", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("the source entry %q changed", k)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("the file behind the symlink disappeared")
	}
}

func TestLegacyExportRefusesAMissingSourceAndADestinationInsideIt(t *testing.T) {
	if _, err := ExportLegacy(filepath.Join(t.TempDir(), "nope"), t.TempDir(), time.Now()); err == nil || codeFor(err).Code != "not_found" {
		t.Fatalf("missing source: %v", err)
	}
	src := t.TempDir()
	if _, err := ExportLegacy(src, filepath.Join(src, "export"), time.Now()); err == nil || codeFor(err).Code != "invalid" {
		t.Fatalf("destination inside the source: %v", err)
	}
	if entries, _ := os.ReadDir(src); len(entries) != 0 {
		t.Fatal("a refused export must not write into the source")
	}
}

// The command path: `assistant legacy-export --from X --to Y` answers the counts and leaves X alone.
func TestLegacyExportCommand(t *testing.T) {
	s, _ := openTest(t)
	api := &API{Store: s}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := api.Handle([]string{"assistant", "legacy-export", "--from", src, "--to", t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var env struct {
		Export LegacyExport `json:"export"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Export.Files != 1 {
		t.Fatalf("answer %+v", out)
	}
	if _, err := os.Stat(filepath.Join(src, "a.json")); err != nil {
		t.Fatal("the source file is gone")
	}
}

// Nothing in either Go module may name the old data folder except the explicit exporter (so no uninstall,
// cleanup or migration path can ever remove it).
func TestOnlyTheExporterTouchesTheOldDataFolder(t *testing.T) {
	for _, root := range []string{"../../../../internal", "../../../../cmd", "../.."} {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || filepath.Base(p) == "legacy.go" {
				return nil
			}
			raw, _ := os.ReadFile(p)
			if strings.Contains(string(raw), ".herdr-projects") {
				t.Errorf("%s names the old herdr-projects data folder", p)
			}
			return nil
		})
	}
}
