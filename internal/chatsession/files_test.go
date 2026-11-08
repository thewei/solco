package chatsession

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"src/app.go", "src/util.go", "README.md", ".env", "docs/guide.md"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestListFilesShallowContainsMatchAndDotfilesOnlyWhenAsked(t *testing.T) {
	root := tree(t)
	if got := ListFiles(root, "", true); !reflect.DeepEqual(got, []string{"README.md", "docs/", "src/"}) {
		t.Fatalf("root: %v", got)
	}
	if got := ListFiles(root, "read", true); !reflect.DeepEqual(got, []string{"README.md"}) {
		t.Fatalf("contains, any case: %v", got)
	}
	if got := ListFiles(root, ".e", true); !reflect.DeepEqual(got, []string{".env"}) {
		t.Fatalf("dotfile when asked: %v", got)
	}
	if got := ListFiles(root, "src/ut", true); !reflect.DeepEqual(got, []string{"util.go"}) {
		t.Fatalf("descends: %v", got)
	}
	if got := ListFiles(root, "./src/", true); !reflect.DeepEqual(got, []string{"app.go", "util.go"}) {
		t.Fatalf("dot-slash prefix: %v", got)
	}
}

func TestListFilesIsBoundedAndQuietOnMissing(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxFileHits+5; i++ {
		_ = os.WriteFile(filepath.Join(root, string(rune('a'+i%26))+string(rune('a'+i/26))+".txt"), nil, 0o644)
	}
	if got := ListFiles(root, "", true); len(got) != maxFileHits {
		t.Fatalf("len %d", len(got))
	}
	if ListFiles(root, "nope/x", true) != nil || ListFiles("", "x", true) != nil {
		t.Fatal("missing folder must list nothing")
	}
}

func TestContainedWalkNeverLeavesTheFolder(t *testing.T) {
	root := tree(t)
	project := filepath.Join(root, "src")
	if got := ListFiles(project, "../", true); got != nil {
		t.Fatalf("dot-dot escaped: %v", got)
	}
	if got := ListFiles(project, "../", false); len(got) == 0 {
		t.Fatal("the desktop keeps its own reach")
	}
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "secret.txt"), nil, 0o644)
	if err := os.Symlink(outside, filepath.Join(project, "link")); err != nil {
		t.Skip("no symlinks")
	}
	if got := ListFiles(project, "link/", true); got != nil {
		t.Fatalf("symlink escaped: %v", got)
	}
}
