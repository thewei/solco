package nativeui

/**
 * [INPUT]: right_panel_files.go 的目录树（忽略过滤、行右键菜单）、files_ops.go 的只读路径助手、headless ui.Tester
 * [OUTPUT]: 面板 Files 页的树契约——忽略项默认隐藏且开关可见，行右键菜单只有 Copy Path / Reveal in Finder（新建/重命名/删除只活在 Solco Git）；filesOpsTestShell 是所有 Files 预览测试共用的夹具
 * [POS]: 2026-11-08 G5：Files 整页退役后，原 files_ops_test.go 的夹具与忽略过滤测试迁到面板 Files 页，新建/重命名/删除三条测试随功能一起删除，换成"面板只读"守卫
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// showFilesPanel stands in for the retired Files page: the right panel opens
// on Files, the way the user's own tab choice would leave it.
func showFilesPanel(s *Shell) {
	s.sidePanel.open = true
	s.setSidePanelPage(sidePanelFiles)
}

// filesOpsTestShell is a shell on an empty temp folder with the panel open on
// Files and the first directory read landed.
func filesOpsTestShell(t *testing.T) (*Shell, *ui.Tester, string) {
	t.Helper()
	root := t.TempDir()
	s := rightPanelTestShell(t, root)
	showFilesPanel(s)
	tester := ui.NewTester(s.View, 1200, 800)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true}) // rows land at once (treeRowMotion)
	tester.Frame()
	tester.Frame() // second frame lets the headless directory read land
	return s, tester, root
}

// TestFilesTreeHidesIgnoredNamesUntilToggled pins the tree-side ignore
// filter: node_modules stays out of the render until "Show ignored files"
// turns on, and the empty-state rows tell the two cases apart.
func TestFilesTreeHidesIgnoredNamesUntilToggled(t *testing.T) {
	s, tester, root := filesOpsTestShell(t)
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "pkg", "index.js"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(root, "placeholder")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	s.refreshFiles()
	tester.Frame()
	if tester.HasText("node_modules") {
		t.Fatalf("node_modules must hide by default: %q", tester.Texts())
	}
	if !tester.HasText("README.md") {
		t.Fatalf("ordinary entries must stay visible: %q", tester.Texts())
	}

	// Expand the empty directory via direct state (headless click plumbing
	// is covered elsewhere): it renders the "Empty" quiet row.
	s.rightPanel.expandedDirs[empty] = true
	settleFrames(tester, 6) // the first frame asks for the folder; a later one shows what it holds
	if !tester.HasText("Empty") {
		t.Fatalf("empty directory state missing: %q", tester.Texts())
	}

	s.rightPanel.showIgnored = true
	tester.Frame()
	if !tester.HasText("node_modules") {
		t.Fatalf("toggle must reveal ignored entries: %q", tester.Texts())
	}
}

// TestFilesPanelRowMenuIsReadOnly pins decision (3) of 2026-11-07 as G5
// landed it: the panel's Files tree writes nothing — a row's right-click menu
// carries the two path helpers and no new / rename / delete, and the toolbar
// no longer creates files. Those operations live only in Solco Git.
func TestFilesPanelRowMenuIsReadOnly(t *testing.T) {
	s, tester, root := filesOpsTestShell(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.refreshFiles()
	tester.Frame()
	if err := tester.RightClick("README.md"); err != nil {
		t.Fatalf("no tree row to right-click: %v", err)
	}
	menu := strings.Join(tester.Menu(), "\n")
	tester.CloseMenu()
	for _, keep := range []string{"Copy Path", "Reveal in Finder"} {
		if !strings.Contains(menu, keep) {
			t.Errorf("the row menu lost %q\n%s", keep, menu)
		}
	}
	for _, gone := range []string{"New File", "New Folder", "Rename", "Delete", "Move to Trash"} {
		if strings.Contains(menu, gone) {
			t.Errorf("the row menu still offers %q — file operations live only in Solco Git\n%s", gone, menu)
		}
	}
	for _, gone := range []string{"New File", "New Folder"} {
		if _, ok := tester.Access(gone); ok {
			t.Errorf("the Files toolbar still offers %q", gone)
		}
	}
}
