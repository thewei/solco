package app

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/remotehost"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func identified(t *testing.T) string {
	t.Helper()
	dir := gitRepo(t)
	gitIn(t, dir, "config", "user.name", "t")
	gitIn(t, dir, "config", "user.email", "t@t")
	gitIn(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func refusalCode(err error) string {
	var r *remotehost.GitWriteError
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

func TestRemoteGitStageUnstageCommitOnARealRepository(t *testing.T) {
	dir := identified(t)
	writeFile(t, dir, "a.go", "package a\n\nfunc A() { println(1) }\n")
	writeFile(t, dir, "new.txt", "hello\n")
	g := NewRemoteGit()
	ctx := context.Background()

	if err := g.Stage(ctx, dir, []string{"a.go"}); err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, dir, "diff", "--cached", "--name-only"); got != "a.go" {
		t.Fatalf("staged %q", got)
	}
	if err := g.Unstage(ctx, dir, []string{"a.go"}); err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, dir, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("still staged %q", got)
	}
	// Only listed files reach Git.
	for _, bad := range []string{"missing.go", "../x", "-A", "b.txt"} {
		if err := g.Stage(ctx, dir, []string{bad}); !errors.Is(err, remotehost.ErrGitFileNotFound) {
			t.Fatalf("stage %q: %v", bad, err)
		}
	}

	status, err := g.Status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	// A moved HEAD fences the commit.
	if _, err := g.Commit(ctx, dir, remotehost.GitCommit{Subject: "x", Paths: []string{"a.go"}, Head: "0000000"}); refusalCode(err) != "git_stale" {
		t.Fatalf("stale head: %v", err)
	}
	hash, err := g.Commit(ctx, dir, remotehost.GitCommit{Subject: "Add things", Body: "why", Paths: []string{"a.go", "new.txt"}, Head: status.Head})
	if err != nil || len(hash) != 7 {
		t.Fatalf("commit %q %v", hash, err)
	}
	if got := gitOut(t, dir, "log", "-1", "--format=%s|%b"); got != "Add things|why" {
		t.Fatalf("message %q", got)
	}
	if got := gitOut(t, dir, "status", "--porcelain"); got != "" {
		t.Fatalf("left over %q", got)
	}
}

func TestRemoteGitSyncAgainstARealRemote(t *testing.T) {
	dir := identified(t)
	remote := t.TempDir()
	gitIn(t, remote, "init", "-q", "--bare", "-b", "main")
	gitIn(t, dir, "remote", "add", "origin", remote)
	g := NewRemoteGit()
	ctx := context.Background()

	if err := g.Sync(ctx, dir, remotehost.GitPull, ""); refusalCode(err) != "git_no_upstream" {
		t.Fatalf("pull without upstream: %v", err)
	}
	// The first push sets tracking.
	if err := g.Sync(ctx, dir, remotehost.GitPush, ""); err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, dir, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/main" {
		t.Fatalf("upstream %q", got)
	}
	if err := g.Sync(ctx, dir, remotehost.GitFetch, ""); err != nil {
		t.Fatal(err)
	}
	if err := g.Sync(ctx, dir, remotehost.GitPull, ""); err != nil {
		t.Fatal(err)
	}

	// A commit the remote lacks goes out on the next push; a stale HEAD fence refuses.
	writeFile(t, dir, "b.txt", "keep\nnext\n")
	if _, err := g.Commit(ctx, dir, remotehost.GitCommit{Subject: "next", Paths: []string{"b.txt"}}); err != nil {
		t.Fatal(err)
	}
	if err := g.Sync(ctx, dir, remotehost.GitPush, "0000000"); refusalCode(err) != "git_stale" {
		t.Fatalf("stale push: %v", err)
	}
	head := shortHash(gitOut(t, dir, "rev-parse", "HEAD"))
	if err := g.Sync(ctx, dir, remotehost.GitPush, head); err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, remote, "log", "-1", "--format=%s", "main"); got != "next" {
		t.Fatalf("remote head %q", got)
	}

	// A diverged history is refused by the fast-forward-only pull, with no Git text.
	other := t.TempDir()
	gitIn(t, other, "clone", "-q", remote, "c")
	clone := other + "/c"
	gitIn(t, clone, "config", "user.name", "t")
	gitIn(t, clone, "config", "user.email", "t@t")
	writeFile(t, clone, "c.txt", "c\n")
	gitIn(t, clone, "add", ".")
	gitIn(t, clone, "commit", "-q", "-m", "remote side")
	gitIn(t, clone, "push", "-q", "origin", "main")
	writeFile(t, dir, "b.txt", "keep\nnext\nlocal\n")
	if _, err := g.Commit(ctx, dir, remotehost.GitCommit{Subject: "local side", Paths: []string{"b.txt"}}); err != nil {
		t.Fatal(err)
	}
	err := g.Sync(ctx, dir, remotehost.GitPull, "")
	if refusalCode(err) != "git_rejected" {
		t.Fatalf("diverged pull: %v", err)
	}
	if err := g.Sync(ctx, dir, remotehost.GitPush, ""); refusalCode(err) != "git_rejected" {
		t.Fatalf("non-fast-forward push: %v", err)
	}
}
