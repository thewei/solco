package nativeui

import (
	"context"
	"slices"
	"testing"

	"github.com/thewei/solco/internal/gitworkbench"
)

/**
 * [INPUT]: 手写的提交窗口（含合并、第二条线、被别处依赖的提交）+ 真实临时仓库的合并
 * [OUTPUT]: 折叠只隐藏合并带进来的提交且不吞别线依赖、折叠后图谱按剩余提交连线、点图标折叠/展开、定位被折叠的提交时自动展开
 * [POS]: git_graph_fold.go 回归
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func foldCommits(spec ...[]string) []gitworkbench.CommitInfo {
	var out []gitworkbench.CommitInfo
	for _, s := range spec {
		out = append(out, gitworkbench.CommitInfo{Hash: s[0], Parents: s[1:]})
	}
	return out
}

func hashesOf(commits []gitworkbench.CommitInfo) []string {
	var out []string
	for _, cm := range commits {
		out = append(out, cm.Hash)
	}
	return out
}

// M merges f2 (f2 → f1 → base) into main (m1 → base); folding M hides f2, f1.
func TestFoldHidesOnlyWhatTheMergeBroughtIn(t *testing.T) {
	commits := foldCommits(
		[]string{"M", "m1", "f2"},
		[]string{"f2", "f1"},
		[]string{"m1", "base"},
		[]string{"f1", "base"},
		[]string{"base"},
	)
	fold := newGitFold(commits)
	if got := fold.set("M"); len(got) != 2 || !got["f1"] || !got["f2"] {
		t.Fatalf("the merge brought in f1,f2, got %v", got)
	}
	if fold.foldable("m1") || fold.foldable("base") {
		t.Fatal("only a merge folds")
	}

	shown, counts := fold.apply(map[string]bool{"M": true})
	if got := hashesOf(shown); !slices.Equal(got, []string{"M", "m1", "base"}) {
		t.Fatalf("folded list = %v", got)
	}
	if counts["M"] != 2 {
		t.Fatalf("count = %v", counts)
	}
	if !slices.Equal(shown[0].Parents, []string{"m1"}) {
		t.Fatalf("a folded merge follows only its first parent, got %v", shown[0].Parents)
	}
	if len(commits[0].Parents) != 2 {
		t.Fatal("folding must not rewrite the loaded window")
	}

	graph := gitHistoryGraph(shown)
	if graph[0].Lanes != 1 || len(graph[0].Diverge) != 0 {
		t.Fatalf("a folded merge draws one lane, got %+v", graph[0])
	}
}

// A commit another visible line still builds on is not swallowed: here f1 is
// the parent of the visible branch tip x, so only f2 can fold.
func TestFoldKeepsCommitsOtherLinesBuildOn(t *testing.T) {
	commits := foldCommits(
		[]string{"x", "f1"},
		[]string{"M", "m1", "f2"},
		[]string{"f2", "f1"},
		[]string{"m1", "base"},
		[]string{"f1", "base"},
		[]string{"base"},
	)
	got := newGitFold(commits).set("M")
	if len(got) != 1 || !got["f2"] {
		t.Fatalf("only f2 is the merge's alone, got %v", got)
	}
}

// A merge whose branch was already merged earlier has nothing of its own.
func TestFoldNothingWhenTheBranchIsAlreadyInMain(t *testing.T) {
	commits := foldCommits(
		[]string{"M", "m1", "f1"},
		[]string{"m1", "f1"},
		[]string{"f1", "base"},
		[]string{"base"},
	)
	if newGitFold(commits).foldable("M") {
		t.Fatal("f1 is reachable from the first parent; nothing to fold")
	}
}

func TestFoldNestedMergeFoldsWithItsOuterMerge(t *testing.T) {
	commits := foldCommits(
		[]string{"M", "m1", "g2"},
		[]string{"m1", "base"},
		[]string{"g2", "g1", "h1"},
		[]string{"h1", "g0"},
		[]string{"g1", "g0"},
		[]string{"g0", "base"},
		[]string{"base"},
	)
	shown, counts := newGitFold(commits).apply(map[string]bool{"M": true, "g2": true})
	if got := hashesOf(shown); !slices.Equal(got, []string{"M", "m1", "base"}) {
		t.Fatalf("shown = %v", got)
	}
	if counts["M"] != 4 {
		t.Fatalf("the outer merge hides g2,g1,h1,g0: %v", counts)
	}
}

// Real repository: fold through the chevron, unfold through the chevron, and a
// located commit inside a fold opens it.
func TestFoldChevronAndLocateOnARealMerge(t *testing.T) {
	f := newGraphFixture(t)
	gitScript(t, f.root, []string{"-c", "user.email=a@b.c", "-c", "user.name=t", "merge", "-q", "--no-ff", "-m", "Merge login", "feature/login"})
	commits, err := f.shell.git.runner.ListCommitGraph(context.Background(), f.root, gitworkbench.GraphQuery{})
	if err != nil {
		t.Fatal(err)
	}
	f.shell.git.commits = commits
	f.shell.commitsMode = true
	f.tester.Frame()
	f.tester.Frame()

	if err := f.tester.Click("Fold Merged Commits"); err != nil {
		t.Fatal(err)
	}
	f.tester.Frame()
	f.tester.Frame()
	if !f.shell.graphFoldedSet()[f.tip(t, "main")] || !f.tester.HasText("Merge login") {
		t.Fatal("folding should record the merge and keep its row")
	}
	if !f.tester.HasText("+2") {
		t.Fatal("a folded merge says how many commits it hides")
	}

	if err := f.tester.Click("Unfold Merged Commits"); err != nil {
		t.Fatal(err)
	}
	f.tester.Frame()
	f.tester.Frame()
	if len(f.shell.graphFoldedSet()) != 0 || f.tester.HasText("+2") {
		t.Fatal("unfolding brings the branch back")
	}

	merge := f.tip(t, "main")
	f.shell.setFolded(merge, true)
	f.shell.revealCommit(f.tip(t, "feature/login"))
	f.drain(t)
	if len(f.shell.graphFoldedSet()) != 0 {
		t.Fatal("locating a folded commit should unfold the merge that hides it")
	}
}
