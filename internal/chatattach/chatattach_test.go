package chatattach

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveExtensionAndMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "att")
	now := time.Now()
	path, err := Save(dir, []byte("png"), "image/png", "", now)
	if err != nil || !strings.HasSuffix(path, ".png") {
		t.Fatalf("path=%q err=%v", path, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if path, _ := Save(dir, []byte("x"), "", "Report.PDF", now); !strings.HasSuffix(path, ".pdf") {
		t.Fatalf("name ext: %q", path)
	}
	if path, _ := Save(dir, []byte("x"), "application/x-weird", "a.b/../c", now); !strings.HasSuffix(path, ".bin") {
		t.Fatalf("fallback ext: %q", path)
	}
	if _, err := Save(dir, nil, "image/png", "", now); err == nil {
		t.Fatal("empty attachment accepted")
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := filepath.Join(dir, "old.png")
	fresh := filepath.Join(dir, "fresh.png")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(old, now.Add(-15*24*time.Hour), now.Add(-15*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	Prune(dir, now)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("15-day-old file kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh file removed")
	}
}

func TestDirIsUnderTheCacheWithoutSpaces(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", "")
	dir, err := Dir("/Users/x/Library/Application Support/Solco")
	if err != nil {
		t.Fatal(err)
	}
	cache, _ := os.UserCacheDir()
	if dir != filepath.Join(cache, "Solco", DirName) || strings.Contains(dir, " ") {
		t.Fatalf("dir %q", dir)
	}
	if _, err := Dir(""); err == nil {
		t.Fatal("no user-data dir must refuse")
	}
}
