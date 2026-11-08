package nativeui

/**
 * [INPUT]: 依赖 mygo/ui 的 Menu/Element、internal/herdr 的 pane 变更 RPC（SplitPane/TogglePaneZoom/ReleaseAgent）、workbench.go 的 paneRuntimeAgent/canReleaseAgent/agentCardForPane/markAgentReviewed、Shell.copyToClipboard、dialogs 的确认通道
 * [OUTPUT]: 对外提供 headerActions（所有路由标题栏共用的唯一 Actions 触发器：⋯ 图标按钮，位于标题栏右侧工具簇最左，无 chevron）、splitPaneAction/zoomPaneAction/closePaneAction（pane 动作唯一实现）、paneOverflowItems/surfaceOverflowItems（菜单构建器；surfaceOverflowItems 是 Workspace 路由 Actions 的菜单；含复制 Herdr workspace_id 与 pane_id；Release Agent 项只对 done/idle 的 agent pane 出现；Mark Reviewed 只对 ReviewPending 的 agent pane 出现）
 * [POS]: nativeui 的 pane 动作单一来源：卡片头按钮、右键菜单、侧栏行、标题栏溢出全部走这里；复制项读取目标 Pane 的 Herdr 投影，不依赖当前选中项；单 Pane 的 zoom 语义（两侧栏最大化，F143；右侧面板随侧栏让位、随侧栏回来，sidePanelState.zoomed）在此收口；agent 退出后 Herdr 仍保留条目（词汇表无 exited），显式 Release 是唯一撤除通道
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"
	"log/slog"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
)

// headerActions draws the Actions trigger with build as its menu: a bare
// ellipsis icon button (shadcn's, 2026-10-29; it was sliders + chevron right
// after the path) that opens the system menu. Every route's title bar shows
// its page actions through this one trigger — the workspace surfaces
// (surfaceOverflowItems, whose pane items are paneOverflowItems, the builder
// behind the tree rows' right-click menu and the pane header's menu), a
// session, a file — as the first of the trailing tools (titlebarTools), so
// the same control sits in the same place and reads the same on each page.
func headerActions(c *ui.Context, build func(m *ui.Menu)) {
	k := gorexColorsOf(c.Theme().Dark)
	var trigger ui.Element
	ui.Box(c).Children(func() {
		trigger = gorexIconButton(c, k, iconActions, "Actions", "Actions", false, gorexIconBtn, gorexIconGlyph)
	})
	trigger.Menu(build)
}

// splitPaneAction splits one Pane; the card header's buttons and the pane
// menu share it. The Info lines are the click-path evidence for device
// acceptance (2026-10-06 annotation A2/F143): if a header button dies on
// device, the log shows whether the shared action ran at all.
func (s *Shell) splitPaneAction(paneID, direction string) {
	slog.Info("pane action", "action", "split", "pane_id", paneID, "direction", direction)
	s.mutateSelecting(func() (herdr.Projection, error) {
		return s.runtime.SplitPane(s.activeInstance, paneID, direction)
	})
}

// zoomPaneAction toggles one Pane's zoom; the card header and the pane
// menu share it. On a split Tab that zooms the Pane through Herdr. On a
// single-Pane Tab Herdr answers reason=single_pane and nothing moves
// (probed live 2026-10-06), so the button maximizes the workspace instead:
// both side rails collapse so the pane fills the window, and a second
// press brings them back (2026-10-06 annotation A2/F143). The right panel is
// the second rail: it steps aside with the sidebar and returns with it
// (sidePanelState.zoomed).
func (s *Shell) zoomPaneAction(paneID string) {
	if s.tabPaneCount(paneID) <= 1 {
		slog.Info("pane action", "action", "zoom-rails", "pane_id", paneID)
		s.sidebarCollapsed = !s.sidebarCollapsed
		s.sidePanel.zoomed = s.sidebarCollapsed && s.sidePanel.open
		return
	}
	slog.Info("pane action", "action", "zoom", "pane_id", paneID)
	s.mutate(func() (herdr.Projection, error) {
		return s.runtime.TogglePaneZoom(s.activeInstance, paneID)
	})
}

// tabPaneCount resolves how many Panes Herdr lays out beside paneID's own:
// the breadth of its Tab's layout, or one when no layout reports otherwise
// (the unit-rect fallback in visiblePaneGeometry).
func (s *Shell) tabPaneCount(paneID string) int {
	pane := paneByID(s.projection, paneID)
	if pane == nil {
		return 1
	}
	if layout, ok := layoutForTab(s.projection, pane.TabID); ok && len(layout.Panes) > 0 {
		return len(layout.Panes)
	}
	return 1
}

// closePaneAction routes one Pane's close through the confirm dialog; the
// card header and the pane menu share it.
func (s *Shell) closePaneAction(paneID string) {
	slog.Info("pane action", "action", "close-confirm", "pane_id", paneID)
	s.openConfirm("close-pane", paneID, "Close Pane?", "This closes the Herdr Pane and its running terminal process.")
}

// paneOverflowItems is the ONE pane menu builder (2026-10-05 round two):
// the attached pane's right-click menu, the project tree row's right-click
// menu and the title-bar Actions pane section all render these items.
func (s *Shell) paneOverflowItems(m *ui.Menu, paneID, label string) {
	if s.isPanePinned(paneID) {
		if m.Item("Unpin").Chosen() {
			s.togglePanePin(paneID)
		}
	} else if m.Item("Pin Pane").Chosen() {
		s.togglePanePin(paneID)
	}
	if m.Item("Split Right").Chosen() {
		s.splitPaneAction(paneID, "right")
	}
	if m.Item("Split Down").Chosen() {
		s.splitPaneAction(paneID, "down")
	}
	zoomLabel := "Zoom / Unzoom"
	if layout, ok := s.selectedLayout(); ok && paneID == s.selectedPaneID && layout.Zoomed {
		zoomLabel = "Unzoom"
	}
	if m.Item(zoomLabel).Chosen() {
		s.zoomPaneAction(paneID)
	}
	if m.Item("Rename Pane…").Chosen() {
		s.openTextDialog("rename-pane", paneID, "Rename Pane", "Name", label)
	}
	if runtimeAgent, ok := s.paneRuntimeAgent(paneID); ok && canReleaseAgent(runtimeAgent) {
		if m.Item("Release Agent").Chosen() {
			s.openConfirm("release-agent", paneID, "Release Agent?",
				"Herdr stops listing "+runtimeAgent.Kind+" on this pane; its output stays in the terminal scrollback.")
		}
	}
	if card, ok := s.agentCardForPane(paneID); ok && card.ReviewPending {
		m.Separator()
		if m.Item("Mark Reviewed").Chosen() {
			s.markAgentReviewed(card.Key)
		}
	}
	if pane := paneByID(s.projection, paneID); pane != nil && pane.ProjectID != "" {
		if m.Item("Copy Workspace and Pane IDs").Chosen() {
			s.copyToClipboard(fmt.Sprintf("workspace_id=%s\npane_id=%s", pane.ProjectID, pane.ID))
		}
	}
	m.Separator()
	if m.Item("Close Pane…").Chosen() {
		s.closePaneAction(paneID)
	}
}

// surfaceOverflowItems fills the title-bar Actions menu with the current
// surface's actions (plan §8.2..8.4).
func (s *Shell) surfaceOverflowItems(m *ui.Menu) {
	switch s.surface.current() {
	case WorkspaceSurfaceCommit:
		s.commitOverflowItems(m)
		return
	case WorkspaceSurfaceChat:
		s.chatOverflowItems(m)
		return
	case WorkspaceSurfaceDetail:
		s.detailOverflowItems(m)
		return
	}
	if s.selectedPaneID != "" {
		label := "Pane"
		if pane := s.selectedPane(); pane != nil && pane.Label != "" {
			label = pane.Label
		}
		s.paneOverflowItems(m, s.selectedPaneID, label)
		m.Separator()
	}
	if project := s.selectedProject(); project != nil {
		m.Submenu("New Tab", func(sub *ui.Menu) { s.newTabMenu(sub, *project) })
	}
	if m.Item("Refresh Runtime").Chosen() {
		s.reloadInstances(false)
	}
}

// commitOverflowItems mirrors the commit actions (plan §8.4): the way out is
// back to the surface the Commit began from.
func (s *Shell) commitOverflowItems(m *ui.Menu) {
	if m.Item("Cancel Commit").Chosen() {
		s.cancelCommitSurface()
	}
	if s.surface.commit.InFlight {
		m.Item("Committing…")
	}
}

// chatOverflowItems mirrors the Chat surface actions.
func (s *Shell) chatOverflowItems(m *ui.Menu) {
	if m.Item("Back to Terminal").Chosen() {
		s.showSurface(WorkspaceSurfaceTerminal)
	}
	if len(s.chatQueuedItems()) > 0 {
		if m.Item("Cancel Follow-Up").Chosen() {
			s.cancelChatFollowUp()
		}
	}
	m.Separator()
	if s.selectedPaneID != "" {
		label := "Pane"
		if pane := s.selectedPane(); pane != nil && pane.Label != "" {
			label = pane.Label
		}
		s.paneOverflowItems(m, s.selectedPaneID, label)
	}
}
