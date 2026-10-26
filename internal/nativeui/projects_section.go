package nativeui

/**
 * [INPUT]: 依赖 workboardProjects（按 Project 名分组）、treeRow/sidebarSection 共享树组件、workboardTaskMenu（与弹层同一份任务右键菜单）、workboardAvailable 门
 * [OUTPUT]: Shell.projectsSection（侧栏 Projects 区：插件可用才渲染；每个 Project 一行显示其名字，行下是它的开放任务；头部 “+” 与右键菜单发起 New Task / Add and Delegate / Open Work Board）
 * [POS]: herdr-projects 任务在侧栏的统一入口，夹在 Pin 与 Workspace 之间；只读缓存 s.workboard.board，写入仍全部走 workboard_actions.go；区折叠 tasksOpen 与单个 Project 的折叠 foldedBoards 随会话持久化（均默认展开）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"github.com/egoist/mygo/ui"
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
		mark := visualMark{svg: iconFolder}
		if project.Pinned {
			mark = visualMark{svg: iconPin}
		}
		row := treeRow(c, treeRowSpec{
			Height:     s.sidebarRowHeight(),
			Key:        "board:" + project.Name,
			Expandable: project.Open > 0,
			Expanded:   !folded,
			Selected:   onPage,
			Mark:       mark,
			Label:      project.Name,
			Dot:        project.Dot(),
			Count:      project.Open,
		})
		if row.Clicked() {
			// As in the Workspace tree: the first click opens the Project, a
			// click on the Project already open folds or unfolds it.
			if onPage && project.Open > 0 {
				s.foldedBoards[project.Name] = !folded
			} else {
				delete(s.foldedBoards, project.Name)
				s.openWorkboardProjectPage(project.Name)
			}
		}
		row.ContextMenu(func(m *ui.Menu) {
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
// dot; the plugin's status note and the writes live on the task's page and
// right-click menu.
func (s *Shell) projectTaskRow(c *ui.Context, r boardRow, last bool) {
	title := r.Task.Title
	if title == "" {
		title = r.Task.ID
	}
	row := treeRow(c, treeRowSpec{
		Height: s.sidebarRowHeight(),
		Key:    "boardtask:" + r.Task.ID,
		Depth:  1,
		Last:   last,
		Mark:   visualMark{svg: iconTask},
		Label:  title,
		Dot:    groupOperationalState(r.Group),
	})
	if row.Clicked() {
		s.openWorkboardTaskPage(r.Task.ID)
	}
	row.ContextMenu(func(m *ui.Menu) { s.workboardTaskMenu(m, r.Task, r.Group, func() {}) })
}
