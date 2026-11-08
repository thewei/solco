package nativeui

import (
	"runtime"

	"github.com/egoist/mygo/ui"

	"github.com/thewei/solco/internal/commandcenter"
)

/**
 * [INPUT]: 依赖 mygo/ui 的 Context/TitleBar（窗口控件带几何）、gorex_style 的图标按钮与配色、tooltip.go 的 tipped/tipKeys 与 shortcuts.go 的 sidebarToggleBinding/Shell.shortcutKeycaps（tooltip 键帽取自有效绑定）、commandcenter 的面板作用域、router 路由态、runtime/time
 * [OUTPUT]: 对外提供 Shell.titlebar（窗口标题栏：红绿灯对齐、身份区——Terminal/Chat 为面包屑单行，选中 Pane 的目录住在末级 crumb 的悬停弹层 breadcrumb_switcher.go crumbPathTip，Commit 与各内页为 route_header.go 的单行 Title 大字标题、工具区、Windows 控件）、titlebarHeight（灯带对齐的行高纯函数，所有路由同一 42 下限）、showViewTerminal/showViewChat/showViewHistory（三个视图的唯一切换入口）、sidebarToggle（侧栏顶带悬浮 chrome 的左右分布：左组=侧栏收起钮+Search（⌘K 的 Command Center 搜索面板）固定在红绿灯右侧，右组=Router Back/Forward 贴侧栏右缘随滑动移动，收起后并入左组右侧）、chromeHistoryButton（历史按钮的无历史灭活态）
 * [POS]: nativeui 的窗口 chrome 所有者，被 shell.View 顶部消费；高度与 MyGo 红灯带对齐（F142），页面标题与页面动作的唯一归属（2026-10-14：内页只画内容）；页面历史与 Search 收口在侧栏顶带的悬浮 chrome（2026-10-29 用户要求：左右分布——左组侧栏钮+搜索钮居左，右组历史钮居右贴侧栏缘），右侧工具簇 titlebarTools 只剩页面 Actions、Agent 活动浮层入口与最右的右侧面板开关（side_panel.go sidePanelToggleButton，⌥⌘B，2026-11-07；窗口置顶图钉已于 2026-10-31 迁入菜单栏 Window 菜单，见 window_actions.go KeepWindowOnTopFromMenu）；内容列让位宽度 sidebarToggleClearance 覆盖收起态最宽的四钮行
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// The content column's title bar (2026-10-12): the sidebar owns the left of
// the window top to bottom, so this bar only spans the content — the page's
// identity and the trailing tools, on the canvas color with no fill of its
// own. The identity is the one place a page is named (2026-10-14): the
// Terminal and Chat show their switcher path on one line — the selected
// Pane's directory lives in the last crumb's hover popup (2026-10-28,
// breadcrumb_switcher.go crumbPathTip) instead of a second row — while
// Commit and every other route get one large title (route_header.go).
// Every route's Actions menu is the first of the trailing tools
// (titlebarTools). Pages render content only, so no two pages open with
// a different header of their own.
func (s *Shell) titlebar(c *ui.Context) {
	t := c.Theme()
	k := gorexColorsOf(t.Dark)
	bar := c.TitleBar()
	// The row never grows past bar.Height (2026-10-06 annotation F142): the
	// traffic lights sit centered inside exactly bar.Height (MyGo places
	// them btnHeight + 2×TrafficLightPosition.Y tall), so centering this
	// row's content in the same height puts every control on the lights'
	// horizontal line.
	height := titlebarHeight(bar.Height)
	// While the sidebar slides shut the content column reaches the window's
	// left edge: its content then clears the traffic lights and the whole
	// floating chrome (the history pair falls in beside the left pair).
	left := max(gorexGap*2, sidebarToggleClearance(bar)-s.sidebarSlideW)
	ui.Row(c).
		Height(height).
		Padding(0, max(bar.Right, 12), 0, left).
		Gap(8).
		DragWindow().
		AlignItems(ui.Center).
		Children(func() {
			// The page's Actions menu rides in the trailing tools on every
			// route (shadcn's header: identity left, actions right), so the
			// control sits where it did on the last page whatever the title
			// or the path ahead of it is.
			var actions func(m *ui.Menu)
			var pageTools func(c *ui.Context)
			if spec, ok := s.routeHeader(); ok {
				actions, pageTools = spec.actions, spec.tools
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					s.routeHeaderBlock(c, spec)
				})
			} else if s.router.Path() == routeWorkspace {
				ui.Row(c).Grow(1).MinWidth(0).Children(func() {
					drew := false
					if s.tabsInTitleBar() {
						drew = s.tabBarRow(c)
					} else {
						drew = s.breadcrumbRow(c)
					}
					if drew {
						actions = s.surfaceOverflowItems
					}
				})
			} else {
				ui.Box(c).Grow(1).PassThrough()
			}
			if pageTools != nil {
				pageTools(c)
			}
			s.titlebarTools(c, k, actions)
		})
}

// sidebarToggleX is where the toggle sits: right after the traffic lights.
func sidebarToggleX(bar ui.TitleBar) float32 { return max(bar.Left, 12) + 4 }

// sidebarNavGap is the gap inside the floating chrome cluster, within each
// pair and between them when the sidebar is shut.
const sidebarNavGap = 4

// sidebarLeftGroupWidth is the left-aligned pair floating after the traffic
// lights: the sidebar toggle with the Search button (the Command Center
// palette, ⌘K) beside it (2026-10-29 user request).
func sidebarLeftGroupWidth() float32 { return 2*gorexIconBtn + sidebarNavGap }

// sidebarRightGroupWidth is the right-aligned pair: the router's Back and
// Forward holding the sidebar band's right edge (2026-10-29 user request).
func sidebarRightGroupWidth() float32 { return 2*gorexIconBtn + sidebarNavGap }

// sidebarChromeStripWidth is the widest the floating chrome can reach: with
// the sidebar shut the history pair can no longer hold the band's right
// edge and settles beside the left pair, so the content clears the whole
// four-button row.
func sidebarChromeStripWidth() float32 {
	return sidebarLeftGroupWidth() + sidebarNavGap + sidebarRightGroupWidth()
}

// sidebarToggleClearance is how far from the window's left edge the content
// must start to leave the lights and the floating chrome uncovered.
func sidebarToggleClearance(bar ui.TitleBar) float32 {
	return sidebarToggleX(bar) + sidebarChromeStripWidth() + 8
}

// sidebarBand is the sidebar's top inset: the strip beside the traffic
// lights, draggable like the rest of the title bar.
func (s *Shell) sidebarBand(c *ui.Context) {
	ui.Box(c).FillWidth().Shrink(0).Height(max(c.TitleBar().Height, 38)).DragWindow()
}

// sidebarToggle lays out the sidebar band's floating chrome as one left/
// right split (2026-10-29 user request): the left pair — the sidebar toggle
// and the Search button into the Command Center palette — sits fixed after
// the traffic lights; the right pair — the router's Back/Forward page
// history — holds the band's right edge and moves with the sidebar's slide.
// With the sidebar shut there is no band edge to hold, so the pair falls in
// beside the left one (the content's clearance covers that widest row). The
// Router stays the behavior owner (Back/Forward/CanGo*; ⌘[ and ⌘] are its
// own shortcuts, the tooltips name them); the buttons keep the chrome's
// shared borderless icon-button face, because the official ui.BackButton's
// bordered toolbar face would clash with the chrome around the lights.
func (s *Shell) sidebarToggle(c *ui.Context) {
	k := gorexColorsOf(c.Theme().Dark)
	bar := c.TitleBar()
	label := "Show Sidebar"
	if !s.sidebarCollapsed {
		label = "Hide Sidebar"
	}
	y := (max(bar.Height, 38) - gorexIconBtn) / 2
	ui.Row(c).Absolute().Left(sidebarToggleX(bar)).Top(y).Gap(sidebarNavGap).
		AlignItems(ui.Center).Children(func() {
		toggle := gorexIconButton(c, k, iconPanel, label, "", false, gorexIconBtn, gorexIconGlyph)
		tipped(c, toggle, label, tipKeys(sidebarToggleBinding.keycaps()))
		if toggle.Clicked() {
			s.toggleSidebar()
		}
		// The sidebar's own search row (2026-11-08) replaces this button
		// wherever it is on screen; the button stays for a shut sidebar and
		// for Settings. The pair's footprint stays the same either way, so
		// the history pair does not move.
		if !s.sidebarSearchRowShown() {
			search := gorexIconButton(c, k, iconSearch, "Search", "", false, gorexIconBtn, gorexIconGlyph)
			tipped(c, search, "Search", tipKeys(s.shortcutKeycaps(shortcutSearch)))
			if search.Clicked() {
				s.openCommandCenter(commandcenter.ScopeAll)
			}
		}
	})
	right := sidebarToggleX(bar) + sidebarLeftGroupWidth() + sidebarNavGap
	// The sidebar column's own right edge (the slide host carries the extra
	// resize-hit strip beyond it) less the band's right inset.
	if edge := s.sidebarSlideW - sidebarResizeHit - 12 - sidebarRightGroupWidth(); edge > right {
		right = edge
	}
	ui.Row(c).Absolute().Left(right).Top(y).Gap(sidebarNavGap).
		AlignItems(ui.Center).Children(func() {
		s.chromeHistoryButton(c, k, iconArrowLeft, "Back", s.shortcutKeycaps(shortcutBack), s.router.CanGoBack(), s.router.Back)
		s.chromeHistoryButton(c, k, iconArrowRight, "Forward", s.shortcutKeycaps(shortcutForward), s.router.CanGoForward(), s.router.Forward)
	})
}

// chromeHistoryButton is one router-history action of the band's right
// pair: the shared icon button while there is a page to reach, the shared
// disabled face (half-strength glyph, no hover) when the history ends
// there. keys is the shortcut's keycaps, shown in the tooltip either way.
func (s *Shell) chromeHistoryButton(c *ui.Context, k *gorexColors, glyph *ui.SVG, label string, keys []string, can bool, move func()) {
	if !can {
		tipped(c, gorexIconButtonDisabled(c, k, glyph, label, "", gorexIconBtn, gorexIconGlyph), label, tipKeys(keys))
		return
	}
	b := gorexIconButton(c, k, glyph, label, "", false, gorexIconBtn, gorexIconGlyph)
	tipped(c, b, label, tipKeys(keys))
	if b.Clicked() {
		move()
	}
}

// titlebarHeight keeps the bar at the window controls' own height: MyGo
// centers the traffic lights inside exactly bar.Height (button height +
// 2× TrafficLightPosition.Y), so centering the bar's content in that same
// height lines every control up with the lights (2026-10-06 annotation
// F142). The 42 floor is the same on every route, so the identity never
// moves between pages.
func titlebarHeight(barHeight float32) float32 {
	return max(barHeight, 42)
}

// titlebarTools is the header's trailing edge: the page's Actions menu (nil
// when the route offers none) and the right panel toggle. On Windows the
// window controls follow. The Agent activity panel has no button here any
// more (2026-11-08: the menu-bar icon and its number already carry it). The
// Sessions toggle and Search live at the top of the
// sidebar (sidebar.go sidebarSearchRow); page history and Search float on
// the sidebar band (sidebarToggle).
func (s *Shell) titlebarTools(c *ui.Context, k *gorexColors, actions func(m *ui.Menu)) {
	if actions != nil {
		headerActions(c, actions)
	}
	s.sidePanelToggleButton(c, k)
	s.windowControls(c, k)
}

// ownControls indicates that the title bar draws custom window controls: on
// Windows, where native controls do not blend with the gorex gradient header.
var ownControls = runtime.GOOS == "windows"

// closeRed is Windows 11's close button hover face.
var closeRed = ui.Hex("#c42b1c")

// windowControls draws minimize, maximize/restore, and close buttons on platforms
// without macOS traffic lights (such as Windows).
func (s *Shell) windowControls(c *ui.Context, k *gorexColors) {
	win := s.win
	if !ownControls || win == nil || win.IsFullScreen() {
		return
	}
	maximized := win.IsMaximized()
	ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Box(c).Size(1, 16).Margin(0, 6).Background(k.textFaint.Alpha(0.25))
		if gorexIconButton(c, k, iconMinus, "Minimize", "Minimize", false, gorexIconBtn, gorexIconGlyph).Clicked() {
			win.Minimize()
		}
		glyph, label := iconSquare, "Maximize"
		if maximized {
			glyph, label = iconCopy, "Restore"
		}
		if gorexIconButton(c, k, glyph, label, label, false, gorexIconBtn, 13).Clicked() {
			if maximized {
				win.Unmaximize()
			} else {
				win.Maximize()
			}
		}
		b := tipped(c, ui.Box(c).Size(gorexIconBtn, gorexIconBtn).Center().Radius(iconButtonRadius(gorexIconBtn)).
			Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Close"), "Close")
		col := k.iconMuted
		if b.Pressed() {
			b.Background(closeRed.Alpha(0.8))
			col = ui.Hex("#ffffff")
		} else if b.Hovered() {
			b.Background(closeRed)
			col = ui.Hex("#ffffff")
		}
		b.Transition(ui.ElementTransition{Colors: true, Duration: motionColor})
		b.Children(func() {
			ui.Icon(c, iconClose).Size(16, 16).TextColor(col)
		})
		if b.Clicked() {
			win.Close()
		}
	})
}

// viewSwitchActive reports which of the header views is on screen: the two
// workspace surfaces answer through the surface state machine (a Commit lights
// neither), every /history* route counts as the History view.
func (s *Shell) viewSwitchActive() (terminal, chat, history bool) {
	path := s.router.Path()
	if isHistoryRoute(path) {
		return false, false, true
	}
	if path != routeWorkspace {
		return false, false, false
	}
	switch s.surface.current() {
	case WorkspaceSurfaceTerminal, WorkspaceSurfaceDetail:
		// The Detail card is the workspace's own third surface: the
		// Terminal view stays the lit way home while it is up.
		terminal = true
	case WorkspaceSurfaceChat:
		chat = true
	}
	return terminal, chat, history
}

// showViewTerminal lands on the first-level Terminal view from anywhere.
func (s *Shell) showViewTerminal() {
	if s.holdCommitNavigation() {
		return
	}
	if s.router.Path() != routeWorkspace {
		s.router.Push(routeWorkspace)
	}
	s.showSurface(WorkspaceSurfaceTerminal)
}

// showViewChat toggles the Chat view: repeating the click exits to Terminal.
func (s *Shell) showViewChat() {
	if s.holdCommitNavigation() {
		return
	}
	if _, chat, _ := s.viewSwitchActive(); chat {
		s.showViewTerminal()
		return
	}
	// Check before changing the route: Settings/History should never be
	// replaced by an empty Chat because a plain shell is selected.
	if !s.workspaceChatAvailable() {
		s.chatUnavailableNotice()
		return
	}
	if s.router.Path() != routeWorkspace {
		s.router.Push(routeWorkspace)
	}
	s.showSurface(WorkspaceSurfaceChat)
}

// showHistoryIndex is the single parent-navigation intent for History.
// Nested History pages never fall back to the previous unrelated route.
func (s *Shell) showHistoryIndex() {
	if s.router.Path() == routeHistory {
		return
	}
	s.router.Push(routeHistory)
}

// showViewHistory: from another page open the History index; from a nested
// History route return to the index; only from the index itself toggle back
// to Terminal (consistent with the header's "active view" toggle contract).
func (s *Shell) showViewHistory() {
	if s.holdCommitNavigation() {
		return
	}
	if s.router.Path() == routeHistory {
		s.showViewTerminal()
		return
	}
	s.showHistoryIndex()
}
