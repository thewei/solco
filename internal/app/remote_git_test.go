package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thewei/solco-gitkit/gitworkbench"
	"github.com/thewei/solco/internal/remotehost"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeFile(t, dir, "a.go", "package a\n\nfunc A() {}\n")
	writeFile(t, dir, "b.txt", "keep\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestRemoteGitStatusAndDiffOnARealRepository(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "a.go", "package a\n\nfunc A() { println(1) }\n")
	writeFile(t, dir, "new.txt", "hello\n")
	writeFile(t, dir, "b.txt", "keep\nmore\n")
	gitIn(t, dir, "add", "b.txt")
	writeFile(t, dir, "b.txt", "keep\nmore\nand more\n")
	g := NewRemoteGit()

	status, err := g.Status(context.Background(), dir)
	if err != nil || !status.Repo || status.Branch != "main" || status.Detached || len(status.Head) != 7 {
		t.Fatalf("status %+v err %v", status, err)
	}
	byPath := map[string]remotehost.GitFile{}
	for _, f := range status.Files {
		byPath[f.Path] = f
	}
	if f := byPath["a.go"]; f.Status != "modified" || !f.Unstaged || f.Staged || f.Additions != 1 || f.Deletions != 1 {
		t.Fatalf("a.go %+v", f)
	}
	if f := byPath["new.txt"]; f.Status != "untracked" {
		t.Fatalf("new.txt %+v", f)
	}
	if f := byPath["b.txt"]; !f.Staged || !f.Unstaged {
		t.Fatalf("b.txt (staged and edited again) %+v", f)
	}

	diff, err := g.Diff(context.Background(), dir, "a.go")
	if err != nil || len(diff.Hunks) != 1 {
		t.Fatalf("diff %+v err %v", diff, err)
	}
	var kinds []string
	for _, l := range diff.Hunks[0].Lines {
		kinds = append(kinds, l.Kind)
	}
	if !strings.Contains(strings.Join(kinds, ""), "-+") || !strings.HasPrefix(diff.Hunks[0].Header, "@@ -") {
		t.Fatalf("hunk %+v", diff.Hunks[0])
	}
	untracked, err := g.Diff(context.Background(), dir, "new.txt")
	if err != nil || len(untracked.Hunks) != 1 || untracked.Hunks[0].Lines[0].Kind != "+" {
		t.Fatalf("untracked diff %+v err %v", untracked, err)
	}
}

func TestRemoteGitOnlyListedPathsReachGit(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "a.go", "package a\n")
	g := NewRemoteGit()
	for _, path := range []string{"../etc/passwd", "/etc/passwd", "b.txt", "a.go --output=/tmp/x", "-p"} {
		if _, err := g.Diff(context.Background(), dir, path); !errors.Is(err, remotehost.ErrGitFileNotFound) {
			t.Errorf("%q: %v, want ErrGitFileNotFound", path, err)
		}
	}
}

func TestRemoteGitNotARepository(t *testing.T) {
	status, err := NewRemoteGit().Status(context.Background(), t.TempDir())
	if err != nil || status.Repo || len(status.Files) != 0 {
		t.Fatalf("%+v %v", status, err)
	}
}

func TestRemoteGitCleanAndDetached(t *testing.T) {
	dir := gitRepo(t)
	g := NewRemoteGit()
	status, _ := g.Status(context.Background(), dir)
	if !status.Repo || len(status.Files) != 0 {
		t.Fatalf("clean: %+v", status)
	}
	gitIn(t, dir, "checkout", "-q", "--detach")
	status, _ = g.Status(context.Background(), dir)
	if !status.Detached || status.Branch != "" || status.Head == "" {
		t.Fatalf("detached: %+v", status)
	}
}

func TestRemoteGitBinaryHasNoHunks(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "img.bin", "a\x00b\x00c")
	gitIn(t, dir, "add", "img.bin")
	gitIn(t, dir, "commit", "-q", "-m", "bin")
	writeFile(t, dir, "img.bin", "a\x00b\x00d\x00")
	diff, err := NewRemoteGit().Diff(context.Background(), dir, "img.bin")
	if err != nil || !diff.Binary || len(diff.Hunks) != 0 {
		t.Fatalf("%+v %v", diff, err)
	}
}

func TestWireHunksBounds(t *testing.T) {
	long := strings.Repeat("é", remoteGitMaxLineBytes) // 2 bytes per rune
	hunks := wireHunksFixture(remoteGitMaxDiffLines+10, long)
	out, truncated := wireHunks(hunks)
	rows := 0
	for _, h := range out {
		rows += len(h.Lines)
		for _, l := range h.Lines {
			if len(l.Text) > remoteGitMaxLineBytes || !strings.HasSuffix(l.Text, "é") {
				t.Fatalf("line not cut on a rune boundary: %d bytes", len(l.Text))
			}
		}
	}
	if !truncated || rows > remoteGitMaxDiffLines {
		t.Fatalf("truncated=%v rows=%d", truncated, rows)
	}
}

func wireHunksFixture(n int, text string) []gitworkbench.Hunk {
	lines := make([]gitworkbench.DiffLine, n)
	for i := range lines {
		lines[i] = gitworkbench.DiffLine{Kind: gitworkbench.KindAdd, NewLine: i + 1, Text: text}
	}
	return []gitworkbench.Hunk{{NewStart: 1, NewLines: n, Lines: lines}}
}
