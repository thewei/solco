package nativeui

/**
 * [INPUT]: 依赖 assistants_state 的门与概览缓存、sidebar_sections 的 sidebarSection 壳、treeRow、tipped、routeAssistants
 * [OUTPUT]: assistantsSection（侧栏 Assistants 区）、showAssistant/openNewAssistant（行点击与 “+” 的去向）
 * [POS]: 产品决策 2026-11-03：Assistants 区取代 Projects 区（门开时），夹在 Pin 与 Workspace 之间；每行一个 Assistant——名字、状态点（需要你 > 运行中 > 暂停/空）与待你处理数；渲染零 IO，只画 refreshAssistants 缓存的概览；门关一切零痕迹
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/thewei/solco/internal/personalagent"
)

// assistantsSection renders the sidebar Assistants section; nothing while the
// gate is closed. Reading the overview is a throttled background refresh.
func (s *Shell) assistantsSection(c *ui.Context) {
	if !s.assistantsAvailable() {
		return
	}
	s.refreshAssistants(false)
	rows := s.assistants.overview.Assistants
	groups := s.assistants.overview.Groups
	// One "+" with a real menu: the two things a person can start here.
	add := &treeRowAction{Label: "New…", Tooltip: "New Assistant or Group", Glyph: iconPlus, Menu: s.newAssistantMenu}
	s.sidebarSection(c, sidebarSectionSpec{
		Key: "section:assistants", Title: "Assistants", Count: len(rows) + len(groups), Action: add,
		Menu: func(m *ui.Menu) {
			s.newAssistantMenu(m)
			m.Separator()
			if m.Item("Open Assistants").Chosen() {
				s.showAssistant("")
			}
		},
	}, &s.tasksOpen, func() {
		for _, row := range rows {
			row := row
			onPage := s.router.Path() == routeAssistants && s.assistants.selectedGroup == "" && s.assistants.selected == row.ID
			entry := treeRow(c, treeRowSpec{
				Height:   s.sidebarRowHeight(),
				Key:      "assistant:" + row.ID,
				Selected: onPage,
				Avatar:   func() { assistantAvatar(c, avatarIdentity{ID: row.ID, Name: row.Name}, 16) },
				Label:    row.Name,
				Dot:      assistantDot(row),
				Count:    row.NeedsYou,
			})
			tipped(c, entry, assistantTip(row))
			if entry.Clicked() {
				s.showAssistant(row.ID)
			}
		}
		for _, g := range groups {
			g := g
			faces := make([]avatarIdentity, 0, len(g.Members))
			for _, m := range g.Members {
				faces = append(faces, avatarIdentity{ID: m.AssistantID, Name: m.Name})
			}
			dot := opUnknown
			if g.Open > 0 {
				dot = opWorking
			}
			entry := treeRow(c, treeRowSpec{
				Height:      s.sidebarRowHeight(),
				Key:         "group:" + g.ID,
				Selected:    s.router.Path() == routeAssistants && s.assistants.selectedGroup == g.ID,
				Avatar:      func() { groupAvatar(c, faces, 16) },
				AvatarWidth: groupAvatarWidth(len(faces), 16),
				Label:       g.Title,
				Dot:         dot,
			})
			tipped(c, entry, groupTip(g))
			if entry.Clicked() {
				s.showGroup(g.ID)
			}
		}
	})
}

// groupTip names a Group by its members.
func groupTip(g personalagent.OverviewGroup) string {
	names := make([]string, 0, len(g.Members))
	for _, m := range g.Members {
		names = append(names, m.Name)
	}
	return g.Title + " · " + strings.Join(names, ", ")
}

// assistantDot is the row's one state: waiting on a person beats work in
// flight; a paused Assistant has no state.
func assistantDot(row personalagent.OverviewRow) operationalState {
	switch {
	case row.Status != "active":
		return opUnknown
	case row.NeedsYou > 0 || row.Approvals > 0:
		return opNeedsAttention
	case row.Running > 0:
		return opWorking
	}
	return opUnknown
}

// assistantTip names the facts the row's slot stands for.
func assistantTip(row personalagent.OverviewRow) string {
	text := row.Name
	if row.Status != "active" {
		return text + " · paused"
	}
	if row.NeedsYou > 0 {
		text += fmt.Sprintf(" · %d need you", row.NeedsYou)
	}
	if row.Approvals > 0 {
		text += fmt.Sprintf(" · %d to approve", row.Approvals)
	}
	if row.Running > 0 {
		text += fmt.Sprintf(" · %d running", row.Running)
	}
	return text
}

// AssistantsSelection is what the sidebar just chose: an Assistant's chat or a
// Group's. A mounted page reads it at once instead of at its next poll.
type AssistantsSelection struct {
	ID    string `json:"id"`
	Group bool   `json:"group"`
}

// assistantsSelectEvent wakes a mounted page on a sidebar selection: without
// it a click waited for the page's 4 s poll before anything changed.
var assistantsSelectEvent = mygo.NewEvent[AssistantsSelection]("assistants:select")

// emitAssistantsSelection tells a mounted page. Window lane.
func (s *Shell) emitAssistantsSelection(id string, group bool) {
	if s.win != nil {
		_ = assistantsSelectEvent.Emit(s.win, AssistantsSelection{ID: id, Group: group})
	}
}

// showAssistant opens the Assistants page on one Assistant ("" keeps the page's
// own choice).
func (s *Shell) showAssistant(id string) {
	if !s.assistantsAvailable() || s.holdCommitNavigation() {
		return
	}
	if id != "" {
		s.assistants.selected = id
		s.assistants.selectedGroup = ""
	}
	s.hideQuickPanel()
	if s.router.Path() != routeAssistants {
		s.router.Push(routeAssistants)
	}
	if id != "" {
		s.emitAssistantsSelection(id, false)
	}
}

// showGroup opens the Assistants page on one Group's conversation.
func (s *Shell) showGroup(id string) {
	if !s.assistantsAvailable() || s.holdCommitNavigation() {
		return
	}
	s.assistants.selectedGroup = id
	s.hideQuickPanel()
	if s.router.Path() != routeAssistants {
		s.router.Push(routeAssistants)
	}
	s.emitAssistantsSelection(id, true)
}

// newAssistantMenu is the "+" menu — exactly two items, shared by the header
// button and the section's context menu.
func (s *Shell) newAssistantMenu(m *ui.Menu) {
	if m.Item("New Assistant").Chosen() {
		s.openNewAssistant()
	}
	if m.Item("New Group").Chosen() {
		s.openNewGroup()
	}
}

// openNewAssistant opens the page with its New Assistant sheet up.
func (s *Shell) openNewAssistant() { s.postAssistantsIntent(intentNewAssistant) }

// openNewGroup opens the page with its New Group sheet up.
func (s *Shell) openNewGroup() { s.postAssistantsIntent(intentNewGroup) }
