package nativeui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: 依赖 nativeui 的 Shell/breadcrumbSegments/breadcrumbSegmentsWithTheme/shortDir/tildePath, mygo/ui 的 Tester 无头渲染（Move/Advance 驱动悬停弹层）, herdr
 * [OUTPUT]: 对外提供 TestBreadcrumbPaneSegmentListsTabPanes, TestBreadcrumbSegmentsVisualMarks, TestLastCrumbHoverShowsDirectoryOneLine
 * [POS]: nativeui 标题栏面包屑与切换器合约测试；TestLastCrumbHoverShowsDirectoryOneLine 钉住 2026-10-28 单行头部——目录不再是第二行，只住在末级 crumb 的悬停弹层
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
