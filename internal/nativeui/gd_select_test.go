package nativeui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: headless Diff surface with a modified file, MyGo pointer drag and ⌘C
 * [OUTPUT]: the diff's code is selectable and copyable without line numbers
 * [POS]: regression for "diff text cannot be selected" (a line's own input handler made MyGo treat its text as a control's)
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestDiffCodeIsSelectableAndCopyable(t *testing.T) {
	for _, wrap := range []bool{true, false} {
		s := gitSurfaceTestShell(t, t.TempDir())
		s.git.rootOf[s.git.root] = s.git.root
		s.git.gdWordWrap = wrap
		s.router.Push(routeWorkspace)
		s.showSurface(WorkspaceSurfaceDiff)
		s.rebuildDiffRows()
		tester := ui.NewTester(s.View, 1280, 800)
		tester.Frame()
		tester.Frame()
		from, ok := tester.Find("package main")
		if !ok {
			t.Fatalf("wrap=%v: no code line; texts=%q", wrap, tester.Texts())
		}
		to, ok := tester.Find("fmt.Println(\"new\")")
		if !ok {
			t.Fatalf("wrap=%v: no added line", wrap)
		}
		tester.Press(from.X+1, from.Y+from.H/2)
		tester.Move(to.X+to.W-1, to.Y+to.H/2)
		tester.Release(to.X+to.W-1, to.Y+to.H/2)
		tester.Frame()
		tester.Key(ui.Cmd, ui.KeyC)
		got := tester.Clipboard()
		if !strings.Contains(got, "package main") || !strings.Contains(got, `fmt.Println("new")`) {
			t.Fatalf("wrap=%v: ⌘C copied %q, want the dragged code lines", wrap, got)
		}
		if strings.ContainsAny(got, "12") && strings.Contains(got, "1\n") {
			t.Fatalf("wrap=%v: the copy carries line numbers: %q", wrap, got)
		}
	}
}

// Sideways scrolling over a long line moves that file's code (no-wrap mode)
// even though the line itself carries no input handler.
func TestDiffScrollsSidewaysOverLongLines(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	s.git.rootOf[s.git.root] = s.git.root
	s.git.snapshot.Files[0].Hunks[0].Lines[2].Text = "fmt.Println(\"" + strings.Repeat("x", 400) + "\")"
	s.router.Push(routeWorkspace)
	s.showSurface(WorkspaceSurfaceDiff)
	s.rebuildDiffRows()
	tester := ui.NewTester(s.View, 1280, 800)
	tester.Frame()
	tester.Frame()
	line, ok := tester.Find("package main")
	if !ok {
		t.Fatalf("no code line; texts=%q", tester.Texts())
	}
	x, y := line.X+line.W/2, line.Y+line.H/2
	tester.Move(x, y)
	tester.Frame()
	tester.Scroll(x, y, 60, 0)
	tester.Frame()
	if got := s.git.gdHScroll["src/main.go"]; got <= 0 {
		t.Fatalf("a sideways scroll over the code moved nothing (offset %v)", got)
	}
}
