package nativeui

/**
 * [INPUT]: 依赖 workboard_tasks 的 board 缓存/GroupOf 分组/projectForWorktree、workboard_actions 的 workboardActionsFor/startWorkboardAction、internal/workboard Client.Details、route_header 的 routeHeaderSpec
 * [OUTPUT]: routeTask（/task 路由）、openWorkboardTaskPage（Tasks 行点击的落点）、loadWorkboardPage、splitTaskDock（决定类动词进 dock）、taskRouteHeader（标题栏身份：任务名 + id · 分组）、openBoardLink
 * [POS]: herdr-projects 单个任务页的 Go 一半：id 存在 workboardPage（路由不带 id：任务 id 含 “/”），页面体是 Projects WebView（projects_webview.go），这里只管打开、重读 details 与标题栏；任务从看板消失时页面显示空态并可返回，不猜测；不进会话恢复路由
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/workboard"
)

const routeTask = "/task"

// workboardPage is the open task page: which task, and the freshest details.
type workboardPage struct {
	id      string
	detail  *workboard.Detail
	loading bool

	// Read once per task page, off the UI lane.
	changes         *gitworkbench.ChangesSnapshot
	changesFor      string // the worktree `changes` was read for
	changesLoading  bool
	settings        *workboard.SettingsView
	settingsLoading bool
}

// openWorkboardTaskPage opens the task page from the Tasks tab.
func (s *Shell) openWorkboardTaskPage(id string) {
	if s.workboardBoardMode() {
		s.hideQuickPanel()
		s.openWorkBoard()
		return
	}
	s.hideQuickPanel()
	page := workboardPage{id: id}
	if d, ok := s.workboard.board.details[id]; ok {
		detail := d.detail
		page.detail = &detail
	}
	s.workboard.page = page
	if s.router.Path() == routeTask {
		s.router.Replace(routeTask)
	} else {
		s.router.Push(routeTask)
	}
	s.loadWorkboardPage()
}

// loadWorkboardPage reads the open task's details once, off the UI lane. A
// result for a task the page has since left is dropped.
func (s *Shell) loadWorkboardPage() {
	page := &s.workboard.page
	if page.id == "" || page.loading || !s.workboardAvailable() {
		return
	}
	page.loading = true
	id := page.id
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	workboardGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), workboardRefreshTimeout)
		defer cancel()
		detail, err := client.Details(ctx, id)
		s.applyOnWindow(func() {
			page := &s.workboard.page
			page.loading = false
			if page.id != id || err != nil {
				return
			}
			page.detail = &detail
		})
	})
}

// workboardTaskByID finds a task on the last board read.
func (s *Shell) workboardTaskByID(id string) (workboard.Task, bool) {
	for _, task := range s.workboard.board.tasks {
		if task.ID == id {
			return task, true
		}
	}
	return workboard.Task{}, false
}

// workboardTaskGroup groups a task with the freshest details the page has.
func (s *Shell) workboardTaskGroup(task workboard.Task) workboard.Group {
	if page := s.workboard.page; page.id == task.ID && page.detail != nil {
		return workboard.GroupOf(task, page.detail)
	}
	var detail *workboard.Detail
	if d, ok := s.workboard.board.details[task.ID]; ok {
		detail = &d.detail
	}
	return workboard.GroupOf(task, detail)
}

// taskRouteHeader names the task in the title bar.
func (s *Shell) taskRouteHeader() routeHeaderSpec {
	spec := routeHeaderSpec{title: "Task"}
	task, ok := s.workboardTaskByID(s.workboard.page.id)
	if !ok {
		return spec
	}
	spec.title = fallbackText(task.Title, task.ID)
	spec.facts = task.ID + " · " + s.workboardTaskGroup(task).Label()
	spec.actions = func(m *ui.Menu) {
		if m.Item("Open Work Board").Chosen() {
			s.openWorkBoard()
		}
	}
	return spec
}

// splitTaskDock separates the verbs that move the task forward (approve the
// plan or send it back, pick an attempt, approve the merge): they live in the
// dock above the message box, where the eye lands after reading. The rest stay
// in the button row.
func splitTaskDock(all []boardWrite) (dock, rest []boardWrite) {
	for _, a := range all {
		switch a.Kind {
		case "approve", "approve-plan", "plan-back", "pick":
			dock = append(dock, a)
		default:
			rest = append(rest, a)
		}
	}
	return dock, rest
}

// openBoardLink opens what the plugin gave: a served http(s) report in the
// browser, a file's path (no page server) in the system's opener. Anything
// else is refused.
func (s *Shell) openBoardLink(link string) {
	switch {
	case strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://"):
		if err := openBoardURL(link); err != nil {
			s.pendingToast = "Could not open the report"
		}
	case strings.HasPrefix(link, "/"):
		s.openExternalPath(link)
	default:
		s.pendingToast = "Could not open the report"
	}
}

// openBoardURL is the browser door, a seam for tests.
var openBoardURL = func(u string) error { return mygo.Shell.OpenExternal(u) }
