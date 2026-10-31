package nativeui

/**
 * [INPUT]: 依赖 workboard_library 的 Library 缓存与 projectlibrary.TaskRefs、internal/gitworkbench Runner.Snapshot（任务工作树的未提交改动，只读）、internal/workboard Client.TaskSettings（`show <id> --json`）与 Tell/Describe 入口、workboard_page 的 workboardPage 缓存
 * [OUTPUT]: loadTaskChanges、loadTaskSettings（任务页 Changes / Settings 页签的一次性只读读取）
 * [POS]: /task 页（Projects WebView）第二层信息的数据来源：Changes = 任务工作树里尚未提交的文件与 +/-（gitworkbench.Snapshot 只读，不另起 Git 路径）；Settings = `show <id> --json` 里只列非内置来源的项；页面只读缓存，渲染路径无 IO
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"time"

	"github.com/thewei/solco/internal/gitworkbench"
	"github.com/thewei/solco/internal/herdr"
)

// changesReadTimeout bounds one worktree snapshot.
const changesReadTimeout = 15 * time.Second

// loadTaskChanges reads the worktree's uncommitted changes once.
func (s *Shell) loadTaskChanges(worktree string) {
	page := &s.workboard.page
	if worktree == "" || page.changesLoading || page.changesFor == worktree {
		return
	}
	page.changesLoading = true
	id := page.id
	workboardGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), changesReadTimeout)
		defer cancel()
		snap, err := (&gitworkbench.Runner{}).Snapshot(ctx, worktree)
		s.applyOnWindow(func() {
			page := &s.workboard.page
			page.changesLoading = false
			if page.id != id {
				return
			}
			page.changesFor = worktree
			page.changes = snap
			if err != nil {
				page.changes = nil
			}
		})
	})
}

// loadTaskSettings reads what the task runs on once per page.
func (s *Shell) loadTaskSettings() {
	page := &s.workboard.page
	if page.id == "" || page.settingsLoading || page.settings != nil || !s.workboardAvailable() {
		return
	}
	page.settingsLoading = true
	id := page.id
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	workboardGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), workboardRefreshTimeout)
		defer cancel()
		view, err := client.TaskSettings(ctx, id)
		s.applyOnWindow(func() {
			page := &s.workboard.page
			page.settingsLoading = false
			if page.id != id || err != nil {
				return
			}
			page.settings = &view
		})
	})
}
