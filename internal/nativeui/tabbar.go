package nativeui

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: 依赖 mygo/ui 的 ScrollHorizontal/ScrollState（滚动与跟随选中）、Herdr 投影的 Tab/Pane、project_tree 的 tabRowMark/liveAgentStatus/tabMenuItems、service_index 的 sidebarActivity 汇总、status_indicator 的状态槽位、gorex_style 的 chip 面
 * [OUTPUT]: 对外提供 Shell.tabsInTitleBar（设置开关的读法）、Shell.titlebarTabs（当前 Workspace 的 Tab 清单模型，纯数据）、Shell.tabBarRow（标题栏浏览器式 Tab 条：每个 Tab 一枚 chip——品牌图/标题/状态槽位/悬停关闭，拥挤时等宽收缩到仅图标+状态、悬停信息卡、All Tabs ▾ 菜单、chip 拖拽排序走 reorderTree→tab.move，过了地板才滚动+‹ ›，末尾 + 新建）
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
	Service  bool
	Keys     []string // the Select Tab shortcut as keycaps; nil past the ninth Tab
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
	for i, tab := range tabs {
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
			Tone: tone, Detail: detail, HasState: ok, Service: spec.serviceSlot(),
			Keys: selectTabKeycaps(i+1, len(tabs)),
		})
	}
	return out
}

// selectTabKeycaps is the Select Tab shortcut of the nth of count Tabs: ⌥⌘n
// up to the eighth, ⌥⌘9 for the last when there are more than eight (Last
// Tab), none for the Tabs between.
func selectTabKeycaps(n, count int) []string {
	switch {
	case n < lastTab:
		return acceleratorKeycaps(selectTabAccelerator(n))
	case n == count:
		return acceleratorKeycaps(selectTabAccelerator(lastTab))
	}
	return nil
}

const (
	tabChipMaxWidth float32 = 180
	tabChipMinWidth float32 = 120
	// The selected chip is allowed more, so its title survives the state
	// slot and the close × that share its row (2026-10-30 UX audit 3).
	tabChipSelectedMinWidth float32 = 160
	tabChipSelectedMaxWidth float32 = 240
	tabScrollStep           float32 = 180
	tabRevealWindow                 = 1500 * time.Millisecond
	// Equal-width shrink (2026-11-02, designs/tab-strip, hybrid A+B): once the
	// natural chips no longer fit, every unselected chip gets the same width
	// down to the compact floor, where it keeps only the brand mark and the
	// state slot; below that the strip scrolls and the All Tabs menu is the
	// way to reach any Tab by name.
	tabChipCompactWidth float32 = 48
	tabChipCompactBelow float32 = 96
	tabStripGap         float32 = 2
	// tabStripReserve is what the bar's other controls (+ and ▾) take beside the
	// strip, with a margin: a strip that overshoots by even a few points grows
	// the ‹ › buttons and steals ~50 more.
	tabStripReserve float32 = 80
	// tabAllMenuSearchFrom is the Tab count from which the All Tabs menu carries a filter.
	tabAllMenuSearchFrom = 8
)

// tabStripPlan is how the strip lays its chips out for one width.
type tabStripPlan struct {
	Width    []float32 // per chip; 0 = natural (the min/max range)
	Compact  []bool    // icon + state only
	Shrunk   bool      // any chip is narrower than natural
	Overflow bool      // even the compact floor does not fit: the strip scrolls
}

// needsMenu reports a strip whose titles are no longer readable (a chip went
// icon-only, or the strip scrolls): only then is the All Tabs menu worth a
// button. Chips that merely truncate a long title do not need it.
func (p tabStripPlan) needsMenu() bool {
	if p.Overflow {
		return true
	}
	for _, compact := range p.Compact {
		if compact {
			return true
		}
	}
	return false
}

// planTabStrip decides the chip widths for avail points of strip room. An
// unknown width (the first frame) and a strip that fits at the widest chips
// keep every chip natural.
func planTabStrip(avail float32, tabs []titlebarTab) tabStripPlan {
	n := len(tabs)
	plan := tabStripPlan{Width: make([]float32, n), Compact: make([]bool, n)}
	selected := -1
	for i, tab := range tabs {
		if tab.Selected {
			selected = i
		}
	}
	others := n
	if selected >= 0 {
		others--
	}
	if avail <= 0 || others <= 0 {
		return plan
	}
	gaps := tabStripGap * float32(n-1)
	ideal := tabChipMaxWidth*float32(others) + gaps
	if selected >= 0 {
		ideal += tabChipSelectedMaxWidth
	}
	if ideal <= avail {
		return plan
	}
	plan.Shrunk = true
	w := (avail - gaps - tabChipSelectedMinWidth) / float32(others)
	w = float32(int(min(w, tabChipMaxWidth)))
	sel := tabChipSelectedMinWidth
	if w < tabChipCompactWidth {
		w = tabChipCompactWidth
		plan.Overflow = true
	} else {
		sel = min(tabChipSelectedMaxWidth, avail-gaps-w*float32(others))
		sel = max(sel, tabChipSelectedMinWidth)
	}
	for i := range tabs {
		if i == selected {
			plan.Width[i] = sel
			continue
		}
		plan.Width[i] = w
		plan.Compact[i] = w < tabChipCompactBelow
	}
	return plan
}

// tabTipSpec is the hover card of a chip, one fact per line: the full title,
// the Workspace with its pane count, and the state in words (a line the chip
// has no room to say, or no word for), plus the Select Tab keycaps.
func tabTipSpec(tab titlebarTab, workspace string) tipCardSpec {
	meta := itoa(max(tab.Panes, 1)) + " pane"
	if tab.Panes > 1 {
		meta += "s"
	}
	if workspace != "" {
		meta = workspace + " · " + meta
	}
	spec := tipCardSpec{Title: tab.Label, Meta: meta, Keys: tab.Keys}
	if tab.HasState && tab.Detail != "" {
		spec.State, spec.Tone, spec.HasTone = tab.Detail, tab.Tone, true
	}
	return spec
}

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
	// The selected chip is revealed for a moment after it is chosen, not just
	// once: the strip's own ‹ › appear only once it overflows (narrowing it),
	// and state dots / brand marks land after the first frames, so a single
	// reveal ends with the chip's close × clipped.
	if s.tabBarSelected != s.selectedTabID {
		s.tabBarRevealUntil = time.Now().Add(tabRevealWindow)
	}
	s.tabBarSelected = s.selectedTabID
	selectionMoved := time.Now().Before(s.tabBarRevealUntil)

	bar := ui.Row(c).Grow(1).MinWidth(0).Gap(2).AlignItems(ui.Center)
	// The previous frame's width sizes this frame's chips; a change settles in
	// one frame.
	s.tabBarWidth = bar.Bounds().W
	plan := planTabStrip(s.tabBarWidth-tabStripReserve, tabs)
	bar.Children(func() {
		// The ‹ › exist only once the compact floor itself overflows: tied to
		// the scroll state they would shrink the strip, overflow it again and
		// keep themselves alive.
		overflowLeft := plan.Overflow && s.tabBarScroll.X > 1
		overflowRight := plan.Overflow && s.tabBarScroll.MaxX-s.tabBarScroll.X > 1
		if overflowLeft || overflowRight {
			scrollButton(c, k, iconChevronLeft, "Scroll Tabs Left", overflowLeft, func() {
				s.tabBarScroll.X = max(0, s.tabBarScroll.X-tabScrollStep)
			})
		}
		strip := ui.ScrollHorizontal(c).TrackScroll(&s.tabBarScroll).Height(Controls().RegularHeight).
			MinWidth(0).Shrink(1).Gap(2).AlignItems(ui.Center)
		strip.Children(func() {
			for i, tab := range tabs {
				s.tabChip(c, k, tab, selectionMoved && tab.Selected, plan.Width[i], plan.Compact[i])
			}
		})
		if overflowLeft || overflowRight {
			scrollButton(c, k, iconChevronRight, "Scroll Tabs Right", overflowRight, func() {
				s.tabBarScroll.X = min(s.tabBarScroll.MaxX, s.tabBarScroll.X+tabScrollStep)
			})
		}
		if plan.needsMenu() {
			s.allTabsControl(c, k, tabs)
		}
		s.newTabControl(c, k, *project)
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

// tabChipMotion moves the strip as it changes: a new Tab opens its width in, a
// closed one folds away, the chips beside slide, and the chosen chip's resize
// and face ease instead of jumping.
var tabChipMotion = ui.ElementTransition{
	Duration: motionPanel,
	Enter:    &ui.Motion{Collapse: true},
	Exit:     &ui.Motion{Collapse: true},
}

// tabChip is one Tab: brand mark, title, the pane tally when it is split, the
// shared state slot, and a close × that shows under the pointer or on the
// selected chip. The selected chip keeps the neutral chip face. width is the
// strip plan's (0 = natural); a compact chip keeps only mark and state.
// Dragging a chip reorders it through the tree's own tab drop (Herdr
// tab.move, same Workspace only); the insertion bar shows on the half the
// pointer is over.
func (s *Shell) tabChip(c *ui.Context, k *gorexColors, tab titlebarTab, reveal bool, width float32, compact bool) {
	t := c.Theme()
	glyph := Controls().IconRegular
	minW, maxW := tabChipMinWidth, tabChipMaxWidth
	if tab.Selected {
		minW, maxW = tabChipSelectedMinWidth, tabChipSelectedMaxWidth
	}
	slot := ui.Row(c.Key("tabslot:" + tab.ID)).AlignItems(ui.Center).Shrink(0).Transition(tabChipMotion)
	drag := s.treeDragFor(treeNodeDrag{s.activeInstance, "tab", s.selectedProjectID, tab.ID})
	// The chips beside a dragged one make way: the half of the slot the pointer
	// is over opens a gap (the dragged chip's own ghost follows the pointer),
	// and the gap holds the insertion bar. The side is read from the chip's
	// own box, which the gap moves, so the gap cannot flip the side it opened on.
	var dragged treeNodeDrag
	gapBefore, gapAfter := float32(0), float32(0)
	// A drop frame no longer reports the drag as over: the side of the last
	// hovering frame is the answer.
	insertBefore := s.tabDrop.id == tab.ID && s.tabDrop.before
	if drag != nil {
		if value, over := ui.DragOver[treeNodeDrag](slot); over && value.Kind == "tab" && drag.Accept(value) {
			dragged = value
			// The pointer against where the chip rests, not where the open
			// gap pushed it: a gap before the chip slides the chip away from
			// the pointer, and judging by the moved chip would make a drag
			// to the right need an ever longer reach to land after it.
			insertBefore = s.tabSideAt(slot, tab.ID)
			s.tabDrop = tabDropSide{tab.ID, insertBefore}
			if insertBefore {
				gapBefore = tabChipGap
			} else {
				gapAfter = tabChipGap
			}
		} else if s.tabDrop.id == tab.ID {
			s.tabDrop = tabDropSide{} // the pointer left this chip
		}
	}
	slot.Children(func() {
		before := s.tabGap(c, gapBefore, tab.ID+":before", insertBefore && dragged.ID != "")
		if s.tabGapBefore == nil {
			s.tabGapBefore = map[string]float32{}
		}
		s.tabGapBefore[tab.ID] = before.Bounds().W
		b := ui.ButtonBase(c.Key("tab:"+tab.ID)).
			Height(Controls().RegularHeight).
			Radius(Radius().Row).Padding(0, Spacing().S, 0, Spacing().M).Gap(Spacing().S).
			AlignItems(ui.Center).Label(tab.Label)
		if width > 0 {
			b.Width(width).MinWidth(width).MaxWidth(width).Shrink(0)
		} else {
			b.MinWidth(minW).MaxWidth(maxW).Shrink(0)
		}
		if compact {
			b.Padding(0, 0).Justify(ui.Center).Gap(4)
		}
		if s.tabRects == nil {
			s.tabRects = map[string]ui.Rect{}
		}
		s.tabRects[tab.ID] = b.Bounds() // last frame's box: the drop side is read against it
		gorexToggleFace(b, k, tab.Selected)
		// On the light ground the neutral chip is a grey on grey: the chosen
		// Tab wears the white chip with its soft lift instead (2026-11-08), the
		// same face as the panel's chosen segment.
		if tab.Selected && !t.Dark && s.contentGround(false) != s.tokens(false).Content {
			b.Background(t.Surface).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.12))
		}
		ink := t.TextMuted
		if tab.Selected || b.Hovered() {
			ink = t.Text
		}
		if reveal {
			b.ScrollIntoView()
		}
		if drag != nil {
			b.Drag(drag.Value).Cursor(ui.CursorGrab)
			if b.Dragging() {
				b.Opacity(0.38).Cursor(ui.CursorGrabbing)
			}
			if value, dropped := ui.Drop[treeNodeDrag](slot); dropped && value.Kind == "tab" && drag.Accept(value) {
				if s.tabDrop.id != tab.ID {
					// Released with no hovering frame on this chip (a jump
					// straight onto it): read the side from the release point.
					insertBefore = s.tabSideAt(slot, tab.ID)
				}
				drag.Drop(value, insertBefore)
				s.tabDrop = tabDropSide{}
			}
		}
		b.Children(func() {
			if tab.Mark.svg != nil || tab.Mark.bmp != nil {
				ui.Box(c).Size(glyph, glyph).Shrink(0).PassThrough().Children(func() {
					markView(c, tab.Mark, glyph, ink)
				})
			} else if compact {
				ui.Text(c, firstLetter(tab.Label)).FontSize(Typography().Section).TextColor(ink).Shrink(0).PassThrough()
			}
			if !compact {
				ui.Text(c, tab.Label).FontSize(Typography().Section).TextColor(ink).
					SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).Grow(1).PassThrough()
				if tab.Panes > 1 {
					ui.Text(c, itoa(tab.Panes)).Label(itoa(tab.Panes) + " panes").FontSize(Typography().Micro).
						TextColor(t.TextMuted).Shrink(0).PassThrough()
				}
			}
			if tab.HasState {
				if tab.Service {
					statusIndicatorService(c, tab.Tone, tab.Detail)
				} else {
					statusIndicatorTone(c, tab.Tone, tab.Detail)
				}
			}
			if !compact && (tab.Selected || b.Hovered()) {
				closeBtn := gorexIconButton(c, k, iconClose, "Close Tab", "Close Tab", false, gorexRowBtn, gorexRowGlyph)
				if closeBtn.Clicked() {
					s.closeTabChip(tab)
				}
			}
		})
		tippedCard(c, b, tabTipSpec(tab, s.workspaceLabel()))
		if b.Clicked() && !tab.Selected {
			s.selectTab(tab.ID)
		}
		b.ContextMenu(func(m *ui.Menu) {
			if herdrTab := tabByID(s.projection, tab.ID); herdrTab != nil {
				s.tabMenuItems(m, *herdrTab)
			}
		})
		s.tabGap(c, gapAfter, tab.ID+":after", !insertBefore && dragged.ID != "")
	})
}

// tabSideAt says whether the pointer is over the leading half of the chip as it
// rests (the open gap that pushed it is taken back out).
func (s *Shell) tabSideAt(slot ui.Element, id string) bool {
	x, _, _ := slot.PointerPosition()
	if chip, ok := s.tabRects[id]; ok && chip.W > 0 {
		return slot.Bounds().X+x < chip.X-s.tabGapBefore[id]+chip.W/2
	}
	return x < slot.Bounds().W/2
}

// tabDropSide remembers which half of a chip the dragged Tab hovers.
type tabDropSide struct {
	id     string
	before bool
}

// tabChipGap is the room a dragged chip's drop opens beside its target.
const tabChipGap float32 = 28

// tabGap is the animated hole beside a chip: closed (0) it takes no room,
// open it holds the insertion bar.
func (s *Shell) tabGap(c *ui.Context, w float32, key string, bar bool) ui.Element {
	t := c.Theme()
	gap := ui.Box(c.Key("tabgap:" + key)).Width(w).Height(Controls().RegularHeight).Shrink(0).PassThrough().Center().
		Transition(ui.ElementTransition{Size: true, Duration: motionRow})
	if w > 0 && bar {
		gap.Children(func() {
			ui.Box(c).Width(3).Height(Controls().RegularHeight - 8).Radius(1.5).Background(t.Accent.Alpha(0.85)).PassThrough()
		})
	}
	return gap
}

// closeTabChip is a chip's ×: a Herdr Tab goes through the shared close confirm.
func (s *Shell) closeTabChip(tab titlebarTab) {
	s.openConfirm("close-tab", tab.ID, "Close Tab?", "This closes the selected Herdr Tab and all Panes in it.")
}

// firstLetter is the glyph a compact chip falls back to when it has no mark.
func firstLetter(label string) string {
	for _, r := range label {
		return strings.ToUpper(string(r))
	}
	return "·"
}

// allTabsControl is the bar's ▾: once chips shrink it lists every Tab by name
// (filterable from tabAllMenuSearchFrom of them), the way to reach a Tab whose
// title the strip has no room for.
func (s *Shell) allTabsControl(c *ui.Context, k *gorexColors, tabs []titlebarTab) {
	t := c.Theme()
	btn := gorexIconButton(c, k, iconChevronDown, "All Tabs", "", s.allTabs.open, gorexIconBtn, gorexIconGlyph)
	tipped(c, btn, "All Tabs")
	wasOpen := s.allTabs.open
	toggleMenu(&s.allTabs.open, btn)
	if s.allTabs.open && !wasOpen {
		s.allTabs.filter = ""
	}
	if !s.allTabs.open {
		return
	}
	ui.Popover(c, btn, &s.allTabs.open, func() {
		sp := Spacing()
		ui.Column(c).Width(280).Padding(sp.S).Gap(sp.XS).Children(func() {
			var shown []titlebarTab
			for _, tab := range tabs {
				if filterMatches(s.allTabs.filter, tab.Label) {
					shown = append(shown, tab)
				}
			}
			if len(tabs) >= tabAllMenuSearchFrom {
				if switcherSearch(c, &s.allTabs.filter, "Search Tabs") && len(shown) > 0 {
					s.selectTab(shown[0].ID)
					s.allTabs.open = false
					return
				}
			}
			ui.Scroll(c).FillWidth().MaxHeight(switcherListMaxHeight).Children(func() {
				ui.Column(c).FillWidth().Gap(1).Children(func() {
					if len(shown) == 0 {
						ui.Text(c, "No matches").FontSize(Typography().BodySmall).TextColor(t.TextMuted).Padding(6, 8)
					}
					for _, tab := range shown {
						s.allTabsRow(c, tab)
					}
				})
			})
		})
	})
}

// allTabsRow is one line of the All Tabs menu: mark, title, panes, state.
func (s *Shell) allTabsRow(c *ui.Context, tab titlebarTab) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	row := ui.ButtonBase(c.Key("alltabs:"+tab.ID)).FillWidth().Height(28).Radius(6).
		Padding(0, 8).Gap(6).AlignItems(ui.Center).Label(tab.Label)
	if tint, ok := rowTint(tokens, tab.Selected, row.Hovered()); ok {
		row.Background(tint)
	}
	row.Children(func() {
		if tab.Mark.svg != nil || tab.Mark.bmp != nil {
			ui.Box(c).Size(16, 16).Shrink(0).PassThrough().Children(func() { markView(c, tab.Mark, 14, t.TextMuted) })
		}
		ui.Text(c, tab.Label).FontSize(Typography().Body).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").PassThrough()
		if tab.Panes > 1 {
			ui.Text(c, itoa(tab.Panes)).FontSize(Typography().Micro).TextColor(t.TextMuted).Shrink(0).PassThrough()
		}
		if tab.HasState {
			if tab.Service {
				statusIndicatorService(c, tab.Tone, tab.Detail)
			} else {
				statusIndicatorTone(c, tab.Tone, tab.Detail)
			}
		}
	})
	if row.Clicked() {
		if !tab.Selected {
			s.selectTab(tab.ID)
		}
		s.allTabs.open = false
	}
}

// workspaceLabel is the selected Workspace's name ("" with none).
func (s *Shell) workspaceLabel() string {
	if project := s.selectedProject(); project != nil {
		return project.Label
	}
	return ""
}
