package nativeui

/**
 * [INPUT]: headless 右侧面板 Files 页（真实临时 Git 仓库）、filesview 搜索域、原生 editor 代码视图
 * [OUTPUT]: Files 统一搜索回归（2026-10-30）：搜索钮开合并清空、Files 分段按名排序与点击打开、Content 分段分组/高亮与点击定位到命中行并选中命中文本、Markdown 命中改开源码、「含忽略文件」跟随眼睛开关、换项目重置
 * [POS]: 只验证 nativeui 的调度与接线；枚举/搜索/选项语义由 solco-gitkit/filesview 测试覆盖
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// filesSearchTestShell is the panel's Files page over a real Git working tree (the
// engines read the repository), with the search already opened.
func filesSearchTestShell(t *testing.T) (*Shell, *ui.Tester, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "dep"), 0o700); err != nil {
		t.Fatal(err)
	}
	var lines []byte
	for i := 0; i < 80; i++ {
		lines = append(lines, "filler line\n"...)
	}
	files := map[string]string{
		"main.go":                   "package main\n" + string(lines) + "\tfunc Needle() {}\n",
		"docs/guide.md":             "# Guide\nsee the needle here\n",
		"node_modules/dep/index.js": "needle in a dependency\n",
		"README.txt":                "nothing to find\n",
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := rightPanelTestShell(t, root)
	showFilesPanel(s)
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	tester.Frame()
	if err := tester.Click("Search files"); err != nil {
		t.Fatalf("the sidebar needs a search button: %v; texts=%q", err, tester.Texts())
	}
	tester.Frame()
	if !s.filesSearch.open {
		t.Fatal("the button did not open the search")
	}
	return s, tester, root
}

func TestFilesSearchByNameRanksAndOpens(t *testing.T) {
	s, tester, root := filesSearchTestShell(t)
	tester.Type("gui")
	tester.Frame()
	tester.Frame()
	if s.filesSearch.query != "gui" || !tester.HasText("guide.md") {
		t.Fatalf("typing must list matching names; query=%q texts=%q", s.filesSearch.query, tester.Texts())
	}
	if tester.HasText("main.go") || tester.HasText("README.txt") {
		t.Fatalf("non-matching files must leave the list; texts=%q", tester.Texts())
	}
	if tester.HasText("node_modules") {
		t.Fatal("fixed heavy folders stay out until the ignored toggle is on")
	}
	if err := tester.Click("guide.md"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if want := filepath.Join(root, "docs", "guide.md"); s.rightPanel.selectedFilePath != want {
		t.Fatalf("selected %q, want %q", s.rightPanel.selectedFilePath, want)
	}
}

func TestFilesSearchClosingTheFieldRestoresTheTree(t *testing.T) {
	s, tester, _ := filesSearchTestShell(t)
	tester.Type("gui")
	tester.Frame()
	if !s.filesSearchOn() {
		t.Fatal("a query must turn the results on")
	}
	if err := tester.Click("Close search files"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if s.filesSearch.open || s.filesSearch.query != "" || len(s.filesSearch.hits) != 0 {
		t.Fatalf("closing must drop the query and its results: %+v", s.filesSearch.query)
	}
	if !tester.HasText("main.go") || !tester.HasText("README.txt") {
		t.Fatalf("the tree must be back; texts=%q", tester.Texts())
	}
}

func TestFilesSearchContentGroupsHighlightsAndRevealsTheLine(t *testing.T) {
	s, tester, root := filesSearchTestShell(t)
	tester.Type("needle") // typed first: the switch takes the focus off the field
	tester.Frame()
	if err := tester.Click("Content"); err != nil {
		t.Fatalf("the Content switch is missing: %v; texts=%q", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
	if s.filesSearch.mode != filesSearchText || s.filesSearch.content.Matches == 0 {
		t.Fatalf("content search found nothing: %+v", s.filesSearch.content)
	}
	for _, want := range []string{"main.go", "guide.md", "func Needle() {}"} {
		if !tester.HasText(want) {
			t.Fatalf("results missing %q; texts=%q", want, tester.Texts())
		}
	}
	if tester.HasText("index.js") {
		t.Fatal("node_modules matches stay out while ignored files are hidden")
	}
	if err := tester.Click("func Needle() {}"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	tester.Frame()
	st := &s.rightPanel
	if st.selectedFilePath != filepath.Join(root, "main.go") || st.nativeCode == nil {
		t.Fatalf("the hit must open its file in the code view: %q code=%v", st.selectedFilePath, st.nativeCode != nil)
	}
	if got := st.nativeCode.CursorPos().Line; got != 81 {
		t.Fatalf("caret on line %d (from 0), want 81 — the hit's line", got)
	}
	if got := st.nativeCode.SelectedText(); got != "Needle" {
		t.Fatalf("selected %q, want the matched text", got)
	}
	if st.reveal != nil {
		t.Fatal("the reveal is consumed once applied")
	}
}

func TestFilesSearchMarkdownHitOpensTheSource(t *testing.T) {
	s, tester, root := filesSearchTestShell(t)
	s.filesSearch.mode = filesSearchText
	s.filesSearch.query = "needle here"
	s.filesSearchChanged()
	tester.Frame()
	tester.Frame()
	s.filesSearchOpenFirst() // Enter in the field
	tester.Frame()
	tester.Frame()
	st := &s.rightPanel
	if st.selectedFilePath != filepath.Join(root, "docs", "guide.md") || !st.previewSource || st.nativeCode == nil {
		t.Fatalf("a Markdown hit shows its source: sel=%q source=%v code=%v", st.selectedFilePath, st.previewSource, st.nativeCode != nil)
	}
	if got := st.nativeCode.SelectedText(); got != "needle here" {
		t.Fatalf("selected %q", got)
	}
}

func TestFilesSearchIncludesIgnoredFilesWithTheEye(t *testing.T) {
	s, tester, _ := filesSearchTestShell(t)
	s.filesSearch.mode = filesSearchText
	s.filesSearch.query = "needle"
	s.filesSearchChanged()
	tester.Frame()
	if tester.HasText("index.js") {
		t.Fatal("ignored files must stay out by default")
	}
	if err := tester.Click("Show ignored files"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	tester.Frame()
	if !tester.HasText("index.js") {
		t.Fatalf("the eye must widen the search to ignored files; texts=%q", tester.Texts())
	}
}

func TestFilesSearchResetsWithTheProject(t *testing.T) {
	s, tester, _ := filesSearchTestShell(t)
	tester.Type("gui")
	tester.Frame()
	other := t.TempDir()
	s.rightPanel.currentRoot = other
	s.filesSearchReset()
	if s.filesSearch.open || s.filesSearch.query != "" || s.filesSearch.indexReady {
		t.Fatalf("a new project starts with no search: %+v", s.filesSearch.query)
	}
}
