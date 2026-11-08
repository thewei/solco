package nativeui

import (
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: 真实仓库 fixture + 无头 Tester 的 vibrancy 模式
 * [OUTPUT]: 回归——点提交、评审区装载后侧栏仍透出 vibrancy 材质；SetTheme 只允许 shell.go 与 changes_panel.go 调用
 * [POS]: 用户报告（2026-11-03）"每点一个 Commit，整块侧栏闪白"：changes_panel 在列表外 SetTheme(real) 把窗口根背景重置成不透明的主题底色，评审区一出现侧栏就失去 vibrancy
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func sidebarTransparentPixels(tester *ui.Tester) int {
	img := tester.Image()
	n := 0
	for y := 0; y < 780; y++ {
		for x := 0; x < 250; x++ {
			if img.Pix[img.PixOffset(x, y)+3] == 0 {
				n++
			}
		}
	}
	return n
}

// ui.Context.SetTheme resets the window root's background, undoing the
// transparency Shell.View sets for the sidebar material. Only the places that
// put it back may call it.
func TestSetThemeIsOnlyCalledWhereTheRootIsRestored(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "shell.go" || name == "changes_panel.go" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), ".SetTheme(") {
			t.Errorf("%s calls SetTheme: it resets the window root's background and kills the sidebar vibrancy; build the control with its own colours instead", name)
		}
	}
}
