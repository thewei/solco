package nativeui

/**
 * [INPUT]: 右侧面板的 Changes 页（gitSurfaceTestShell 的种子快照）
 * [OUTPUT]: 面板 Changes 页只有文件列表 + Diff + 提交栏，没有已退役的 Review Inspector / Workspace Tools / 提交历史视图
 * [POS]: 右栏退役后的回归；2026-11-08 G5 起 Git 整页（含 Commits 视图）也已退役，面板是 Changes 的唯一落点
 * [PROTOCOL]: update header and check CLAUDE.md
 */

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestChangesPanelNeverMountsRetiredInspector(t *testing.T) {
	s := gitSurfaceTestShell(t, t.TempDir())
	showChangesPanel(s)
	tt := ui.NewTester(s.View, 1280, 800)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	for _, want := range []string{"Unstaged Files", "Changes"} {
		if !tt.HasText(want) {
			t.Fatalf("Changes panel missing %q: %q", want, tt.Texts())
		}
	}
	for _, gone := range []string{"Review Inspector", "Show Workspace Tools", "History details", "Commits"} {
		if tt.HasText(gone) {
			t.Fatalf("a retired surface (%q) appeared in the Changes panel: %q", gone, tt.Texts())
		}
	}
}
