package nativeui

import (
	"runtime"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
)

/**
 * [INPUT]: 依赖 mygo 的 Flyout（Tray/Parent 锚定、Popover 系统材质、菜单式关闭与 Escape）、gorexIconButton（右上角页签图标）、agent_card 共享卡片、agent_quick_panel 的分组/过滤芯片与条目行
 * [OUTPUT]: 对外提供 Shell.QuickPanelView（Agents 为主：标题 + 右上 Agents/Quota/Services/Tasks 图标页签（Automations 仅插件门打开时第五枚），Agents 列表按状态分组带过滤芯片；Quota 额度卡片 / Services / Tasks——仅 herdr-projects 门打开时 / Automations 未读收件——仅 automations 门打开时）、Shell.selectQuickPanelTab、Shell.ToggleQuickPanel（托盘）、Shell.toggleAgentActivityPanel（标题栏按钮）、QuickPanelHeightFor（内容驱动高度纯函数）
 * [POS]: Agent 活动的唯一浮层：托盘/标题栏打开，一层到底——点击 Agent 直达其面板并收起浮层，没有第二层详情；同一 Flyout 生命周期，锚定/贴边/翻转/关闭归框架
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Floating Agent activity panel (0.7 §17.1/§17.2, 2026-10-14 revision; the
// chrome is Agents-first since 2026-10-31): one MyGo flyout at a time — a
// system Popover on macOS — anchored beneath the menu-bar item or the
// titlebar's agent button. It takes the keyboard while shown and closes
// like a menu: outside click, focus moving away, app deactivation, or
// Escape. One shared StatusCenterSnapshot; the tab's name leads and every
// tab — Agents included — is a trailing icon; on Agents the sidebar's
// compact agent cards appear grouped by state behind filter chips. There is
// one layer: a click lands on the Agent's own panel and closes the flyout.
// The height follows the content up to the fixed cap — beyond it the list
// scrolls inside.

// QuickPanelWidth is 340 DIP (2026-10-31, ux-audit option C): the
// Agents-first chrome — tab name, three trailing tab icons, filter chips —
// leaves the agent cards ample room; the four-label strip needed 440.
const QuickPanelWidth = 340

// The panel's tabs are named by a stable id (s.quickPanelTab): Agents leads
// the header, the others are trailing icons. Tasks exists only while the
// herdr-projects plugin gate is open.
const (
	quickPanelAgentsTab   = 0
	quickPanelServicesTab = 1
	quickPanelTasksTab    = 2
	quickPanelQuotaTab    = 3
	// quickPanelAutomationsTab lists automation runs nobody has opened; it
	// exists only while the automations plugin gate is open.
	quickPanelAutomationsTab = 4
)

// quickPanelTabItem is one tab: its stable id and the icon that stands for
// it (the label names the button for readers and its tooltip).
type quickPanelTabItem struct {
	id    int
	label string
	svg   *ui.SVG
}

// quickPanelTabItems lists the tabs in display order. Agents has no icon
// button — it is the panel itself; its count lives in the filter chips.
func (s *Shell) quickPanelTabItems() []quickPanelTabItem {
	items := []quickPanelTabItem{
		{quickPanelAgentsTab, "Agents", iconAgent},
		{quickPanelQuotaTab, "Quota", iconQuota},
		{quickPanelServicesTab, "Services", iconServer},
	}
	if s.workboardAvailable() {
		items = append(items, quickPanelTabItem{quickPanelTasksTab, "Tasks", iconTask})
	}
	if s.automationsAvailable() {
		items = append(items, quickPanelTabItem{quickPanelAutomationsTab, "Automations", iconBolt})
	}
	return items
}

// quickPanelTabTitle names the tab the panel currently shows; it leads the
// header where the four-label strip used to.
func quickPanelTabTitle(tab int) string {
	switch tab {
	case quickPanelQuotaTab:
		return "Quota"
	case quickPanelServicesTab:
		return "Services"
	case quickPanelTasksTab:
		return "Tasks"
	case quickPanelAutomationsTab:
		return "Automations"
	}
	return "Agents"
}

// QuickPanelHeight caps the content-driven height; the rows scroll inside
// beyond it. The flyout shrinks to fit a small work area on its own.
const QuickPanelHeight = 480

// quickPanelGap keeps breathing room between the anchor and the panel.
const quickPanelGap = 8

// quickPanelHeaderHeight budgets the title row and the list's own top
// padding (every tab); quickPanelChipsHeight adds the Agents tab's filter
// chip row. quickPanelGroupHeight is one status header; quickPanelRowHeight
// is one agent card; quickPanelUsageHeight is the usage caption under a
// card that has one; quickPanelEmptyHeight is the empty state's budget;
// quickPanelOfflineHeight is the Reconnect row's. They are measured from the
// rendered layout (TestQuickPanelHeightHoldsTheContent keeps them honest).
const (
	quickPanelHeaderHeight  = 46
	quickPanelChipsHeight   = 32
	quickPanelGroupHeight   = 25
	quickPanelRowHeight     = 38
	quickPanelUsageHeight   = 17
	quickPanelEmptyHeight   = 72
	quickPanelOfflineHeight = 36
	// quickPanelSubagentHeight is one nested sub-agent row (subagentRow's 24).
	quickPanelSubagentHeight = 24
)

// quickPanelMinHeight keeps the panel presentable when it is nearly empty.
const quickPanelMinHeight = 160

// quickPanelListHeight is the grouped-list budget without the chip row: the
// header, one status header per group, one row per listed agent and one
// caption per row that carries a usage line, clamped between the minimum
// and the fixed cap (the panel sizes to content, it is not a fixed-height
// window). The Tasks tab shares it for its own groups.
func quickPanelListHeight(rows, groups, captions int) int {
	budget := quickPanelHeaderHeight + groups*quickPanelGroupHeight +
		rows*quickPanelRowHeight + captions*quickPanelUsageHeight
	if rows == 0 {
		budget = quickPanelHeaderHeight + quickPanelEmptyHeight
	}
	return min(max(budget, quickPanelMinHeight), QuickPanelHeight)
}

// QuickPanelHeightFor is the Agents tab's budget: the list plus its filter
// chip row, never beyond the fixed cap.
func QuickPanelHeightFor(rows, groups, captions int) int {
	return min(quickPanelListHeight(rows, groups, captions)+quickPanelChipsHeight, QuickPanelHeight)
}

// QuickPanelView renders the floating panel content (§17.2): over the
// popover's material on macOS the content draws no background of its own;
// elsewhere it draws the panel (Windows 11 rounds and outlines the flyout,
// so no radius or border there). The chrome is Agents-first (2026-10-31,
// ux-audit option C): the tab's name leads, all four tabs are icon buttons
// at the trailing edge, and on Agents a chip row filters by status group.
// Escape closes through the flyout itself (§17.1) — the view deliberately
// does not take it.
func (s *Shell) QuickPanelView(c *ui.Context) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	c.Root().Background(ui.Transparent)
	body := ui.Column(c).Fill()
	if !c.Vibrancy() {
		body = body.Background(tokens.Content)
		if runtime.GOOS != "windows" {
			body = body.Radius(Radius().Card).Border(1, tokens.BorderSubtle)
		}
	}
	body.Children(func() {
		s.quickPanelChrome(c)
		if s.quickPanelTabIndex() == quickPanelAgentsTab {
			s.quickPanelRemoteDesktop(c)
		}
		// One keyed body per tab: a tab switch builds a new one, which fades
		// in (rowEnter) instead of the content snapping.
		tab := s.quickPanelTabIndex()
		ui.Column(c.Key("qp-tab:" + strconv.Itoa(tab))).FillWidth().Grow(1).MinHeight(0).Transition(rowEnter).Children(func() {
			s.quickPanelTabBody(c, tab)
		})
	})
}

// quickPanelTabBody draws the chosen tab under the chrome.
func (s *Shell) quickPanelTabBody(c *ui.Context, tab int) {
	t := c.Theme()
	sp := Spacing()
	switch tab {
	case quickPanelServicesTab:
		s.quickPanelServices(c)
		return
	case quickPanelTasksTab:
		s.quickPanelTasks(c)
		return
	case quickPanelQuotaTab:
		s.quickPanelQuota(c)
		return
	case quickPanelAutomationsTab:
		s.quickPanelAutomations(c)
		return
	}
	if s.offline {
		ui.Row(c).FillWidth().Padding(sp.S, sp.M, 0).Gap(sp.S).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Herdr disconnected").FontSize(Typography().Caption).Grow(1).TextColor(t.TextMuted)
			if regularButton(c, "Reconnect").Clicked() {
				s.reloadInstances(false)
			}
		})
	}
	entries := s.filterStatusEntries(s.BuildStatusCenterSnapshot())
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(0, sp.S, sp.M).Children(func() {
			if len(entries) == 0 {
				emptyState(c, "No active agents", "")
				return
			}
			cards := make([]agent.AgentCardModel, len(entries))
			for i, entry := range entries {
				cards[i] = entry.Card
			}
			dupProjects := duplicateAgentProjectLabels(cards)
			for _, group := range quickPanelGroups(entries) {
				tone := group.Tone
				quickPanelGroupHeader(c, group.Label, len(group.Entries), &tone)
				for _, entry := range group.Entries {
					s.quickPanelEntryRow(c, entry, dupProjects)
				}
			}
		})
	})
}

// quickPanelChrome draws the panel's header: the tab's name, then every tab
// — Agents included — as a trailing icon button, so the way back to the
// Agents list is always on the screen. On the Agents tab a chip per status
// group follows, each with its live count; the chosen chip narrows the
// list, clicking it again lists every group.
func (s *Shell) quickPanelChrome(c *ui.Context) {
	t := c.Theme()
	sp := Spacing()
	k := gorexColorsOf(t.Dark)
	tab := s.quickPanelTabIndex()
	ui.Row(c).FillWidth().Padding(sp.L, sp.M, 0).Gap(sp.XS).AlignItems(ui.Center).Children(func() {
		ui.Text(c, quickPanelTabTitle(tab)).FontSize(Typography().Section).FontWeight(WeightSemibold).Grow(1).SingleLine()
		if tab == quickPanelQuotaTab {
			s.usageReadingToggle(c)
		}
		for _, item := range s.quickPanelTabItems() {
			if gorexIconButton(c, k, item.svg, item.label+" tab", item.label, item.id == tab, gorexPaneBtn, gorexPaneGlyph).Clicked() {
				s.selectQuickPanelTab(item.id)
			}
		}
	})
	if tab != quickPanelAgentsTab {
		return
	}
	counts := quickPanelChipCounts(s.BuildStatusCenterSnapshot().Entries)
	ui.Row(c).FillWidth().Padding(sp.XS, sp.M, sp.XS).Gap(sp.XS).Children(func() {
		for g := range quickPanelGroupSpecs {
			selected := !s.workbench.filterAll && s.workbench.filter == g
			if quickPanelChip(c, g, counts[g], selected) {
				s.workbench.filterAll, s.workbench.filter = selected, g
				s.fitQuickPanel()
			}
		}
	})
}

// selectQuickPanelTab switches the tab and loads what it needs: Tasks reads
// the board, Quota reads the switched-on providers once (the service's own
// TTL makes a quick return a cache hit; no timer).
func (s *Shell) selectQuickPanelTab(id int) {
	s.quickPanelTab = id
	switch id {
	case quickPanelTasksTab:
		s.refreshWorkboardTasks(true)
	case quickPanelQuotaTab:
		s.refreshUsage(false)
	case quickPanelAutomationsTab:
		s.refreshAutomations(true)
	}
	s.fitQuickPanel()
}

// quickPanelTabIndex is the tab to show: the Tasks tab collapses back to
// Agents the moment its plugin gate closes.
func (s *Shell) quickPanelTabIndex() int {
	if s.quickPanelTab == quickPanelTasksTab && !s.workboardAvailable() {
		return 0
	}
	if s.quickPanelTab == quickPanelAutomationsTab && !s.automationsAvailable() {
		return 0
	}
	return s.quickPanelTab
}

// filterStatusEntries narrows the shared snapshot to the chosen chip group;
// the snapshot order (interactions first, then attention > review >
// working > idle) is kept.
func (s *Shell) filterStatusEntries(snapshot StatusCenterSnapshot) []StatusCenterEntry {
	if s.workbench.filterAll {
		return snapshot.Entries
	}
	entries := make([]StatusCenterEntry, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		if quickPanelBucketGroup(entry.Bucket) == s.workbench.filter {
			entries = append(entries, entry)
		}
	}
	return entries
}

// quickPanelDismissGrace is the window in which an anchor click right after
// the flyout dismissed still counts as "close": on macOS the outside press
// that dismissed the panel goes on to the element under it, so clicking the
// tray item or the titlebar button closes the panel first and only then
// delivers its action — without this grace the click would re-open the
// panel it meant to close.
const quickPanelDismissGrace = 250 * time.Millisecond

// quickPanelToggleCloses decides whether an anchor click closes the panel
// instead of opening it: either a panel is open, or one just dismissed.
func quickPanelToggleCloses(panel *mygo.Window, dismissedAt, now time.Time) bool {
	return panel != nil || now.Sub(dismissedAt) < quickPanelDismissGrace
}

// ToggleQuickPanel is the tray click action (§17.1 lifecycle): an open
// panel closes; otherwise one opens beneath the menu-bar item — the
// framework centers it under the icon and keeps it in the work area. The
// icon's own press does not dismiss a focusable flyout, so the click keeps
// toggling.
func (s *Shell) ToggleQuickPanel() {
	if s.trayHandle == nil {
		return
	}
	if quickPanelToggleCloses(s.quickPanel, s.quickPanelDismissedAt, time.Now()) {
		s.closeQuickPanel()
		return
	}
	s.openQuickPanelFlyout(mygo.FlyoutOptions{
		Tray:      s.trayHandle,
		Placement: mygo.PlacementBottom,
		Gap:       quickPanelGap,
	})
}

// toggleAgentActivityPanel is the titlebar button's action: the same
// flyout, anchored beneath the button — ui.Element.Bounds is already the
// content-area rectangle the flyout anchor wants. No main window (headless
// tests, platform fallback) keeps the click a no-op.
func (s *Shell) toggleAgentActivityPanel(anchor ui.Element) {
	if s.win == nil {
		return
	}
	if quickPanelToggleCloses(s.quickPanel, s.quickPanelDismissedAt, time.Now()) {
		s.closeQuickPanel()
		return
	}
	s.openQuickPanelFlyout(mygo.FlyoutOptions{
		Parent:    s.win,
		Anchor:    quickAnchorRect(anchor.Bounds()),
		Placement: mygo.PlacementBottom,
		Gap:       quickPanelGap,
	})
}

// quickAnchorRect converts the element bounds (content-area DIPs) into the
// flyout anchor rectangle.
func quickAnchorRect(b ui.Rect) mygo.Rectangle {
	return mygo.Rectangle{X: int(b.X), Y: int(b.Y), Width: int(b.W), Height: int(b.H)}
}

// openQuickPanelFlyout creates and shows the panel (§17.1): a focusable
// Popover — the system's material, arrow and animation on macOS — that
// closes as the user clicks elsewhere, another window takes the keyboard,
// the app deactivates, or Escape reaches it. A panel still open closes
// first; its OnClosed is identity-guarded so a replacement created here
// survives the stale callback.
func (s *Shell) openQuickPanelFlyout(base mygo.FlyoutOptions) {
	// Detach before closing: a programmatic Close fires OnClosed inline, and
	// the stale listener must not clear the replacement opened right here.
	if prev := s.quickPanel; prev != nil {
		s.quickPanel = nil
		prev.Close()
	}
	// The actionable rows' usage captions refresh right before showing,
	// forced past the throttle.
	s.refreshUsageProjection(true)
	if s.quickPanelTabIndex() == quickPanelTasksTab {
		s.refreshWorkboardTasks(true)
	}
	if s.quickPanelTabIndex() == quickPanelQuotaTab {
		s.refreshUsage(false)
	}
	if s.quickPanelTabIndex() == quickPanelAutomationsTab {
		s.refreshAutomations(true)
	}
	base.Width = QuickPanelWidth
	base.Height = s.quickPanelContentHeight()
	base.Focusable = true
	base.Popover = true
	base.Shadow = true
	base.Content = ui.View(s.QuickPanelView)
	panel := mygo.NewFlyout(base)
	s.quickPanel = panel
	panel.OnClosed(func() {
		s.quickPanelDismissedAt = time.Now()
		if s.quickPanel == panel {
			s.quickPanel = nil
		}
	})
}

// closeQuickPanel closes the open panel. Nil-safe (headless sessions, tests,
// already-closed panel).
func (s *Shell) closeQuickPanel() {
	if s.quickPanel != nil {
		s.quickPanel.Close()
	}
}

// hideQuickPanel is the navigation path (a row click, a menu action): close
// the panel before landing, so the content does not flicker to another
// layout during the close animation.
func (s *Shell) hideQuickPanel() {
	s.closeQuickPanel()
}

// invalidateQuickPanel repaints the open panel after an interaction or a
// background projection change.
func (s *Shell) invalidateQuickPanel() {
	if s.quickPanel != nil && s.quickPanel.IsVisible() {
		s.quickPanel.Invalidate()
	}
}

// quickPanelContentHeight is the panel height for the current content
// (§17.1): the fixed Services tab, or the Agents list's content-driven
// height (the Reconnect row included while Herdr is unreachable).
func (s *Shell) quickPanelContentHeight() int {
	switch s.quickPanelTabIndex() {
	case quickPanelServicesTab:
		return QuickPanelHeight
	case quickPanelTasksTab:
		return s.quickPanelTasksHeight()
	case quickPanelQuotaTab:
		return s.quickPanelQuotaHeight()
	case quickPanelAutomationsTab:
		return s.quickPanelAutomationsHeight()
	}
	entries := s.filterStatusEntries(s.BuildStatusCenterSnapshot())
	captions := 0
	for _, entry := range entries {
		if s.usageLineFor(entry.Card.Key) != "" {
			captions++
		}
	}
	height := QuickPanelHeightFor(len(entries), len(quickPanelGroups(entries)), captions)
	if len(entries) > 0 {
		height += s.quickPanelSubagentRows(entries) * quickPanelSubagentHeight
	}
	if s.offline {
		height += quickPanelOfflineHeight
	}
	if s.remoteDesktopTrayLabel() != "" {
		height += quickPanelRemoteHeight
	}
	return min(height, QuickPanelHeight)
}

// fitQuickPanel resizes the open flyout to the current content height; the
// framework re-places it against its anchor and the work area.
func (s *Shell) fitQuickPanel() {
	if s.quickPanel == nil {
		return
	}
	s.quickPanel.SetSize(QuickPanelWidth, s.quickPanelContentHeight())
}
