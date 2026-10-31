package nativeui

/**
 * [INPUT]: 依赖主窗口 mygo.WebView（ui.WebView 元素）、projects_view 的快照、webviewBackground
 * [OUTPUT]: Shell.projectsWebviewFor（惰性创建 + 失败锁存）、Shell.projectsPageBody（/project 与 /task 共用的页面体）、Shell.projectsPublishIfChanged（渲染内无 IO 的变更检测 + 有序发布）
 * [POS]: 产品决策 2026-10-31：Projects 整套表面是一个 WebView 页（同一嵌入 SPA 的 /projects 路由），原生 Project/任务页被它整体替换；WebView 只在帧构建它的 ui.WebView 元素时显示，路由离开即隐藏；创建失败时显示真实空态，不回退到原生页
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"bytes"
	"encoding/json"
	"log/slog"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// projectsViewEvent carries every changed snapshot to the page. Events
// emitted before the page is ready are replayed by the runtime.
var projectsViewEvent = mygo.NewEvent[ProjectsView]("projects:view")

// projectsWebviewFor returns the lazily created Projects page WebView, or nil
// when no window exists or creation failed once (latched). Call on the UI lane.
func (s *Shell) projectsWebviewFor() *mygo.WebView {
	if s.win == nil || s.projectsWebviewBroken {
		return nil
	}
	if s.projectsWebview == nil {
		view, err := s.win.NewWebView(mygo.WebViewOptions{URL: "/projects", BackgroundColor: webviewBackground})
		if err != nil {
			s.projectsWebviewBroken = true
			slog.Warn("create projects webview failed", "error", err)
			return nil
		}
		// The page never navigates away; safe links go to the system browser.
		view.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
			e.PreventDefault()
			if pageNavigationDecision(e.URL, e.UserInitiated) {
				go func() { _ = mygo.Shell.OpenExternal(e.URL) }()
			}
		})
		s.projectsWebview = view
	}
	return s.projectsWebview
}

// projectsHosts reports whether the Projects page is the route on screen for
// project (any Project when empty): the Project home of it, or a task of it.
func (s *Shell) projectsHosts(project string) bool {
	switch s.router.Path() {
	case routeProject:
		return project == "" || s.workboard.project.name == project
	case routeTask:
		if project == "" {
			return true
		}
		task, ok := s.workboardTaskByID(s.workboard.page.id)
		return ok && fallbackText(task.Project(), workboardOtherProject) == project
	}
	return false
}

// projectsPageBody is the body of /project and /task.
func (s *Shell) projectsPageBody(c *ui.Context) {
	if !s.workboardAvailable() {
		emptyState(c, "The work board isn't available", "")
		return
	}
	s.projectsEnter()
	s.projectsPublishIfChanged()
	s.projectsChatSync()
	view := s.projectsWebviewFor()
	if view == nil {
		emptyState(c, "Projects could not start", "Restart Solco to try again.")
		return
	}
	ui.WebView(c, view).Grow(1)
}

// projectsEnter runs once per subject the page opens on: it reads that
// Project's library (small, local) so the Library tab and the New Task sheet
// have it at once.
func (s *Shell) projectsEnter() {
	key, project := routeProject+"|"+s.workboard.project.name, s.workboard.project.name
	if s.router.Path() == routeTask {
		key = routeTask + "|" + s.workboard.page.id
		if task, ok := s.workboardTaskByID(s.workboard.page.id); ok {
			project = task.Project()
		}
	}
	if key == s.projectsSubject {
		return
	}
	s.projectsSubject = key
	if project != "" && project != workboardOtherProject && s.libraryStore() != nil && s.workboard.library.project != project {
		s.reloadLibrary(project)
	}
}

// projectsPublishIfChanged builds the snapshot and, when it differs from the
// last one sent, delivers it. Called from the frame builder: the build does no
// IO, and the emit hops to the UI lane in a follow-up update, never inside the
// frame being built.
func (s *Shell) projectsPublishIfChanged() {
	if s.win == nil {
		return
	}
	view := s.projectsView()
	body, err := json.Marshal(view)
	if err != nil || bytes.Equal(body, s.projectsLast) {
		return
	}
	s.projectsLast = body
	s.projectsPublish()
}

// projectsPublish emits the current snapshot from a follow-up update on the
// UI lane; changes that arrive meanwhile ride the same emit (it builds last).
func (s *Shell) projectsPublish() {
	if s.win == nil || s.projectsPublishing {
		return
	}
	s.projectsPublishing = true
	s.win.Update(func() {
		s.projectsPublishing = false
		view := s.projectsView()
		if body, err := json.Marshal(view); err == nil {
			s.projectsLast = body
		}
		s.projectsSeq++
		view.Seq = s.projectsSeq
		_ = projectsViewEvent.Emit(s.win, view)
	})
}
