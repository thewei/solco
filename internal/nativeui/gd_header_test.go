package nativeui

/**
 * [INPUT]: gitSurfaceTestShell 的内存快照、headless Tester（1156/1700 宽度）
 * [OUTPUT]: Diff 文件卡片头在中心 Detail 卡窄时让位于文件名、宽时保留全部控件的回归；卡片宽度随停靠的右面板与侧栏变化（Commit 背景卡同一来源）
 * [POS]: gdFileHeader 的响应式契约；宽度估算（gdCardWidth）与 gitCenterWidth 共用同一来源
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// gdHeaderShell opens the Detail card on a file whose name needs room.
func gdHeaderShell(t *testing.T) *Shell {
	t.Helper()
	shell := gitSurfaceTestShell(t, t.TempDir())
	shell.git.snapshot.Files[0].Path = "internal/nativeui/diff_surface.go"
	showChangesPanel(shell)
	shell.rebuildDiffRows()
	shell.buildGdRows()
	return shell
}

// gdNarrowestWindow is the narrowest window that still docks the panel: the
// sidebar, the center at its floor, the gutter and the panel's narrowest card.
// The Detail card there is a little under gdHeaderRoomy, the narrow form's only
// home.
const gdNarrowestWindow = int(sidebarWidth + sidePanelCenterFloor + sidePanelGutter + sidePanelMinWidth)

// When the center is squeezed to its floor the header must spend its width on
// the name, not on tools.
func TestDiffHeaderKeepsItsFileNameWhenNarrow(t *testing.T) {
	shell := gdHeaderShell(t)
	shell.sidePanel.width = sidePanelMinWidth
	tester := ui.NewTester(shell.View, gdNarrowestWindow, 800)
	tester.Frame()

	name, ok := tester.Find("diff_surface.go")
	if !ok {
		t.Fatalf("the header does not show the file name: %q", tester.Texts())
	}
	// 15 monospace columns: anything much under 100 is an ellipsis stub.
	if name.W < 100 {
		t.Fatalf("file name squeezed to %.0f px in the narrow card", name.W)
	}
	if dir, ok := tester.Find("internal/nativeui/"); !ok || dir.Y <= name.Y {
		t.Errorf("a narrow header shows the folder below the name: dir=%v (found %v) name=%v", dir, ok, name)
	}
	for _, tool := range []string{"Copy Path", "Open in Editor"} {
		if _, ok := tester.Find(tool); ok {
			t.Errorf("%q still takes room in a narrow header", tool)
		}
	}
	// Viewed is not drawn at all while the card is narrow: an unlabeled box
	// collapsed the card in the middle of a stage (2026-11-08).
	if _, ok := tester.Find("Viewed"); ok {
		t.Errorf("a narrow header must not draw a bare Viewed box: %q", tester.Texts())
	}
}

// A wide card — the window with room to spare — has room for everything, so
// nothing steps aside.
func TestDiffHeaderKeepsEveryControlWhenWide(t *testing.T) {
	shell := gdHeaderShell(t)
	tester := ui.NewTester(shell.View, 1700, 800)
	tester.Frame()

	if _, ok := tester.Find("Open in Editor"); !ok {
		t.Fatalf("a wide header lost its Open button: %q", tester.Texts())
	}
	viewed, ok := tester.Find("Viewed")
	if !ok || viewed.W < 80 {
		t.Fatalf("a wide header shows Viewed with its label, got %v (found %v)", viewed, ok)
	}
	name, ok := tester.Find("diff_surface.go")
	if !ok || name.W < 100 {
		t.Fatalf("file name = %v (found %v)", name, ok)
	}
	if dir, ok := tester.Find("internal/nativeui/"); !ok || dir.Y != name.Y {
		t.Errorf("a wide header keeps the folder on the name's line: dir=%v (found %v) name=%v", dir, ok, name)
	}
}

// The Detail card is the center, less the sidebar and a docked right panel
// (its card and the gutter), and the whole of it behind the Commit editor and
// beside a floating panel.
func TestGdCardWidth(t *testing.T) {
	shell := gdHeaderShell(t)
	var ctxWidth float32
	probe := func(c *ui.Context) {
		ctxWidth = shell.gdCardWidth(c)
		ui.Box(c)
	}

	// Docked: the panel's card and gutter come off the center.
	docked := shell.sidePanelDockedWidth(1340)
	if docked <= 0 {
		t.Fatalf("the fixture's panel must dock at 1340 px, got %v", docked)
	}
	ui.NewTester(probe, 1340, 800).Frame()
	want := 1340 - sidebarWidth - docked - 2*gdListMargin
	if ctxWidth != want {
		t.Fatalf("beside the docked panel the card width = %v, want %v", ctxWidth, want)
	}

	// With the sidebar hidden the card has its width back.
	shell.sidebarCollapsed = true
	ui.NewTester(probe, 1340, 800).Frame()
	if ctxWidth != want+sidebarWidth {
		t.Fatalf("with the sidebar hidden the card width = %v, want %v", ctxWidth, want+sidebarWidth)
	}
	shell.sidebarCollapsed = false

	// Shut, the panel takes nothing.
	shell.closeSidePanel()
	ui.NewTester(probe, 1340, 800).Frame()
	if ctxWidth != 1340-sidebarWidth-2*gdListMargin {
		t.Fatalf("with the panel shut the card width = %v, want %v", ctxWidth, 1340-sidebarWidth-2*gdListMargin)
	}

	// A floating panel (a window too narrow to dock it) takes nothing either:
	// it covers the card instead of squeezing it.
	shell.sidePanel.open = true
	if shell.sidePanelDockedWidth(900) != 0 {
		t.Fatalf("the panel must float at 900 px")
	}
	ui.NewTester(probe, 900, 800).Frame()
	if ctxWidth != 900-sidebarWidth-2*gdListMargin {
		t.Fatalf("beside a floating panel the card width = %v, want %v", ctxWidth, 900-sidebarWidth-2*gdListMargin)
	}
}
