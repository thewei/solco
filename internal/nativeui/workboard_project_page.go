package nativeui

/**
 * [INPUT]: 依赖 workboardProjects（按 Project 分组）、workboard_actions 的 workboardWrite/openWorkboardAdd 机制、quickPanelTaskRow（共用任务行）、internal/workboard Client.CoordinatorTell/Tell/Add、route_header 的 routeHeaderSpec
 * [OUTPUT]: routeProject（/project 路由）、openWorkboardProjectPage（侧栏 Project 行的落点）、workboardProjectPage（页面内容）、projectRouteHeader、workboardMessageBox（页底消息框，Project 页发给 coordinator、任务页发给 agent）、openWorkboardAddIn/submitWorkboardAddIn（在该 Project 里 New Task）
 * [POS]: herdr-projects 一个 Project 的页面，布局沿用插件 TUI：筛选行（All / Needs you / Working / Ready / To do / Done，带计数）→ 按状态分组、标题可折叠的任务列表（Done 默认不展开，仅留 “↓ N done”）→ 页底消息框；任务列表上方有官方 SearchField “Find tasks”（⌘F 聚焦；按标题/ID/消息/agent 过滤当前筛选；↓ 进列表），分组头与任务行是 listNav 一个焦点组（↑↓/Home/End 移动、←/→ 折叠分组、Enter 开任务页；不绑字母键，动作快捷键不加——与侧栏列表同一约定，需字母键走插件看板 Tab）；一次一个 Project，名字存在 workboardProjectPage（路由不带参数，与 /task 同）；全部派生自 board 缓存，写入只走插件 CLI
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/herdr-client/internal/herdr"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

const (
	routeProject = "/project"
)

// Filters of the Project page, in the plugin TUI's order.
const (
	filterAll = iota
	filterNeedsYou
	filterWorking
	filterReady
	filterTodo
	filterDone
	filterPinned // shown only while the Project has a ★ task
)

var projectFilterLabels = []string{"All", "Needs you", "Working", "Ready", "To do", "Done", "Pinned"}

// workboardProjectPage is the open Project page: which Project, the filter,
// the groups the user folded, and the unsent message.
type workboardProjectPage struct {
	name     string
	filter   int
	foldedBy map[workboard.Group]bool
	draft    string
	delegate bool
	find     string // the Find field: narrows the chosen filter's tasks
}

// openWorkboardProjectPage opens the Project page from the sidebar.
func (s *Shell) openWorkboardProjectPage(name string) {
	if s.workboardBoardMode() {
		s.hideQuickPanel()
		s.openWorkBoard()
		return
	}
	s.hideQuickPanel()
	if s.workboard.project.name != name {
		s.workboard.project = workboardProjectPage{name: name, foldedBy: map[workboard.Group]bool{}}
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
		m.Separator()
		s.boardProjectMenuItems(m, project)
		if m.Item("Open Work Board").Chosen() {
			s.openWorkBoard()
		}
	}
	return spec
}

// groupCount is how many tasks a Project holds in one group.
func (p workboardProject) groupCount(g workboard.Group) int {
	for _, group := range p.Groups {
		if group.Group == g {
			return len(group.Tasks)
		}
	}
	return 0
}

// filterGroups is what the chosen filter shows: All lists the open groups,
// a group filter only that group, Done the finished tasks.
func (p workboardProject) filterGroups(filter int) []workboardGroup {
	only := map[int]workboard.Group{
		filterNeedsYou: workboard.GroupNeedsYou, filterWorking: workboard.GroupWorking,
		filterReady: workboard.GroupReady, filterTodo: workboard.GroupTodo,
	}
	switch {
	case filter == filterPinned:
		var out []workboardGroup
		for _, group := range append(append([]workboardGroup{}, p.Groups...), workboardGroup{Group: workboard.GroupDone, Tasks: p.Done}) {
			var starred []workboard.Task
			for _, task := range group.Tasks {
				if p.PinnedTasks[task.ID] {
					starred = append(starred, task)
				}
			}
			if len(starred) > 0 {
				out = append(out, workboardGroup{Group: group.Group, Tasks: starred})
			}
		}
		return out
	case filter == filterDone:
		if len(p.Done) == 0 {
			return nil
		}
		return []workboardGroup{{Group: workboard.GroupDone, Tasks: p.Done}}
	case filter == filterAll:
		return p.Groups
	}
	var out []workboardGroup
	for _, group := range p.Groups {
		if group.Group == only[filter] {
			out = append(out, group)
		}
	}
	return out
}

// workboardProjectPage renders the Project page.
func (s *Shell) workboardProjectPage(c *ui.Context) {
	sp := Spacing()
	if !s.workboardAvailable() {
		emptyState(c, "The work board isn't available", "")
		return
	}
	page := &s.workboard.project
	project, ok := s.workboardProjectByName(page.name)
	if !ok {
		ui.Column(c).Grow(1).Center().Gap(sp.M).Children(func() {
			emptyState(c, "This project is no longer on the board", "")
			if ui.Button(c, "Back").Clicked() {
				s.router.Back()
			}
		})
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			contentColumn(c, formColumnWidth, func() {
				ui.Column(c).FillWidth().Padding(pageTopInset(), sp.XL, sp.XL).Gap(sp.M).Children(func() {
					s.projectFilterRow(c, project)
					s.projectGroups(c, project)
					if s.workboard.project.filter == filterAll {
						s.projectAutomations(c, project)
					}
				})
			})
		})
		if project.Name != workboardOtherProject {
			s.coordinatorSaid(c, project.Coord)
			s.workboardMessageBox(c, "Message "+project.Name+"'s coordinator"+coordinatorSuffix(project.Coord), "Tell the coordinator what you want…",
				&page.draft, &page.delegate, func(text string, delegate bool) {
					s.sendProjectMessage(project.Name, text, delegate)
				})
		}
	})
}

// projectFilterRow is the TUI's filter row (each filter with its count) and
// the Project's task entry points.
func (s *Shell) projectFilterRow(c *ui.Context, project workboardProject) {
	sp := Spacing()
	page := &s.workboard.project
	counts := []int{
		project.Open,
		project.groupCount(workboard.GroupNeedsYou), project.groupCount(workboard.GroupWorking),
		project.groupCount(workboard.GroupReady), project.groupCount(workboard.GroupTodo),
		len(project.Done), len(project.PinnedTasks),
	}
	// Pinned is a filter only while something is starred; a filter that
	// emptied under the user falls back to All.
	shown := len(projectFilterLabels)
	if len(project.PinnedTasks) == 0 {
		shown--
		if page.filter == filterPinned {
			page.filter = filterAll
		}
	}
	labels := make([]string, shown)
	for i := range labels {
		labels[i] = projectFilterLabels[i] + " " + itoa(counts[i])
	}
	ui.Row(c).FillWidth().Gap(sp.M).Wrap().AlignItems(ui.Center).Children(func() {
		choice := page.filter
		if ui.Segmented(c, &choice, labels...).Label("Task filter").Changed() {
			page.filter = choice
		}
		ui.Box(c).Grow(1)
		if project.Coord.Running() && ui.Button(c, "Open Coordinator").Clicked() {
			s.openProjectCoordinator(project.Name)
		}
		if ui.Button(c, "New Task").Clicked() {
			s.openWorkboardAddIn(project.Name, false)
		}
		if ui.Button(c, "Add and Delegate").Clicked() {
			s.openWorkboardAddIn(project.Name, true)
		}
	})
}

// projectGroups lists the chosen filter's groups under foldable headings,
// narrowed by the Find field. The groups are one keyboard list (listNav): ↓
// from the field enters it, ↑/↓/Home/End move the cursor, Enter opens a task
// and ←/→ fold a heading.
func (s *Shell) projectGroups(c *ui.Context, project workboardProject) {
	page := &s.workboard.project
	if page.foldedBy == nil {
		page.foldedBy = map[workboard.Group]bool{}
	}
	nav := &s.nav.boardTasks
	field := ui.SearchField(c, &page.find).FillWidth().Label("Find tasks")
	if c.Shortcut(ui.Cmd, ui.KeyF) {
		field.Focus()
	}
	if field.Shortcut(0, ui.KeyDown) {
		nav.enter(c)
	}
	needle := strings.ToLower(strings.TrimSpace(page.find))
	groups := narrowGroups(project.filterGroups(page.filter), needle)
	if len(groups) == 0 {
		text := "No open tasks"
		switch {
		case needle != "":
			text = "No matching tasks"
		case page.filter != filterAll:
			text = "Nothing here"
		}
		ui.Text(c, text).Padding(Spacing().M, 0).TextColor(c.Theme().TextMuted)
	}
	s.navList(c, nav, false, ui.Column(c).FillWidth().Gap(Spacing().M), func() {
		for _, group := range groups {
			folded := page.foldedBy[group.Group]
			if _, toggled := s.projectGroupHeader(c, group, folded, nav); toggled {
				page.foldedBy[group.Group] = !folded
			}
			if folded {
				continue
			}
			for _, task := range group.Tasks {
				s.quickPanelTaskRowIn(c, task, group.Group, nav)
			}
		}
	})
	// As in the TUI, finished work stays out of the list; one line counts it.
	if page.filter == filterAll && needle == "" && len(project.Done) > 0 {
		label := "↓ " + itoa(len(project.Done)) + " done"
		if ui.Button(c, label).Clicked() {
			page.filter = filterDone
		}
	}
}

// narrowGroups keeps the tasks whose title, id, message or agent holds needle
// (already lower-cased); a group left empty goes with them.
func narrowGroups(groups []workboardGroup, needle string) []workboardGroup {
	if needle == "" {
		return groups
	}
	var out []workboardGroup
	for _, group := range groups {
		var kept []workboard.Task
		for _, task := range group.Tasks {
			for _, field := range []string{task.Title, task.ID, task.Msg, task.App} {
				if strings.Contains(strings.ToLower(field), needle) {
					kept = append(kept, task)
					break
				}
			}
		}
		if len(kept) > 0 {
			out = append(out, workboardGroup{Group: group.Group, Tasks: kept})
		}
	}
	return out
}

// projectGroupHeader is one foldable status heading: chevron, state dot,
// name and count. toggled is a click or the keyboard's ←/→ on it.
func (s *Shell) projectGroupHeader(c *ui.Context, group workboardGroup, folded bool, nav *listNav) (ui.Element, bool) {
	t := c.Theme()
	sp := Spacing()
	row := ui.ButtonBase(c.Key("boardgroup:"+group.Group.Label())).FillWidth().Height(30).
		Padding(0, sp.S).Radius(treeRowRadius).FocusRing(false).AlignItems(ui.Center).Gap(sp.S).
		Label("Toggle " + group.Group.Label())
	keyed := nav.row(c, row, navRow{Key: "group:" + group.Group.Label(), Section: true, Folder: true, Expanded: !folded})
	switch {
	case nav.showsCursor(row):
		row.Background(designTokens(t.Dark).SidebarSelect)
	case row.Hovered():
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		treeChevron(c, true, !folded, t.TextMuted)
		tone := operationalTone(groupOperationalState(group.Group))
		ui.Box(c).Size(7, 7).Radius(3.5).Shrink(0).
			Background(designTokens(t.Dark).StatusColor(tone, t.Dark))
		ui.Text(c, group.Group.Label()).FontSize(Typography().BodySmall).FontWeight(WeightMedium).SingleLine()
		ui.Text(c, itoa(len(group.Tasks))).FontSize(Typography().Caption).TextColor(t.TextMuted).SingleLine()
		ui.Box(c).Grow(1) // keeps the heading flush left inside the full-width button
	})
	return row, row.Clicked() || keyed
}

// workboardMessageBox is the page-foot box of the TUI: a draft, an optional
// Delegate choice (a coordinator only) and Send. Enter sends; the draft stays
// until the plugin accepted the message.
func (s *Shell) workboardMessageBox(c *ui.Context, label, placeholder string, draft *string, delegate *bool, send func(text string, delegate bool)) {
	t := c.Theme()
	sp := Spacing()
	ui.Column(c).FillWidth().Shrink(0).Padding(sp.M, sp.XL).Gap(sp.S).
		BorderWidth(1, 0, 0, 0).BorderColor(designTokens(t.Dark).BorderSubtle).Children(func() {
		contentColumn(c, formColumnWidth, func() {
			ui.Column(c).FillWidth().Gap(sp.S).Children(func() {
				ui.Text(c, label).FontSize(Typography().Caption).TextColor(t.TextMuted)
				field := ui.TextArea(c, draft).FillWidth().Height(64).Placeholder(placeholder).Label(label)
				ready := strings.TrimSpace(*draft) != "" && !field.Composing()
				submit := func() {
					d := false
					if delegate != nil {
						d = *delegate
					}
					send(strings.TrimSpace(*draft), d)
				}
				if field.Shortcut(0, ui.KeyEnter) && ready {
					submit()
				}
				ui.Row(c).FillWidth().Gap(sp.M).AlignItems(ui.Center).Children(func() {
					if delegate != nil {
						ui.Checkbox(c, delegate, "Delegate")
					}
					ui.Box(c).Grow(1)
					if ui.PrimaryButton(c, "Send").Disabled(!ready).Clicked() {
						submit()
					}
				})
			})
		})
	})
}

// sendProjectMessage tells a Project's coordinator what the user wants.
func (s *Shell) sendProjectMessage(project, text string, delegate bool) {
	if !s.workboardAvailable() {
		return
	}
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("work board write requested", "op", "coordinator-tell", "project", project, "delegate", delegate)
	s.workboardWrite("coordinator-tell", func(ctx context.Context) error {
		return client.CoordinatorTell(ctx, project, text, delegate)
	}, func() {
		if s.workboard.project.name == project {
			s.workboard.project.draft = ""
		}
		s.pendingToast = "Sent to " + project + "'s coordinator"
	})
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
		if s.workboard.page.id == id {
			s.workboard.page.draft = ""
		}
		s.pendingToast = "Sent to " + id + "'s agent"
	})
}

// coordinatorSuffix is the plugin's word for what the coordinator is doing,
// appended to the message box label; nothing when it is not known or not running.
func coordinatorSuffix(co workboard.Coordinator) string {
	switch co.State {
	case "", "none":
		return ""
	}
	return " · " + co.State
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

// coordinatorSaid shows the coordinator's latest message above the box that
// answers it, clipped; the plugin's board has the whole conversation.
func (s *Shell) coordinatorSaid(c *ui.Context, co workboard.Coordinator) {
	said := strings.TrimSpace(string(co.Said))
	if said == "" {
		return
	}
	t := c.Theme()
	sp := Spacing()
	ui.Column(c).FillWidth().Shrink(0).Padding(sp.M, sp.XL, 0).Children(func() {
		contentColumn(c, formColumnWidth, func() {
			ui.Column(c).FillWidth().Gap(sp.XS).Children(func() {
				ui.Text(c, "Coordinator said").FontSize(Typography().Caption).TextColor(t.TextMuted)
				ui.Text(c, clipPreview(said, 800, 6)).FontSize(Typography().BodySmall).Selectable()
			})
		})
	})
}
