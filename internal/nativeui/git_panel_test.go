package nativeui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

/**
 * [INPUT]: headless Git sidebar/center with seeded refs, commits and a commit review
 * [OUTPUT]: the 2026-10-30 Git panel contract: select-then-act refs, search toggles, adaptive graph, copyable commit message, one-line composer
 * [POS]: Git panel optimisation (designs/git-panel Option A) regression
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// A click on a branch row only selects it (and says what a double click does);
// the switch — confirmed first when the worktree is dirty — waits for the
// second click.
func TestGitRefSingleClickSelectsDoubleClickSwitches(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	if err := tester.Click("develop"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if shell.confirmOpen || shell.git.branchBusy {
		t.Fatal("one click on a branch must not switch it")
	}
	if !strings.HasPrefix(shell.gitRefsSelected, "git-ref:develop") {
		t.Fatalf("the clicked branch is not selected: %q", shell.gitRefsSelected)
	}
	if !shell.gitRefsHinted {
		t.Fatal("the first select-only click did not say what a double click does")
	}

	if err := tester.Click("develop"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("develop"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !shell.confirmOpen || shell.confirmKind != "git-switch-branch" || shell.confirmTarget != "develop" {
		t.Fatalf("a double click did not ask to switch: open=%v kind=%q target=%q",
			shell.confirmOpen, shell.confirmKind, shell.confirmTarget)
	}
}

// Folders change nothing in the repository, so one click still folds them.
func TestGitRefFolderStillFoldsOnOneClick(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	if !tester.HasText("login") {
		t.Fatalf("control: the folder starts open; texts=%q", tester.Texts())
	}
	if err := tester.Click("feature"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if tester.HasText("login") {
		t.Fatal("one click on a folder did not fold it")
	}
	if shell.confirmOpen {
		t.Fatal("folding a folder must not ask anything")
	}
}

// The search is a button: closed, no field draws; opened, the field takes the
// keys, and closing it (the button, or Esc) drops the query.
func TestGitRefsSearchOpensFromItsButtonAndClosesClean(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	if tester.HasText("Filter Git references") {
		t.Fatal("the filter field must stay hidden until its button is pressed")
	}
	if err := tester.Click("Search references"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !shell.gitRefsSearchOpen || !tester.HasText("Filter Git references") {
		t.Fatalf("the button did not open the field; texts=%q", tester.Texts())
	}
	tester.Type("dev")
	tester.Frame()
	if shell.gitRefsFilter != "dev" {
		t.Fatalf("query = %q, want what was typed", shell.gitRefsFilter)
	}
	// The search field's own Esc first empties what was typed; the next Esc
	// (nothing left to clear) closes the search.
	pressKey(tester, 0, ui.KeyEscape)
	if shell.gitRefsFilter != "" {
		t.Fatalf("Esc kept the query %q", shell.gitRefsFilter)
	}
	pressKey(tester, 0, ui.KeyEscape)
	tester.Frame()
	if shell.gitRefsSearchOpen || shell.gitRefsFilter != "" {
		t.Fatalf("Esc left the search open=%v query=%q", shell.gitRefsSearchOpen, shell.gitRefsFilter)
	}
}

// The graph column is as wide as the lanes the history uses, so the dot sits
// by its subject instead of in a fixed gutter, and a filtered list gets one lane.
func TestGitGraphWidthFollowsLanes(t *testing.T) {
	one := []gitGraphRow{{Lane: 0, Before: []string{"a"}, After: []string{"a"}}}
	three := []gitGraphRow{{Lane: 2, Before: []string{"a", "b", "c"}, After: []string{"a", "b", "c"}}}
	if got, want := gitGraphWidth(one), gitGraphWidthFor(1); got != want {
		t.Fatalf("a linear history is %v wide, want %v", got, want)
	}
	if gitGraphWidth(three) <= gitGraphWidth(one) {
		t.Fatalf("three lanes (%v) are not wider than one (%v)", gitGraphWidth(three), gitGraphWidth(one))
	}
	if gitGraphWidth(one) > 24 {
		t.Fatalf("the single-lane graph is %v wide: the dot would sit far from the subject", gitGraphWidth(one))
	}
	many := []gitGraphRow{{Lane: 20}}
	if got, want := gitGraphWidth(many), gitGraphWidthFor(gitGraphMaxLanes); got != want {
		t.Fatalf("lanes are capped at %d: width %v, want %v", gitGraphMaxLanes, got, want)
	}
}

// The commit under review shows its whole message (selectable, so it can be
// copied by hand) and Copy Message copies subject and body together.
func TestGitCommitCardCopiesTheFullMessage(t *testing.T) {
	got := captureClipboard(t)
	s := gitSurfaceTestShell(t, t.TempDir())
	s.git.rootOf[s.git.root] = s.git.root
	s.git.refreshing = true
	s.showSurface(WorkspaceSurfaceDiff)
	cm := gitworkbench.CommitInfo{
		Hash: "0123456789abcdef0123456789abcdef01234567", Short: "0123456",
		Subject: "Fix the parser", Author: "Tester", Time: time.Now(),
	}
	s.git.commits = []gitworkbench.CommitInfo{cm}
	s.commitsMode = true
	s.git.source = gdSourceCommit
	s.git.commitHash = cm.Hash
	s.git.commitMeta = &cm
	s.git.commitBody = "Fix the parser\n\nIt dropped the last token."
	snap := *s.git.snapshot
	s.git.commitSnap = &snap
	s.git.gdFilesFor = nil
	s.rebuildDiffRows()

	tester := ui.NewTester(s.View, 1280, 800)
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"Fix the parser", "It dropped the last token.", "Copy Message", "Cherry-pick…", "Revert…"} {
		if !tester.HasText(want) {
			t.Fatalf("the commit card lacks %q; texts=%q", want, tester.Texts())
		}
	}
	if err := tester.Click("Copy Message"); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 || (*got)[0] != "Fix the parser\n\nIt dropped the last token." {
		t.Fatalf("Copy Message copied %q", *got)
	}
	if err := tester.Click("Copy hash"); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 || (*got)[1] != cm.Hash {
		t.Fatalf("Copy hash copied %q", *got)
	}
}

// Idle, the composer is a single line (subject + Commit); focusing the subject
// or holding a draft opens the description and options.
func TestGitComposerIsOneLineUntilUsed(t *testing.T) {
	root := t.TempDir()
	s := gitSurfaceTestShell(t, root)
	s.git.rootOf[root] = root
	s.router.Push(routeWorkspace)
	s.showSurface(WorkspaceSurfaceDiff)
	s.rebuildDiffRows()
	tester := ui.NewTester(s.View, 1280, 800)
	tester.Frame()
	tester.Frame()
	if tester.HasText("Commit description draft") || tester.HasText("Amend") {
		t.Fatalf("an idle composer shows its second row; texts=%q", tester.Texts())
	}
	if err := tester.Click("Commit subject draft"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	tester.Frame()
	if !tester.HasText("Commit description draft") || !tester.HasText("Amend") {
		t.Fatalf("focusing the subject did not open the composer; texts=%q", tester.Texts())
	}
}

// In a narrow sidebar the project chip keeps its icon and gives up its name,
// so the branch, search and refresh buttons always fit one row.
func TestSidebarHeaderCollapsesProjectNameWhenNarrow(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	shell.sidebarW = sidebarHeaderNarrow + 40
	tester.Frame()
	if _, ok := tester.Find("demo"); !ok {
		t.Fatalf("a wide sidebar must show the project name; texts=%q", tester.Texts())
	}
	shell.sidebarW = sidebarHeaderNarrow
	tester.Frame()
	tester.Frame()
	if _, ok := tester.Find("demo"); ok {
		t.Fatal("a narrow sidebar must hide the project name")
	}
	for _, label := range []string{"Switch project", "Switch branch", "Search references", "Refresh repository"} {
		if _, ok := tester.Find(label); !ok {
			t.Fatalf("narrow header lost %q; texts=%q", label, tester.Texts())
		}
	}
}

// A section's count ends where a row's trailing mark (the current branch's
// check) ends — one right column — and the current branch wears the
// selection face instead of resting on the check alone.
func TestSidebarCountsShareTheRowsRightEdge(t *testing.T) {
	_, tester, _ := treeTestShell(t)
	header, ok := tester.Find("Toggle Branches")
	if !ok {
		t.Fatalf("no Branches header; texts=%q", tester.Texts())
	}
	count, ok := tester.Find("4") // the four seeded branches
	if !ok {
		t.Fatalf("no Branches count; texts=%q", tester.Texts())
	}
	row, ok := tester.Find("Branch main")
	if !ok {
		t.Fatalf("no current-branch row; texts=%q", tester.Texts())
	}
	if !near(header.X+header.W, row.X+row.W) {
		t.Fatalf("header and row end at %v and %v", header.X+header.W, row.X+row.W)
	}
	if got, want := count.X+count.W, header.X+header.W-treeRowPad; !near(got, want) {
		t.Fatalf("the count ends at %v, want the row's content edge %v", got, want)
	}
	// The checked-out row is filled; an ordinary branch is not.
	face := func(r ui.Rect) [4]uint8 { return pixelAt(tester, int(r.X+r.W-2), int(r.Y+1)) }
	other, _ := tester.Find("Branch develop")
	if rgbDiff(face(row), face(other)) < 4 {
		t.Fatalf("the current branch (%v) is not told apart from another (%v) by its face", face(row), face(other))
	}
}
