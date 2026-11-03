package nativeui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thewei/solco/internal/gitworkbench"
)

/**
 * [INPUT]: 真实临时仓库（main: initial,m2；feature/login: f1,f2；feature/signup: s1）+ 一个 stash 与两个 worktree
 * [OUTPUT]: 侧栏 stash / worktree 单击像分支一样定位到 Commits 里对应的提交；提交行 chip 按种类（分支/远程/Tag/stash/worktree）带图示且来自真实装饰；折叠状态跨重启保留
 * [POS]: git_commit_chips.go 与侧栏联动回归
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func (f *graphFixture) reloadRefs(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	var err error
	if f.shell.git.stashes, err = f.shell.git.runner.StashList(ctx, f.root); err != nil {
		t.Fatal(err)
	}
	if f.shell.git.worktrees, err = f.shell.git.runner.ListWorktrees(ctx, f.root); err != nil {
		t.Fatal(err)
	}
	f.tester.Frame()
	f.tester.Frame()
}

func TestClickingAStashLocatesTheCommitItWasMadeOn(t *testing.T) {
	f := newGraphFixture(t)
	if err := os.WriteFile(filepath.Join(f.root, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitScript(t, f.root, []string{"-c", "user.email=a@b.c", "-c", "user.name=t", "stash", "push", "-q", "-u", "-m", "WIP parser"})
	f.reloadRefs(t)
	if len(f.shell.git.stashes) != 1 || f.shell.git.stashes[0].Base != f.tip(t, "main") {
		t.Fatalf("a stash is attached to the commit it was made on: %+v", f.shell.git.stashes)
	}
	if err := f.tester.Click(stashTitle(f.shell.git.stashes[0].Subject)); err != nil {
		t.Fatal(err)
	}
	want := f.tip(t, "main")
	stash := f.shell.git.stashes[0]
	if !f.shell.commitsMode || f.shell.git.commitHash != stash.Hash {
		t.Fatalf("a stash is reviewed as itself: commits=%v reviewing=%q want the stash commit %q",
			f.shell.commitsMode, f.shell.git.commitHash, stash.Hash)
	}
	if f.shell.git.flashHash != want {
		t.Fatalf("the base commit's row is not flashing: %q want %q", f.shell.git.flashHash, want)
	}
	if !f.tester.HasText("stash@{0}") {
		t.Fatal("the base commit should wear a stash chip")
	}
	f.drain(t)
	if snap := f.shell.git.commitSnap; snap == nil || len(snap.Files) != 1 || snap.Files[0].Path != "wip.txt" {
		t.Fatalf("the reviewer should show the stash's content (its untracked file): %+v", snap)
	}
}

// A chip is a way back: it opens its commit and brings its sidebar row into
// view, opening the section it was folded in.
func TestClickingAChipRevealsItsSidebarRow(t *testing.T) {
	f := newGraphFixture(t)
	f.shell.gitTreeClosed[f.shell.gitFoldKey("section/Branches")] = true
	f.shell.commitsMode = true
	f.tester.Frame()
	f.tester.Frame()
	if err := f.tester.Click("feature/login"); err != nil {
		t.Fatal(err)
	}
	want := f.tip(t, "feature/login")
	key := gitRefRowKey("login", "feature/login")
	if f.shell.git.commitHash != want {
		t.Fatalf("the chip should open its commit: %q want %q", f.shell.git.commitHash, want)
	}
	if f.shell.gitTreeClosed[f.shell.gitFoldKey("section/Branches")] || f.shell.gitTreeClosed[f.shell.gitFoldKey("local/feature")] {
		t.Fatal("the section and folder above the row must open")
	}
	f.tester.Frame()
	if f.shell.gitRefsSelected != key || f.shell.gitRefsReveal != "" {
		t.Fatalf("the sidebar row is selected and scrolled to once: selected=%q reveal=%q", f.shell.gitRefsSelected, f.shell.gitRefsReveal)
	}
	if !f.tester.HasText("login") {
		t.Fatal("the branch row should be on screen")
	}
}

func TestClickingADetachedWorktreeLocatesItsHead(t *testing.T) {
	f := newGraphFixture(t)
	dir := filepath.Join(t.TempDir(), "wt-detached")
	gitScript(t, f.root, []string{"worktree", "add", "-q", "--detach", dir, "feature/login~1"})
	f.reloadRefs(t)
	if err := f.tester.Click("(detached)"); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	want := f.tip(t, "feature/login~1")
	if !f.shell.commitsMode || f.shell.git.commitHash != want {
		t.Fatalf("worktree click did not locate its HEAD: commits=%v hash=%q want %q", f.shell.commitsMode, f.shell.git.commitHash, want)
	}
	f.tester.Frame()
	if !f.tester.HasText("wt-detached") {
		t.Fatal("a detached worktree is a chip on its HEAD commit")
	}
}

func TestCommitChipsCarryTheirKindsGlyph(t *testing.T) {
	f := newGraphFixture(t)
	dir := filepath.Join(t.TempDir(), "wt-signup")
	gitScript(t, f.root,
		[]string{"worktree", "add", "-q", dir, "feature/signup"},
		[]string{"-c", "user.email=a@b.c", "-c", "user.name=t", "tag", "-a", "-m", "release", "v1", "feature/login"},
	)
	commits, err := f.shell.git.runner.ListCommitGraph(context.Background(), f.root, gitworkbench.GraphQuery{})
	if err != nil {
		t.Fatal(err)
	}
	f.shell.git.commits = commits
	f.reloadRefs(t)
	x := f.shell.newGitChipIndex()
	byName := map[string]gitChip{}
	for _, cm := range commits {
		for _, chip := range x.chips(cm, nil) {
			byName[chip.text] = chip
		}
	}
	if byName["main"].glyph != iconBranch || !byName["main"].branch {
		t.Fatalf("a branch wears the branch glyph: %+v", byName["main"])
	}
	if byName["feature/signup"].glyph != iconWorktree {
		t.Fatalf("a branch checked out in another worktree wears the worktree glyph: %+v", byName["feature/signup"])
	}
	if byName["feature/login"].glyph != iconBranch {
		t.Fatalf("a branch nobody checked out stays a branch: %+v", byName["feature/login"])
	}
	if byName["v1"].glyph != iconTag {
		t.Fatalf("a tag wears the tag glyph: %+v", byName["v1"])
	}
}

func TestFoldsSurviveARestart(t *testing.T) {
	f := newGraphFixture(t)
	f.shell.setFolded("abc123", true)
	f.shell.setFolded("def456", true)
	f.shell.setFolded("abc123", false)
	got := f.shell.graphFoldedSet()
	if len(got) != 1 || !got["def456"] {
		t.Fatalf("folded set = %v", got)
	}
	f.shell.setFolded("def456", false)
	if len(f.shell.graphFoldedSet()) != 0 || len(f.shell.settings.Workbench.GraphFolded) != 0 {
		t.Fatal("the last unfold leaves nothing in settings")
	}
}
