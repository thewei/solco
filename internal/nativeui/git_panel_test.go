package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: headless right panel on Changes (seeded snapshot), the Sessions sidebar head
 * [OUTPUT]: the panel's contract after the Git page retired: a chosen file row wears the neutral tint, the composer is one line until used, the shared project head collapses by the width of the container it sits in
 * [POS]: 2026-10-30 Git panel optimisation (designs/git-panel Option A) regression, kept for the pieces the right panel still wears (2026-11-08 G5)
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// A chosen file row wears the neutral selection tint, focused or not — never
// the theme's blue accent MyGo paints on a chosen list row (user report
// 2026-11-03: it read far brighter than the Commits list's own selection).
func TestChosenFileRowIsNeutralNotAccent(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.git.rootOf[s.git.root] = s.git.root
	s.git.refreshing = true
	showChangesPanel(s)
	s.rebuildDiffRows()
	tester := ui.NewTester(s.View, 1280, 800)
	tester.Frame()
	tester.Frame()
	name, ok := tester.Find("README.md")
	if !ok {
		t.Fatalf("no file row; texts=%q", tester.Texts())
	}
	if err := tester.Click("README.md"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	tester.Frame()
	px := pixelAt(tester, int(name.X+name.W+60), int(name.Y+name.H/2))
	if int(px[2])-int(px[0]) > 40 {
		t.Fatalf("a chosen file row is blue (%v): the accent leaked through the list's own selection face", px)
	}
}

// Idle, the composer is a single line (subject + Commit); focusing the subject
// or holding a draft opens the description and options.
func TestGitComposerIsOneLineUntilUsed(t *testing.T) {
	root := t.TempDir()
	s := gitSurfaceTestShell(t, root)
	s.git.rootOf[root] = root
	s.router.Push(routeWorkspace)
	showChangesPanel(s)
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
// so the branch and refresh buttons always fit one row.
func TestSidebarHeaderCollapsesProjectNameWhenNarrow(t *testing.T) {
	root := t.TempDir()
	shell := newMultiAgentHistoryShell(t)
	shell.activeInstance = "inst-1"
	shell.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "demo", CWD: root}},
		Tabs:     []herdr.Tab{{ID: "t1", Label: "main", ProjectID: "w1"}},
		Panes:    []herdr.Pane{{ID: "p1", TerminalID: "term-1", Label: "editor", TabID: "t1", CWD: root}},
	}
	shell.reconcileLocalSelection(shell.projection)
	shell.router.Replace(routeHistory) // the Sessions sidebar wears the shared head
	tester := ui.NewTester(shell.View, 1200, 800)
	shell.sidebarW = sidebarHeaderNarrow + 40
	settleFrames(tester, 2)
	if _, ok := tester.Find("demo"); !ok {
		t.Fatalf("a wide sidebar must show the project name; texts=%q", tester.Texts())
	}
	shell.sidebarW = sidebarHeaderNarrow
	settleFrames(tester, 2)
	if _, ok := tester.Find("demo"); ok {
		t.Fatal("a narrow sidebar must hide the project name")
	}
	for _, label := range []string{"Switch project", "Refresh sessions"} {
		if _, ok := tester.Find(label); !ok {
			t.Fatalf("narrow header lost %q; texts=%q", label, tester.Texts())
		}
	}
}

// The same head in the right panel judges the card it sits in, not the
// sidebar: a narrow sidebar must not strip the project name from a 400 px card.
func TestPanelHeadJudgesItsOwnCardNotTheSidebar(t *testing.T) {
	shell, tester, _ := treeTestShell(t)
	shell.sidebarW = sidebarHeaderNarrow
	settleFrames(tester, 2)
	if _, ok := tester.Find("demo"); !ok {
		t.Fatalf("the panel's head lost the project name to a narrow sidebar; texts=%q", tester.Texts())
	}
}
