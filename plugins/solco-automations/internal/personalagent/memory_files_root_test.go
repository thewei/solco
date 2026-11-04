package personalagent

import (
	"os"
	"path/filepath"
	"testing"
)

// AUD-14: opening a Memory directory must pin its inode, not the pathname.
// A replacement symlink to B must not redirect subsequent writes by A.
func TestMemoryTreePinsFactsAcrossDirectorySwap(t *testing.T) {
	f, s := memFiles(t)
	a := mustAssistant(t, s, "a-ada", "Ada")
	b := mustAssistant(t, s, "a-bo", "Bo")
	mustSync(t, f, a.ID)
	mustSync(t, f, b.ID)
	adaDir, _ := f.Dir(a.ID)
	boDir, _ := f.Dir(b.ID)
	tree, err := openMemoryTree(f.Root, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	oldFacts := filepath.Join(adaDir, "facts-still-ada")
	if err := os.Rename(filepath.Join(adaDir, "facts"), oldFacts); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(boDir, "facts"), filepath.Join(adaDir, "facts")); err != nil {
		t.Fatal(err)
	}
	if err := tree.writeFileAtomic("facts/pinned.md", []byte("Ada only")); err != nil {
		t.Fatal(err)
	}
	if raw, ok := tree.readSmall("facts/pinned.md", 1024); !ok || string(raw) != "Ada only" {
		t.Fatalf("root no longer pinned: %q %v", raw, ok)
	}
	if _, err := os.Stat(filepath.Join(oldFacts, "pinned.md")); err != nil {
		t.Fatalf("Ada's original directory not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(boDir, "facts", "pinned.md")); !os.IsNotExist(err) {
		t.Fatalf("cross-Assistant redirected write: %v", err)
	}
	if _, err := f.Sync(a.ID); err == nil {
		t.Fatal("new Sync must reject replaced facts symlink")
	}
}

// Replacing the whole memory/ parent after opening must also be harmless:
// the open directory handles keep the original A folders alive.
func TestMemoryTreePinsParentAcrossDirectorySwap(t *testing.T) {
	f, s := memFiles(t)
	a := mustAssistant(t, s, "a-ada", "Ada")
	b := mustAssistant(t, s, "a-bo", "Bo")
	mustSync(t, f, a.ID)
	mustSync(t, f, b.ID)
	adaDir, _ := f.Dir(a.ID)
	boDir, _ := f.Dir(b.ID)
	tree, err := openMemoryTree(f.Root, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	detached := adaDir + "-detached"
	if err := os.Rename(adaDir, detached); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(boDir, adaDir); err != nil {
		t.Fatal(err)
	}
	if err := tree.writeFileAtomic("INDEX.md", []byte("Ada's index")); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(detached, "INDEX.md")); err != nil || string(raw) != "Ada's index" {
		t.Fatalf("original A memory not written: %q %v", raw, err)
	}
	if raw, err := os.ReadFile(filepath.Join(boDir, "INDEX.md")); err != nil || string(raw) == "Ada's index" {
		t.Fatalf("B's memory was modified: %q %v", raw, err)
	}
	if _, err := f.Sync(a.ID); err == nil {
		t.Fatal("new Sync must reject replaced memory root")
	}
}

// Tracked file input must reject a leaf symlink instead of reading another
// Assistant's private file, even when the enclosing category is pinned.
func TestMemoryTreeReadRejectsLeafSymlink(t *testing.T) {
	f, s := memFiles(t)
	a := mustAssistant(t, s, "a-ada", "Ada")
	b := mustAssistant(t, s, "a-bo", "Bo")
	mustSync(t, f, a.ID)
	mustSync(t, f, b.ID)
	adaDir, _ := f.Dir(a.ID)
	boDir, _ := f.Dir(b.ID)
	secret := filepath.Join(boDir, "notes", "secret.md")
	if err := os.WriteFile(secret, []byte("Bo's secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(adaDir, "notes", "steal.md")); err != nil {
		t.Fatal(err)
	}
	tree, err := openMemoryTree(f.Root, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	if b, ok := tree.readSmall("notes/steal.md", 1024); ok {
		t.Fatalf("symlink read: %q", b)
	}
}
