package nativeui

/**
 * [INPUT]: 依赖 workboardProjects（按 Project 分组）、workboard_actions 的 workboardWrite、route_header 的 routeHeaderSpec、internal/workboard Client.Tell
 * [OUTPUT]: routeProject（/project 路由）、openWorkboardProjectPage（侧栏 Project 行的落点）、workboardProjectPage（仅记 Project 名）、projectRouteHeader、sendTaskMessage、boardProjectMenuItems、openProjectCoordinator/projectCoordinatorPane
 * [POS]: herdr-projects 一个 Project 页的 Go 一半：页面体是 Projects WebView（筛选/查找/折叠都在页内，Go 不存）；一次一个 Project，名字存在 workboardProjectPage（路由不带参数，与 /task 同）；全部派生自 board 缓存，写入只走插件 CLI
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/workboard"
)

const (
	routeProject = "/project"
)

// workboardProjectPage is the open Project page: which Project. Filter, find
// and folds live in the WebView page itself.
type workboardProjectPage struct {
	name string
}

// openWorkboardProjectPage opens the Project page from the sidebar.
func (s *Shell) openWorkboardProjectPage(name string) {
	if s.workboardBoardMode() {
		s.hideQuickPanel()
		s.workboard.boardFor = name
		s.openWorkBoard()
		return
	}
	s.hideQuickPanel()
	if s.workboard.project.name != name {
		s.workboard.project = workboardProjectPage{name: name}
	}
	if s.router.Path() == routeProject {
		s.router.Replace(routeProject)
	} else {
		s.router.Push(routeProject)
	}
}

// workboardProjectByName finds a Project on the last board read.
func (s *Shell) workboardProjectByName(name string) (workboardProject, bool) {
	for _, project := range workboardProjects(s.workboard.board) {
		if project.Name == name {
			return project, true
		}
	}
	return workboardProject{}, false
}

// projectRouteHeader names the Project in the title bar.
func (s *Shell) projectRouteHeader() routeHeaderSpec {
	spec := routeHeaderSpec{title: "Project"}
	name := s.workboard.project.name
	project, ok := s.workboardProjectByName(name)
	if !ok {
		return spec
	}
	spec.title = project.Name
	spec.facts = itoa(project.Open) + " open · " + itoa(len(project.Done)) + " done"
	spec.actions = func(m *ui.Menu) {
		if m.Item("New Task").Chosen() {
			s.openWorkboardAddIn(name, false)
		}
		if m.Item("Add and Delegate").Chosen() {
			s.openWorkboardAddIn(name, true)
		}
		if name != workboardOtherProject && m.Item("New Automation…").Chosen() {
			s.openWorkboardAutomation(name)
		}
		if n := len(s.projectWorkingTasks(project)); n > 0 && m.Item("Stop All Tasks…").Chosen() {
			s.openConfirm(workboardStopAllConfirm, project.Name, "Stop all "+itoa(n)+" working task"+pluralS(n)+"?",
				"Each is stopped on its own, one after another; it is not one atomic step. A task that finishes first is left alone, and the coordinator keeps running.")
		}
		m.Separator()
		s.boardProjectMenuItems(m, project)
		if m.Item("Open Work Board").Chosen() {
			s.openWorkBoard()
		}
	}
	return spec
}

// sendTaskMessage tells a task's agent something.
func (s *Shell) sendTaskMessage(id, text string) {
	if !s.workboardAvailable() {
		return
	}
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("work board write requested", "op", "tell", "task", id)
	s.workboardWrite("tell", func(ctx context.Context) error {
		return client.Tell(ctx, id, text)
	}, func() {
		s.pendingToast = "Sent to " + id + "'s agent"
	})
}

// boardProjectMenuItems is what a Project row and the Project page share: pin and
// the way into a running coordinator.
func (s *Shell) boardProjectMenuItems(m *ui.Menu, project workboardProject) {
	label := "Pin Project"
	if project.Pinned {
		label = "Unpin Project"
	}
	if m.Item(label).Chosen() {
		s.setWorkboardPin(project.Name, !project.Pinned)
	}
	if project.Coord.Running() && m.Item("Open Coordinator").Chosen() {
		s.openProjectCoordinator(project.Name)
	}
	s.boardManageItems(m, project)
	if m.Item("Archived Tasks").Chosen() {
		s.openWorkboardArchived()
	}
}

// openProjectCoordinator goes into the Project's coordinator pane. The pane
// comes from the plugin's own status; one this session does not show is said
// so rather than guessed at.
func (s *Shell) openProjectCoordinator(name string) {
	co := s.workboard.board.extras.coordinators[name]
	if !co.Running() {
		s.pendingToast = name + "'s coordinator isn't running"
		return
	}
	if paneByID(s.projection, co.Pane) == nil {
		s.pendingToast = name + "'s coordinator isn't open in this session"
		return
	}
	s.hideQuickPanel()
	s.router.Push(routeWorkspace)
	s.selectPane(co.Pane)
}

// projectCoordinatorPane is the pane of the Project's running coordinator, when
// this session has it open.
func (s *Shell) projectCoordinatorPane(project workboardProject) (string, bool) {
	return s.coordinatorPane(project.Coord)
}

// coordinatorPane is a coordinator's pane when it runs and this session has it.
func (s *Shell) coordinatorPane(co workboard.Coordinator) (string, bool) {
	if !co.Running() || paneByID(s.projection, co.Pane) == nil {
		return "", false
	}
	return co.Pane, true
}
