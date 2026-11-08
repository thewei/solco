package nativeui

/**
 * [INPUT]: filesview.Entry（树行/搜索命中）、openExternalPathParent/copyToClipboard
 * [OUTPUT]: filesRowMenu（右侧面板 Files 树行与搜索命中的右键菜单）、filesPathItems（Quick Look（仅文件）/ Copy Path / Reveal in Finder 三个只读助手；树行、搜索命中、编辑器/Markdown/图片/SVG 正文的右键共用）、filesQuickLook（测试可替换的 Quick Look 入口）
 * [POS]: 面板 Files 页只读：新建/重命名/删除（含确认与选中迁移）随 Files 整页于 2026-11-08 G5 退役，只活在 Solco Git（决定记录第 (3) 条）；此处不写磁盘、不碰 git index、不新增 Herdr 方法
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/filesview"
)

// filesQuickLook opens macOS Quick Look on a file; a variable so a test can
// watch it instead of launching the viewer.
var filesQuickLook = quickLook

// filesRowMenu is a tree row's right-click menu: the read-only path helpers.
func (s *Shell) filesRowMenu(m *ui.Menu, e filesview.Entry) {
	s.filesPathItems(m, e.Path, !e.IsDir && !e.TargetDir)
}

// filesPathItems are the read-only helpers of one file or folder: Quick Look
// (a file has something to look at; the old preview page's right-click had it,
// restored 2026-11-08), Copy Path, Reveal in Finder.
func (s *Shell) filesPathItems(m *ui.Menu, path string, file bool) {
	if file && m.Item("Quick Look").Chosen() {
		filesQuickLook(path)
	}
	if m.Item("Copy Path").Chosen() {
		s.copyToClipboard(path)
	}
	if m.Item("Reveal in Finder").Chosen() {
		s.openExternalPathParent(path)
	}
}
