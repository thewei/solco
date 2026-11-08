package nativeui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
)

/**
 * [INPUT]: 依赖 workspace_surface.go 的主表面状态机、git_service* 的快照/刷新、right_panel*.go 的 Files 树与预览、panel_motion.go 的 slideWidth、gorex_style 的卡片面、tooltip.go 的 tipped、shortcuts.go 的 sidePanelToggleBinding
 * [OUTPUT]: 对外提供 Shell.sidePanel（右侧面板状态：开合/页/宽度，随会话持久化）、sidePanelLayout（几何纯函数：停靠宽度或浮层）、toggleSidePanel/closeSidePanel、sidePanelHost（/workspace 内容列的右侧停靠槽，窄窗口为浮层）、sidePanelToggleButton（标题栏右端开关）、sidePanelCard（与终端窗格卡片同一纸色/无边/柔影）/sidePanelHeader（Changes | Files 页头）、sidePanelRefreshChanges 定时刷新
 * [POS]: 2026-11-07 决定「Solco Git and the right panel」第 2 条、2026-11-08 决定「The panel is a list」：T3 式右侧面板，只列条目（Changes 的文件列表 + 一行提交条，Files 的树与搜索），点击的条目在中心以 Detail 卡片显示（detail_surface.go）；只在 /workspace 的 Terminal/Chat/Detail 表面旁出现，从不自动打开；中心表面的唯一所有者仍是 WorkspacePrimarySurface，面板不是第二个表面，也不是第二套 Git/Files 实现——Git 整页与 Files 整页已于 G5 退役，更重的功能在 Solco Git
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// The right panel (2026-11-07 decision, item 2). T3 Code's right panel,
// carried over: a card beside the Terminal or Chat that shows what the work
// changed (Changes) or what is on disk (Files) without leaving the work.
//
//   - It exists only on /workspace while the center is the Terminal or Chat.
//     Diff and Commit own the whole center, so the panel steps aside and
//     comes back with the surface it left; every other page has none.
//   - It never opens by itself — no Agent event, no commit, no file click
//     elsewhere. ⌥⌘B and the title bar's right-most button open and close
//     it; the next launch finds it as it was left.
//   - Docked, it takes sidePanelDefaultWidth (drag the left seam; double-click
//     resets), but never so much that the center falls under sidePanelCenterFloor.
//     Where even sidePanelMinWidth would not fit, it floats over the center
//     instead (Esc or a click outside closes it) — the toggle stays.
//   - The width commits when the drag lets go: a live drag would re-measure
//     the Terminal on every frame and resize the Herdr pane with it. A guide
//     line shows where the seam will land.

const (
	sidePanelDefaultWidth = float32(400)
	sidePanelMinWidth     = float32(320)
	// sidePanelCenterFloor is the room the center keeps: a docked panel may take
	// the rest of the window, no more.
	sidePanelCenterFloor = float32(560)
	// sidePanelGutter is the breathing room the floor formula keeps beside the
	// panel card (the card's own margin and the center's right inset).
	sidePanelGutter = float32(24)
	// sidePanelStoredMax bounds a restored width; the window caps it again at
	// layout time.
	sidePanelStoredMax = 900
	// sidePanelResizeHit is the seam's drag handle width, over the card's left
	// edge.
	sidePanelResizeHit = float32(6)
	// sidePanelTabsW is the Changes | Files switch: wide enough for the
	// Changes label with its count at the narrowest card.
	sidePanelTabsW = float32(236)
	// sidePanelRefreshEvery is how often the Changes page re-reads the worktree
	// while it is on screen: the panel is a live view next to a working
	// Agent.
	sidePanelRefreshEvery = 4 * time.Second
)

// sidePanelPage is the panel's page: the two T3 Code ships.
type sidePanelPage string

const (
	sidePanelChanges sidePanelPage = "changes"
	sidePanelFiles   sidePanelPage = "files"
)

// parseSidePanelPage reads a persisted page; anything unknown is "no choice yet".
func parseSidePanelPage(value string) sidePanelPage {
	switch sidePanelPage(value) {
	case sidePanelChanges, sidePanelFiles:
		return sidePanelPage(value)
	}
	return ""
}

// sidePanelState is the right panel's presentation state.
type sidePanelState struct {
	// open is the user's wish; shown is what the surface allows of it.
	open bool
	// page is the remembered page; "" until the first open picks one.
	page sidePanelPage
	// width is the dragged width; 0 is the default.
	width float32
	// dragging/dragW: the seam is held, and the width its release applies.
	dragging bool
	dragW    float32
	// wasShown is whether the panel was on screen last frame: the edge into
	// view re-reads what the page shows, and only open/close eases.
	wasShown bool
	// overlay is whether the last layout floated the panel.
	overlay bool
	// zoomed: a single-pane zoom (zoomPaneAction) hid the panel with the
	// sidebar; it comes back with the sidebar, or when the user toggles it.
	zoomed bool
	// tick is the Changes page's re-reading timer (nil = not armed).
	tick *time.Timer
}

// sidePanelLayout is the panel's geometry in a window this wide whose sidebar
// takes sidebarW (0 when shut): the card's width and whether it floats. want
// is the remembered width (0 = default). Docked, the panel narrows toward
// sidePanelMinWidth before it takes room from the center; below that it floats.
func sidePanelLayout(windowW, sidebarW, want float32) (width float32, overlay bool) {
	if want <= 0 {
		want = sidePanelDefaultWidth
	}
	want = max(want, sidePanelMinWidth)
	if room := windowW - sidebarW - sidePanelCenterFloor - sidePanelGutter; room >= sidePanelMinWidth {
		return min(want, room), false
	}
	// Floating, the card keeps a margin on each side inside the content area.
	return min(want, max(windowW-sidebarW-2*gorexGap, 0)), true
}

// sidePanelMaxWidth is the widest a drag may make a docked panel.
func sidePanelMaxWidth(windowW, sidebarW float32) float32 {
	return max(windowW-sidebarW-sidePanelCenterFloor-sidePanelGutter, sidePanelMinWidth)
}

// clampSessionSidePanelWidth bounds a restored width; the window is unknown at
// restore time, so layout applies the window-relative cap.
func clampSessionSidePanelWidth(width int) int {
	return min(max(width, int(sidePanelMinWidth)), sidePanelStoredMax)
}

// sidePanelSidebarW is the sidebar's width in the layout: its target, not the
// sliding value, so the panel does not flip between docked and floating
// while the sidebar eases.
func (s *Shell) sidePanelSidebarW() float32 {
	if s.sidebarCollapsed {
		return 0
	}
	return s.sidebarW
}

// sidePanelSurfaceAllowed: the panel lives beside the Terminal, the Chat and
// the Detail card it opens — the center's own reading (workspaceCenter), where
// a surface not chosen yet is the Terminal. A Commit owns the whole center
// while its editor is up.
func (s *Shell) sidePanelSurfaceAllowed() bool {
	return s.surface.current() != WorkspaceSurfaceCommit
}

// sidePanelDockedWidth is the room a docked panel takes from the center —
// its card and the gutter — and 0 when it is shut, hidden or floating over
// the center. The Detail card lays its diff out in what is left.
func (s *Shell) sidePanelDockedWidth(windowW float32) float32 {
	if !s.sidePanelShown() {
		return 0
	}
	width, overlay := sidePanelLayout(windowW, s.sidePanelSidebarW(), s.sidePanel.width)
	if overlay {
		return 0
	}
	return width + gorexGap
}

// changesOnScreen is whether the worktree's changes are being read right now:
// the panel's Changes page, or the Detail card showing them.
func (s *Shell) changesOnScreen() bool {
	if s.router.Path() != routeWorkspace {
		return false
	}
	if s.sidePanelShown() && s.sidePanel.page == sidePanelChanges {
		return true
	}
	return s.surface.current() == WorkspaceSurfaceDetail && s.surface.detailKind() == DetailChanges
}

// sidePanelAvailable is whether the current page can carry the panel.
func (s *Shell) sidePanelAvailable() bool {
	return s.router.Path() == routeWorkspace && s.sidePanelSurfaceAllowed()
}

// sidePanelShown is whether the panel is on screen: wanted, allowed, not hidden
// by a zoom.
func (s *Shell) sidePanelShown() bool {
	return s.sidePanel.open && !s.sidePanel.zoomed && s.sidePanelAvailable()
}

// sidePanelOverlayUp is whether the panel floats over the center right now.
func (s *Shell) sidePanelOverlayUp() bool { return s.sidePanelShown() && s.sidePanel.overlay }

// toggleSidePanel is ⌥⌘B and the title bar button. Where the page cannot carry
// the panel, asking for it lands the workspace first — the panel has no
// other home — and never closes a panel nobody can see.
func (s *Shell) toggleSidePanel() {
	s.sidePanel.zoomed = false
	if !s.sidePanelAvailable() {
		s.sidePanel.open = true
		s.landSelectedPane()
		return
	}
	s.sidePanel.open = !s.sidePanel.open
	if !s.sidePanel.open {
		s.sidePanelRelaxTick()
	}
}

// closeSidePanel is the panel's ✕, the overlay's Esc and its outside click.
func (s *Shell) closeSidePanel() {
	s.sidePanel.open = false
	s.sidePanelRelaxTick()
}

// openSidePanelChanges is what "review these changes" does now that the Git
// page is gone (2026-11-08, G5): the panel opens on Changes with path picked
// ("" picks the first file). It answers a click of the user's own — Chat's
// Review changes — so the panel still never opens by itself.
func (s *Shell) openSidePanelChanges(path string) {
	s.sidePanel.zoomed = false
	wasShown := s.sidePanelShown()
	s.sidePanel.open = true
	s.sidePanel.page = sidePanelChanges
	switch {
	case !s.sidePanelAvailable():
		s.landSelectedPane() // the panel has no other home
	case wasShown:
		s.sidePanelPrepare() // already on screen: re-read the worktree now
	}
	if path == "" {
		path = firstChangedPath(s.gitSnapshot())
	}
	if path != "" {
		s.sidePanelSelectChange(path, gdViewCombined)
	}
}

// setSidePanelPage switches pages and re-reads what the new page shows.
func (s *Shell) setSidePanelPage(page sidePanelPage) {
	if s.sidePanel.page == page {
		return
	}
	s.sidePanel.page = page
	s.sidePanelPrepare()
}

// sidePanelPageNow is the page the panel shows. The first open has no
// remembered page: Changes when the worktree has changes, else Files.
func (s *Shell) sidePanelPageNow() sidePanelPage {
	if s.sidePanel.page != "" {
		return s.sidePanel.page
	}
	if snap := s.gitSnapshot(); snap != nil && len(snap.Files) > 0 {
		return sidePanelChanges
	}
	return sidePanelFiles
}

// sidePanelChangeCount is the Changes tab's badge: the worktree's changed files.
func (s *Shell) sidePanelChangeCount() int {
	if snap := s.gitSnapshot(); snap != nil {
		return len(snap.Files)
	}
	return 0
}

// sidePanelPrepare readies the page that is about to show: the Changes page
// reads the worktree, the Files page fences its tree to the selected Tab's
// folder. The first call also makes the page choice sticky.
func (s *Shell) sidePanelPrepare() {
	s.sidePanel.page = s.sidePanelPageNow()
	switch s.sidePanel.page {
	case sidePanelChanges:
		s.syncGitContext()
		s.ensureGitSnapshot(false)
		s.sidePanelArmTick()
	case sidePanelFiles:
		s.syncRightPanelRoot()
		s.sidePanelRelaxTick()
	}
}

// sidePanelArmTick starts the Changes page's re-reading timer. Headless runs have
// no window and no lane to apply on, so none starts there.
func (s *Shell) sidePanelArmTick() {
	if s.win == nil || s.sidePanel.tick != nil {
		return
	}
	s.sidePanel.tick = time.AfterFunc(sidePanelRefreshEvery, func() { s.applyOnWindow(s.sidePanelTickFired) })
}

// sidePanelRelaxTick disarms the timer unless the Detail card still shows the
// changes it re-reads for.
func (s *Shell) sidePanelRelaxTick() {
	if !s.changesOnScreen() {
		s.sidePanelStopTick()
	}
}

// sidePanelStopTick disarms the timer.
func (s *Shell) sidePanelStopTick() {
	if s.sidePanel.tick != nil {
		s.sidePanel.tick.Stop()
		s.sidePanel.tick = nil
	}
}

// sidePanelTickFired re-reads the worktree and re-arms, for as long as the
// changes are on screen (the Changes page or the Detail card showing them);
// leaving them lets the timer lapse until they show again (sidePanelPrepare
// and openDetail arm it).
func (s *Shell) sidePanelTickFired() {
	s.sidePanel.tick = nil
	if !s.changesOnScreen() {
		return
	}
	if s.win != nil && s.win.IsVisible() {
		s.sidePanelRefreshChanges()
	}
	s.sidePanelArmTick()
}

// sidePanelRefreshChanges re-reads the worktree in the background and swaps the
// snapshot in only when something moved: an unchanged read leaves the Diff
// rows alone, so the panel does not rebuild under the reader every few
// seconds. A moved snapshot lands as an expected drift — the panel is a live
// view, so it has no "Changes updated" pill to click.
func (s *Shell) sidePanelRefreshChanges() {
	g := s.git
	if g == nil || g.root == "" || g.refreshing || g.opBusy || g.committing {
		return
	}
	root := g.root
	g.cache.Invalidate(root)
	g.refreshing = true
	gen := g.snapshotGen.Add(1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*gitworkbench.ReadTimeout)
		defer cancel()
		snap, _ := g.cache.Refresh(ctx, root)
		s.applyGuarded(g.snapshotGen.Load, gen, func() {
			g.refreshing = false
			if snap == nil || snap.Root != g.root {
				return
			}
			if cur := g.snapshot; cur != nil && cur.Signature != "" && cur.Signature == snap.Signature {
				return
			}
			s.applyGitSnapshot(snap)
		})
	}()
}

// sidePanelHost is the panel's slot in the /workspace row, after the center:
// docked it is a persistent slide host (open and close ease, a width change
// lands at once), floating it is an overlay over the center.
func (s *Shell) sidePanelHost(c *ui.Context) {
	if !s.sidePanelAvailable() {
		s.sidePanel.wasShown = false
		s.sidePanel.dragging = false
		return
	}
	if !s.sidebarCollapsed {
		s.sidePanel.zoomed = false // the zoom's sidebar is back; the panel follows it
	}
	windowW, _ := c.Size()
	sidebarW := s.sidePanelSidebarW()
	width, overlay := sidePanelLayout(windowW, sidebarW, s.sidePanel.width)
	s.sidePanel.overlay = overlay
	shown := s.sidePanel.open && !s.sidePanel.zoomed
	if shown && !s.sidePanel.wasShown {
		s.sidePanelPrepare()
	}
	if overlay {
		s.sidePanelFloating(c, width, shown)
		s.sidePanel.wasShown = shown
		return
	}
	// Open and close ease; anything else that changes the width (a window
	// resize, a committed drag) lands at once.
	full := width + gorexGap
	host := ui.Row(c.Key("side-panel-slide")).Shrink(0).AlignItems(ui.Stretch)
	slide := slideWidth(host, "width", !shown, full, shown == s.sidePanel.wasShown)
	s.sidePanel.wasShown = shown
	if slide <= panelSlideEpsilon {
		s.sidePanel.dragging = false
		return
	}
	host.Width(slide).Clip()
	host.Children(func() {
		ui.Row(c).Width(full).Shrink(0).AlignItems(ui.Stretch).Padding(gorexGap, gorexGap, gorexGap, 0).Children(func() {
			s.sidePanelCard(c, width, false)
		})
		s.sidePanelSeam(c, width, windowW, sidebarW)
	})
	if s.sidePanel.dragging {
		// Where the seam lands: the guide is the card's future left edge,
		// measured from the row's right edge, in the row — not in the
		// clipped host.
		ui.Box(c.Key("side-panel-guide")).Absolute().Top(0).Bottom(0).Right(gorexGap + s.sidePanel.dragW - 1).Width(2).
			Background(c.Theme().Accent.Alpha(0.7)).PassThrough()
	}
}

// sidePanelSeam is the drag handle over the docked card's left edge. Dragging it
// previews with a guide and applies on release; a double-click restores the
// default width.
func (s *Shell) sidePanelSeam(c *ui.Context, width, windowW, sidebarW float32) {
	seam := ui.Box(c.Key("side-panel-seam")).Absolute().Left(0).Top(0).Bottom(0).Width(sidePanelResizeHit).
		Cursor(ui.CursorResizeEW).Label("Resize right panel")
	if seam.DoubleClicked() {
		s.sidePanel.width = 0
		return
	}
	dx, _, held := seam.Dragged()
	switch {
	case held:
		if !s.sidePanel.dragging {
			s.sidePanel.dragging, s.sidePanel.dragW = true, width
		}
		s.sidePanel.dragW = clampF(s.sidePanel.dragW-dx, sidePanelMinWidth, sidePanelMaxWidth(windowW, sidebarW))
	case s.sidePanel.dragging:
		s.sidePanel.dragging = false
		s.sidePanel.width = s.sidePanel.dragW
	}
}

// sidePanelFloating is the narrow-window panel: a card over the right of the
// center. Esc or a click outside closes it; it takes no focus of its own.
func (s *Shell) sidePanelFloating(c *ui.Context, width float32, shown bool) {
	s.sidePanel.dragging = false
	if !shown {
		return
	}
	if c.Shortcut(0, ui.KeyEscape) {
		s.closeSidePanel()
		return
	}
	scrim := ui.Box(c.Key("side-panel-scrim")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Cursor(ui.CursorDefault)
	if scrim.Clicked() {
		s.closeSidePanel()
		return
	}
	ui.Row(c.Key("side-panel-overlay")).Absolute().Top(0).Bottom(0).Right(0).Width(width + 2*gorexGap).
		AlignItems(ui.Stretch).Padding(gorexGap).Transition(ui.ElementTransition{
		Enter: &ui.Motion{X: 24}, Exit: &ui.Motion{X: 24}, Duration: motionPanel,
	}).Children(func() {
		s.sidePanelCard(c, width, true)
	})
}

// sidePanelCard is the panel's face: a pane card (paneCardFaceOf), the
// Changes | Files header over the page's body — one colour, no rule between
// the bands (2026-11-08).
func (s *Shell) sidePanelCard(c *ui.Context, width float32, floating bool) {
	t := c.Theme()
	k := gorexColorsOf(t.Dark)
	// The panel is a pane card in every respect (2026-11-08): the same paper,
	// edge and soft shadow as a Terminal card, resting state; only the
	// floating one, which lies over the center, casts the deeper tip shadow.
	card := ui.Column(c.Key("side-panel-card")).Width(width).Shrink(0).Radius(gorexCardR).Clip().MinHeight(0).Label("Right panel")
	paneCardFaceOf(t.Dark, false, false).dress(card)
	if floating {
		card.Shadow(0, 8, 28, 0, tipShadow(t))
	}
	card.Children(func() {
		s.sidePanelHeader(c, k)
		ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).Children(func() {
			s.sidePanelBuilding, s.sidePanelCardW = true, width
			defer func() { s.sidePanelBuilding = false }()
			_, windowH := c.Size()
			body := windowH - titlebarHeight(c.TitleBar().Height) - 2*gorexGap - gorexHeaderH
			switch s.sidePanelPageNow() {
			case sidePanelFiles:
				s.sidePanelFilesPage(c, body)
			default:
				s.sidePanelChangesPage(c, body)
			}
		})
	})
}

// sidePanelHeader is the card's strip: the Changes | Files switch, Open in
// Solco Git (once Solco Git is found) and the close button.
func (s *Shell) sidePanelHeader(c *ui.Context, k *gorexColors) {
	ui.Row(c).FillWidth().Height(gorexHeaderH).Shrink(0).Padding(0, 6, 0, 8).Gap(6).AlignItems(ui.Center).Children(func() {
		chosen := 0
		if s.sidePanelPageNow() == sidePanelFiles {
			chosen = 1
		}
		ui.Row(c).Width(sidePanelTabsW).Shrink(0).Children(func() {
			moved := segmentTabs(c, "Right panel page", &chosen,
				[]segmentTab{{"Changes", iconFileDiff}, {"Files", iconFile}},
				func(i int) int {
					if i == 0 {
						return s.sidePanelChangeCount()
					}
					return 0
				})
			if moved {
				s.setSidePanelPage([]sidePanelPage{sidePanelChanges, sidePanelFiles}[chosen])
			}
		})
		ui.Spacer(c)
		// Where the T3 panel offers "expand to the full page", this one hands
		// the page to Solco Git, which draws the entry only once it is found.
		if s.solcoGitReady() {
			if gorexIconButton(c, k, iconOpen, "Open in Solco Git", "Open in Solco Git", false, gorexPaneBtn, gorexPaneGlyph).Clicked() {
				s.openInSolcoGit(s.solcoGitPanelTarget())
			}
		}
		if gorexIconButton(c, k, iconClose, "Close Right Panel", "Close Right Panel", false, gorexPaneBtn, gorexPaneGlyph).Clicked() {
			s.closeSidePanel()
		}
	})
}

// sidePanelToggleButton is the title bar's right-most button: highlighted while
// the panel is open, present wherever the page can carry one.
func (s *Shell) sidePanelToggleButton(c *ui.Context, k *gorexColors) {
	if !s.sidePanelAvailable() {
		return
	}
	on := s.sidePanelShown()
	label := "Show Right Panel"
	if on {
		label = "Hide Right Panel"
	}
	button := gorexIconButton(c, k, iconPanelRight, label, "", on, gorexIconBtn, gorexIconGlyph)
	tipped(c, button, label, tipKeys(sidePanelToggleBinding.keycaps()))
	if button.Clicked() {
		s.toggleSidePanel()
	}
}
