package nativeui

/**
 * [INPUT]: 依赖 workboardProjects（按 Project 名分组）、treeRow/sidebarSection 共享树组件、workboardTaskMenu（与弹层同一份任务右键菜单）、workboardAvailable 门
 * [OUTPUT]: Shell.projectsSection（侧栏 Projects 区：插件可用才渲染；每个 Project 一行显示其名字，行下是它的开放任务；协调者在运行且 pane 在当前 Session 时，行点击直接进它的 pane（再点折叠任务），Open Tasks 在右键菜单；视图由 Settings → Work Board 的 Native | Board 切换（默认 Board：行点击一律开插件看板 Tab）；头部 “+” 与右键菜单发起 New Task / Add and Delegate / Open Work Board）
 * [POS]: herdr-projects 任务在侧栏的统一入口，夹在 Pin 与 Workspace 之间；只读缓存 s.workboard.board，写入仍全部走 workboard_actions.go；区折叠 tasksOpen 与单个 Project 的折叠 foldedBoards 随会话持久化（均默认展开）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/herdr-client/internal/herdr"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

// projectsSection renders the sidebar Projects section. It draws nothing
// while the plugin gate is closed: no heading, no count, no trace.
func (s *Shell) projectsSection(c *ui.Context) {
	if !s.workboardAvailable() {
		return
	}
	projects := workboardProjects(s.workboard.board)
	add := &treeRowAction{Label: "New Task", Glyph: iconPlus, Run: func() { s.openWorkboardAdd(false) }}
	s.sidebarSection(c, sidebarSectionSpec{
		Key: "section:projects", Title: "Projects", Count: len(projects), Action: add,
		Menu: func(m *ui.Menu) {
			if m.Item(add.Label).Chosen() {
				add.Run()
			}
			if m.Item("Add and Delegate").Chosen() {
				s.openWorkboardAdd(true)
			}
			m.Separator()
			if m.Item("Archived Tasks").Chosen() {
				s.openWorkboardArchived()
			}
			if m.Item("Open Work Board").Chosen() {
				s.openWorkBoard()
			}
		},
	}, &s.tasksOpen, func() {
		s.projectsItems(c, projects)
	})
}

// projectsItems is the open section: one row per Project, named as the plugin
// names it, with the Project's open tasks beneath while it is unfolded. A
// Project row folds on click; a task row opens the task's page.
func (s *Shell) projectsItems(c *ui.Context, projects []workboardProject) {
	board := s.workboard.board
	if board.failed {
		ui.Row(c).FillWidth().Padding(Spacing().S, Spacing().M, 0).Gap(Spacing().S).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Couldn't read the board").FontSize(Typography().Caption).Grow(1).TextColor(c.Theme().TextMuted)
			if ui.Button(c, "Retry").Clicked() {
				s.refreshWorkboardTasks(true)
			}
		})
	}
	if len(projects) == 0 {
		if !board.failed && !board.loadedAt.IsZero() {
			ui.Text(c, "No projects yet").Padding(Spacing().M, Spacing().L).TextColor(c.Theme().TextMuted)
		}
		return
	}
	for _, project := range projects {
		project := project
		folded := s.foldedBoards[project.Name]
		onPage := s.router.Path() == routeProject && s.workboard.project.name == project.Name
		// A running coordinator open in this session is where the Project's
		// row leads: its pane. The tasks stay a menu item (Open Tasks) away.
		coordPane, hasCoord := s.projectCoordinatorPane(project)
		onCoord := hasCoord && s.router.Path() == routeWorkspace && s.selectedPaneID == coordPane
		mark := visualMark{svg: iconFolder}
		if project.Pinned {
			mark = visualMark{svg: iconPin}
		}
		row := treeRow(c, treeRowSpec{
			Height:     s.sidebarRowHeight(),
			Key:        "board:" + project.Name,
			Expandable: project.Open > 0,
			Expanded:   !folded,
			Selected:   onPage || onCoord,
			Mark:       mark,
			Label:      project.Name,
			Dot:        project.Dot(),
			Count:      project.Open,
		})
		if row.Clicked() {
			// As in the Workspace tree: the first click opens the Project (its
			// coordinator when one runs, else its page), a click on the Project
			// already open folds or unfolds it.
			switch {
			case (onPage || onCoord) && project.Open > 0:
				s.foldedBoards[project.Name] = !folded
			default:
				delete(s.foldedBoards, project.Name)
				s.openProjectRow(project.Name)
			}
		}
		row.ContextMenu(func(m *ui.Menu) {
			if project.Coord.Running() {
				if m.Item("Open Coordinator").Chosen() {
					s.openProjectCoordinator(project.Name)
				}
				if m.Item("Open Tasks").Chosen() {
					s.openWorkboardProjectPage(project.Name)
				}
				m.Separator()
			}
			if m.Item("New Task").Chosen() {
				s.openWorkboardAddIn(project.Name, false)
			}
			if m.Item("Add and Delegate").Chosen() {
				s.openWorkboardAddIn(project.Name, true)
			}
			m.Separator()
			s.boardProjectMenuItems(m, project)
			if m.Item("Open Work Board").Chosen() {
				s.openWorkBoard()
			}
		})
		if folded {
			continue
		}
		var rows []boardRow
		for _, group := range project.Groups {
			for _, task := range group.Tasks {
				rows = append(rows, boardRow{task, group.Group})
			}
		}
		for i, r := range rows {
			s.projectTaskRow(c, r, i == len(rows)-1)
		}
	}
}

// boardRow is one open task with the group it sits in.
type boardRow struct {
	Task  workboard.Task
	Group workboard.Group
}

// projectTaskRow is one task under its Project: a single line with the state
// dot; a click goes to the task's agent pane when this session has it open (else
// the task's page); the plugin's status note and the writes live on the task's page and
// right-click menu.
func (s *Shell) projectTaskRow(c *ui.Context, r boardRow, last bool) {
	title := r.Task.Title
	if title == "" {
		title = r.Task.ID
	}
	pane, hasPane := s.taskAgentPane(r.Task.ID)
	row := treeRow(c, treeRowSpec{
		Height:   s.sidebarRowHeight(),
		Key:      "boardtask:" + r.Task.ID,
		Depth:    1,
		Last:     last,
		Selected: hasPane && s.router.Path() == routeWorkspace && s.selectedPaneID == pane,
		Mark:     visualMark{svg: iconTask},
		Label:    title,
		Dot:      groupOperationalState(r.Group),
	})
	if row.Clicked() {
		if hasPane {
			s.openPane(pane)
		} else {
			s.openWorkboardTaskPage(r.Task.ID)
		}
	}
	row.ContextMenu(func(m *ui.Menu) {
		if hasPane {
			if m.Item("Open Task").Chosen() {
				s.openWorkboardTaskPage(r.Task.ID)
			}
			m.Separator()
		}
		s.workboardTaskMenu(m, r.Task, r.Group, func() {})
	})
}

// taskAgentPane is the pane of the task's agent: one in this session sitting in
// the task's worktree. An agent pane wins over a plain shell there, the task's
// own program over another agent. False until the task has a worktree.
func (s *Shell) taskAgentPane(id string) (string, bool) {
	d, ok := s.workboard.board.details[id]
	root := cleanPath(d.detail.Worktree)
	if !ok || root == "" {
		return "", false
	}
	best, bestRank := "", 0
	for _, pane := range s.projection.Panes {
		if !pathWithin(root, cleanPath(pane.CWD)) {
			continue
		}
		rank := 1
		if pane.Agent != "" {
			rank = 2
			if pane.Agent == d.detail.Agent {
				rank = 3
			}
		}
		if rank > bestRank {
			best, bestRank = pane.ID, rank
		}
	}
	return best, best != ""
}

// openPane shows one pane of this session.
func (s *Shell) openPane(id string) {
	s.hideQuickPanel()
	s.router.Push(routeWorkspace)
	s.selectPane(id)
}

// openProjectRow is the Project row's click: its coordinator when one runs and
// this session shows it, else the Project's page. The coordinator is read fresh
// first — the cached status can be a refresh behind a coordinator that just
// started (or moved panes) — and the cache keeps the answer.
func (s *Shell) openProjectRow(name string) {
	if !s.workboardAvailable() {
		return
	}
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	workboardGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), workboardRefreshTimeout)
		defer cancel()
		co, err := client.Coordinator(ctx, name)
		s.applyOnWindow(func() {
			extras := &s.workboard.board.extras
			if err == nil {
				if extras.coordinators == nil {
					extras.coordinators = map[string]workboard.Coordinator{}
				}
				extras.coordinators[name] = co
			}
			pane, ok := s.coordinatorPane(extras.coordinators[name])
			slog.Info("project row open", "project", name, "coordinator_state", extras.coordinators[name].State,
				"pane", extras.coordinators[name].Pane, "in_session", ok, "read_failed", err != nil)
			if ok {
				s.openPane(pane)
				return
			}
			if extras.coordinators[name].Running() {
				s.pendingToast = name + "'s coordinator isn't open in this session"
			}
			s.openWorkboardProjectPage(name)
		})
	})
}
