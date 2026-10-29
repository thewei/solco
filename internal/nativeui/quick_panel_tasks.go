package nativeui

/**
 * [INPUT]: 依赖 workboard_tasks 的 workboardBoard/workboardGroups/projectForWorktree、quickPanelGroupHeader 与 agent 卡片同款行尺寸、statusDot/operationalState、openWorkboardTaskPage
 * [OUTPUT]: Shell.quickPanelTasks（Agent 活动浮层的 Tasks 页签：按 Needs you / Ready to approve / Working / To do 分组的只读列表）、quickPanelTasksHeight
 * [POS]: herdr-projects work board 在浮层里的唯一呈现；点击打开任务详情页（workboard_page.go，页内有 Open Project 进入已匹配的 Project）；quickPanelTaskRowIn 让 Project 页复用同一行并接入 listNav（弹层传 nil）；行右键菜单与底部 New task 只发起意图，写入全部走 workboard_actions.go（Approve 经 shared confirm）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/workboard"
)

// groupOperationalState maps a board group onto the shared attention model,
// so the Tasks tab speaks the same dot language as the Agents tab.
func groupOperationalState(g workboard.Group) operationalState {
	switch g {
	case workboard.GroupNeedsYou:
		return opNeedsAttention
	case workboard.GroupReady:
		return opReadyForReview
	case workboard.GroupWorking:
		return opWorking
	default:
		return opIdle
	}
}

// quickPanelTasksHeight sizes the tab like the Agents list: one header per
// group, one row per task, the empty state's budget when there is nothing.
func (s *Shell) quickPanelTasksHeight() int {
	groups := workboardGroups(s.workboard.board)
	rows := 0
	for _, group := range groups {
		rows += len(group.Tasks)
	}
	return min(QuickPanelHeightFor(rows, len(groups), 0)+quickPanelTasksFooterHeight, QuickPanelHeight)
}

// quickPanelTasksFooterHeight is the New Task button row under the list.
const quickPanelTasksFooterHeight = 44

// quickPanelTasks renders the Tasks tab.
func (s *Shell) quickPanelTasks(c *ui.Context) {
	sp := Spacing()
	board := s.workboard.board
	groups := workboardGroups(board)
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(0, sp.S, sp.M).Children(func() {
			if board.failed {
				ui.Row(c).FillWidth().Padding(sp.S, sp.M, 0).Gap(sp.S).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Couldn't read the board").FontSize(Typography().Caption).Grow(1).TextColor(c.Theme().TextMuted)
					if regularButton(c, "Retry").Clicked() {
						s.refreshWorkboardTasks(true)
					}
				})
			}
			if len(groups) == 0 {
				if board.loadedAt.IsZero() && !board.failed {
					return // first read in flight: nothing to say yet
				}
				if !board.failed {
					emptyState(c, "No open tasks", "")
				}
				return
			}
			for _, group := range groups {
				tone := operationalTone(groupOperationalState(group.Group))
				quickPanelGroupHeader(c, group.Group.Label(), len(group.Tasks), &tone)
				for _, task := range group.Tasks {
					s.quickPanelTaskRow(c, task, group.Group)
				}
			}
		})
	})
	ui.Row(c).FillWidth().Padding(sp.S, sp.M).Gap(sp.S).Justify(ui.End).Children(func() {
		if regularButton(c, "New Task").Clicked() {
			s.openWorkboardAdd(false)
		}
		if regularButton(c, "Add and Delegate").Clicked() {
			s.openWorkboardAdd(true)
		}
	})
}

// quickPanelTaskRow is one task: the state dot, the title, and the id with the
// plugin's status note beneath. A click opens the task's page.
func (s *Shell) quickPanelTaskRow(c *ui.Context, task workboard.Task, group workboard.Group) {
	s.quickPanelTaskRowIn(c, task, group, nil)
}

// quickPanelTaskRowIn is quickPanelTaskRow as one row of a keyboard list; a
// nil nav is a plain row (the popover's).
func (s *Shell) quickPanelTaskRowIn(c *ui.Context, task workboard.Task, group workboard.Group, nav *listNav) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	title := task.Title
	if title == "" {
		title = task.ID
	}
	caption := task.ID
	if s.workboard.board.extras.pinnedTasks[task.ID] {
		caption = "★ " + caption
	}
	if tags := task.Tags(); len(tags) > 0 {
		caption += " · " + strings.Join(tags, " ")
	}
	if note := task.Msg; note != "" {
		caption += " · " + note
	}
	var row ui.Element
	ui.Box(c.Key("task:" + task.ID)).FillWidth().Children(func() {
		row = ui.ButtonBase(c).
			FillWidth().
			MinHeight(38).
			Radius(6).
			Padding(0, agentCardPadX).
			Gap(agentCardGap).
			AlignItems(ui.Center).
			Label(title)
		nav.row(c, row, navRow{Key: "task:" + task.ID, Level: 1})
		if nav != nil {
			row.FocusRing(false) // the list's cursor face replaces the ring
		}
		if tint, ok := rowTint(tokens, nav.showsCursor(row), row.Hovered()); ok {
			row.Background(tint)
		}
		row.Children(func() {
			ui.Box(c).Size(agentCardMark, agentCardMark).Shrink(0).Children(func() {
				ui.Box(c).Absolute().Left(5).Top(5).Children(func() {
					statusDot(c, groupOperationalState(group))
				})
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
				tipped(c, ui.Text(c, title).FontSize(Typography().Body).SingleLine(), title)
				tipped(c, ui.Text(c, caption).FontSize(Typography().Micro).TextColor(t.TextMuted).SingleLine(), caption)
			})
			if group == workboard.GroupWorking {
				ui.Spinner(c)
			}
		})
	})
	if row.Clicked() {
		s.openWorkboardTaskPage(task.ID)
	}
	row.ContextMenu(func(m *ui.Menu) { s.workboardTaskMenu(m, task, group, s.hideQuickPanel) })
}

// workboardTaskMenu is a task row's context menu, shared by the popover and
// the sidebar: the group's writes, then Open Work Board. before runs ahead of
// a choice (the popover closes itself).
func (s *Shell) workboardTaskMenu(m *ui.Menu, task workboard.Task, group workboard.Group, before func()) {
	var detail *workboard.Detail
	if d, ok := s.workboard.board.details[task.ID]; ok {
		detail = &d.detail
	}
	for _, a := range workboardActionsForTask(task, group, detail) {
		if m.Item(a.Label).Chosen() {
			before()
			s.startWorkboardAction(a, task)
		}
	}
	m.Separator()
	pinned := s.workboard.board.extras.pinnedTasks[task.ID]
	label := "Pin"
	if pinned {
		label = "Unpin"
	}
	if m.Item(label).Chosen() {
		before()
		s.setWorkboardPin(task.ID, !pinned)
	}
	if m.Item("Open Work Board").Chosen() {
		before()
		s.openWorkBoard()
	}
}
