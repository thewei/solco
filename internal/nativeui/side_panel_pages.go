package nativeui

import (
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
)

/**
 * [INPUT]: 依赖 side_panel.go 的面板状态/sidePanelBuilding、detail_surface.go 的 openDetail/openFileDetail、sidebar_switchers.go 的项目头 sidebarProjectHeader、sidebar.go 的 sidebarFilesView（Unstaged/Staged 文件列表）、git_quick_commit.go 的 gitQuickCommitComposer（一行提交输入 + Commit）、git_clean_state.go 的干净态、right_panel_files.go 的 filesToolView
 * [OUTPUT]: 对外提供 sidePanelChangesPage（项目头 + 文件列表 + 提交栏）、sidePanelFilesPage（项目头 + 目录树）、sidePanelSelectChange（选中文件并在中心打开 Changes 详情卡片）、stagedPaneCap（Staged 列表的最高值，按面板列表高度取份额）
 * [POS]: 右侧面板两页的内容层，2026-11-08 决定「The panel is a list」：面板只列条目——Changes 与 Files——不再画 Diff 或预览；点击的条目在中心以 Detail 卡片显示（detail_surface.go）。只拼装既有部件，不新增 Git/Files 实现；Commit… 仍走唯一的 Commit 表面（提交或取消后回到发起它的 Terminal/Chat/Detail，见 landAfterCommit）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// sidePanelListChrome is what the Changes page spends on the head, the
// section headers' own padding and the commit bar: the list is the card's body
// less this, which stagedPaneCap shares out.
const sidePanelListChrome = float32(110)

// sidePanelChangesPage is T3 Code's Changes as a list: the project head, the
// changed files, then the commit bar. Every piece is the retired Git page's
// own — the same lists, rows, menus and Commit modal. A click on a file opens
// its diff in the center (the Detail card), so the card stays a list however
// wide the panel is. The head is the shared sidebarProjectHeader: project and
// branch switch in place (merge and delete ride the branch rows), so the
// branch stays reachable now that the Git page's sidebar is gone.
func (s *Shell) sidePanelChangesPage(c *ui.Context, bodyH float32) {
	g := s.git
	root := s.selectedTabCWD()
	if g != nil && g.root != "" {
		root = g.root
	}
	s.sidebarProjectHeader(c, root, "Refresh repository", s.refreshGitChanges, nil)
	switch {
	case g == nil || g.root == "":
		emptyState(c, "No Git repository", gdAbbreviateHome(s.selectedTabCWD()))
		return
	case s.gitSnapshot() == nil:
		s.gdThinking(c)
		return
	}
	snap := s.gitSnapshot()
	if len(snap.Files) == 0 {
		// A clean tree is one block, the Git page's own; the bar stays only
		// while a draft or an amend is open (gitQuickCommitComposer).
		s.gitCleanState(c, "Working tree clean", gdAbbreviateHome(g.root))
		s.gitQuickCommitComposer(c)
		return
	}
	s.sidePanelListH = max(bodyH-sidePanelListChrome, 120)
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		s.sidebarFilesView(c)
	})
	s.gitQuickCommitComposer(c)
}

// sidePanelSelectChange picks a changed file in the panel and opens the
// worktree's diff in the center with that file's card at the top: the file
// list's click. It is also what Chat's Review changes does.
func (s *Shell) sidePanelSelectChange(path string, view gdViewSide) {
	s.surface.diff.SelectedPath = path
	if s.git == nil {
		return
	}
	gitworkbench.EnsureAncestorsOpen(s.git.collapsedDirs, path)
	if f, _ := s.gdFileForPath(path); f != nil {
		f.setView(view)
		s.git.gdRowsDirty = true
	}
	s.revealDiffFile(path)
	s.openDetail(DetailChanges)
}

// stagedPaneCap is the tallest the Staged list may stand: half of the panel's
// file list, so the Unstaged list above it never shrinks to nothing.
func (s *Shell) stagedPaneCap() float32 {
	return max(s.sidePanelListH/2, 60)
}

// sidePanelFilesPage is T3 Code's Files as a list: the project head and the
// tree — the retired Files page's left column. A click on a file opens its
// preview in the center (the Detail card); the tree takes the whole card.
func (s *Shell) sidePanelFilesPage(c *ui.Context, _ float32) {
	s.syncRightPanelRoot()
	st := &s.rightPanel
	root := st.currentRoot
	if root == "" {
		emptyState(c, "No active directory", "Select a project tab or pane with a valid working directory.")
		return
	}
	s.sidebarProjectHeader(c, root, "Refresh files", s.refreshFiles, &headerSearch{
		Query: &s.filesSearch.query, Open: &s.filesSearch.open, Label: "Search files", Button: "Search files",
		Changed: s.filesSearchChanged, Submit: s.filesSearchOpenFirst, Tools: s.filesIgnoredToggle,
	})
	ui.Column(c.Key("side-panel-files-tree")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		s.filesToolView(c)
	})
}
