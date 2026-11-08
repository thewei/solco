package nativeui

/**
 * [INPUT]: filesview.Entry（树行/搜索命中）、openExternalPathParent/copyToClipboard
 * [OUTPUT]: filesRowMenu（右侧面板 Files 树行与搜索命中的右键菜单）、filesPathItems（Copy Path / Reveal in Finder 两个只读路径助手）
 * [POS]: 面板 Files 页只读：新建/重命名/删除（含确认与选中迁移）随 Files 整页于 2026-11-08 G5 退役，只活在 Solco Git（决定记录第 (3) 条）；此处不写磁盘、不碰 git index、不新增 Herdr 方法
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/filesview"
)

// filesRowMenu is a tree row's right-click menu: the read-only path helpers.
func (s *Shell) filesRowMenu(m *ui.Menu, e filesview.Entry) {
	s.filesPathItems(m, e.Path)
}

// filesPathItems are the read-only path helpers of one file or folder.
func (s *Shell) filesPathItems(m *ui.Menu, path string) {
	if m.Item("Copy Path").Chosen() {
		s.copyToClipboard(path)
	}
	if m.Item("Reveal in Finder").Chosen() {
		s.openExternalPathParent(path)
	}
}
