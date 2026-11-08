package nativeui

/**
 * [INPUT]: files_ops.go（filesPathItems、filesQuickLook）、files_native_preview.go 各正文的右键菜单、右侧面板 Files 树
 * [OUTPUT]: 2026-11-08 Quick Look 回归：树行（文件有、文件夹没有）、代码视图、Markdown 渲染视图、SVG 图像视图的右键（位图另由源码守卫钉住接线）都有 Quick Look 且作用在当前文件上
 * [POS]: 只验证菜单接线（用 filesQuickLook 变量拦下真实的 qlmanage）；Quick Look 本身是系统的
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// watchQuickLook replaces the Quick Look launcher for the test and returns
// the paths it was asked to show.
func watchQuickLook(t *testing.T) *[]string {
	t.Helper()
	var shown []string
	prev := filesQuickLook
	filesQuickLook = func(path string) { shown = append(shown, path) }
	t.Cleanup(func() { filesQuickLook = prev })
	return &shown
}

// rightClickBody right-clicks in the middle of the Detail card's body, below
// its header (whose close button marks the card's top right).
func rightClickBody(t *testing.T, tester *ui.Tester) []string {
	t.Helper()
	header, ok := tester.Find("Close Detail")
	if !ok {
		t.Fatalf("no Detail card to place the body by: %q", tester.Texts())
	}
	tester.RightClickAt(header.X-300, header.Y+300)
	menu := tester.Menu()
	if menu == nil {
		t.Fatal("the body has no context menu")
	}
	return menu
}

func menuHas(menu []string, label string) bool {
	for _, item := range menu {
		if item == label {
			return true
		}
	}
	return false
}

func TestFilesQuickLookIsOnEveryFileSurface(t *testing.T) {
	shown := watchQuickLook(t)
	s, tester, root := filesOpsTestShell(t)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	notes := writePreviewFile(t, root, "notes.md", []byte("# Notes\n\nbody text\n"))
	logo := writePreviewFile(t, root, "logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20" fill="#c00"/></svg>`))
	code := writePreviewFile(t, root, "main.go", []byte("package main\n\nfunc main() {}\n"))
	s.refreshFiles()
	settleFrames(tester, 4)

	// The tree: a file's row has it, a folder's does not.
	if err := tester.RightClick("notes.md"); err != nil {
		t.Fatalf("no tree row to right-click: %v", err)
	}
	if menu := tester.Menu(); !menuHas(menu, "Quick Look") || !menuHas(menu, "Copy Path") || !menuHas(menu, "Reveal in Finder") {
		t.Fatalf("a file row offers Quick Look with the path helpers: %q", menu)
	} else if i, j := indexOf(menu, "Quick Look"), indexOf(menu, "Copy Path"); i > j {
		t.Fatalf("Quick Look comes first, as the old page's menu had it: %q", menu)
	}
	if err := tester.ChooseMenuItem("Quick Look"); err != nil {
		t.Fatal(err)
	}
	if len(*shown) != 1 || (*shown)[0] != notes {
		t.Fatalf("Quick Look must show the row's file, got %q", *shown)
	}
	if err := tester.RightClick("docs"); err != nil {
		t.Fatalf("no folder row: %v", err)
	}
	if menu := tester.Menu(); menuHas(menu, "Quick Look") || !menuHas(menu, "Reveal in Finder") {
		t.Fatalf("a folder has nothing to look at, only the path helpers: %q", menu)
	}
	tester.CloseMenu()

	for name, tc := range map[string]struct {
		path  string
		extra []string
	}{
		"Markdown, rendered": {notes, []string{"Copy", "Select All"}},
		"SVG, drawing":       {logo, nil},
		"code":               {code, nil},
	} {
		*shown = nil
		s.openFileDetail(tc.path)
		tester.Frame()
		tester.Frame()
		menu := rightClickBody(t, tester)
		for _, want := range append([]string{"Quick Look", "Copy Path", "Reveal in Finder"}, tc.extra...) {
			if !menuHas(menu, want) {
				t.Errorf("%s: the body's menu lost %q: %q", name, want, menu)
			}
		}
		if err := tester.ChooseMenuItem("Quick Look"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(*shown) != 1 || (*shown)[0] != tc.path {
			t.Errorf("%s: Quick Look must show the open file %q, got %q", name, tc.path, *shown)
		}
	}
}

// TestImagePreviewKeepsItsContextMenu: a decoded image is a GPU bitmap, which a
// headless window cannot hold, so its card cannot be right-clicked here; the
// wiring is pinned in the source instead — the image's body carries the same
// file menu (Quick Look, Copy Path, Reveal in Finder) as the other previews.
func TestImagePreviewKeepsItsContextMenu(t *testing.T) {
	src, err := os.ReadFile("files_native_preview.go")
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(src), "case st.nativeImage != nil:")
	if !ok {
		t.Fatal("the image's case is gone from nativePreviewBody; update this guard with it")
	}
	body, _, _ := strings.Cut(after, "case st.webImage:")
	if !strings.Contains(body, "ContextMenu(s.previewFileMenu)") {
		t.Errorf("the image preview lost its file menu: %.200q", body)
	}
}

func indexOf(items []string, label string) int {
	for i, item := range items {
		if item == label {
			return i
		}
	}
	return -1
}
