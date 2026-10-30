package nativeui

import (
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

/**
 * [INPUT]: 依赖 gitService 的已缓存 ChangesSnapshot、现有 stage/unstage/commit/undo/stash 方法
 * [OUTPUT]: Diff Git Workbench 的标题栏工具（gitHeaderTools：文件栏开关、Fetch/Pull/Push 图标、Split/Unified，仅 Changes 视图）和并入页面 Actions 菜单的 Git 项（gitActionItems：Stage/Unstage All 只在这里；窗格头的 Stage/Unstage 已按选区或整窗格操作）
 * [POS]: nativeui 统一 Git 行为入口；Diff 拥有文件审阅，弹层拥有轻量操作，Terminal 不重复 Git toolbar
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// gitHeaderToolsShown reports whether the title bar carries the Git tools:
// the local worktree's Changes view only. A historical commit diff is
// read-only and shows no mutation UI.
func (s *Shell) gitHeaderToolsShown() bool {
	return s.git != nil && s.router.Path() == routeWorkspace &&
		s.surface.current() == WorkspaceSurfaceDiff && !s.gdReviewingCommit() && s.gitSnapshot() != nil
}

// gitHeaderTools is the Changes view's share of the title bar's trailing
// tools (2026-10-30; it was a toolbar row above the diff): the file list
// toggle, Fetch/Pull/Push with the ahead/behind counts, and the Split/Unified
// switch. It sits ahead of the page Actions, so Actions keeps its place from
// the right edge on every route. The less common actions are in that Actions
// menu (gitActionItems). The branch selector lives in the repo bar, and the
// Commit entry is the bottom composer.
func (s *Shell) gitHeaderTools(c *ui.Context) {
	sp := Spacing()
	ui.Row(c).Gap(sp.XS).AlignItems(ui.Center).Shrink(0).Children(func() {
		s.gitFilePaneToggle(c)
		s.gdSyncButtons(c, gdPaletteFor(c.Theme()))
		// Split/Unified must remain reachable even when actions compact.
		s.gdLayoutControl(c)
	})
}

// gitActionItems adds the Git actions to the page Actions menu of the
// Changes view: batch stage/unstage, stash, undo, and the way to Commits and
// Git settings.
func (s *Shell) gitActionItems(m *ui.Menu) {
	snap := s.gitSnapshot()
	if snap == nil {
		return
	}
	stage, unstage := gitActionPaths(snap)
	busySequencer := s.git.sequencerChecked && s.git.sequencer.Active()
	canStage := !s.git.opBusy && len(stage) > 0 && (!busySequencer || s.git.sequencer.Unmerged == 0)
	canUnstage := !s.git.opBusy && len(unstage) > 0 && !busySequencer
	if m.Item("Stage All").Disabled(!canStage).Chosen() {
		s.stageFiles(stage)
	}
	if m.Item("Unstage All").Disabled(!canUnstage).Chosen() {
		s.unstageFiles(unstage)
	}
	m.Separator()
	if m.Item("Stash All Changes").Disabled(s.git.opBusy || busySequencer || len(snap.Files) == 0).Chosen() {
		s.stashChanges()
	}
	if m.Item("Undo Last Commit…").Disabled(s.git.opBusy || busySequencer || snap.NoHead || snap.Branch == "").Chosen() {
		s.undoLastCommit()
	}
	if m.Item("View Commits").Chosen() {
		s.showGitCommits()
	}
	m.Separator()
	if m.Item("Git Settings…").Chosen() {
		s.router.Push("/settings/git")
	}
}

func gitActionPaths(snap *gitworkbench.ChangesSnapshot) (stage, unstage []string) {
	if snap == nil {
		return nil, nil
	}
	for _, file := range snap.Files {
		if file.Untracked || file.Unstaged {
			stage = append(stage, file.Path)
		}
		if file.Staged {
			unstage = append(unstage, file.Path)
		}
	}
	return stage, unstage
}
