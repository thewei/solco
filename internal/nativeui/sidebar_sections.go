package nativeui

/**
 * [INPUT]: 依赖 sidebar_tree 的 sidebarSectionHeader 共享树网格与 components 的 treeRowAction、mygo.Dialog 的系统文件夹选择器、Herdr 的 CreateProject
 * [OUTPUT]: 对外提供 sidebarSection（Workspace 侧栏一个可折叠分区壳）、workspaceSection（Workspace 分区 + 标题 “+” 从文件夹新建）、createWorkspaceFromFolder
 * [POS]: Workspace 侧栏的分区呈现；原 session_selector.go 的页脚会话芯片已被 device_switcher.go 的设备/会话切换器取代，分区壳留在这里
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

// sidebarSection is one collapsible block of the Workspace sidebar: the shared
// tree header (chevron, semibold title, count — the same row the Git
// references and the Hosts groups wear) over its items while open. The fold
// state is the caller's open flag.
func (s *Shell) sidebarSection(c *ui.Context, spec sidebarSectionSpec, open *bool, items func()) {
	ui.Column(c).Shrink(0).Children(func() {
		spec.Closed = !*open
		if _, clicked := s.sidebarSectionHeader(c, spec); clicked {
			*open = !*open
		}
		if *open {
			items()
		}
	})
}

// workspaceSection renders the Workspace section (2026-10-05 round three:
// "Projects" renamed to match the Herdr fact — a sidebar entry IS a Herdr
// Workspace) with a "+" on the header that creates one from a system
// folder.
func (s *Shell) workspaceSection(c *ui.Context) {
	add := &treeRowAction{Label: "New Workspace from Folder…", Glyph: iconPlus, Run: s.createWorkspaceFromFolder}
	s.sidebarSection(c, sidebarSectionSpec{
		Key: "section:workspace", Title: "Workspace", Count: len(s.projection.Projects), Action: add,
		Menu: func(m *ui.Menu) {
			if m.Item(add.Label).Chosen() {
				add.Run()
			}
		},
	}, &s.projectsOpen, func() {
		s.projectItems(c)
	})
}

// createWorkspaceFromFolder picks a system folder and creates a Herdr
// Workspace rooted there (workspace.create stays the runtime authority).
func (s *Shell) createWorkspaceFromFolder() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:     "Choose a folder for the new Workspace",
			Directory: true,
		})
		s.applyOnUI(func() {
			if err != nil || len(paths) == 0 || paths[0] == "" {
				return
			}
			cwd := paths[0]
			label := filepath.Base(cwd)
			if s.activeInstance == "" {
				s.status = "Choose a Session first"
				return
			}
			if s.activeIsRemote() {
				// The folder picker can only see this machine's disk; a
				// remote workspace needs a path that exists on the remote.
				s.status = "Switch to a local Session to add a Workspace from a local folder"
				return
			}
			s.mutateSelecting(func() (herdr.Projection, error) {
				return s.runtime.CreateProject(s.activeInstance, cwd, label)
			})
		})
	}()
}
