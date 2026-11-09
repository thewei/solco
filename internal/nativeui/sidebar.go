package nativeui

/**
 * [INPUT]: 依赖 Shell 当前 Router/Workspace/Agent 投影、共享 agentCardRow/agentReviewAction 和 native Quick Panel
 * [OUTPUT]: 提供 Sidebar 的顶部搜索行（sidebarSearchRow：搜索框打开 Command Center、右端 Sessions 切换钮，Settings 路由不渲染，侧栏展开且有该行时标题栏搜索钮隐藏；2026-11-08 U9 取代图标条，Hosts/Automations 改走 Command Center 置顶 Go to 组）、Workspace 树、Sessions（项目头 + 范围行）/Settings 导航、Settings/Sessions（含 Quota）/Hosts 的底部 Back（sidebarBackFoot；Sessions 与 Hosts 落到 landSelectedPane）与其余路由的底部标准页脚（设备/会话切换芯片 deviceSwitcher + Settings 钮）、右侧面板 Changes 页复用的 sidebarFilesView 文件列表与 Agent 行点击/聚焦取消 Review/右键动作（含 Mark Reviewed）；Agents 区排序（头部悬停排序图标 → By Workspace/By Time/By Status 分组菜单，下附 Sort by Updated Time 勾选与升降序切换，默认按 Pane 顺序排序，随 settings.Session.AgentsSort/AgentsSortByTime/AgentsSortAscending 持久；Workspace 分组模式组行悬停 "+" 走 Tab 条同款 startNewTab）
 * [POS]: 唯一左栏宿主，Agent 详情复用 Quick Panel；搜索与 Sessions 切换在栏顶搜索行（Settings 无），Workspace/Automations/Assistants 共用标准页脚
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/commandcenter"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

func (s *Shell) sidebar(c *ui.Context) {
	t := c.Theme()
	tokens := s.tokens(t.Dark)
	// No fill of its own: the sidebar column (sidebarSlide) is the vibrancy
	// material or its opaque fallback, one continuous surface with the band
	// beside the traffic lights above this body. The strip at the top owns
	// the view switch (2026-10-14) — except in Settings, where it is hidden
	// (2026-10-15 user request): Settings is no strip destination, and its
	// one way back is the Back foot at the bottom.
	ui.Column(c).Width(s.sidebarW).Grow(1).MinHeight(0).Shrink(0).Children(func() {
		if !s.sidebarInSettings() {
			s.sidebarSearchRow(c)
		}
		// No divider on the plain workspace sidebar: with no row above it
		// the line hung orphaned between the title bar and the tree
		// (2026-10-07 user report) — the sidebar and the header are one
		// continuous gradient surface there.

		// The sidebar body is route-scoped: inner pages use their own
		// navigation, and the default remains the canonical Herdr runtime
		// navigator.
		if isHistoryRoute(s.router.Path()) {
			// Sessions wears the same project/branch head as the right panel's
			// two pages; under it the scope rows say whose sessions the list
			// shows.
			refreshLabel, refresh := "Refresh sessions", s.refreshHistoryScan
			if s.router.Path() == routeQuota {
				refreshLabel, refresh = "Refresh quota", func() { s.refreshUsage(true) }
			}
			s.sidebarProjectHeader(c, s.historyProjectRoot(), refreshLabel, refresh, nil)
			ui.Divider(c)
			s.historyNav(c)
		} else if s.sidebarInnerPage() {
			s.sidebarInnerNav(c)
		} else if s.clearSidebarSelectionOnHostedPage(); ui.Sidebar(c, &s.selected, func() {
			// The Agents/Pin/Workspace sections wear the same tree header as
			// the Hosts groups. The Agents header's
			// hover icon opens the list's sort menu (2026-11-02 user request):
			// by Workspace group, by last activity, by state.
			s.sidebarSection(c, sidebarSectionSpec{
				Key: "section:agents", Title: "Agents", Count: len(s.workbenchCards()),
				Action: &treeRowAction{Label: "Sort Agents", Glyph: iconSort, Menu: s.agentSortMenu},
				Menu:   s.agentSortMenu,
			}, &s.agentsOpen, func() {
				s.agentItems(c)
			})
			s.servicesSection(c)
			if pins := s.pinnedPaneIDs(); len(pins) > 0 {
				s.sidebarSection(c, sidebarSectionSpec{Key: "section:pin", Title: "Pin", Count: len(pins)}, &s.pinsOpen, func() {
					s.pinnedItemsView(c)
				})
			}
			if s.assistantsAvailable() {
				s.assistantsSection(c)
			}
			s.workspaceSection(c)
			// The widget's own Surface fill is dropped (2026-10-06): the
			// Agents/Workspace blocks sit on the window gradient like the
			// rest of the chrome. Of the widget's uniform inset only the
			// top is zeroed (2026-10-13 user report): the sections start
			// flush under the band, while the sides and the foot keep the
			// framework spacing.
		}).Background(ui.Transparent).Padding(0, t.Space(2.5), t.Space(2.5)).
			Grow(1).Label("Solco sidebar").Changed() {
			s.handleSidebarSelection(s.selected)
		}

		// The sidebar foot. On the workspace sidebar the Workspace is the
		// bound Herdr instance, so switching belongs in the footer rather
		// than nesting Projects under a Workspace tree. Every other route —
		// the strip pages (Sessions, Hosts) — wears the same standard foot
		// (2026-10-15 user requests). Settings keeps its one way back
		// instead: it is not a strip destination and the strip is hidden
		// above it.
		foot := func(content func()) {
			ui.Row(c).Height(42).Padding(5, 8).Gap(6).AlignItems(ui.Center).
				BorderWidth(1, 0, 0, 0).BorderColor(tokens.BorderSubtle).Children(content)
		}
		standardFoot := func() {
			foot(func() {
				s.deviceSwitcher(c)
				btn := gorexIconButton(c, gorexColorsOf(t.Dark), iconSettings, "Settings", "Settings", false, gorexIconBtn, gorexIconGlyph)
				if s.hasUpdate() {
					btn.DrawOver(func(p *ui.Painter, r ui.Rect) {
						dotX := r.X + r.W - 8
						dotY := r.Y + 3
						dotSize := float32(6.5)
						p.Fill(ui.Rect{X: dotX - 1, Y: dotY - 1, W: dotSize + 2, H: dotSize + 2}, t.Surface, (dotSize+2)/2)
						p.Fill(ui.Rect{X: dotX, Y: dotY, W: dotSize, H: dotSize}, t.Danger, dotSize/2)
					})
				}
				if btn.Clicked() {
					s.router.Push(routeSettings)
				}
			})
		}
		if s.sidebarBackFoot() {
			foot(func() { s.sidebarBackRow(c) })
		} else {
			standardFoot()
		}
	})
}

// sidebarInnerPage reports whether the current route is an inner page:
// Settings, History (list, detail, by-project) or Hosts. Inner pages swap the
// sidebar body for their own navigation.
func (s *Shell) sidebarInnerPage() bool {
	path := s.router.Path()
	return isHistoryRoute(path) || path == routeHosts || s.sidebarInSettings()
}

// sidebarInSettings reports whether the route is a Settings page — the one
// inner page whose sidebar keeps a Back foot.
func (s *Shell) sidebarInSettings() bool {
	return strings.HasPrefix(s.router.Path(), "/settings/")
}

// sidebarSearchRow is the sidebar's first row (2026-11-08 U9, T3 review round
// 2): a search field that opens the Command Center — a click, or the user's
// own Search shortcut whose keycaps it reads from the current binding — and,
// at the row's right end, the Sessions toggle. It replaced the strip of
// view-tab icons; Hosts and Automations are reached from the Command Center's
// pinned "Go to" group, the Go menu and their shortcuts, Changes and Files
// from the right panel. The row is on every route except Settings (whose way
// out is the Back foot and which has its own search field). The Sessions
// toggle is lit while Sessions is open; pressing it lit returns to the
// Terminal (from a nested Sessions page, first to the list), so a view is
// always one click away and one click out. Accessible labels stay stable for
// shortcuts and assistive technologies; the tooltip describes what happens.
func (s *Shell) sidebarSearchRow(c *ui.Context) {
	t := c.Theme()
	tokens := s.tokens(t.Dark)
	k := gorexColorsOf(t.Dark)
	typ := Typography()
	sp := Spacing()
	_, _, onHistory := s.viewSwitchActive()
	historyTip := "Sessions"
	switch s.router.Path() {
	case routeHistory:
		historyTip = "Back to Terminal"
	default:
		if onHistory {
			historyTip = "All conversations"
		}
	}
	ui.Row(c).FillWidth().Padding(0, sidebarGutter, sp.S).Gap(sp.S).AlignItems(ui.Center).Children(func() {
		field := ui.ButtonBase(c).Grow(1).MinWidth(0).Height(gorexIconBtn).PaddingX(sp.M).Gap(sp.M).
			Radius(Radius().Control).Border(1, tokens.BorderSubtle).Label("Search")
		if field.Hovered() {
			field.Background(tokens.PanelHover)
		}
		field.Transition(rowFade)
		field.Children(func() {
			ui.Icon(c, iconSearch).Size(14, 14).TextColor(t.TextMuted)
			ui.Text(c, "Search").FontSize(typ.Body).TextColor(t.TextMuted).Grow(1).SingleLine()
			if keys := strings.Join(s.shortcutKeycaps(shortcutSearch), ""); keys != "" {
				ui.Text(c, keys).FontSize(typ.Micro).TextColor(t.TextMuted).SingleLine()
			}
		})
		if field.Clicked() {
			s.openCommandCenter(commandcenter.ScopeAll)
		}
		if gorexIconButton(c, k, iconHistory, "History view", historyTip, onHistory, gorexIconBtn, gorexIconGlyph).Clicked() {
			s.showViewHistory()
		}
	})
}

// sidebarSearchRowShown reports whether the sidebar's own search row is on
// screen. The title bar's Search button draws exactly when it is not: with the
// sidebar shut, and on Settings.
func (s *Shell) sidebarSearchRowShown() bool {
	return !s.sidebarCollapsed && !s.sidebarInSettings()
}

// sidebarBackFoot reports whether the sidebar's foot is a Back row in place of
// the standard foot (device switcher chip and Settings button): on Settings
// and on the pages that are left rather than lived in — Sessions (Quota
// included) and Hosts (2026-11-08 U9, amending the 2026-10-15 "one foot on
// every page" request for these pages only). Workspace, Automations and
// Assistants keep the standard foot.
func (s *Shell) sidebarBackFoot() bool {
	path := s.router.Path()
	return s.sidebarInSettings() || isHistoryRoute(path) || path == routeHosts || path == routeFeedback
}

// sidebarBackRow is the Back foot, labelled Back; its tooltip names the
// destination. Settings returns to the Terminal; Sessions and Hosts land where
// the Terminal tab used to land — the selected Pane, in the Chat or the
// Terminal face it last wore (landSelectedPane). The global router history
// shortcut remains independent of this explicit product navigation.
func (s *Shell) sidebarBackRow(c *ui.Context) {
	tip, land := "Back to Terminal", s.showViewTerminal
	if !s.sidebarInSettings() {
		land = s.landSelectedPane
		if s.panePrefersChat(s.selectedPaneID) && s.workspaceChatAvailable() {
			tip = "Back to Chat"
		}
	}
	if tipped(c, navButton(c, iconArrowLeft, "Back", "", false), tip).Clicked() {
		land()
	}
}

// Top insets of the inner pages' sidebar body: Settings (no tab strip) and
// Hosts (under the strip).
const (
	innerSidebarTop          = float32(14)
	innerSidebarTopUnderTabs = float32(6)
)

// sidebarInnerNav is the Settings and Hosts sidebar body: the page's
// navigation lives in the app sidebar while the route owns the content area
// (Sessions carries the project header and builds its own).
func (s *Shell) sidebarInnerNav(c *ui.Context) {
	// Settings has no view tabs above it and keeps the roomier inset; Hosts
	// sits under the tab strip, so its first row starts right below it.
	top := innerSidebarTop
	if s.router.Path() == routeHosts {
		top = innerSidebarTopUnderTabs
	}
	ui.Column(c).Grow(1).MinHeight(0).Padding(top, 9, 9).Gap(3).Children(func() {
		switch path := s.router.Path(); {
		case strings.HasPrefix(path, "/settings/"):
			s.settingsNav(c)
		case path == routeHosts:
			s.hostsNav(c)
		}
	})
}

// sidebarFilesView renders the staged/unstaged file lists: the right panel's
// Changes page body above its diff.
func (s *Shell) sidebarFilesView(c *ui.Context) {
	t := c.Theme()
	pal := gdPaletteFor(t)

	snap := s.gdActiveSnapshot()
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		s.gitListHead(c, &headerSearch{
			Query: &s.changes.filter, Open: &s.changesSearchOpen, Label: "Filter files", Button: "Search files",
			Changed: func() {
				s.changesTree = nil
				s.git.gdRowsDirty = true
			},
		}, 10, func() {
			if snap == nil {
				return
			}
			ui.Text(c, pluralFiles(len(snap.Files))).FontSize(Typography().BodySmall).TextColor(t.TextMuted).SingleLine()
			tipped(c, ui.Row(c).Gap(6).Shrink(0), "Total change: "+gdLines(snap.TotalAdditions, "added")+", "+gdLines(snap.TotalDeletions, "removed")).Children(func() {
				ui.Text(c, "+"+gdThousands(snap.TotalAdditions)).Font(gdCodeFont()).FontSize(Typography().BodySmall).FontWeight(WeightSemibold).TextColor(pal.addText)
				ui.Text(c, "-"+gdThousands(snap.TotalDeletions)).Font(gdCodeFont()).FontSize(Typography().BodySmall).FontWeight(WeightSemibold).TextColor(pal.delText)
			})
			if snap.Truncated {
				ui.Text(c, "partial").FontSize(Typography().Micro).TextColor(t.TextMuted)
			}
		})

		if s.git.root == "" {
			ui.Column(c).Grow(1).Center().Padding(16).Children(func() {
				ui.Text(c, "No Git repository").FontSize(Typography().Body).TextColor(t.TextMuted)
			})
			return
		}
		if snap == nil {
			ui.Column(c).Grow(1).Center().Padding(16).Gap(6).Children(func() {
				if s.git.refreshing {
					ui.Spinner(c).Size(16, 16)
				}
				ui.Text(c, "Reading changes…").FontSize(Typography().Body).TextColor(t.TextMuted)
			})
			return
		}

		// A clean tree: the center's "Working tree clean" says it once; two
		// empty Unstaged/Staged headers would say it again.
		if len(snap.Files) == 0 {
			return
		}
		unstagedFiles, stagedFiles := s.splitSections(snap)
		unstagedRows := s.sectionRows(unstagedFiles)
		stagedRows := s.sectionRows(stagedFiles)
		// Files leave a pane as they are staged, unstaged or filtered away;
		// a choice keyed by a path the pane no longer shows must not quietly
		// feed the pane's action.
		pruneChoice(&s.unstagedChoice, unstagedFiles)
		pruneChoice(&s.stagedChoice, stagedFiles)

		ui.Column(c).Grow(1).MinHeight(0).Children(func() {
			// The Unstaged pane (untracked files included) takes the space
			// above the divider. The pane action targets the Cmd/Shift
			// selection when there is one, else the whole pane.
			s.gdSectionHeader(c, "Unstaged Files", len(unstagedFiles), &s.unstagedOpen,
				"Stage", gdActionPaths(&s.unstagedChoice, unstagedFiles), s.stageFiles)
			if s.unstagedOpen && len(unstagedRows) > 0 {
				s.gdChangedTreeList(c, pal, unstagedRows, &s.unstagedList, &s.unstagedSelected,
					&s.unstagedChoice, gdViewUnstaged, func(row gitworkbench.FlatRow) { s.changesClickSide(row, gdViewUnstaged) })
			}

			if s.gdPaneDividerShown(len(unstagedFiles), len(stagedFiles)) {
				s.gdPaneDivider(c)
			}

			// The Staged pane sits below the divider at the dragged height.
			s.gdSectionHeader(c, "Staged Files", len(stagedFiles), &s.stagedOpen,
				"Unstage", gdActionPaths(&s.stagedChoice, stagedFiles), s.unstageFiles)
			if s.stagedOpen && len(stagedRows) > 0 {
				list := s.gdChangedTreeList(c, pal, stagedRows, &s.stagedList, &s.stagedSelected,
					&s.stagedChoice, gdViewStaged, func(row gitworkbench.FlatRow) { s.changesClickSide(row, gdViewStaged) })
				if s.unstagedOpen && len(unstagedFiles) > 0 {
					// The dragged height within the cap (written back, so a drag past
					// it leaves no dead stretch to drag back through), but no taller
					// than its own rows and the list's 3-point padding top and bottom.
					// Basis, not Height: the list's Grow(1) gave it a 0% basis that
					// Height does not override, so both lists shared the room equally
					// whatever the divider said — a one-file Staged list took half the
					// short list and hid an Unstaged row behind its header (2026-11-08
					// run).
					s.stagedPaneHeight = clampF(s.stagedPaneHeight, 60, s.stagedPaneCap())
					own := float32(len(stagedRows))*s.treeRowHeight() + 6
					list.Basis(min(s.stagedPaneHeight, own)).Grow(0)
				} else {
					list.Grow(1).MinHeight(0)
				}
			}
		})
	})
}

// gdRelativeTime writes how long ago t was: just now, 5m ago, 3d ago.
func gdRelativeTime(t time.Time) string {
	d := timeNow().Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d/(30*24*time.Hour)))
	}
	return fmt.Sprintf("%dy ago", int(d/(365*24*time.Hour)))
}

// splitSections partitions the snapshot's files into the unstaged pane
// (worktree changes and untracked files) and the staged pane.
func (s *Shell) splitSections(snap *gitworkbench.ChangesSnapshot) (unstaged, staged []gitworkbench.ChangeFile) {
	for i := range snap.Files {
		cf := snap.Files[i]
		switch {
		case cf.Untracked:
			unstaged = append(unstaged, cf)
		default:
			if cf.Unstaged {
				unstaged = append(unstaged, cf)
			}
			if cf.Staged {
				staged = append(staged, cf)
			}
		}
	}
	return unstaged, staged
}

// sectionRows builds the collapsible tree rows of one pane, honoring the
// shared filter and directory-collapse state.
func (s *Shell) sectionRows(files []gitworkbench.ChangeFile) []gitworkbench.FlatRow {
	tree := gitworkbench.FilterTree(gitworkbench.BuildTree(files, true), s.changes.filter)
	return gitworkbench.Flatten(tree, s.git.collapsedDirs)
}

// panePaths lists a pane's file paths in snapshot order.
func panePaths(files []gitworkbench.ChangeFile) []string {
	paths := make([]string, 0, len(files))
	for _, cf := range files {
		paths = append(paths, cf.Path)
	}
	return paths
}

// pruneChoice drops chosen paths the pane no longer shows, so a staged,
// unstaged or filtered-away file cannot linger in its old pane's choice.
func pruneChoice(choice *ui.Selection[string], files []gitworkbench.ChangeFile) {
	if choice.Len() == 0 {
		return
	}
	shown := make(map[string]bool, len(files))
	for _, cf := range files {
		shown[cf.Path] = true
	}
	for path := range choice.All() {
		if !shown[path] {
			choice.Remove(path)
		}
	}
}

// gdActionPaths returns the paths a pane's Stage/Unstage action targets:
// the Cmd/Shift selection when there is one, else every file of the pane.
func gdActionPaths(choice *ui.Selection[string], files []gitworkbench.ChangeFile) []string {
	if choice.Len() == 0 {
		return panePaths(files)
	}
	paths := make([]string, 0, choice.Len())
	for _, cf := range files {
		if choice.Has(cf.Path) {
			paths = append(paths, cf.Path)
		}
	}
	return paths
}

// gdSectionHeader is one pane's title row: the shared tree header (chevron,
// semibold title, count) with the pane's Stage/Unstage action kept visible at
// its right edge.
func (s *Shell) gdSectionHeader(c *ui.Context, title string, count int, open *bool, actionLabel string, paths []string, action func([]string)) {
	spec := sidebarSectionSpec{Key: "changes:" + title, Title: title, Count: count, Closed: !*open}
	if count > 0 && !s.git.opBusy {
		spec.Trailing = func() {
			if sectionTextAction(c, actionLabel, actionLabel+" ("+pluralFiles(len(paths))+")").Clicked() {
				action(paths)
			}
		}
	}
	if _, clicked := s.sidebarSectionHeader(c, spec); clicked {
		*open = !*open
	}
}

// gdPaneDividerHeight is the divider's height — a row of the list's column
// between the Unstaged and Staged panes.
const gdPaneDividerHeight float32 = 7

// gdPaneDividerShown reports whether the divider stands between the two lists:
// both open and both with rows.
func (s *Shell) gdPaneDividerShown(unstaged, staged int) bool {
	return s.unstagedOpen && s.stagedOpen && unstaged > 0 && staged > 0
}

// gdPaneDivider is the draggable line between the two panes: dragging it
// hands the space to the pane above or below. Dragging down grows the
// unstaged pane above, so the staged pane below shrinks by the same delta.
func (s *Shell) gdPaneDivider(c *ui.Context) {
	pal := gdPaletteFor(c.Theme())
	divider := ui.Box(c).Height(gdPaneDividerHeight).Shrink(0).Cursor(ui.CursorResizeNS).Label("Resize panes").
		Children(func() {
			ui.Box(c).Height(1).Margin(3, 8).Background(pal.cardBorder)
		})
	if _, dy, held := divider.Dragged(); held {
		s.stagedPaneHeight = clampF(s.stagedPaneHeight-dy, 80, 4000)
	}
}

// clampF bounds a pane dimension.
func clampF(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (s *Shell) activeInstanceInfo() *herdr.Instance {
	for index := range s.instances {
		if s.instances[index].Name == s.activeInstance {
			return &s.instances[index]
		}
	}
	return nil
}

func (s *Shell) agentItems(c *ui.Context) {
	// Two-line Agent cards (2026-10-05 round two) via the shared card
	// builder (2026-10-07): the same rows the agent activity panel shows.
	// The directory is reconcile-gated on measured agent liveness, so a
	// dead binding never surfaces here (2026-10-30).
	cards := s.workbenchCards()
	if len(cards) == 0 {
		ui.Text(c, "No active agents").Padding(Spacing().M, Spacing().L).TextColor(c.Theme().TextMuted)
		return
	}
	s.refreshSubagents() // throttled, background; keeps the nested rows live
	dupProjects := duplicateAgentProjectLabels(cards)
	paneOrder := s.paneOrderMap()

	var groups []agentGroup
	switch s.agentsSortMode() {
	case agentSortTime:
		groups = s.groupAgentCardsByTime(cards, paneOrder)
	case agentSortStatus:
		groups = s.groupAgentCardsByStatus(cards, paneOrder)
	default:
		groups = s.groupAgentCardsByWorkspace(cards, paneOrder)
	}

	// One Tab stop for the whole list; ↑/↓ walk the rows, ←/→ fold and unfold
	// a group or a parent's sub-agents (list_nav.go).
	s.navList(c, &s.nav.agents, false, ui.Column(c).FillWidth(), func() {
		s.agentGroupItems(c, groups, dupProjects)
	})
}

// agentGroupItems draws the grouped list: Workspace, Time, or Status headers,
// with foldable sections. Under Workspace mode, hover on the group header shows
// "+" to start a new Pane in that project (2026-11-02).
func (s *Shell) agentGroupItems(c *ui.Context, groups []agentGroup, dupProjects map[string]bool) {
	s.ensureLaunchKinds() // the group "+" starts the kind last used, as ⌘T does
	isWorkspaceMode := s.agentsSortMode() == agentSortWorkspace
	for _, group := range groups {
		groupRoot, groupCWD := "", ""
		if isWorkspaceMode {
			for _, card := range group.Cards {
				if root := s.agentGitRoot(card); root != "" {
					groupRoot, groupCWD = root, s.agentCWD(card)
					break
				}
			}
		}
		foldKey := agentFoldKey("group", group.Key)
		groupFolded := group.Name != "" && s.foldedAgents[foldKey]
		if group.Name != "" {
			t := c.Theme()
			project := group.Project
			ui.Box(c.Key("agent-group-gap:" + group.Key)).Height(5).Shrink(0)
			head := ui.ButtonBase(c.Key("agent-group:"+group.Key)).FillWidth().Height(24).Radius(treeRowRadius).
				Padding(0, agentCardPadX, 0, 2).Gap(4).AlignItems(ui.Center).FocusRing(false).Label("Toggle " + group.Name)
			switch {
			case s.nav.agents.showsCursor(head):
				head.Background(designTokens(t.Dark).SidebarSelect)
			case head.Hovered():
				head.Background(t.SurfaceHover)
			}
			head.Transition(rowFade)
			if s.nav.agents.row(c, head, navRow{Key: "agent-group:" + group.Key, Section: true, Folder: true, Expanded: !groupFolded}) {
				s.toggleAgentFold(foldKey)
			}
			head.Children(func() {
				treeChevron(c, true, !groupFolded, t.TextMuted)
				ui.Text(c, group.Name).FontSize(Typography().Micro).
					TextColor(t.TextMuted.Alpha(0.75)).SingleLine().Ellipsis("…").Grow(1).MinWidth(0).PassThrough()
				resting := func() {
					switch {
					case groupFolded:
						ui.Text(c, itoa(len(group.Cards))).FontSize(Typography().Micro).TextColor(t.TextMuted).SingleLine().Shrink(0).PassThrough()
						if group.State != opUnknown {
							statusIndicator(c, group.State)
						} else {
							statusIndicator(c, groupIndicatorState(group.Cards, s.clock()))
						}
					case isWorkspaceMode && groupRoot != "":
						branch, adds, dels, files, ok := s.gitMetaForCWD(groupCWD)
						gitFactsView(c, branch, adds, dels, files, ok)
					default:
						ui.Text(c, itoa(len(group.Cards))).FontSize(Typography().Micro).TextColor(t.TextMuted).SingleLine().Shrink(0).PassThrough()
						if group.State != opUnknown {
							statusIndicator(c, group.State)
						} else {
							statusIndicator(c, groupIndicatorState(group.Cards, s.clock()))
						}
					}
				}
				if project == nil {
					resting()
					return
				}
				// The "+" is the kind menu. Its button is built on every frame — the glyph's size under
				// the pointer, no width at rest — so the item chosen from the popup (the pointer is on
				// the popup, not the row) still reaches it, and what the row shows at rest stays a
				// plain label that toggles the fold.
				hovered := head.Hovered()
				if !hovered {
					resting()
				}
				w := float32(0)
				if hovered {
					w = gorexRowBtn
				}
				slot := tipped(c, ui.ButtonBase(c.Key("agent-group-add:"+group.Key)).Size(w, gorexRowBtn).Shrink(0).
					Radius(iconButtonRadius(gorexRowBtn)).Center().FocusRing(false).Label("New Pane in "+group.Name), "New Pane in "+group.Name)
				slot.Menu(func(m *ui.Menu) { s.newTabMenu(m, *project) })
				if hovered && slot.Hovered() {
					slot.Background(t.SurfaceHover)
				}
				slot.Children(func() {
					if hovered {
						ui.Icon(c, iconPlus).FontSize(gorexRowGlyph).TextColor(t.TextMuted)
					}
				})
			})
			head.ContextMenu(func(m *ui.Menu) {
				if m.Item("Fold All Groups").Chosen() {
					s.setAgentFolds(groups, "group", true)
				}
				if m.Item("Unfold All Groups").Chosen() {
					s.setAgentFolds(groups, "group", false)
				}
				m.Separator()
				if m.Item("Fold All Sub-agents").Chosen() {
					s.setAgentFolds(groups, "sub", true)
				}
				if m.Item("Unfold All Sub-agents").Chosen() {
					s.setAgentFolds(groups, "sub", false)
				}
			})
			if head.Clicked() {
				s.toggleAgentFold(foldKey)
			}
		}
		if groupFolded {
			continue
		}
		for _, card := range group.Cards {
			s.agentCardItem(c, card, dupProjects, isWorkspaceMode, groupRoot)
		}
	}
}

// agentCardItem draws one Agent card row and its wiring: keyboard row, click
// (to the pane's remembered surface), context menu and the nested sub-agent
// rows. grouped hides the project label under a Workspace group; groupRoot
// is the group's shared repository root, whose git facts the group label
// already carries.
func (s *Shell) agentCardItem(c *ui.Context, card agent.AgentCardModel, dupProjects map[string]bool, grouped bool, groupRoot string) {
	label, _ := agentCardLabel(card, dupProjects)
	selected := card.PaneID != "" && card.PaneID == s.selectedPaneID
	row := s.agentCardRow(c, card, selected, dupProjects, grouped, grouped && groupRoot != "" && s.agentGitRoot(card) == groupRoot, func() {
		s.agentReviewAction(c, card, nil)
	})
	subKey := subagentFoldKey(card)
	if s.nav.agents.row(c, row, navRow{Key: "agent:" + strings.TrimPrefix(subKey, "sub:"), Folder: len(card.Subagents) > 0, Expanded: !s.subagentsFolded(card, time.Now())}) {
		s.toggleSubagentFold(card)
	}
	row.FocusRing(false)
	if s.nav.agents.showsCursor(row) || row.Focused() {
		s.markAgentReviewedIfPending(card.Key)
		row.Background(designTokens(c.Theme().Dark).SidebarSelect)
	}
	if row.Clicked() {
		// N19 #4: a sidebar Agent click jumps straight to the pane's
		// remembered surface (Chat or Terminal) instead of the detail
		// popover; the popover stays on the tray and header entry
		// points.
		s.openAgentFromSidebar(card)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Open Chat").Chosen() {
			s.openChat(card)
		}
		if card.PaneID != "" {
			s.paneOverflowItems(m, card.PaneID, label)
		}
		if card.ReviewPending {
			m.Separator()
			if m.Item("Mark Reviewed").Chosen() {
				s.markAgentReviewed(card.Key)
			}
		}
	})
	// Sub-agents hang under the agent that spawned them, running or
	// done; the card's chevron folds them. A row click opens the
	// sub-agent's own drawer in Chat, like the page's cards do
	// (2026-10-31 sub-agent views).
	s.nestedSubagentRows(c, card, &s.nav.agents,
		func(sub history.Subagent) { s.openSubagentDrawer(card, sub) },
		func() { s.openChat(card) })
}

// agentGroup is one group of cards in the sidebar Agents list (grouped by
// Workspace, Time, or Status).
type agentGroup struct {
	Key     string
	Name    string
	Cards   []agent.AgentCardModel
	Project *herdr.Project
	State   operationalState
}

// groupAgentCards splits cards by Workspace; retained for backward compatibility.
func groupAgentCards(cards []agent.AgentCardModel) []agentGroup {
	var groups []agentGroup
	at := map[string]int{}
	for _, card := range cards {
		key := card.ProjectID
		i, ok := at[key]
		if !ok {
			i = len(groups)
			at[key] = i
			groups = append(groups, agentGroup{Key: key, Name: card.ProjectName})
		}
		groups[i].Cards = append(groups[i].Cards, card)
	}
	return groups
}

// agentSortMode is the sidebar Agents list's grouping mode (settings.Session.
// AgentsSort).
type agentSortMode string

const (
	agentSortWorkspace agentSortMode = "workspace"
	agentSortGroup     agentSortMode = "" // alias / backward compatibility
	agentSortTime      agentSortMode = "time"
	agentSortStatus    agentSortMode = "status"
)

// agentSortModes is the sort menu's grouping rows, in menu order.
var agentSortModes = []struct {
	Mode  agentSortMode
	Label string
}{
	{agentSortWorkspace, "By Workspace"},
	{agentSortTime, "By Time"},
	{agentSortStatus, "By Status"},
}

// normalizeAgentSort maps a stored sort onto a mode; anything unknown (an
// old file, a typo) reads as the default (Workspace).
func normalizeAgentSort(v string) agentSortMode {
	switch agentSortMode(v) {
	case agentSortTime, agentSortStatus:
		return agentSortMode(v)
	case agentSortWorkspace, "group", "":
		return agentSortWorkspace
	}
	return agentSortWorkspace
}

func (s *Shell) agentsSortMode() agentSortMode { return normalizeAgentSort(s.agentsSort) }

func (s *Shell) setAgentsSort(mode agentSortMode) { s.agentsSort = string(mode) }

// agentSortMenu fills both of the Agents header's menus — the hover sort
// icon's dropdown and the header's right-click menu; the checked row is the
// current grouping mode, followed by an orthogonal toggle for sorting by updated time.
func (s *Shell) agentSortMenu(m *ui.Menu) {
	for _, opt := range agentSortModes {
		if m.Item(opt.Label).Checked(s.agentsSortMode() == opt.Mode).Chosen() {
			s.setAgentsSort(opt.Mode)
		}
	}
	m.Separator()
	if m.Item("Sort by Updated Time").Checked(s.agentsSortByTime).Chosen() {
		s.agentsSortByTime = !s.agentsSortByTime
	}
	if s.agentsSortByTime {
		if m.Item("Newest First").Checked(!s.agentsSortAscending).Chosen() {
			s.agentsSortAscending = false
		}
		if m.Item("Oldest First").Checked(s.agentsSortAscending).Chosen() {
			s.agentsSortAscending = true
		}
	}
}

// paneOrderMap builds a stable index for all panes based on their spatial
// and hierarchical layout (Workspace -> Tab -> LayoutPane).
func (s *Shell) paneOrderMap() map[string]int {
	order := make(map[string]int, len(s.projection.Panes))
	idx := 0
	for _, prj := range s.projection.Projects {
		for _, tab := range tabsForProject(s.projection, prj.ID) {
			panes := panesForTab(s.projection, tab.ID)
			sortPanesByLayout(s.projection, tab.ID, panes)
			for _, p := range panes {
				if _, ok := order[p.ID]; !ok {
					order[p.ID] = idx
					idx++
				}
			}
		}
	}
	for _, p := range s.projection.Panes {
		if _, ok := order[p.ID]; !ok {
			order[p.ID] = idx
			idx++
		}
	}
	for _, a := range s.projection.Agents {
		if a.PaneID != "" {
			if _, ok := order[a.PaneID]; !ok {
				order[a.PaneID] = idx
				idx++
			}
		}
	}
	return order
}

// compareCardsByPaneOrder orders cards by the index of their owning pane,
// falling back to title and key for deterministic tie-breaking.
func compareCardsByPaneOrder(a, b agent.AgentCardModel, order map[string]int) int {
	idxA, okA := order[a.PaneID]
	idxB, okB := order[b.PaneID]
	switch {
	case okA && okB:
		if idxA != idxB {
			return idxA - idxB
		}
	case okA && !okB:
		return -1
	case !okA && okB:
		return 1
	}
	if a.Title != b.Title {
		if a.Title < b.Title {
			return -1
		}
		return 1
	}
	if a.Key.TerminalID < b.Key.TerminalID {
		return -1
	}
	if a.Key.TerminalID > b.Key.TerminalID {
		return 1
	}
	return 0
}

// compareCardsByTime orders cards chronologically by LastActive.
func compareCardsByTime(a, b agent.AgentCardModel, ascending bool) int {
	if a.LastActive.IsZero() && b.LastActive.IsZero() {
		return 0
	}
	if a.LastActive.IsZero() {
		return 1
	}
	if b.LastActive.IsZero() {
		return -1
	}
	if ascending {
		switch {
		case a.LastActive.Before(b.LastActive):
			return -1
		case a.LastActive.After(b.LastActive):
			return 1
		default:
			return 0
		}
	}
	switch {
	case a.LastActive.After(b.LastActive):
		return -1
	case a.LastActive.Before(b.LastActive):
		return 1
	default:
		return 0
	}
}

// sortGroupCards orders cards within a single group according to the active
// sort mode (update time or default pane order).
func (s *Shell) sortGroupCards(cards []agent.AgentCardModel, paneOrder map[string]int) {
	if s.agentsSortByTime {
		slices.SortStableFunc(cards, func(a, b agent.AgentCardModel) int {
			c := compareCardsByTime(a, b, s.agentsSortAscending)
			if c != 0 {
				return c
			}
			return compareCardsByPaneOrder(a, b, paneOrder)
		})
	} else {
		slices.SortStableFunc(cards, func(a, b agent.AgentCardModel) int {
			return compareCardsByPaneOrder(a, b, paneOrder)
		})
	}
}

// groupAgentCardsByWorkspace partitions cards by their Workspace (ProjectID).
func (s *Shell) groupAgentCardsByWorkspace(cards []agent.AgentCardModel, paneOrder map[string]int) []agentGroup {
	groupsByKey := make(map[string]*agentGroup)
	var keysInOrder []string
	for _, card := range cards {
		key := card.ProjectID
		g, exists := groupsByKey[key]
		if !exists {
			name := card.ProjectName
			if name == "" {
				name = "Workspace"
			}
			g = &agentGroup{Key: key, Name: name}
			groupsByKey[key] = g
			keysInOrder = append(keysInOrder, key)
		}
		g.Cards = append(g.Cards, card)
	}

	for _, g := range groupsByKey {
		s.sortGroupCards(g.Cards, paneOrder)
	}

	groups := make([]agentGroup, 0, len(keysInOrder))
	for _, k := range keysInOrder {
		g := *groupsByKey[k]
		g.Project = s.groupProject(g)
		groups = append(groups, g)
	}

	if s.agentsSortByTime {
		slices.SortStableFunc(groups, func(a, b agentGroup) int {
			if len(a.Cards) == 0 || len(b.Cards) == 0 {
				return len(b.Cards) - len(a.Cards)
			}
			return compareCardsByTime(a.Cards[0], b.Cards[0], s.agentsSortAscending)
		})
	} else {
		slices.SortStableFunc(groups, func(a, b agentGroup) int {
			if len(a.Cards) == 0 || len(b.Cards) == 0 {
				return len(b.Cards) - len(a.Cards)
			}
			return compareCardsByPaneOrder(a.Cards[0], b.Cards[0], paneOrder)
		})
	}

	return groups
}

// groupAgentCardsByTime partitions cards into time buckets: Today, Yesterday,
// Last 7 Days, Earlier.
func (s *Shell) groupAgentCardsByTime(cards []agent.AgentCardModel, paneOrder map[string]int) []agentGroup {
	now := s.clock().Local()
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()) }
	nowDay := day(now)

	buckets := map[string][]agent.AgentCardModel{
		"time:today":     nil,
		"time:yesterday": nil,
		"time:week":      nil,
		"time:earlier":   nil,
	}

	for _, card := range cards {
		if card.LastActive.IsZero() {
			buckets["time:earlier"] = append(buckets["time:earlier"], card)
			continue
		}
		at := card.LastActive.Local()
		days := int(nowDay.Sub(day(at)).Hours()/24 + 0.5)
		switch {
		case days <= 0:
			buckets["time:today"] = append(buckets["time:today"], card)
		case days == 1:
			buckets["time:yesterday"] = append(buckets["time:yesterday"], card)
		case days < 7:
			buckets["time:week"] = append(buckets["time:week"], card)
		default:
			buckets["time:earlier"] = append(buckets["time:earlier"], card)
		}
	}

	defs := []struct {
		Key   string
		Label string
	}{
		{"time:today", "Today"},
		{"time:yesterday", "Yesterday"},
		{"time:week", "Last 7 Days"},
		{"time:earlier", "Earlier"},
	}

	if s.agentsSortByTime && s.agentsSortAscending {
		defs = []struct {
			Key   string
			Label string
		}{
			{"time:earlier", "Earlier"},
			{"time:week", "Last 7 Days"},
			{"time:yesterday", "Yesterday"},
			{"time:today", "Today"},
		}
	}

	var groups []agentGroup
	for _, def := range defs {
		bCards := buckets[def.Key]
		if len(bCards) == 0 {
			continue
		}
		s.sortGroupCards(bCards, paneOrder)
		groups = append(groups, agentGroup{
			Key:   def.Key,
			Name:  def.Label,
			Cards: bCards,
		})
	}
	return groups
}

// agentCardStatusState maps an agent card onto its status group bucket.
func agentCardStatusState(card agent.AgentCardModel) (key string, label string, state operationalState) {
	switch {
	case card.Attention == agent.OpNeedsAttention || card.RuntimePhase == agent.PhaseBlocked || card.RuntimePhase == agent.PhaseFailed:
		return "status:attention", "Needs Attention", opNeedsAttention
	case card.Working():
		return "status:working", "Working", opWorking
	case card.ReviewPending || card.Attention == agent.OpReadyForReview:
		return "status:review", "Ready for Review", opReadyForReview
	case card.Attention == agent.OpResolved:
		return "status:resolved", "Resolved", opResolved
	default:
		return "status:idle", "Idle", opIdle
	}
}

// groupAgentCardsByStatus partitions cards by their operational state:
// Needs Attention, Working, Ready for Review, Idle, Resolved.
func (s *Shell) groupAgentCardsByStatus(cards []agent.AgentCardModel, paneOrder map[string]int) []agentGroup {
	buckets := map[string][]agent.AgentCardModel{}
	for _, card := range cards {
		k, _, _ := agentCardStatusState(card)
		buckets[k] = append(buckets[k], card)
	}

	defs := []struct {
		Key   string
		Label string
		State operationalState
	}{
		{"status:attention", "Needs Attention", opNeedsAttention},
		{"status:working", "Working", opWorking},
		{"status:review", "Ready for Review", opReadyForReview},
		{"status:idle", "Idle", opIdle},
		{"status:resolved", "Resolved", opResolved},
	}

	var groups []agentGroup
	for _, def := range defs {
		bCards := buckets[def.Key]
		if len(bCards) == 0 {
			continue
		}
		s.sortGroupCards(bCards, paneOrder)
		groups = append(groups, agentGroup{
			Key:   def.Key,
			Name:  def.Label,
			Cards: bCards,
			State: def.State,
		})
	}
	return groups
}

// agentCardsByTime orders the cards by when they last worked, newest first;
// unknown instants sink, ties keep the directory's stable order.
func agentCardsByTime(cards []agent.AgentCardModel) []agent.AgentCardModel {
	out := append([]agent.AgentCardModel(nil), cards...)
	slices.SortStableFunc(out, func(a, b agent.AgentCardModel) int {
		switch {
		case a.LastActive.After(b.LastActive):
			return -1
		case a.LastActive.Before(b.LastActive):
			return 1
		}
		return 0
	})
	return out
}

// groupProject resolves a group's Herdr Workspace from the live projection;
// nil when the runtime no longer reports it — then the group carries no "+".
func (s *Shell) groupProject(group agentGroup) *herdr.Project {
	for i := range s.projection.Projects {
		if s.projection.Projects[i].ID == group.Key {
			return &s.projection.Projects[i]
		}
	}
	return nil
}

func (s *Shell) handleSidebarSelection(selected string) {
	kind, id, ok := strings.Cut(selected, ":")
	if !ok || id == "" {
		return
	}
	// No bare workspace push beside the selection: selectProject/selectTab/
	// selectPane record the landing as /workspace?pane=… themselves, so a
	// row click is one history entry, not two.
	switch kind {
	case "instance":
		s.openInstance(id)
	case "project":
		s.selectProject(id)
	case "tab":
		s.selectTab(id)
	case "pane", "agent":
		s.selectPane(id)
	case "recent":
		s.router.Push("/history/" + id)
	}
}

// clearSidebarSelectionOnHostedPage keeps the Workspace tree from holding a
// selection while a page that shares it is up (treeHostedPage): the page is
// not that Workspace, and a tree row can only fire its click when it is not
// already the selected one — so leaving the page by clicking the Workspace you
// came from works.
func (s *Shell) clearSidebarSelectionOnHostedPage() {
	if treeHostedPage(s.router.Path()) {
		s.selected = ""
	}
}
