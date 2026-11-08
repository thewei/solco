package nativeui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
	"github.com/thewei/solco/internal/herdr"
)

// gitSurfaceTestShell builds a shell bound to a repo context with a seeded
// snapshot (headless; no live Herdr needed for surface behavior).
func gitSurfaceTestShell(t testing.TB, root string) *Shell {
	t.Helper()
	shell := NewShell()
	shell.activeInstance = "inst-1"
	shell.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "demo", CWD: root}},
		Tabs:     []herdr.Tab{{ID: "t1", Label: "main", ProjectID: "w1"}},
		Panes:    []herdr.Pane{{ID: "p1", TerminalID: "term-1", Label: "editor", TabID: "t1", CWD: root}},
	}
	shell.reconcileLocalSelection(shell.projection)
	shell.git.root = root
	shell.git.snapshot = &gitworkbench.ChangesSnapshot{
		Root:      root,
		Branch:    "main",
		Signature: "sig-1",
		Files: []gitworkbench.ChangeFile{
			{Path: "src/main.go", Status: gitworkbench.StatusModified, Additions: 12, Deletions: 4,
				Hunks: []gitworkbench.Hunk{{
					OldStart: 1, OldLines: 4, NewStart: 1, NewLines: 5,
					Lines: []gitworkbench.DiffLine{
						{Kind: gitworkbench.KindContext, OldLine: 1, NewLine: 1, Text: "package main"},
						{Kind: gitworkbench.KindDelete, OldLine: 2, Text: "fmt.Println(\"old\")"},
						{Kind: gitworkbench.KindAdd, NewLine: 2, Text: "fmt.Println(\"new\")"},
					},
				}}},
			{Path: "README.md", Status: gitworkbench.StatusAdded, Additions: 3},
		},
	}
	// What the selection sync does in production: the Tab's center starts on
	// the Terminal.
	shell.surface.resetToTerminal(shell.workspaceContext())
	return shell
}

// TestPanelAndPaneSwitchRetainTerminalState pins GWB-040/041 for the panel
// era: opening the right panel on Changes is no surface change, so the
// terminal map stays intact, the Terminal keeps showing, and a pane-only
// switch keeps all of it.
func TestPanelAndPaneSwitchRetainTerminalState(t *testing.T) {
	root := t.TempDir()
	shell := gitSurfaceTestShell(t, root)
	shell.terminals["term-1"] = &terminalSurface{key: "term-1", paneID: "p1"}
	// A second pane in the SAME tab, local to this test: the shared fixture
	// stays single-Pane for every tree test.
	shell.projection.Panes = append(shell.projection.Panes,
		herdr.Pane{ID: "p2", ProjectID: "w1", TerminalID: "term-2", Label: "builder", TabID: "t1", CWD: root})

	showChangesList(shell)
	if shell.surface.current() != WorkspaceSurfaceTerminal || !shell.sidePanelShown() {
		t.Fatalf("surface = %v, panel shown = %v; want the Terminal with the panel beside it", shell.surface.current(), shell.sidePanelShown())
	}
	if len(shell.terminals) != 1 {
		t.Fatalf("terminals must be retained while the panel is open, got %d", len(shell.terminals))
	}

	// A pane-only switch keeps the surface and the panel: the context key is
	// instance/project/tab/repo — no pane in it. The Terminal landing belongs
	// to project/tab-level navigation, pinned by TestSidebarWayHomeDoesNotRestoreChat.
	shell.selectPane("p2")
	if shell.surface.current() != WorkspaceSurfaceTerminal || !shell.sidePanel.open {
		t.Fatalf("pane-only selection: surface = %v, panel open = %v, want both kept", shell.surface.current(), shell.sidePanel.open)
	}
	if len(shell.terminals) != 1 {
		t.Fatalf("terminals must survive the pane switch, got %d", len(shell.terminals))
	}
	if shell.selectedPaneID != "p2" {
		t.Fatalf("pane selection = %q, want p2", shell.selectedPaneID)
	}
}

// TestChangesDetailRendersTheSelectedDiffCard pins the Godiff presentation
// port in the center's Detail card: the card header carries the Viewed toggle
// and the +/- pill, line rows render without the old hunk-header rows, no
// terminal chrome leaks into the card, and the right panel beside it keeps
// only the list.
func TestChangesDetailRendersTheSelectedDiffCard(t *testing.T) {
	shell := gitSurfaceTestShell(t, t.TempDir())
	showChangesPanel(shell)
	shell.rebuildDiffRows()
	shell.buildGdRows()

	tester := ui.NewTester(shell.View, 1400, 800)
	tester.Frame()

	if shell.surface.current() != WorkspaceSurfaceDetail {
		t.Fatalf("surface = %v, want the Detail card", shell.surface.current())
	}
	// The diff card for the seeded file renders.
	if !tester.HasText("src/main.go") {
		t.Fatalf("detail missing the file card; texts=%q", tester.Texts())
	}
	viewed, ok := tester.Find("Viewed")
	if !ok {
		t.Fatalf("diff card header missing the Viewed toggle; texts=%q", tester.Texts())
	}
	if panel, shown := sidePanelCardBounds(tester); !shown || viewed.X+viewed.W > panel.X {
		t.Fatalf("the diff card belongs in the center: Viewed at x %.0f–%.0f, panel card %+v (shown %v)",
			viewed.X, viewed.X+viewed.W, panel, shown)
	}
	if !tester.HasText("+12") || !tester.HasText("-4") {
		t.Fatalf("diff card header missing the +/- pill; texts=%q", tester.Texts())
	}
	if tester.HasText("@1,4 +1,5") {
		t.Fatal("old hunk-header rows must not render; Godiff folds hunks without headers")
	}
	if tester.HasText("Split Right") {
		t.Fatalf("terminal actions visible in the detail card; texts=%q", tester.Texts())
	}
}

// TestSplitTrackedUntracked pins the discard routing: untracked paths go to
// the delete lane, tracked ones to the restore lane.
func TestSplitTrackedUntracked(t *testing.T) {
	shell := gitSurfaceTestShell(t, t.TempDir())
	shell.git.snapshot.Files[1].Untracked = true

	tracked, untracked := shell.splitTrackedUntracked([]string{"src/main.go", "README.md"})
	if len(tracked) != 1 || tracked[0] != "src/main.go" {
		t.Fatalf("tracked = %v", tracked)
	}
	if len(untracked) != 1 || untracked[0] != "README.md" {
		t.Fatalf("untracked = %v", untracked)
	}
}

// TestPendingToastFlushesOnFrame pins the background-lane notice channel: a
// queued toast is consumed by the next rendered frame.
func TestPendingToastFlushesOnFrame(t *testing.T) {
	shell := gitSurfaceTestShell(t, t.TempDir())
	shell.pendingToast = "Changes stashed"
	tester := ui.NewTester(shell.View, 1200, 800)
	tester.Frame()
	if shell.pendingToast != "" {
		t.Fatalf("toast must flush on the next frame, got %q", shell.pendingToast)
	}
}

// TestAmendWithoutHeadRefused pins the amend guard: amending on an unborn
// branch fails honestly instead of reaching git.
func TestAmendWithoutHeadRefused(t *testing.T) {
	shell := gitSurfaceTestShell(t, t.TempDir())
	shell.git.snapshot.NoHead = true

	shell.submitCommitAmend("subject", "", []string{"README.md"}, true)
	if shell.surface.commit.ErrText == "" {
		t.Fatal("amend without HEAD must be refused with an error message")
	}
}

// TestChangesPanelSplitPanesAndCommitBar pins the panel's Changes page: the
// Unstaged/Staged split panes with their Stage/Unstage actions and the
// one-line commit bar, over a sidebar that stays the runtime navigator (the
// repo bar, the Changes|Commits toggle and the Git toolbar left with the Git
// page, 2026-11-08) — and everything Git-specific is gone again once the
// panel closes.
func TestChangesPanelSplitPanesAndCommitBar(t *testing.T) {
	root := t.TempDir()
	shell := gitSurfaceTestShell(t, root)
	shell.git.rootOf[root] = root
	// src/main.go is modified in the worktree only; give it a staged side
	// too so both panes have an entry.
	cf := &shell.git.snapshot.Files[0]
	cf.Staged, cf.Unstaged = true, true

	if shell.router.Path() != routeWorkspace {
		shell.router.Push(routeWorkspace)
	}
	showChangesPanel(shell)
	shell.rebuildDiffRows()
	tester := ui.NewTester(shell.View, 1200, 800)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Frame()
	if !tester.HasText("Unstaged Files") || !tester.HasText("Staged Files") {
		t.Fatalf("panel missing the split panes; texts=%q", tester.Texts())
	}
	// Per-pane Stage/Unstage remain scoped to their file selection.
	if !tester.HasText("Stage") || !tester.HasText("Unstage") {
		t.Fatalf("pane headers missing the Stage/Unstage actions; texts=%q", tester.Texts())
	}
	for _, label := range []string{"Commit subject draft", "Commit"} {
		if !tester.HasText(label) {
			t.Fatalf("commit bar missing %s: %q", label, tester.Texts())
		}
	}
	for _, gone := range []string{"Tags", "Stashes", "Worktrees", "Commits", "Unified"} {
		if tester.HasText(gone) {
			t.Fatalf("%q belongs to the retired Git page; texts=%q", gone, tester.Texts())
		}
	}
	// The sidebar keeps its runtime navigator beside the panel.
	if !tester.HasText("Agents") || !tester.HasText("Workspace") {
		t.Fatalf("sidebar lost the runtime navigator beside the panel; texts=%q", tester.Texts())
	}

	shell.closeSidePanel()
	tester.Frame()
	if tester.HasText("Unstaged Files") || tester.HasText("Commit subject draft") {
		t.Fatalf("a closed panel must drop the Git controls: texts=%q", tester.Texts())
	}
}

// TestDiffFileClickRevealsAndSyncs pins GWB-180/181/182 for the panel: opening
// Changes on a path selects it and shows its diff in the Detail card, and a
// programmatic reveal guards the center→right oscillation window.
func TestDiffFileClickRevealsAndSyncs(t *testing.T) {
	shell := gitSurfaceTestShell(t, t.TempDir())

	shell.openSidePanelChanges("README.md")
	shell.rebuildDiffRows() // what the snapshot's arrival does in production
	if shell.surface.current() != WorkspaceSurfaceDetail || shell.surface.detailKind() != DetailChanges {
		t.Fatalf("surface = %v kind = %q, want the Changes detail card", shell.surface.current(), shell.surface.detailKind())
	}
	if shell.surface.diff.SelectedPath != "README.md" {
		t.Fatalf("selected = %q", shell.surface.diff.SelectedPath)
	}

	// Reveal sets the guard window.
	shell.revealDiffFile("README.md")
	if !shell.diffScrollGuardActive() {
		t.Fatal("reveal must open the oscillation guard window")
	}
	time.Sleep(5 * time.Millisecond)

	// Center→right sync respects the guard while it is active.
	shell.git.revealAt = timeNow()
	shell.syncChangesSelectionFromDiff("src/main.go")
	if shell.surface.diff.SelectedPath != "README.md" {
		t.Fatalf("guard window violated: selected = %q", shell.surface.diff.SelectedPath)
	}
}

// TestCommitSurfaceFlow pins GWB-218..227 headless behavior: the surface
// lists files with default selection, refuses to submit without a subject
// or fence, and cancel lands back on the surface it came from.
func TestCommitSurfaceFlow(t *testing.T) {
	shell := gitSurfaceTestShell(t, t.TempDir())
	shell.showSurface(WorkspaceSurfaceCommit)
	if shell.surface.current() != WorkspaceSurfaceCommit {
		t.Fatalf("surface = %v", shell.surface.current())
	}

	// Default selection: every changed file (plan §22.2).
	if len(shell.surface.commit.Selected) != 2 {
		t.Fatalf("default selection = %v", shell.surface.commit.Selected)
	}
	if paths := shell.selectedCommitPaths(); len(paths) != 2 {
		t.Fatalf("selectedCommitPaths = %v", paths)
	}

	// No preflight yet: submit must be refused.
	shell.submitCommit("subject", "", shell.selectedCommitPaths())
	if shell.surface.commit.ErrText == "" {
		t.Fatal("commit without captured preflight must fail honestly")
	}

	// Cancel returns to the previous surface.
	shell.cancelCommitSurface()
	if got := shell.surface.current(); got != WorkspaceSurfaceTerminal {
		t.Fatalf("cancel landed on %v", got)
	}
}

// TestChangesPanelClicksOpenDiff pins GWB-145 for the panel: a Changes row
// activation selects the file's diff and opens it in the center's Detail card.
func TestChangesPanelClicksOpenDiff(t *testing.T) {
	shell := gitSurfaceTestShell(t, t.TempDir())
	shell.changesClickSide(gitworkbench.FlatRow{
		Node:  &gitworkbench.TreeNode{Name: "README.md", Path: "README.md", File: &gitworkbench.ChangeFile{Path: "README.md"}},
		Depth: 1,
	}, gdViewCombined)
	if shell.surface.current() != WorkspaceSurfaceDetail || shell.surface.detailKind() != DetailChanges {
		t.Fatalf("surface = %v, want the Detail card", shell.surface.current())
	}
	if shell.surface.diff.SelectedPath != "README.md" {
		t.Fatalf("selected = %q", shell.surface.diff.SelectedPath)
	}
}

// TestGeneratedFileCardsCollapseByDefault pins GWB-155: a generated file's
// card starts collapsed — a header row only, never line rows — until the
// user expands it.
func TestGeneratedFileCardsCollapseByDefault(t *testing.T) {
	shell := gitSurfaceTestShell(t, t.TempDir())
	shell.git.snapshot.Files = append(shell.git.snapshot.Files, gitworkbench.ChangeFile{
		Path:      "go.sum",
		Status:    gitworkbench.StatusModified,
		Generated: true,
		Additions: 500,
		Hunks:     []gitworkbench.Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1}},
	})
	shell.rebuildDiffRows()
	shell.buildGdRows()

	idx := -1
	for i, f := range shell.git.gdFiles {
		if f.cf.Path == "go.sum" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("generated file missing from the review files")
	}
	if !shell.git.gdFiles[idx].collapsed {
		t.Fatal("generated file must collapse by default")
	}
	for _, row := range shell.git.gdRows {
		if int(row.file) == idx && row.kind == gdRowLine {
			t.Fatal("generated file must not emit line rows while collapsed")
		}
	}
}

// TestProductionDirectoryLoadNeverBlocksRender pins GWB-003 on the
// production path: beginDirectoryLoad marks the request in-flight and
// returns without populating entries — the render path cannot have waited
// on filesystem IO.
func TestProductionDirectoryLoadNeverBlocksRender(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shell := gitSurfaceTestShell(t, dir)
	shell.rightPanel.currentRoot = dir

	gen := shell.rightPanel.filesGen.Add(1)
	done := make(chan struct{})
	go func() { shell.beginDirectoryLoad(dir, dir, gen); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("beginDirectoryLoad blocked the caller")
	}
	if _, ok := shell.rightPanel.cachedDirEntries[dir]; ok {
		t.Fatal("production lane must not apply entries synchronously")
	}
	if !shell.rightPanel.loadingDirs[dir] {
		t.Fatal("production lane must mark the request in-flight")
	}
}

// TestPanelChangesWithoutSnapshotDoesNotPanic pins the 2026-10-05 production
// crash: opening Changes resolved firstChangedPath over a nil snapshot
// (non-repo cwd, or the refresh still in flight) and dereferenced snap.Files,
// killing the whole process. It must degrade to an empty selection instead.
func TestPanelChangesWithoutSnapshotDoesNotPanic(t *testing.T) {
	shell := NewShell()
	shell.git.root = "" // no repository bound, no snapshot ever loaded

	if firstChangedPath(nil) != "" {
		t.Fatal("firstChangedPath(nil) must return an empty path")
	}

	shell.openSidePanelChanges("") // must not panic
	if shell.surface.diff.SelectedPath != "" {
		t.Fatalf("selected path = %q, want empty", shell.surface.diff.SelectedPath)
	}
}

// TestApplyGitSnapshotNeverRaisesADriftBanner pins 2026-11-08 (it replaced
// the F25 own-mutation fence): a refreshed snapshot with a new signature —
// the first read after a load, the app's own stage or commit, a file an Agent
// wrote — replaces the one on screen and says nothing more. Every view that
// shows the changes re-reads them itself, so "Files changed on disk — Refresh
// to update" could only ever be a false alarm over data already updated.
func TestApplyGitSnapshotNeverRaisesADriftBanner(t *testing.T) {
	root := t.TempDir()
	shell := gitSurfaceTestShell(t, root)
	showChangesPanel(shell)
	for _, sig := range []string{"sig-2", "sig-3"} {
		shell.applyGitSnapshot(&gitworkbench.ChangesSnapshot{
			Root: root, Branch: "main", Signature: sig,
			Files: []gitworkbench.ChangeFile{{Path: "src/main.go", Status: gitworkbench.StatusModified}},
		})
		shell.rebuildDiffRows()
		shell.buildGdRows()
		tester := ui.NewTester(shell.View, 1400, 800)
		tester.Frame()
		if tester.HasText("Files changed on disk") || tester.HasText("Refresh to update.") {
			t.Fatalf("a new signature %q raised the drift banner: %q", sig, tester.Texts())
		}
		if got := shell.git.snapshot.Signature; got != sig {
			t.Fatalf("the fresh snapshot did not replace the displayed one: %q, want %q", got, sig)
		}
	}
}
