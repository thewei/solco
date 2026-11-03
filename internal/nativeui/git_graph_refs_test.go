package nativeui

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

/**
 * [INPUT]: 真实临时仓库（main: initial,m2；feature/login: f1,f2；feature/signup: s1）+ headless Shell
 * [OUTPUT]: 单击引用定位到提交（切到 Commits、选中、滚动并高亮、隐藏的引用只提示）、窗口自动扩大、眼睛隐藏/显示（叶子与目录、持久化、Show All、N hidden）、提交行 chip 取真实装饰
 * [POS]: git_graph_refs.go 回归
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type graphFixture struct {
	shell  *Shell
	tester *ui.Tester
	root   string
	queue  chan func()
}

func newGraphFixture(t *testing.T) *graphFixture {
	t.Helper()
	root := t.TempDir()
	commit := func(msg string) {
		gitScript(t, root,
			[]string{"-c", "user.email=a@b.c", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", msg})
	}
	gitScript(t, root, []string{"init", "-q", "-b", "main"})
	commit("initial")
	gitScript(t, root, []string{"switch", "-qc", "feature/login"})
	commit("f1")
	commit("f2")
	gitScript(t, root, []string{"switch", "-q", "main"}, []string{"switch", "-qc", "feature/signup"})
	commit("s1")
	gitScript(t, root, []string{"switch", "-q", "main"})
	commit("m2")

	shell := gitSurfaceTestShell(t, root)
	shell.git.rootOf[root] = root
	shell.git.refreshing = true
	openAllGitSections(shell)
	f := &graphFixture{shell: shell, root: root, queue: make(chan func(), 16)}
	shell.uiApplyOverride = func(fn func()) { f.queue <- fn }
	ctx := context.Background()
	branches, err := shell.git.runner.ListBranches(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	shell.git.branches = branches
	shell.git.worktrees = []gitworkbench.Worktree{{Path: root, Branch: "main"}}
	commits, err := shell.git.runner.ListCommitGraph(ctx, root, gitworkbench.GraphQuery{})
	if err != nil {
		t.Fatal(err)
	}
	shell.git.commits = commits
	if shell.router.Path() != routeWorkspace {
		shell.router.Push(routeWorkspace)
	}
	shell.showSurface(WorkspaceSurfaceDiff)
	shell.rebuildDiffRows()
	f.tester = ui.NewTester(shell.View, 1200, 800)
	f.tester.SetPreferences(ui.Preferences{ReduceMotion: true}) // ref rows land at once (treeRowMotion)
	f.tester.SetPreferences(ui.Preferences{ReduceMotion: true}) // ref rows land at once (treeRowMotion)
	f.tester.Frame()
	f.tester.Frame()
	return f
}

// drain applies queued UI-lane completions until none arrive for a moment.
func (f *graphFixture) drain(t *testing.T) {
	t.Helper()
	for {
		select {
		case fn := <-f.queue:
			fn()
		case <-time.After(1500 * time.Millisecond):
			return
		}
	}
}

func (f *graphFixture) subjects() string {
	var out []string
	for _, cm := range f.shell.git.commits {
		out = append(out, cm.Subject)
	}
	return strings.Join(out, ",")
}

func (f *graphFixture) tip(t *testing.T, ref string) string {
	t.Helper()
	return gitOut(t, f.root, "rev-parse", ref)
}

// A single click on a branch switches to Commits, selects its tip commit,
// scrolls the graph to it and flashes the row for a moment.
func TestClickingARefLocatesItsCommit(t *testing.T) {
	f := newGraphFixture(t)
	if err := f.tester.Click("develop"); err == nil {
		t.Fatal("control: no develop branch in this repo")
	}
	if err := f.tester.Click("signup"); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	want := f.tip(t, "feature/signup")
	if !f.shell.commitsMode || f.shell.git.commitHash != want {
		t.Fatalf("click did not reveal the tip: commits=%v hash=%q want %q", f.shell.commitsMode, f.shell.git.commitHash, want)
	}
	if f.shell.git.locateHash != want {
		t.Fatalf("the scroll is not queued for the list: %q", f.shell.git.locateHash)
	}
	f.tester.Frame()
	if f.shell.git.locateHash != "" || f.shell.git.flashHash != want {
		t.Fatalf("the list did not take the scroll and flash: locate=%q flash=%q", f.shell.git.locateHash, f.shell.git.flashHash)
	}
	if !f.tester.HasText("s1") {
		t.Fatalf("the located commit is not on screen; texts=%q", f.tester.Texts())
	}
}

// A hidden ref has no place in the graph: the click says so and goes nowhere.
func TestClickingAHiddenRefOnlySaysSo(t *testing.T) {
	f := newGraphFixture(t)
	f.shell.setGraphHidden([]string{"refs/heads/feature/signup"}, true)
	f.drain(t)
	f.shell.pendingToast = ""
	f.shell.locateRef("refs/heads/feature/signup")
	if f.shell.commitsMode || !strings.Contains(f.shell.pendingToast, "eye") {
		t.Fatalf("a hidden ref must not locate; commitsMode=%v toast=%q", f.shell.commitsMode, f.shell.pendingToast)
	}
}

// A commit past the loaded window grows it to just reach the commit — however
// deep — and one the graph never shows is reported, not chased.
func TestRevealGrowsTheWindowToTheCommit(t *testing.T) {
	f := newGraphFixture(t)
	gitScript(t, f.root, []string{"-c", "user.email=a@b.c", "-c", "user.name=t", "tag", "-a", "-m", "old", "old", "main"})
	var more [][]string
	for i := 0; i < 260; i++ {
		more = append(more, []string{"-c", "user.email=a@b.c", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "later"})
	}
	gitScript(t, f.root, more...)
	f.shell.loadCommitGraph(nil)
	f.drain(t)
	want := f.tip(t, "old^{commit}")
	if len(f.shell.git.commits) != gitGraphDefaultWindow {
		t.Fatalf("control: the default window holds %d commits, has %d", gitGraphDefaultWindow, len(f.shell.git.commits))
	}
	if slicesHasHash(f.shell.git.commits, want) {
		t.Fatal("control: the tagged commit must start outside the window")
	}

	f.shell.locateRef("refs/tags/old")
	f.drain(t)
	if !slicesHasHash(f.shell.git.commits, want) || f.shell.git.commitHash != want {
		t.Fatalf("the tag's commit was not reached: window=%d reviewing=%q want %q",
			len(f.shell.git.commits), f.shell.git.commitHash, want)
	}
	if got := f.shell.git.graphLimit; got < 261 || got > 261+gitGraphMargin+10 {
		t.Fatalf("the window should grow to just reach the commit, got %d", got)
	}

	f.shell.pendingToast = ""
	f.shell.revealCommit(strings.Repeat("a", 40))
	f.drain(t)
	if !strings.Contains(f.shell.pendingToast, "not in the graph") {
		t.Fatalf("an unreachable commit must say so; toast %q", f.shell.pendingToast)
	}
}

// A tag deeper than the old 2000-commit ceiling is still one click away.
func TestRevealReachesATagBeyondTwoThousandCommits(t *testing.T) {
	f := newGraphFixture(t)
	gitScript(t, f.root, []string{"-c", "user.email=a@b.c", "-c", "user.name=t", "tag", "-a", "-m", "old", "old", "main"})
	var stream strings.Builder
	for i := 0; i < 2300; i++ {
		stream.WriteString("commit refs/heads/main\ncommitter t <a@b.c> " + itoa(1800000000+i) + " +0000\ndata 6\nlater\n")
		if i == 0 {
			stream.WriteString("from refs/heads/main^0\n")
		}
		stream.WriteString("\n")
	}
	cmd := exec.Command("git", "fast-import", "--force", "--quiet")
	cmd.Dir, cmd.Stdin = f.root, strings.NewReader(stream.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fast-import: %v\n%s", err, out)
	}
	f.shell.loadCommitGraph(nil)
	f.drain(t)
	want := f.tip(t, "old^{commit}")

	f.shell.locateRef("refs/tags/old")
	f.drain(t)
	if f.shell.git.commitHash != want || !slicesHasHash(f.shell.git.commits, want) {
		t.Fatalf("a tag past 2000 commits was not reached: toast=%q window=%d", f.shell.pendingToast, len(f.shell.git.commits))
	}
}

func slicesHasHash(commits []gitworkbench.CommitInfo, hash string) bool {
	for _, cm := range commits {
		if cm.Hash == hash {
			return true
		}
	}
	return false
}

// The eye hides a branch (and, on a folder, every branch in it), remembers it
// for the repository, and Show All brings everything back.
func TestEyeHidesBranchesPersistsAndShowAllRestores(t *testing.T) {
	f := newGraphFixture(t)
	if !strings.Contains(f.subjects(), "f2") || !strings.Contains(f.subjects(), "s1") {
		t.Fatalf("control: every branch is in the graph: %s", f.subjects())
	}
	// The folder row hides its whole group.
	rect, ok := f.tester.Find("feature")
	if !ok {
		t.Fatalf("no feature folder; texts=%q", f.tester.Texts())
	}
	f.tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	f.tester.Frame()
	if err := f.tester.Click("Hide from Graph"); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	if got := f.subjects(); strings.Contains(got, "f1") || strings.Contains(got, "s1") || !strings.Contains(got, "m2") {
		t.Fatalf("hiding the folder must drop both branches only: %s", got)
	}
	hidden := f.shell.settings.Workbench.GraphHidden[f.root]
	if len(hidden) != 2 || hidden[0] != "refs/heads/feature/login" || hidden[1] != "refs/heads/feature/signup" {
		t.Fatalf("hidden refs not remembered per repository: %v", hidden)
	}
	f.shell.commitsMode = true
	f.tester.Frame()
	f.tester.Frame()
	if !f.tester.HasText("Show in Graph") {
		t.Fatalf("a hidden row must keep its eye on screen; texts=%q", f.tester.Texts())
	}
	if !f.tester.HasText("2 hidden · Show All") {
		t.Fatalf("the graph head must count what is hidden; texts=%q", f.tester.Texts())
	}

	if err := f.tester.Click("2 hidden · Show All"); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	if got := f.subjects(); !strings.Contains(got, "f1") || !strings.Contains(got, "s1") {
		t.Fatalf("Show All must bring the branches back: %s", got)
	}
	if len(f.shell.settings.Workbench.GraphHidden) != 0 {
		t.Fatalf("Show All must clear the remembered list: %v", f.shell.settings.Workbench.GraphHidden)
	}
}

// The checked-out branch has no eye: HEAD is always in the graph.
func TestCurrentBranchHasNoEye(t *testing.T) {
	f := newGraphFixture(t)
	for _, n := range gitRefTree([]string{"main"}, "") {
		if refs := f.shell.graphHideable("local", n); len(refs) != 0 {
			t.Fatalf("the current branch must not be hideable: %v", refs)
		}
	}
	if refs := f.shell.graphHideable("tag", &gitRefNode{name: "v1", full: "v1", leaf: true}); len(refs) != 0 {
		t.Fatalf("tags have no eye: %v", refs)
	}
}

// Chips come from the refs Git reports at the commit, and a hidden ref is not
// one.
func TestCommitChipsAreTheRealDecorations(t *testing.T) {
	f := newGraphFixture(t)
	f.shell.commitsMode = true
	f.tester.Frame()
	f.tester.Frame()
	for _, want := range []string{"main", "feature/login", "feature/signup"} {
		if !f.tester.HasText(want) {
			t.Fatalf("missing chip %q; texts=%q", want, f.tester.Texts())
		}
	}
}
