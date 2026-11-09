package nativeui

/**
 * [INPUT]: 依赖 Herdr 权威 Workspace/Tab 顺序及 Pane layout、reorder.go 的受限拖动规则、markAgentReviewedForPane 聚焦取消 Review
 * [OUTPUT]: 提供侧栏 Workspace → Tab → Pane 交互树（项目行悬停 ⋯ = 右键同一份 projectMenuItems，2026-11-08）、排序与原生拖放入口，Pane 行聚焦时取消对应 Agent 的 Review 状态
 * [POS]: nativeui 的运行时导航树；状态标识不再干扰用户持久化的拖动顺序；单 Pane Tab 行的身份图取该 Pane 绑定 Agent 的品牌图（次选可识别程序品牌，其余 tab 字形，tabRowMark）；Agent working 的行（单 Pane Tab 行与 pane 行）行尾画 Agents 区卡片同款 Spinner（treeRowSpec.Working，Working 桶同语义）；pane 实测回 shell 提示符时死绑定不再上色（paneAgent 门控、liveAgentStatus 清空行 Status）；有活 Agent 的 pane 行与单 Pane Tab 行标签取 Agents 区同款标题（agentCardTitleForPane，会话标题优先）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/solcogit"
)

func (s *Shell) projectItems(c *ui.Context) {
	dark := c.Theme().Dark
	if s.activeInstance == "" {
		ui.Text(c, "Choose a Workspace first").Padding(8, 10).TextColor(c.Theme().TextMuted)
		return
	}
	projects := orderedProjects(s.projection)
	activity := buildSidebarActivity(s.projection, s.serviceIndex, s.paneShellIdle)
	for _, project := range projects {
		project := project
		// On /assistants nothing is selected in the tree, so a click always selects
		// (and leaves the page) instead of toggling the row it would be on.
		selectedProject := project.ID == s.selectedProjectID && s.treeShowsSelection()
		tabs := tabsForProject(s.projection, project.ID)
		expandedProject := s.projectExpanded(project.ID)
		// The pane tally (2026-10-07, widened 2026-10-15 user request): the
		// project row shows how many panes run beneath it whether it is
		// folded or expanded, so the nesting depth never hides the load.
		projectPaneCount := 0
		projectPanes := make([]herdr.Pane, 0, 8)
		for _, tab := range tabs {
			tabPanes := panesForTab(s.projection, tab.ID)
			projectPaneCount += len(tabPanes)
			projectPanes = append(projectPanes, tabPanes...)
		}
		projectCount := projectPaneCount
		branch, _, _, dirtyFiles, _ := s.gitMetaForCWD(project.CWD)
		row := treeRow(c, treeRowSpec{
			Height:     s.sidebarRowHeight(),
			Key:        "project:" + project.ID,
			Depth:      0,
			Selected:   s.workspaceRowSelected(selectedProject),
			Expandable: len(tabs) > 0,
			Expanded:   expandedProject,
			Tile:       project.Label,
			Branch:     branch,
			Dirty:      dirtyFiles > 0,
			StatusSlot: true,
			Label:      project.Label,
			Status:     s.liveAgentStatus(project.AgentStatus, projectPanes),
			Activity:   activity[project.ID],
			Count:      projectCount,
			// The hover icon is the row's own menu (2026-11-08 U9/U10): the "⋯"
			// opens exactly what the right click does, so New Tab ▸ is one
			// level in; the Tab bar's "+" and ⌘T are unchanged.
			Action: &treeRowAction{Label: "Actions for " + project.Label, Glyph: iconActions, Menu: func(m *ui.Menu) { s.projectMenuItems(m, project) }},
			Drag:   s.treeDragFor(treeNodeDrag{s.activeInstance, "project", "", project.ID}),
		})
		if row.Clicked() {
			if selectedProject {
				s.setProjectExpanded(project.ID, !expandedProject)
			} else {
				// The selection lands as /workspace?project=… (one history
				// entry); no bare page push beside it.
				s.setProjectExpanded(project.ID, true)
				s.selectProject(project.ID)
			}
		}
		row.ContextMenu(func(m *ui.Menu) { s.projectMenuItems(m, project) })
		if !expandedProject || s.tabsInTitleBar() {
			// With the Tab bar in the title bar the tree lists Workspaces only.
			continue
		}

		for tabIndex, tab := range tabs {
			tab := tab
			lastTab := tabIndex == len(tabs)-1
			selectedTab := tab.ID == s.selectedTabID && s.treeShowsSelection()
			panes := panesForTab(s.projection, tab.ID)
			sortPanesByLayout(s.projection, tab.ID, panes)
			expandedTab := s.tabExpanded(tab.ID)
			// A folded tab shows its pane tally; an expanded one (and a
			// single-pane leaf) shows none.
			tabCount := 0
			if !expandedTab {
				tabCount = len(panes)
			}
			tabLabel := tab.Label
			if len(panes) == 1 {
				// A single-pane Tab is the pane's face: it speaks the
				// Agent's conversation title, the same one the Agents
				// section shows, so the tree reads like the cards.
				if title, ok := s.agentCardTitleForPane(panes[0].ID); ok {
					tabLabel = title
				}
			}
			row := treeRow(c, treeRowSpec{
				Height:     s.sidebarRowHeight(),
				Key:        "tab:" + tab.ID,
				Depth:      1,
				Last:       lastTab,
				Selected:   s.treeShowsSelection() && selectedTab && tab.PaneCount <= 1,
				Expandable: len(panes) > 1,
				Expanded:   expandedTab,
				Mark:       s.tabRowMark(panes, dark),
				Label:      tabLabel,
				Status:     s.liveAgentStatus(tab.AgentStatus, panes),
				StatusSlot: true,
				Activity:   activity[tab.ID],
				Count:      tabCount,
				Drag:       s.treeDragFor(treeNodeDrag{s.activeInstance, "tab", project.ID, tab.ID}),
			})
			if row.Clicked() {
				if selectedTab && len(panes) > 1 {
					s.setTabExpanded(tab.ID, !expandedTab)
				} else if !selectedTab {
					s.setTabExpanded(tab.ID, true)
					s.selectTab(tab.ID)
				}
			}
			row.ContextMenu(func(m *ui.Menu) { s.tabMenuItems(m, tab) })
			if !expandedTab || len(panes) <= 1 {
				continue
			}

			for paneIndex, pane := range panes {
				pane := pane
				label := fallbackText(pane.Label, "Pane")
				// A pane with a live Agent introduces itself by the same
				// title the Agents section carries (session title first).
				if title, ok := s.agentCardTitleForPane(pane.ID); ok {
					label = title
				}
				mark, isAgent := s.agentMarkForPane(pane.ID, dark)
				if !isAgent {
					// A Pane running a recognizable program shows that
					// program's brand (2026-10-06 A11); the rest keep the
					// terminal glyph.
					if brand, ok := s.procMarkForPaneWithTheme(pane.ID, dark); ok {
						mark = brand
					}
				}
				dot := opUnknown
				if isAgent {
					if a := s.paneAgent(pane.ID); a != nil {
						dot = normalizeRuntimeStatus(a.Status)
					}
				}
				paneStatus := pane.AgentStatus
				if s.paneShellIdle[pane.ID] {
					// Measured at its shell prompt: Herdr's status belongs
					// to a dead binding, not to anything running here.
					paneStatus = ""
				}
				row := treeRow(c, treeRowSpec{
					Height:            s.sidebarRowHeight(),
					Key:               "pane:" + pane.ID,
					Depth:             2,
					AncestorContinues: []bool{!lastTab},
					Last:              paneIndex == len(panes)-1,
					Selected:          s.treeShowsSelection() && pane.ID == s.selectedPaneID,
					Mark:              mark,
					Dot:               dot,
					StatusSlot:        true,
					Label:             label,
					Status:            paneStatus,
					Activity:          activity[pane.ID],
					Drag:              s.treeDragFor(treeNodeDrag{s.activeInstance, "pane", tab.ID, pane.ID}),
				})
				if row.Clicked() {
					s.selectPane(pane.ID)
				}
				if row.Focused() {
					s.markAgentReviewedForPane(pane.ID)
				}
				row.ContextMenu(func(m *ui.Menu) { s.paneOverflowItems(m, pane.ID, label) })
			}
		}
	}
	if len(projects) == 0 && !s.loading {
		ui.Text(c, "No Workspaces in this Session").Padding(8, 10).TextColor(c.Theme().TextMuted)
	}
}

// tabRowMark picks a single-pane Tab row's leading mark (2026-10-06 A5):
// a Tab presenting one Pane wears that Pane's identity — the bound
// Agent's brand first (2026-10-30: matching the breadcrumb switcher and
// the pane rows; a single-pane Tab renders no pane row, so the Tab row is
// where the Agent's icon shows), then a recognized program's brand for a
// long-lived service. Multi-pane Tabs and unrecognized shells keep the
// plain tab glyph; the icon-corner dot carries the running state.
func (s *Shell) tabRowMark(panes []herdr.Pane, dark bool) visualMark {
	if len(panes) == 1 {
		if mark, isAgent := s.agentMarkForPane(panes[0].ID, dark); isAgent {
			return mark
		}
		if brand, ok := s.procMarkForPane(panes[0].ID); ok {
			return brand
		}
	}
	return visualMark{svg: iconTab}
}

// liveAgentStatus returns Herdr's rolled-up status only while some pane
// beneath it may still carry a live agent: when every pane is measured at
// its bare shell prompt (paneShellIdle), the rollup is a dead binding's
// leftover and the row must not paint it. Unmeasured panes keep Herdr's
// status.
func (s *Shell) liveAgentStatus(status string, panes []herdr.Pane) string {
	for _, pane := range panes {
		if !s.paneShellIdle[pane.ID] {
			return status
		}
	}
	return ""
}

// workspaceRowSelected tells whether the selected Workspace's own row carries
// the selection: when no Tab is selected, and always in the Tab bar mode,
// where the tree lists no Tab rows for the selection to land on.
func (s *Shell) workspaceRowSelected(isSelectedProject bool) bool {
	return s.treeShowsSelection() && isSelectedProject && (s.selectedTabID == "" || s.tabsInTitleBar())
}

// treeShowsSelection is whether the Workspace tree lights its selected rows:
// not on a page that shares the tree but is not a Workspace (treeHostedPage).
func (s *Shell) treeShowsSelection() bool { return !treeHostedPage(s.router.Path()) }

// treeHostedPage reports whether path is a page that shares the sidebar's
// Workspace tree without being a Workspace: Assistants (2026-11-04), Feedback
// and Automations (2026-11-09 user report: a Workspace click on Feedback did
// nothing and Back was the only way out). Nothing is lit in the tree there,
// and a row click selects and leaves the page.
func treeHostedPage(path string) bool {
	return path == routeAssistants || path == routeFeedback || path == routeAutomations
}

// newProjectTab opens a Tab in project: the project row's hover "+" and its
// context menu's New Tab share it.
func (s *Shell) newProjectTab(project herdr.Project) {
	s.mutateSelecting(func() (herdr.Projection, error) {
		return s.runtime.CreateTab(s.activeInstance, project.ID, project.CWD)
	})
}

// projectMenuItems is the shared New Tab / Rename / Open in Solco Git / Close
// builder behind the project row's right-click menu and its hover "⋯".
func (s *Shell) projectMenuItems(m *ui.Menu, project herdr.Project) {
	m.Submenu("New Tab", func(sub *ui.Menu) { s.newTabMenu(sub, project) })
	if m.Item("Rename Project…").Chosen() {
		s.openTextDialog("rename-project", project.ID, "Rename Project", "Name", project.Label)
	}
	m.Separator()
	// The row's own project, not the selected one; drawn once Solco Git is found.
	if s.solcoGitReady() {
		if m.Item("Open in Solco Git").Disabled(project.CWD == "").Chosen() {
			s.openInSolcoGit(project.CWD, solcogit.Changes, "")
		}
		m.Separator()
	}
	if m.Item("Close Project…").Chosen() {
		s.openConfirm("close-project", project.ID, "Close Project?", "This closes the Herdr runtime workspace and its Tabs/Panes.")
	}
}

// tabMenuItems is the shared Rename / Close Tab builder.
func (s *Shell) tabMenuItems(m *ui.Menu, tab herdr.Tab) {
	if m.Item("Rename Tab…").Chosen() {
		s.openTextDialog("rename-tab", tab.ID, "Rename Tab", "Name", tab.Label)
	}
	m.Separator()
	if m.Item("Close Tab…").Chosen() {
		s.openConfirm("close-tab", tab.ID, "Close Tab?", "This closes the selected Herdr Tab and all Panes in it.")
	}
}

func tabsForProject(projection herdr.Projection, projectID string) []herdr.Tab {
	result := make([]herdr.Tab, 0, 4)
	for _, tab := range projection.Tabs {
		if tab.ProjectID == projectID {
			result = append(result, tab)
		}
	}
	// Herdr's snapshot lists a workspace's Tabs in their real order; Tab.Number
	// is a stable creation number that tab.move never renumbers, so sorting by
	// it would undo every reorder.
	return result
}

func panesForTab(projection herdr.Projection, tabID string) []herdr.Pane {
	result := make([]herdr.Pane, 0, 4)
	for _, pane := range projection.Panes {
		if pane.TabID == tabID {
			result = append(result, pane)
		}
	}
	return result
}
