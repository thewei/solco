package nativeui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: 依赖 nativeui 的 Shell/breadcrumbSegments/breadcrumbSegmentsWithTheme/shortDir/tildePath, mygo/ui 的 Tester 无头渲染（Move/Advance 驱动悬停弹层）, herdr
 * [OUTPUT]: 对外提供 TestBreadcrumbPaneSegmentListsTabPanes, TestBreadcrumbSegmentsVisualMarks, TestLastCrumbHoverShowsDirectoryOneLine, TestBreadcrumbChipsAreGhostUntilTheyAreUsed, TestBreadcrumbGivesWayBeforeTheToolsDo, TestClippedAncestorCrumbNamesItselfInATooltip, TestCrumbPopoverRowsScrollInsideThePanel（crumbShell/crumbRect/settleHover 为其共用辅助）
 * [POS]: nativeui 标题栏面包屑与切换器合约测试；TestLastCrumbHoverShowsDirectoryOneLine 钉住 2026-10-28 单行头部——目录不再是第二行，只住在末级 crumb 的悬停弹层；2026-10-29 三项钉住 shadcn 形态——ghost 芯片（静止无底、悬停/弹层打开才有芯片脸）、分隔符不可点、窄栏先让祖先再让工具、截断 crumb 带 tooltip；同日实机复核补一项——crumb 弹层兄弟过多时行在面板内滚动（switcherListMaxHeight）而不是溢出面板
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// TestBreadcrumbPaneSegmentListsTabPanes pins the pane switcher contract:
// the title-bar Pane segment carries the Tab id so its popover can list the
// Tab's Panes; carrying the Pane's own id filtered the list to nothing
// (2026-10-05 regression).
func TestBreadcrumbPaneSegmentListsTabPanes(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "Solco", TabCount: 1}},
		Tabs:     []herdr.Tab{{ID: "t1", ProjectID: "w1", Label: "main", PaneCount: 2}},
		Panes: []herdr.Pane{
			{ID: "p1", TerminalID: "term1", ProjectID: "w1", TabID: "t1", Label: "left"},
			{ID: "p2", TerminalID: "term2", ProjectID: "w1", TabID: "t1", Label: "right"},
		},
	}
	s.selectedProjectID = "w1"
	s.selectedTabID = "t1"
	s.selectedPaneID = "p1"

	segments := s.breadcrumbSegments()
	if len(segments) != 3 {
		t.Fatalf("segments = %#v, want project/tab/pane", segments)
	}
	pane := segments[len(segments)-1]
	if pane.kind != "pane" {
		t.Fatalf("last segment kind = %q, want pane", pane.kind)
	}
	siblings := panesForTab(s.projection, pane.id)
	if len(siblings) != 2 {
		t.Fatalf("pane popover rows = %d, want 2 (segment id %q)", len(siblings), pane.id)
	}
}

// TestLastCrumbHoverShowsDirectoryOneLine pins the 2026-10-28 one-line
// workspace header: the Pane's directory is not a second row anymore; it
// shows in the last crumb's hover popup once the pointer rests on it, and
// hides again when the pointer leaves.
func TestLastCrumbHoverShowsDirectoryOneLine(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory for a tilde path")
	}
	cwd := filepath.Join(home, "Work", "solco-hover-test")
	s := NewShell()
	s.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "Solco", CWD: cwd}},
		Tabs:     []herdr.Tab{{ID: "t1", Label: "main", ProjectID: "w1"}},
		Panes:    []herdr.Pane{{ID: "p1", TerminalID: "term1", Label: "editor", TabID: "t1", CWD: cwd}},
	}
	s.reconcileLocalSelection(s.projection)

	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	// One line: the directory is not a static row anywhere on screen.
	if tester.HasText(shortDir(cwd)) {
		t.Fatalf("directory %q renders as a static row; want the last crumb's hover popup only", shortDir(cwd))
	}
	// The pointer resting on the last crumb shows the directory in full.
	r, ok := tester.Find("main")
	if !ok {
		t.Fatalf("last crumb %q not on screen: %q", "main", tester.Texts())
	}
	tester.Move(r.X+r.W/2, r.Y+r.H/2)
	tester.Advance(700 * time.Millisecond)
	tester.Frame()
	tip := tildePath(cwd)
	if !tester.HasText(tip) {
		t.Fatalf("hover popup %q missing after the pointer rested: %q", tip, tester.Texts())
	}
	// Leaving the crumb takes the popup away.
	tester.Move(1150, 780)
	tester.Advance(200 * time.Millisecond)
	tester.Frame()
	if tester.HasText(tip) {
		t.Fatalf("hover popup %q still shows after the pointer left", tip)
	}
}

// crumbShell is a Shell on a Project › Tab › Pane path whose labels the test
// picks (the second Pane makes the Pane a crumb of its own).
func crumbShell(project, tab, pane string) *Shell {
	s := NewShell()
	s.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: project, CWD: "/tmp/crumbs"}},
		Tabs:     []herdr.Tab{{ID: "t1", Label: tab, ProjectID: "w1", PaneCount: 2}},
		Panes: []herdr.Pane{
			{ID: "p1", TerminalID: "term1", Label: pane, TabID: "t1", ProjectID: "w1", CWD: "/tmp/crumbs"},
			{ID: "p2", TerminalID: "term2", Label: "other", TabID: "t1", ProjectID: "w1", CWD: "/tmp/crumbs"},
		},
	}
	s.reconcileLocalSelection(s.projection)
	return s
}

// crumbRect finds a crumb by its label and insists it is the title bar's.
func crumbRect(t *testing.T, tt *ui.Tester, label string) ui.Rect {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok || r.Y+r.H > titlebarHeight(0) {
		t.Fatalf("crumb %q is not in the title bar (found=%v at %+v); texts=%q", label, ok, r, tt.Texts())
	}
	return r
}

// settleHover lets a pointer move be seen, then painted: hover takes a pass
// to be seen, a second (and the clock) to be painted.
func settleHover(tt *ui.Tester) {
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
}

// TestBreadcrumbChipsAreGhostUntilTheyAreUsed pins the 2026-10-29 shadcn
// shape of the path: no crumb — the current one included — wears a face at
// rest; a chip face shows under the pointer, and stays while the crumb's own
// popover is open; the chevrons between crumbs are not part of any button.
func TestBreadcrumbChipsAreGhostUntilTheyAreUsed(t *testing.T) {
	for _, dark := range []bool{false, true} {
		s := crumbShell("Solco", "main", "editor")
		tt := ui.NewTester(s.View, 1200, 800)
		tt.SetDark(dark)
		tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		settleFrames(tt, 3)
		crumbs := map[string]ui.Rect{
			"project": crumbRect(t, tt, "Solco"),
			"tab":     crumbRect(t, tt, "main"),
			"pane":    crumbRect(t, tt, "editor"),
		}
		bar := pixelAt(tt, 800, 21) // empty bar between the path and the tools
		face := func(name string) [4]uint8 {
			r := crumbs[name]
			return pixelAt(tt, int(r.X)+2, int(r.Y+r.H/2)) // inside the chip, left of its content
		}
		for name := range crumbs {
			if d := rgbDiff(face(name), bar); d > 2 {
				t.Errorf("dark=%v: the %s crumb wears a face at rest (diff %d); chips are ghost until hovered or open", dark, name, d)
			}
		}

		// A chevron is a separator, not a hit target.
		project, tab := crumbs["project"], crumbs["tab"]
		tt.ClickAt((project.X+project.W+tab.X)/2, project.Y+project.H/2)
		settleFrames(tt, 2)
		if s.crumbOpen != [3]bool{} {
			t.Errorf("dark=%v: pressing the chevron between two crumbs opened %v", dark, s.crumbOpen)
		}

		// The pointer lights one chip, and takes it away again.
		tt.Move(tab.X+tab.W/2, tab.Y+tab.H/2)
		settleHover(tt)
		if d := rgbDiff(face("tab"), bar); d <= 6 {
			t.Errorf("dark=%v: a hovered crumb shows no chip face (diff %d)", dark, d)
		}
		if d := rgbDiff(face("project"), bar); d > 2 {
			t.Errorf("dark=%v: a neighbour of the hovered crumb is lit too (diff %d)", dark, d)
		}
		tt.Move(1100, 700)
		settleHover(tt)
		if d := rgbDiff(face("tab"), bar); d > 2 {
			t.Errorf("dark=%v: the chip face stays after the pointer left (diff %d)", dark, d)
		}

		// While its popover is open the chip stays lit with the pointer away.
		tt.ClickAt(tab.X+tab.W/2, tab.Y+tab.H/2)
		tt.Move(1100, 700)
		settleHover(tt)
		if !s.crumbOpen[1] {
			t.Fatalf("dark=%v: clicking the Tab crumb opened no popover (open=%v)", dark, s.crumbOpen)
		}
		if d := rgbDiff(face("tab"), bar); d <= 6 {
			t.Errorf("dark=%v: the crumb whose popover is open shows no chip face (diff %d)", dark, d)
		}
	}
}

// TestBreadcrumbGivesWayBeforeTheToolsDo pins the narrow bar: three long
// crumbs end in ellipses, ancestors first, never overlap each other, and
// never push the Actions button and the trailing tools out of the window.
func TestBreadcrumbGivesWayBeforeTheToolsDo(t *testing.T) {
	project := "A very long project name that keeps going and going and going on"
	tab := "feature/an-exceedingly-long-branch-name-for-the-current-tab-here"
	pane := "A pane label that is also unreasonably long for a title bar crumb"
	for _, width := range []int{1200, 900, 760} {
		s := crumbShell(project, tab, pane)
		tt := ui.NewTester(s.View, width, 800)
		settleFrames(tt, 3)
		actions, ok := tt.Find("Actions")
		if !ok {
			t.Fatalf("width %d: no Actions button; texts=%q", width, tt.Texts())
		}
		pin, ok := tt.Find(windowPinLabel)
		if !ok {
			t.Fatalf("width %d: no trailing tools; texts=%q", width, tt.Texts())
		}
		if pin.X+pin.W > float32(width) {
			t.Errorf("width %d: the tools end at %.1f, outside the window", width, pin.X+pin.W)
		}
		var prev ui.Rect
		var widths []float32
		for i, label := range []string{project, tab, pane} {
			r := crumbRect(t, tt, label)
			if r.W < crumbMinWidth-0.5 {
				t.Errorf("width %d: crumb %d is %.1f wide, under the %.0f floor", width, i, r.W, float32(crumbMinWidth))
			}
			if i > 0 && r.X < prev.X+prev.W-0.5 {
				t.Errorf("width %d: crumb %d starts at %.1f inside the one before it (ends %.1f)", width, i, r.X, prev.X+prev.W)
			}
			prev = r
			widths = append(widths, r.W)
		}
		if prev.X+prev.W > actions.X+0.5 {
			t.Errorf("width %d: the path ends at %.1f, past the Actions button at %.1f", width, prev.X+prev.W, actions.X)
		}
		if width < 1000 && (widths[2] < widths[0] || widths[2] < widths[1]) {
			t.Errorf("width %d: the current crumb (%.1f) gave way before its ancestors (%.1f, %.1f)", width, widths[2], widths[0], widths[1])
		}
	}
}

// TestClippedAncestorCrumbNamesItselfInATooltip keeps the rule that every
// truncated text has its whole in a tooltip: an ancestor crumb too long for
// its chip shows its label when hovered; a short one adds nothing.
func TestClippedAncestorCrumbNamesItselfInATooltip(t *testing.T) {
	long := "A very long project name that keeps going and going and going on"
	s := crumbShell(long, "main", "editor")
	tt := ui.NewTester(s.View, 1200, 800)
	settleFrames(tt, 3)
	for _, tc := range []struct {
		label string
		tip   bool
	}{{long, true}, {"main", false}} {
		r := crumbRect(t, tt, tc.label)
		before := countTextExact(tt.Texts(), tc.label)
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Advance(700 * time.Millisecond)
		tt.Frame()
		if got := countTextExact(tt.Texts(), tc.label) - before; tc.tip && got != 1 || !tc.tip && got != 0 {
			t.Errorf("crumb %q: hovering added %d tooltip texts; clipped=%v", tc.label, got, tc.tip)
		}
		tt.Move(1100, 700)
		tt.Advance(300 * time.Millisecond)
		tt.Frame()
	}
}

// TestCrumbPopoverRowsScrollInsideThePanel pins the 2026-10-29 live finding:
// a crumb's popover with more siblings than its cap holds let the rows run
// out of the panel. The panel stops at switcherListMaxHeight and the wheel
// moves the rows inside it — the last row ends up at the panel's foot, never
// beyond it.
func TestCrumbPopoverRowsScrollInsideThePanel(t *testing.T) {
	const siblings = 40
	for _, kind := range []string{"project", "tab"} {
		s := crumbShell("Solco", "main", "editor")
		for i := 0; i < siblings; i++ {
			id, label := fmt.Sprintf("x%02d", i), fmt.Sprintf("sibling-%02d", i)
			if kind == "project" {
				s.projection.Projects = append(s.projection.Projects, herdr.Project{ID: id, Label: label})
			} else {
				s.projection.Tabs = append(s.projection.Tabs, herdr.Tab{ID: id, ProjectID: "w1", Label: label, PaneCount: 1})
			}
		}
		tt := ui.NewTester(s.View, 1200, 800)
		tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		settleFrames(tt, 3)
		opener := "Solco"
		if kind == "tab" {
			opener = "main"
		}
		crumb := crumbRect(t, tt, opener)
		tt.ClickAt(crumb.X+crumb.W/2, crumb.Y+crumb.H/2)
		settleFrames(tt, 3)

		lastLabel := fmt.Sprintf("sibling-%02d", siblings-1)
		first, okFirst := tt.Find("sibling-00")
		last, okLast := tt.Find(lastLabel)
		if !okFirst || !okLast {
			t.Fatalf("%s popover: rows missing (first=%v last=%v); texts=%q", kind, okFirst, okLast, tt.Texts())
		}
		top, foot := first.Y, first.Y+switcherListMaxHeight
		if last.Y+last.H <= foot {
			t.Fatalf("%s popover: %d rows end at %.0f, inside the list's foot at %.0f; the test cannot tell overflow from fit",
				kind, siblings, last.Y+last.H, foot)
		}

		// The wheel over the list moves the rows up to the last one, which
		// then rests inside the list, between its top and its foot.
		tt.Scroll(first.X+first.W/2, first.Y+first.H/2, 0, 10000)
		settleFrames(tt, 3)
		last, _ = tt.Find(lastLabel)
		if last.Y < top-0.5 || last.Y+last.H > foot+0.5 {
			t.Errorf("%s popover: after scrolling to the end the last row spans %.0f–%.0f, outside the list's %.0f–%.0f",
				kind, last.Y, last.Y+last.H, top, foot)
		}
	}
}

func TestBreadcrumbSegmentsVisualMarks(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "MyProject", TabCount: 1}},
		Tabs:     []herdr.Tab{{ID: "t1", ProjectID: "w1", Label: "Tab", PaneCount: 1}},
		Panes: []herdr.Pane{
			{ID: "p1", TerminalID: "term1", ProjectID: "w1", TabID: "t1", Label: "Terminal"},
		},
		Agents: []herdr.Agent{
			{PaneID: "p1", Kind: "claude-code", Name: "Claude Code", Status: "working"},
		},
	}
	s.selectedProjectID = "w1"
	s.selectedTabID = "t1"
	s.selectedPaneID = "p1"

	segments := s.breadcrumbSegmentsWithTheme(false)
	if len(segments) != 2 { // single pane tab only yields project and tab segments
		t.Fatalf("len(segments) = %d, want 2", len(segments))
	}

	tabSeg := segments[1]
	if tabSeg.label != "Claude Code" {
		t.Fatalf("tabSeg.label = %q, want 'Claude Code'", tabSeg.label)
	}
	if tabSeg.mark.bmp == nil && tabSeg.mark.svg == nil {
		t.Fatalf("tabSeg.mark should carry agent mark")
	}
}
