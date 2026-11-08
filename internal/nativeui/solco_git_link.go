package nativeui

/**
 * [INPUT]: internal/solcogit 的 Link（找得到 Solco Git 吗、请它打开）、side_panel.go 的当前页与右侧面板的选中文件、git_service 的仓库根与选中的变更文件、selection.go 的选中 Tab 目录
 * [OUTPUT]: 对外提供 WithSolcoGit（生产接线；没接的 Shell——所有 headless 测试——一个入口都不画）、Shell.solcoGitReady（入口显不显示）、openInSolcoGit（三处入口共用的唯一动作）、solcoGitPanelTarget / solcoGitRepoTarget（传什么：仓库、视图、文件）
 * [POS]: 2026-11-07 决定「Solco Git and the right panel」第 2、5 条的 Solco 一半：面板头部按钮（面板当前页 + 选中文件）、项目行菜单（被点那一行的项目，Changes）、Command Center（选中 Tab 的仓库，Changes）三处入口，找不到 Solco Git 就一处都不画；打开的参数（路径、文件）从不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/thewei/solco/internal/solcogit"
)

// solcoGitLink is the part of internal/solcogit the presentation uses; a test
// puts a fake in its place.
type solcoGitLink interface {
	Available() bool
	Open(repo string, view solcogit.View, file string) error
}

// WithSolcoGit wires the link to the Solco Git app. A shell without one (every
// headless test) draws none of the three entries.
func WithSolcoGit(link solcoGitLink) ShellOption {
	return func(s *Shell) { s.solcoGit = link }
}

// solcoGitReady is whether to draw the entries: Solco Git is on this machine.
func (s *Shell) solcoGitReady() bool {
	return s.solcoGit != nil && s.solcoGit.Available()
}

// solcoGitRepo is the repository the selected Tab works in: its Git root once
// known, else the Tab's folder (Solco Git resolves the root from any path in
// it).
func (s *Shell) solcoGitRepo() string {
	if root := s.gitRoot(); root != "" {
		return root
	}
	return s.selectedTabCWD()
}

// solcoGitRepoTarget is what the palette asks for: the selected Tab's
// repository on Changes, no file.
func (s *Shell) solcoGitRepoTarget() (repo string, view solcogit.View, file string) {
	return s.solcoGitRepo(), solcogit.Changes, ""
}

// solcoGitPanelTarget is what the panel's button asks for: the selected Tab's
// repository on the page the panel shows, with the file selected there
// (repository-relative; none when nothing is selected or it is outside the
// repository).
func (s *Shell) solcoGitPanelTarget() (repo string, view solcogit.View, file string) {
	repo = s.solcoGitRepo()
	if s.sidePanelPageNow() == sidePanelFiles {
		view = solcogit.Files
		if abs := s.rightPanel.selectedFilePath; abs != "" && repo != "" {
			if rel, err := filepath.Rel(repo, abs); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				file = filepath.ToSlash(rel)
			}
		}
		return repo, view, file
	}
	if cf, ok := s.selectedChangeFile(); ok {
		file = cf.Path
	}
	return repo, solcogit.Changes, file
}

// openInSolcoGit is the one action behind the three entries. The call is
// handed to macOS off the UI lane (headless runs inline); a refusal shows in
// the status line. The log names the operation and the outcome — never the
// repository, the file or the link (2026-11-07 decision, item 5).
func (s *Shell) openInSolcoGit(repo string, view solcogit.View, file string) {
	link := s.solcoGit
	if link == nil {
		return
	}
	if repo == "" {
		s.status = "Select a workspace to open in Solco Git"
		return
	}
	run := func() {
		err := link.Open(repo, view, file)
		slog.Info("open in solco git", "view", string(view), "ok", err == nil)
		if err != nil {
			s.applyOnWindow(func() { s.status = "Could not open Solco Git" })
		}
	}
	if s.win == nil {
		run()
		return
	}
	go run()
}
