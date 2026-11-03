package nativeui

/**
 * [INPUT]: 依赖 assistants_state 的门与概览缓存、sidebar_sections 的 sidebarSection 壳、treeRow、tipped、routeAssistants
 * [OUTPUT]: assistantsSection（侧栏 Assistants 区）、showAssistant/openNewAssistant（行点击与 “+” 的去向）
 * [POS]: 产品决策 2026-11-03：Assistants 区取代 Projects 区（门开时），夹在 Pin 与 Workspace 之间；每行一个 Assistant——名字、状态点（需要你 > 运行中 > 暂停/空）与待你处理数；渲染零 IO，只画 refreshAssistants 缓存的概览；门关一切零痕迹
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"

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
	add := &treeRowAction{Label: "New Assistant…", Glyph: iconPlus, Run: s.openNewAssistant}
	s.sidebarSection(c, sidebarSectionSpec{
		Key: "section:assistants", Title: "Assistants", Count: len(rows), Action: add,
		Menu: func(m *ui.Menu) {
			if m.Item(add.Label).Chosen() {
				add.Run()
			}
			if m.Item("Open Assistants").Chosen() {
				s.showAssistant("")
			}
		},
	}, &s.tasksOpen, func() {
		for _, row := range rows {
			row := row
			onPage := s.router.Path() == routeAssistants && s.assistants.selected == row.ID
			entry := treeRow(c, treeRowSpec{
				Height:   s.sidebarRowHeight(),
				Key:      "assistant:" + row.ID,
				Selected: onPage,
				Mark:     visualMark{svg: iconAssistant},
				Label:    row.Name,
				Dot:      assistantDot(row),
				Count:    row.NeedsYou,
			})
			tipped(c, entry, assistantTip(row))
			if entry.Clicked() {
				s.showAssistant(row.ID)
			}
		}
	})
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

// showAssistant opens the Assistants page on one Assistant ("" keeps the page's
// own choice).
func (s *Shell) showAssistant(id string) {
	if !s.assistantsAvailable() || s.holdCommitNavigation() {
		return
	}
	if id != "" {
		s.assistants.selected = id
	}
	s.hideQuickPanel()
	if s.router.Path() != routeAssistants {
		s.router.Push(routeAssistants)
	}
}

// openNewAssistant opens the page with its New Assistant form up.
func (s *Shell) openNewAssistant() {
	s.assistants.selected = ""
	s.showAssistant("")
	s.assistants.newForm = true
}
