package nativeui

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: a real temporary Git repository with two unstaged hunks, the headless Diff review, gutter clicks
 * [OUTPUT]: picking lines in the gutter and staging/unstaging them (or a whole hunk) from the hunk header reaches the real index
 * [POS]: Fork-style partial staging regression (gd_stage_lines.go over gitworkbench.ApplyLines)
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// linesRepo is a repo whose f.txt (20 lines) has two unstaged hunks: line 2
// edited and line 15 deleted.
func linesRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitScript(t, root,
		[]string{"init", "-q", "-b", "main"},
		[]string{"config", "user.email", "test@example.com"},
		[]string{"config", "user.name", "Test"},
	)
	var base, edit strings.Builder
	for i := 1; i <= 20; i++ {
		base.WriteString("line " + itoa(i) + "\n")
		switch i {
		case 2:
			edit.WriteString("line two\n")
		case 15:
		default:
			edit.WriteString("line " + itoa(i) + "\n")
		}
	}
	writeRepoFile(t, root, "f.txt", base.String())
	gitScript(t, root, []string{"add", "."}, []string{"commit", "-q", "-m", "base"})
	writeRepoFile(t, root, "f.txt", edit.String())
	return root
}

func linesShell(t *testing.T, root string) (*Shell, *ui.Tester) {
	t.Helper()
	s := gitSurfaceTestShell(t, root)
	s.git.rootOf[root] = root
	snap, err := s.git.runner.Snapshot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	s.git.snapshot = snap
	s.git.refreshing = true
	s.router.Push(routeWorkspace)
	s.showSurface(WorkspaceSurfaceDiff)
	s.rebuildDiffRows()
	s.openChangesSide("f.txt", gdViewUnstaged)
	tester := ui.NewTester(s.View, 1280, 900)
	settleFrames(tester, 3)
	return s, tester
}

func cachedDiff(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "diff", "--cached", "-U0", "--", "f.txt").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if !time.Now().Before(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A click on a changed line's number picks it; the hunk header then offers
// "Stage 1 Line", which writes just that line into the index.
func TestGutterPickStagesTheChosenLines(t *testing.T) {
	root := linesRepo(t)
	s, tester := linesShell(t, root)

	if !tester.HasText("Stage Hunk") && tester.HasText("Unstage Hunk") {
		t.Fatalf("the unstaged side offers Unstage; texts=%q", tester.Texts())
	}
	// Two hunk headers, one per change.
	for _, want := range []string{"@@ -1,5 +1,5 @@", "@@ -12,7 +12,6 @@"} {
		found := false
		for _, text := range tester.Texts() {
			if strings.HasPrefix(text, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no hunk header %q; texts=%q", want, tester.Texts())
		}
	}

	// Pick the added line ("line two") by its number: left of its code.
	code, ok := tester.Find("line two")
	if !ok {
		t.Fatalf("no added line; texts=%q", tester.Texts())
	}
	tester.ClickAt(code.X-16, code.Y+code.H/2)
	tester.Frame()
	if n := len(s.git.gdPick.lines); n != 1 {
		t.Fatalf("one click picked %d lines, want 1", n)
	}
	if err := tester.Click("Stage 1 Line"); err != nil {
		t.Fatalf("the header offers no Stage 1 Line: %v; texts=%q", err, tester.Texts())
	}
	if len(s.git.gdPick.lines) != 0 {
		t.Fatal("the pick must clear once its lines are on their way")
	}
	waitUntil(t, "the added line in the index", func() bool { return strings.Contains(cachedDiff(t, root), "+line two") })
	staged := cachedDiff(t, root)
	if strings.Contains(staged, "-line 2\n") {
		t.Fatalf("the deletion half of the edit must stay unstaged:\n%s", staged)
	}
	if strings.Contains(staged, "line 15") {
		t.Fatalf("the other hunk must stay unstaged:\n%s", staged)
	}
}

// "Stage Hunk" stages every change of that hunk and only that hunk.
func TestHunkHeaderStagesTheWholeHunk(t *testing.T) {
	root := linesRepo(t)
	s, tester := linesShell(t, root)
	// The pointer must be on a header for its buttons to show.
	var header ui.Rect
	for _, text := range tester.Texts() {
		if strings.HasPrefix(text, "@@ -12,7") {
			header, _ = tester.Find(text)
		}
	}
	tester.Move(header.X+10, header.Y+header.H/2)
	settleFrames(tester, 2)
	if err := tester.Click("Stage Hunk"); err != nil {
		t.Fatalf("no Stage Hunk under the pointer: %v; texts=%q", err, tester.Texts())
	}
	waitUntil(t, "line 15 in the index", func() bool { return strings.Contains(cachedDiff(t, root), "-line 15") })
	if strings.Contains(cachedDiff(t, root), "line two") {
		t.Fatalf("the first hunk was staged too:\n%s", cachedDiff(t, root))
	}
	_ = s
}

// Shift extends a pick to the changed lines between; Cmd toggles one; a plain
// click on the only picked line clears it.
func TestGutterPickModifiers(t *testing.T) {
	root := linesRepo(t)
	s, tester := linesShell(t, root)
	h := s.git.gdFiles[0].hunks()[0]
	var del, add int
	for i, l := range h.Lines {
		switch l.Text {
		case "line 2":
			del = i
		case "line two":
			add = i
		}
	}
	f := s.git.gdFiles[0]
	s.gdPickLine(f, 0, del, 0)
	if !s.gdPicked(f, 0, del) || s.gdPicked(f, 0, add) {
		t.Fatal("a plain click picks just its line")
	}
	s.gdPickLine(f, 0, add, ui.Shift)
	if !s.gdPicked(f, 0, del) || !s.gdPicked(f, 0, add) {
		t.Fatal("Shift must pick the range between the anchor and the click")
	}
	s.gdPickLine(f, 0, del, ui.Cmd)
	if s.gdPicked(f, 0, del) || !s.gdPicked(f, 0, add) {
		t.Fatal("Cmd toggles one line")
	}
	s.gdPickLine(f, 0, add, 0)
	if len(s.git.gdPick.lines) != 0 {
		t.Fatal("a plain click on the only picked line clears the pick")
	}
	// A refreshed snapshot voids an older pick.
	s.gdPickLine(f, 0, del, 0)
	snap := *s.git.snapshot
	s.git.snapshot = &snap
	if s.gdPicked(f, 0, del) {
		t.Fatal("a pick must not survive a new snapshot")
	}
	_ = tester
}

// Only a tracked text file's modification offers line staging, and never in
// commit review.
func TestOnlyModifiedTextFilesOfferLineStaging(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	f := &gdFile{cf: &s.git.snapshot.Files[0], view: gdViewCombined}
	if _, ok := s.gdStageSide(f); ok {
		t.Fatal("a file with neither side flagged must not offer it")
	}
	f.cf.Unstaged = true
	if unstage, ok := s.gdStageSide(f); !ok || unstage {
		t.Fatalf("an unstaged-only change stages (unstage=%v ok=%v)", unstage, ok)
	}
	f.cf.Staged = true
	if _, ok := s.gdStageSide(f); ok {
		t.Fatal("a change on both sides is ambiguous until a pane picks the side")
	}
	f.cf.StagedHunks = f.cf.Hunks
	f.view = gdViewStaged
	if unstage, ok := s.gdStageSide(f); !ok || !unstage {
		t.Fatalf("the staged side unstages (unstage=%v ok=%v)", unstage, ok)
	}
	added := &gdFile{cf: &s.git.snapshot.Files[1], view: gdViewUnstaged}
	added.cf.Unstaged = true
	if _, ok := s.gdStageSide(added); ok {
		t.Fatal("an added file is staged whole, not by line")
	}
	s.git.source = gdSourceCommit
	if _, ok := s.gdStageSide(f); ok {
		t.Fatal("commit review never stages")
	}
}

// A press on one changed line's number, swept over another's, picks the
// changed lines between them and the release is no click.
func TestGutterDragPicksTheSweptLines(t *testing.T) {
	root := linesRepo(t)
	s, tester := linesShell(t, root)
	// A first hunk that carries two changed lines: one delete, one add.
	del, ok := tester.Find("line 2")
	add, ok2 := tester.Find("line two")
	if !ok || !ok2 {
		t.Fatalf("lines not shown; texts=%q", tester.Texts())
	}
	tester.Move(del.X-16, del.Y+del.H/2)
	tester.Press(del.X-16, del.Y+del.H/2)
	tester.Frame()
	tester.Move(add.X-16, add.Y+add.H/2)
	settleFrames(tester, 2)
	tester.Release(add.X-16, add.Y+add.H/2)
	settleFrames(tester, 2)
	if n := len(s.git.gdPick.lines); n != 2 {
		t.Fatalf("the sweep picked %d lines, want 2 (the delete and the add); pick=%v", n, s.git.gdPick.lines)
	}
	if s.git.gdDrag.active {
		t.Fatal("the sweep must end with the release")
	}
	if !tester.HasText("Stage 2 Lines") {
		t.Fatalf("no Stage 2 Lines; texts=%q", tester.Texts())
	}
}

// The first file's card sits flush under the title bar: no band of paper
// between the header and the card, in either theme, and the diff column
// shows the window's own paper beside the card.
func TestDiffFirstCardIsFlushWithTheHeader(t *testing.T) {
	for _, dark := range []bool{false, true} {
		root := linesRepo(t)
		_, tester := linesShell(t, root)
		tester.SetDark(dark)
		settleFrames(tester, 3)
		paper := pixelAt(tester, 556, 60)
		top := -1
		for y := int(titlebarHeight(0)) - 2; y < 120; y++ {
			if rgbDiff(pixelAt(tester, 1000, y), paper) > 1 {
				top = y
				break
			}
		}
		if top < 0 || top > int(titlebarHeight(0))+2 {
			t.Errorf("dark=%v: the first card starts at y=%d, %d px below the title bar", dark, top, top-int(titlebarHeight(0)))
		}
	}
}
