package nativeui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/gitworkbench"
)

/**
 * [INPUT]: 真实仓库 fixture（newGraphFixture）、提交行双击
 * [OUTPUT]: 双击提交 → 检出的计划与端到端回归（切本地分支 / 游离 / 被本地文件拒绝时的说明弹窗）
 * [POS]: 2026-11-03 用户要求"双击 Commit 切到对应分支，同 Fork"
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestPlanCommitCheckoutFollowsFork(t *testing.T) {
	ref := func(kind gitworkbench.CommitRefKind, name string, head bool) gitworkbench.CommitRef {
		return gitworkbench.CommitRef{Kind: kind, Name: name, Head: head}
	}
	hash := strings.Repeat("a", 40)
	for _, c := range []struct {
		name string
		refs []gitworkbench.CommitRef
		want commitCheckoutPlan
	}{
		{"current branch", []gitworkbench.CommitRef{ref("remote", "origin/main", false), ref("branch", "main", true)}, commitCheckoutPlan{"here", "main"}},
		{"local branch wins over remote", []gitworkbench.CommitRef{ref("remote", "origin/dev", false), ref("branch", "dev", false), ref("tag", "v1", false)}, commitCheckoutPlan{"branch", "dev"}},
		{"remote only", []gitworkbench.CommitRef{ref("tag", "v1", false), ref("remote", "origin/dev", false)}, commitCheckoutPlan{"remote", "origin/dev"}},
		{"tag only detaches", []gitworkbench.CommitRef{ref("tag", "v1", false)}, commitCheckoutPlan{"detach", hash}},
		{"no ref detaches", nil, commitCheckoutPlan{"detach", hash}},
	} {
		if got := planCommitCheckout(gitworkbench.CommitInfo{Hash: hash, Refs: c.refs}); got != c.want {
			t.Errorf("%s: plan = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func (f *graphFixture) reloadCommits(t *testing.T) {
	t.Helper()
	commits, err := f.shell.git.runner.ListCommitGraph(context.Background(), f.root, gitworkbench.GraphQuery{})
	if err != nil {
		t.Fatal(err)
	}
	f.shell.git.commits = commits
	f.shell.git.commitView = nil
	f.tester.Frame()
	f.tester.Frame()
}

func TestDoubleClickingACommitSwitchesToItsBranch(t *testing.T) {
	f := newGraphFixture(t)
	f.shell.commitsMode = true
	f.tester.Frame()
	f.tester.Frame()
	for i := 0; i < 2; i++ {
		if err := f.tester.Click("s1"); err != nil { // the tip of feature/signup
			t.Fatal(err)
		}
	}
	f.drain(t)
	if got := gitOut(t, f.root, "branch", "--show-current"); got != "feature/signup" {
		t.Fatalf("current branch = %q, want feature/signup", got)
	}
}

func TestDoubleClickingAnUnnamedCommitDetachesHead(t *testing.T) {
	f := newGraphFixture(t)
	f.shell.commitsMode = true
	f.tester.Frame()
	f.tester.Frame()
	want := gitOut(t, f.root, "rev-parse", "main~1")
	for i := 0; i < 2; i++ {
		if err := f.tester.Click("initial"); err != nil {
			t.Fatal(err)
		}
	}
	f.drain(t)
	if got := gitOut(t, f.root, "branch", "--show-current"); got != "" {
		t.Fatalf("HEAD should be detached, on %q", got)
	}
	if got := gitOut(t, f.root, "rev-parse", "HEAD"); got != want {
		t.Fatalf("HEAD = %q, want %q", got, want)
	}
}

// Local files that the target branch would overwrite stop the switch; the
// user is told which files to deal with first, and nothing changes.
func TestDoubleClickRefusedByLocalFilesExplainsWhich(t *testing.T) {
	f := newGraphFixture(t)
	gitScript(t, f.root,
		[]string{"switch", "-q", "feature/login"},
		[]string{"-c", "user.email=a@b.c", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "marker"},
	)
	if err := os.WriteFile(filepath.Join(f.root, "clash.txt"), []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitScript(t, f.root,
		[]string{"add", "clash.txt"},
		[]string{"-c", "user.email=a@b.c", "-c", "user.name=t", "commit", "-q", "-m", "adds clash"},
		[]string{"switch", "-q", "main"},
	)
	if err := os.WriteFile(filepath.Join(f.root, "clash.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.reloadCommits(t)
	f.shell.commitsMode = true
	f.tester.Frame()
	for i := 0; i < 2; i++ {
		if err := f.tester.Click("adds clash"); err != nil {
			t.Fatal(err)
		}
	}
	f.drain(t)
	f.tester.Frame()
	n := f.shell.gitNotice
	if !n.open || n.title != "Can't Switch Yet" || len(n.paths) != 1 || n.paths[0] != "clash.txt" {
		t.Fatalf("notice = %+v, want the Can't Switch dialog naming clash.txt", n)
	}
	if got := gitOut(t, f.root, "branch", "--show-current"); got != "main" {
		t.Fatalf("a refused switch must leave the branch alone, on %q", got)
	}
	if n.retry == nil || n.retryLabel != "Stash and Reapply" {
		t.Fatalf("the notice should offer Stash and Reapply: %q", n.retryLabel)
	}
}

// "Stash and Reapply" on the refusal: the changes go into a stash, the switch
// happens, and they come back when they fit.
func TestStashAndReapplyGetsPastABlockedDoubleClick(t *testing.T) {
	f := newGraphFixture(t)
	body := strings.Repeat("line\n", 10)
	gitScript(t, f.root, []string{"switch", "-q", "feature/login"})
	if err := os.WriteFile(filepath.Join(f.root, "doc.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitScript(t, f.root,
		[]string{"add", "doc.txt"},
		[]string{"-c", "user.email=a@b.c", "-c", "user.name=t", "commit", "-q", "-m", "doc on login"},
		[]string{"switch", "-q", "main"},
		[]string{"checkout", "-q", "feature/login", "--", "doc.txt"},
		[]string{"-c", "user.email=a@b.c", "-c", "user.name=t", "commit", "-q", "-m", "doc on main"},
		[]string{"switch", "-q", "feature/login"},
		[]string{"-c", "user.email=a@b.c", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "login top"},
	)
	// feature/login rewrites the first line; main edits the last one locally.
	if err := os.WriteFile(filepath.Join(f.root, "doc.txt"), []byte("login first\n"+strings.Repeat("line\n", 9)), 0o644); err != nil {
		t.Fatal(err)
	}
	gitScript(t, f.root,
		[]string{"-c", "user.email=a@b.c", "-c", "user.name=t", "commit", "-qam", "login edits top"},
		[]string{"switch", "-q", "main"},
	)
	if err := os.WriteFile(filepath.Join(f.root, "doc.txt"), []byte(strings.Repeat("line\n", 9)+"local last\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.reloadCommits(t)
	f.shell.commitsMode = true
	f.tester.Frame()
	for i := 0; i < 2; i++ {
		if err := f.tester.Click("login edits top"); err != nil {
			t.Fatal(err)
		}
	}
	f.drain(t)
	f.tester.Frame()
	n := f.shell.gitNotice
	if !n.open || n.retry == nil {
		t.Fatalf("the refusal should offer Stash and Reapply: %+v", n)
	}
	if err := f.tester.Click("Stash and Reapply"); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	if got := gitOut(t, f.root, "branch", "--show-current"); got != "feature/login" {
		t.Fatalf("current branch = %q, want feature/login", got)
	}
	data, _ := os.ReadFile(filepath.Join(f.root, "doc.txt"))
	if !strings.HasPrefix(string(data), "login first\n") || !strings.HasSuffix(string(data), "local last\n") {
		t.Fatalf("the local edit must come back on top of the branch:\n%s", data)
	}
	if got := gitOut(t, f.root, "stash", "list"); got != "" {
		t.Fatalf("a clean reapply leaves no stash: %q", got)
	}
}
