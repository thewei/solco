package nativeui

/**
 * [INPUT]: 依赖 internal/workboard 的 manage.go（Archived/Restore/Clear/ArchiveDone/ArchiveProject/RestoreProject/CoordinatorControl/Autopilots/SetAutopilot）、workboardWrite 单一写通道、openConfirm/submitConfirm 确认机制、board 缓存 extras.autopilot
 * [OUTPUT]: routeArchived（/archived 路由）、openWorkboardArchived/loadWorkboardArchived、workboardArchivedPage、archivedRouteHeader、boardManageItems（Project 菜单的管理项：Archive Done / Archive Project / 协调者 Restart·New·Stop / Autopilot）、startBoardManage/runBoardManage
 * [POS]: 管理面（P6）：每个动词的含义归插件，这里只负责带数量与后果的确认文案（archive --repo 会停 Agent/协调者/自动化，clear 永久移除记录，autopilot on 持续耗 token）；归档列表不进板缓存，只在 /archived 页打开或写入后读取；/archived 与 /task、/project 一样不随重启恢复
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/workboard"
)

const (
	routeArchived = "/archived"

	boardAutomationRunConfirm    = "board-automation-run"
	boardAutomationRemoveConfirm = "board-automation-remove"

	boardArchiveDoneConfirm    = "board-archive-done"
	boardArchiveProjectConfirm = "board-archive-project"
	boardRestoreProjectConfirm = "board-restore-project"
	boardClearConfirm          = "board-clear"
	boardCoordinatorNewConfirm = "board-coordinator-new"
	boardAutopilotOnConfirm    = "board-autopilot-on"
)

// workboardArchived is the archived list the /archived page shows.
type workboardArchived struct {
	tasks   []workboard.Task
	loading bool
	failed  bool
	loaded  bool
}

// openWorkboardArchived opens the archived page and reads the list.
func (s *Shell) openWorkboardArchived() {
	if s.workboardBoardMode() {
		s.hideQuickPanel()
		s.openWorkBoard()
		return
	}
	s.hideQuickPanel()
	if s.router.Path() == routeArchived {
		s.router.Replace(routeArchived)
	} else {
		s.router.Push(routeArchived)
	}
	s.loadWorkboardArchived()
}

// loadWorkboardArchived reads `list --archived` off the UI lane.
func (s *Shell) loadWorkboardArchived() {
	page := &s.workboard.archived
	if page.loading || !s.workboardAvailable() {
		return
	}
	page.loading = true
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	workboardGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), workboardRefreshTimeout)
		defer cancel()
		tasks, err := client.Archived(ctx)
		s.applyOnWindow(func() {
			page := &s.workboard.archived
			page.loading = false
			page.failed = err != nil
			if err == nil {
				page.tasks, page.loaded = tasks, true
			}
		})
	})
}

func (s *Shell) archivedRouteHeader() routeHeaderSpec {
	spec := routeHeaderSpec{title: "Archived"}
	if n := len(s.workboard.archived.tasks); n > 0 {
		spec.facts = itoa(n) + " task" + pluralS(n)
	}
	return spec
}

// archivedGroup is one Project's archived tasks.
type archivedGroup struct {
	Name  string
	Tasks []workboard.Task
}

func archivedGroups(tasks []workboard.Task) []archivedGroup {
	byName := map[string]*archivedGroup{}
	var names []string
	for _, t := range tasks {
		name := t.Project()
		if name == "" {
			name = workboardOtherProject
		}
		g := byName[name]
		if g == nil {
			g = &archivedGroup{Name: name}
			byName[name] = g
			names = append(names, name)
		}
		g.Tasks = append(g.Tasks, t)
	}
	sort.Strings(names)
	out := make([]archivedGroup, 0, len(names))
	for _, n := range names {
		out = append(out, *byName[n])
	}
	return out
}

// workboardArchivedPage lists archived tasks by Project, each with Restore
// and Remove, and each Project with Restore Project.
func (s *Shell) workboardArchivedPage(c *ui.Context) {
	sp := Spacing()
	t := c.Theme()
	if !s.workboardAvailable() {
		emptyState(c, "The work board isn't available", "")
		return
	}
	page := s.workboard.archived
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		contentColumn(c, formColumnWidth, func() {
			ui.Column(c).FillWidth().Padding(pageTopInset(), sp.XL, sp.XL).Gap(sp.M).Children(func() {
				switch {
				case page.failed && !page.loaded:
					ui.Text(c, "Couldn't read the archive").TextColor(t.TextMuted)
					if regularButton(c, "Retry").Clicked() {
						s.loadWorkboardArchived()
					}
				case !page.loaded:
					ui.Spinner(c)
				case len(page.tasks) == 0:
					emptyState(c, "Nothing archived", "")
				}
				for _, g := range archivedGroups(page.tasks) {
					g := g
					ui.Row(c.Key("archived:" + g.Name)).FillWidth().Gap(sp.M).AlignItems(ui.Center).Children(func() {
						ui.Text(c, g.Name).FontSize(Typography().BodySmall).FontWeight(WeightMedium)
						ui.Text(c, itoa(len(g.Tasks))).FontSize(Typography().Caption).TextColor(t.TextMuted)
						ui.Box(c).Grow(1)
						if g.Name != workboardOtherProject && regularButton(c, "Restore Project").Clicked() {
							s.startBoardManage(boardRestoreProjectConfirm, g.Name, len(g.Tasks))
						}
					})
					for _, task := range g.Tasks {
						task := task
						title := fallbackText(task.Title, task.ID)
						ui.Row(c.Key("archived-task:" + task.ID)).FillWidth().Gap(sp.M).AlignItems(ui.Center).Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
								tipped(c, ui.Text(c, title).FontSize(Typography().Body).SingleLine(), title)
								ui.Text(c, task.ID).FontSize(Typography().Micro).TextColor(t.TextMuted).SingleLine()
							})
							if regularButton(c, "Restore").Clicked() {
								s.runBoardManage("restore-task", task.ID)
							}
							if regularButton(c, "Remove…").Clicked() {
								s.startBoardManage(boardClearConfirm, task.ID, 0)
							}
						})
					}
				}
			})
		})
	})
}

// boardManageItems is the Project menu's management group. It is absent for
// the catch-all Project, which is not a repo the plugin can address.
func (s *Shell) boardManageItems(m *ui.Menu, project workboardProject) {
	if project.Name == workboardOtherProject {
		return
	}
	m.Separator()
	if n := len(project.Done); n > 0 && m.Item("Archive Done Tasks…").Chosen() {
		s.startBoardManage(boardArchiveDoneConfirm, project.Name, n)
	}
	if m.Item("Archive Project…").Chosen() {
		s.startBoardManage(boardArchiveProjectConfirm, project.Name, project.Open+len(project.Done))
	}
	if project.Coord.Running() {
		if m.Item("Restart Coordinator").Chosen() {
			s.runBoardManage("coordinator-restart", project.Name)
		}
		if m.Item("New Coordinator Conversation…").Chosen() {
			s.startBoardManage(boardCoordinatorNewConfirm, project.Name, 0)
		}
		if m.Item("Stop Coordinator").Chosen() {
			s.runBoardManage("coordinator-stop", project.Name)
		}
	} else if m.Item("Start Coordinator").Chosen() {
		s.runBoardManage("coordinator-start", project.Name)
	}
	if s.workboard.board.extras.autopilot[project.Name] {
		if m.Item("Turn Autopilot Off").Chosen() {
			s.runBoardManage("autopilot-off", project.Name)
		}
	} else if m.Item("Turn Autopilot On…").Chosen() {
		s.startBoardManage(boardAutopilotOnConfirm, project.Name, 0)
	}
}

// startBoardManage asks first, with the verb's real consequence and count.
func (s *Shell) startBoardManage(kind, target string, n int) {
	s.hideQuickPanel()
	count := strconv.Itoa(n)
	switch kind {
	case boardArchiveDoneConfirm:
		s.openConfirm(kind, target, "Archive done tasks?", "Archive the "+count+" done task"+pluralS(n)+" of "+target+"? They leave the board; Archived lists them and Restore brings them back.")
	case boardArchiveProjectConfirm:
		s.openConfirm(kind, target, "Archive "+target+"?", "Archive "+target+" and its "+count+" task"+pluralS(n)+"? Anything running in it is stopped, its coordinator is stopped and its automations are turned off. Restore Project brings the tasks and automations back.")
	case boardRestoreProjectConfirm:
		s.openConfirm(kind, target, "Restore "+target+"?", "Restore "+target+" and its "+count+" archived task"+pluralS(n)+"? Its automations that were on come back on. Work stopped when it was archived stays stopped.")
	case boardClearConfirm:
		s.openConfirm(kind, target, "Remove "+target+" for good?", "Its record is deleted and cannot be restored. The plugin keeps any work that is not saved elsewhere and tells you what.")
	case boardCoordinatorNewConfirm:
		s.openConfirm(kind, target, "Start a new conversation?", target+"'s coordinator starts a fresh conversation; the current one is not continued.")
	case boardAutomationRunConfirm:
		s.openConfirm(kind, target, "Run "+s.automationName(target)+" now?", "It adds a task and puts an agent on it right away, spending tokens.")
	case boardAutomationRemoveConfirm:
		s.openConfirm(kind, target, "Remove "+s.automationName(target)+"?", "The automation is deleted and stops firing. The tasks it made stay.")
	case boardAutopilotOnConfirm:
		s.openConfirm(kind, target, "Turn autopilot on?", "The board will wake "+target+"'s coordinator on its own and it will work toward its saved goal, spending tokens until you turn autopilot off. With no saved goal the plugin refuses: tell the coordinator its goal first.")
	}
}

// runBoardManage runs one management verb through the single write channel.
// Only the confirm kinds above and the unconfirmed ones below reach it.
func (s *Shell) runBoardManage(op, target string) {
	if !s.workboardAvailable() {
		return
	}
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("work board write requested", "op", op, "target", target)
	toast := ""
	s.workboardWrite(op, func(ctx context.Context) error {
		switch op {
		case "restore-task":
			toast = "Restored " + target
			return client.Restore(ctx, target)
		case "clear-task":
			toast = "Removed " + target
			return client.Clear(ctx, target)
		case "archive-done":
			swept, err := client.ArchiveDone(ctx, target)
			if err != nil {
				return err
			}
			switch n := len(swept.Archived); {
			case len(swept.Failed) > 0:
				toast = "Archived " + itoa(n) + " done task" + pluralS(n) + " of " + target + "; " + itoa(len(swept.Failed)) + " could not be"
			case n == 0:
				toast = "No done tasks to archive in " + target
			default:
				toast = "Archived " + itoa(n) + " done task" + pluralS(n) + " of " + target
			}
			return nil
		case "archive-project":
			toast = "Archived " + target
			return client.ArchiveProject(ctx, target)
		case "restore-project":
			toast = "Restored " + target
			return client.RestoreProject(ctx, target)
		case "coordinator-start":
			toast = "Starting " + target + "'s coordinator"
			return client.CoordinatorControl(ctx, target, "restart") // plugin: "start its program again"
		case "coordinator-restart":
			toast = "Restarting " + target + "'s coordinator"
			return client.CoordinatorControl(ctx, target, "restart")
		case "coordinator-new":
			toast = "Starting a new conversation with " + target + "'s coordinator"
			return client.CoordinatorControl(ctx, target, "new")
		case "coordinator-stop":
			toast = "Stopped " + target + "'s coordinator"
			return client.CoordinatorControl(ctx, target, "stop")
		case "automation-on":
			toast = s.automationName(target) + " is on"
			return client.SetAutomation(ctx, target, true)
		case "automation-off":
			toast = s.automationName(target) + " is off"
			return client.SetAutomation(ctx, target, false)
		case "automation-run":
			toast = "Ran " + s.automationName(target)
			return client.RunAutomation(ctx, target)
		case "automation-remove":
			toast = "Removed " + s.automationName(target)
			return client.RemoveAutomation(ctx, target)
		case "autopilot-on":
			toast = "Autopilot on for " + target
			return client.SetAutopilot(ctx, target, true)
		case "autopilot-off":
			toast = "Autopilot off for " + target
			return client.SetAutopilot(ctx, target, false)
		}
		return errors.New("unknown work board action")
	}, func() {
		s.pendingToast = toast
		if s.router.Path() == routeArchived {
			s.loadWorkboardArchived()
		}
		// An archived Project has left the board: leave its page too.
		if op == "archive-project" && s.router.Path() == routeProject && s.workboard.project.name == target {
			s.router.Back()
		}
	})
}

// boardManageConfirmed maps a confirmed kind to its verb.
func boardManageConfirmed(kind string) (string, bool) {
	op, ok := map[string]string{
		boardArchiveDoneConfirm:      "archive-done",
		boardArchiveProjectConfirm:   "archive-project",
		boardRestoreProjectConfirm:   "restore-project",
		boardClearConfirm:            "clear-task",
		boardCoordinatorNewConfirm:   "coordinator-new",
		boardAutopilotOnConfirm:      "autopilot-on",
		boardAutomationRunConfirm:    "automation-run",
		boardAutomationRemoveConfirm: "automation-remove",
	}[kind]
	return op, ok
}
