package nativeui

import (
	"time"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: 依赖 mygo/ui 的 ScrollHorizontal/ScrollState（滚动与跟随选中）、Herdr 投影的 Tab/Pane、project_tree 的 tabRowMark/liveAgentStatus/tabMenuItems、service_index 的 sidebarActivity 汇总、status_indicator 的状态槽位、gorex_style 的 chip 面
 * [OUTPUT]: 对外提供 Shell.tabsInTitleBar（设置开关的读法）、Shell.titlebarTabs（当前 Workspace 的 Tab 清单模型，纯数据）、Shell.tabBarRow（标题栏浏览器式 Tab 条：每个 Tab 一枚 chip——品牌图/标题/状态槽位/悬停关闭，溢出时渐隐+‹ ›+滚动并让选中项入视，末尾 + 新建）
 * [POS]: 设置「Show Tabs in Sidebar」关闭时占据面包屑位置的 Tab 条（2026-10-30 用户拍板，design: designs/agent-surfaces，Flat 样式——与面包屑 ghost chip 同族）；Tab 仍是 Herdr 的 Tab：点击走 selectTab、关闭走同一个 close-tab 确认、新建走 newProjectTab，没有第二套 Tab 状态；此时侧栏 Workspace 树只列 Workspace
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// tabsInTitleBar reports the Show Tabs in Sidebar switch turned off.
func (s *Shell) tabsInTitleBar() bool { return s.settings.General.TabsInTitleBar() }

// titlebarTab is one chip of the bar: everything the chip draws, resolved
// once from the live projection.
type titlebarTab struct {
	ID       string
	Label    string
	Mark     visualMark
	Selected bool
	Panes    int
	Tone     StatusTone
	Detail   string
	HasState bool
}

// titlebarTabs lists the selected Workspace's Tabs in Herdr's order, each
// with its own state (the strongest agent under it, or a running service).
func (s *Shell) titlebarTabs(dark bool) []titlebarTab {
	project := s.selectedProject()
	if project == nil {
		return nil
	}
	activity := buildSidebarActivity(s.projection, s.serviceIndex, s.paneShellIdle)
	tabs := tabsForProject(s.projection, project.ID)
	out := make([]titlebarTab, 0, len(tabs))
	for _, tab := range tabs {
		panes := panesForTab(s.projection, tab.ID)
		label := tab.Label
		if len(panes) == 1 {
			if title, ok := s.agentCardTitleForPane(panes[0].ID); ok {
				label = title
			}
		}
		spec := treeRowSpec{Status: s.liveAgentStatus(tab.AgentStatus, panes), Activity: activity[tab.ID]}
		tone, detail, ok := spec.indicator()
		out = append(out, titlebarTab{
			ID: tab.ID, Label: fallbackText(label, "Tab"), Mark: s.tabRowMark(panes, dark),
			Selected: tab.ID == s.selectedTabID, Panes: len(panes),
			Tone: tone, Detail: detail, HasState: ok,
		})
	}
	return out
}

const (
	tabChipMaxWidth float32 = 180
	tabChipMinWidth float32 = 84
	tabScrollStep   float32 = 180
)

// tabBarRow draws the title-bar Tab strip and reports whether it drew one.
func (s *Shell) tabBarRow(c *ui.Context) bool {
	t := c.Theme()
	k := gorexColorsOf(t.Dark)
	tabs := s.titlebarTabs(t.Dark)
	project := s.selectedProject()
	if project == nil || len(tabs) == 0 {
		ui.Text(c, "Herdr").TextColor(t.TextMuted).Grow(1).SingleLine()
		return false
	}
	selectionMoved := s.tabBarSelected != s.selectedTabID
	s.tabBarSelected = s.selectedTabID

	ui.Row(c).Grow(1).MinWidth(0).Gap(2).AlignItems(ui.Center).Children(func() {
		overflowLeft := s.tabBarScroll.X > 1
		overflowRight := s.tabBarScroll.MaxX-s.tabBarScroll.X > 1
		if overflowLeft || overflowRight {
			scrollButton(c, k, iconChevronLeft, "Scroll Tabs Left", overflowLeft, func() {
				s.tabBarScroll.X = max(0, s.tabBarScroll.X-tabScrollStep)
			})
		}
		strip := ui.ScrollHorizontal(c).TrackScroll(&s.tabBarScroll).Height(Controls().RegularHeight).
			MinWidth(0).Shrink(1).Gap(2).AlignItems(ui.Center)
		strip.Children(func() {
			for _, tab := range tabs {
				s.tabChip(c, k, tab, selectionMoved && tab.Selected)
			}
		})
		if overflowLeft || overflowRight {
			scrollButton(c, k, iconChevronRight, "Scroll Tabs Right", overflowRight, func() {
				s.tabBarScroll.X = min(s.tabBarScroll.MaxX, s.tabBarScroll.X+tabScrollStep)
			})
		}
		add := gorexIconButton(c, k, iconPlus, "New Tab", "New Tab in "+project.Label, false, gorexIconBtn, gorexIconGlyph)
		if add.Clicked() {
			s.newProjectTab(*project)
		}
		ui.Box(c).Grow(1).MinWidth(0).PassThrough()
	})
	return true
}

// scrollButton is the bar's ‹ / › : the shared small icon button while there
// is more to reach, the disabled face when the strip is at that end.
func scrollButton(c *ui.Context, k *gorexColors, glyph *ui.SVG, label string, can bool, move func()) {
	if !can {
		gorexIconButtonDisabled(c, k, glyph, label, label, gorexPaneBtn, gorexPaneGlyph)
		return
	}
	b := gorexIconButton(c, k, glyph, label, label, false, gorexPaneBtn, gorexPaneGlyph)
	if b.Clicked() {
		move()
	}
}

// tabChip is one Tab: brand mark, title, the pane tally when it is split, the
// shared state slot, and a close × that shows under the pointer or on the
// selected chip. The selected chip keeps the neutral chip face.
func (s *Shell) tabChip(c *ui.Context, k *gorexColors, tab titlebarTab, reveal bool) {
	t := c.Theme()
	glyph := Controls().IconRegular
	b := ui.ButtonBase(c.Key("tab:"+tab.ID)).
		Height(Controls().RegularHeight).
		MinWidth(tabChipMinWidth).MaxWidth(tabChipMaxWidth).Shrink(0).
		Radius(Radius().Row).Padding(0, Spacing().S, 0, Spacing().M).Gap(Spacing().S).
		AlignItems(ui.Center).Label(tab.Label)
	gorexToggleFace(b, k, tab.Selected)
	b.Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
	ink := t.TextMuted
	if tab.Selected || b.Hovered() {
		ink = t.Text
	}
	if reveal {
		b.ScrollIntoView()
	}
	b.Children(func() {
		if tab.Mark.svg != nil || tab.Mark.bmp != nil {
			ui.Box(c).Size(glyph, glyph).Shrink(0).PassThrough().Children(func() {
				markView(c, tab.Mark, glyph, ink)
			})
		}
		ui.Text(c, tab.Label).FontSize(Typography().Section).TextColor(ink).
			SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).Grow(1).PassThrough()
		if tab.Panes > 1 {
			ui.Text(c, itoa(tab.Panes)).Label(itoa(tab.Panes) + " panes").FontSize(Typography().Micro).
				TextColor(t.TextMuted).Shrink(0).PassThrough()
		}
		if tab.HasState {
			statusIndicatorTone(c, tab.Tone, tab.Detail)
		}
		if tab.Selected || b.Hovered() {
			closeBtn := gorexIconButton(c, k, iconClose, "Close Tab", "Close Tab", false, gorexRowBtn, gorexRowGlyph)
			if closeBtn.Clicked() {
				s.openConfirm("close-tab", tab.ID, "Close Tab?", "This closes the selected Herdr Tab and all Panes in it.")
			}
		}
	})
	if b.Clicked() && !tab.Selected {
		s.selectTab(tab.ID)
	}
	b.ContextMenu(func(m *ui.Menu) {
		if herdrTab := tabByID(s.projection, tab.ID); herdrTab != nil {
			s.tabMenuItems(m, *herdrTab)
		}
	})
}
