package nativeui

/**
 * [INPUT]: external_reveal.go 的 RouteExternalURL 分发与 runtime.go 的 applyProjection 收口、sidebar_sections.go 的 createWorkspaceAt（Herdr workspace.create）、selection.go 的 selectProject、machines.go 的 activeIsRemote
 * [OUTPUT]: 对外提供 parseTerminalURL（solco://terminal?cwd=<绝对路径>）、openTerminalAt（已有根目录等于该文件夹的 Workspace 就切过去并显示它的终端，没有就按文件夹新建一个；本机 Session 才行）、tryPendingTerminal（Session 还没就绪时等到投影到达，15 秒过期）
 * [POS]: Solco Git「终端归属：In Solco」的落点（2026-11-13 决定第 (5) 条）：终端始终由 Herdr 拥有，这里只做选择与新建；链接没有 command 参数——任何应用 / 网页都能触发自定义链接，所以它至多在一个已存在的本机文件夹里开一个空 shell；路径不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// terminalCWDMax bounds the folder a link may name.
const terminalCWDMax = 4096

type terminalRequest struct {
	cwd string
	at  time.Time
}

// parseTerminalURL accepts solco://terminal?cwd=<absolute path> and nothing
// else: no command, no other parameters taken.
func parseTerminalURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "solco" || u.Host != "terminal" {
		return "", false
	}
	cwd := u.Query().Get("cwd")
	if cwd == "" || len(cwd) > terminalCWDMax || strings.ContainsRune(cwd, 0) || !filepath.IsAbs(cwd) {
		return "", false
	}
	return filepath.Clean(cwd), true
}

// sameFolder reports two paths that name one folder, symlinks resolved.
func sameFolder(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if samePath(a, b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && samePath(ra, rb)
}

// openTerminalAt shows the terminal of the Workspace rooted at cwd, creating
// the Workspace when none is. Call on the window lane.
func (s *Shell) openTerminalAt(cwd string) {
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		s.pendingToast = "That folder isn't there any more"
		return
	}
	s.pendingTerminal = &terminalRequest{cwd: cwd, at: time.Now()}
	slog.Info("external terminal requested")
	s.tryPendingTerminal()
}

// tryPendingTerminal acts on the request once there is a projection to look
// in; an expired one is dropped.
func (s *Shell) tryPendingTerminal() {
	req := s.pendingTerminal
	if req == nil {
		return
	}
	if time.Since(req.at) > revealPendingTTL {
		s.pendingTerminal = nil
		return
	}
	if s.activeInstance == "" {
		return // applyProjection resumes it
	}
	s.pendingTerminal = nil
	if s.activeIsRemote() {
		s.pendingToast = "Switch to a local Session to open a terminal in a local folder"
		return
	}
	s.hideQuickPanel()
	if s.router.Path() != routeWorkspace {
		s.router.Push(routeWorkspace)
	}
	for _, project := range s.projection.Projects {
		if project.CWD != "" && sameFolder(project.CWD, req.cwd) {
			s.selectProject(project.ID)
			s.showPaneSurface(s.selectedPaneID)
			s.activateWindow()
			return
		}
	}
	if s.createFolderWorkspace != nil {
		s.createFolderWorkspace(req.cwd)
		return
	}
	s.createWorkspaceAt(req.cwd) // Herdr creates it with a root terminal; the result is adopted
}
