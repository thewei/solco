package nativeui

import (
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/settings"
)

/**
 * [INPUT]: 依赖 mygo/plugins/terminal, mygo/ui, herdr, settings, gorex_style, brands 的 agentProvider/agentMark、markAgentReviewedForPane 聚焦取消 Review
 * [OUTPUT]: 对外提供 terminalCanvas（浅色窗口铺 paneGround 灰底，顶行卡片贴齐侧栏搜索栏）, paneCard（纸色/边/阴影来自 paneCardFaceOf，选中态使用中性细边）, paneCardHeader, paneStackInfo, paneLogoView, syncTerminals, closeAllTerminals, paneHeaderVisuals；画布内另渲染可拖拽的 split 分隔条（renderSplitDividers → splitGrowTarget/splitResizeAmount → resizePane），把拖拽解析成 Herdr pane.resize；终端聚焦时自动清除对应 Agent 的 Review 状态
 * [POS]: nativeui 的终端画布与 Gorex 窗格卡片主渲染器；可见 Pane 的 Herdr attach 与 OSC 回调通过 applyOnWindow + terminal identity 防迟到事件（隐藏不会删除状态记录），布局权威始终在 Herdr
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// terminalOptionsFromSettings maps persisted presentation preferences onto the
// official MyGo terminal options. It never carries Herdr terminal identity or
// runtime state; Herdr owns the PTY, scrollback semantics and process.
func terminalOptionsFromSettings(value settings.TerminalSettings) terminal.Options {
	return terminal.Options{
		Font: terminal.Font{
			Family:     fontStack(value.FontFamily),
			Size:       float32(value.FontSize),
			LineHeight: float32(value.LineHeight),
		},
		Scrollback:  value.Scrollback,
		OptionAsAlt: value.OptionAsAlt,
		Transparent: value.Transparent,
	}
}

// LocalDragSelect is a per-pane decision, not a setting: panes whose
// content lives in Herdr's scrollback (shells, scrollback-backed TUIs
// like agy, claude, codex) keep the primary-button drag and the right click
// local to the view — plain-drag text selection and a right-click menu — while
// the wheel still reports (routeAttachMouse moves Herdr's viewport). True
// alt-screen Agent TUIs (pi, omp; no scrollback range) keep full reporting:
// their wheel must reach the program, and selection is Shift+drag.
func localDragSelectFor(geometry paneGeometry) bool {
	if isInteractiveMouseAgent(geometry.pane.Agent) {
		return geometry.pane.Scroll != nil && geometry.pane.Scroll.MaxOffsetFromBottom > 0
	}
	return true
}

// fontStack keeps one user-chosen family first and proven monospace
// fallbacks behind it. The Nerd Font families only serve glyphs the
// earlier fonts lack — the Private Use Area icons CLIs like eza and lsd
// print, which the system cascade never resolves — and are skipped when
// not installed.
func fontStack(family string) string {
	return family + ", Menlo, Cascadia Mono, JetBrainsMono Nerd Font Mono, SauceCodePro Nerd Font Mono, Hack Nerd Font Mono, Symbols Nerd Font, monospace"
}

type terminalSurface struct {
	key     string
	paneID  string
	label   string
	term    *terminal.Terminal
	area    herdr.LayoutRect
	rect    herdr.LayoutRect
	focused bool
	// dragSelect mirrors the LocalDragSelect option the surface's terminal
	// was created with; syncTerminals reattaches when the classification
	// flips (fresh Agent panes gain their scroll metrics after first
	// output). The view reads the option per pointer event, but the
	// Terminal's opts are fixed at New.
	dragSelect bool
	// cwd, agent and agentStatus feed the gorex pane card header; all are
	// presentation snapshots from the last projection. agent is Herdr's
	// own classification of the pane's Agent TUI (pi and friends): it
	// routes the wheel, not just the label.
	cwd         string
	agent       string
	agentStatus string
	// scrollOffset mirrors the Herdr viewport offset this window last asked
	// for (pane.scroll is absolute); scrollMax is the runtime's clamp.
	// reconcileSurfaceScroll keeps both honest from projections, and
	// routeAttachMouse steps the offset per reported wheel event.
	scrollOffset uint64
	scrollMax    uint64
	// conn is the app-owned pty Conn of this attachment (nil on the plugin's
	// own spawn); takenOver is set on the window lane once the attach
	// process exited because a phone controller replaced it (Host takeover,
	// 2026-11-07): the card then offers "Take Back" instead of a dead
	// "[Disconnected]" view.
	conn      *attachConn
	takenOver bool
	// heldElsewhere: the card's attach was refused because another client (the phone's takeover) already held the
	// terminal; the banner says so instead of claiming a takeover it did not see.
	heldElsewhere bool
}

type paneGeometry struct {
	pane    herdr.Pane
	area    herdr.LayoutRect
	rect    herdr.LayoutRect
	focused bool
}

// terminalCanvas lays the visible Panes out over the workspace canvas as
// gorex cards. The geometry is computed in DIPs from the canvas bounds —
// percent frames cannot carry the gutter reliably, and the daemon's pane
// rects are contiguous cells. 顶行卡片贴齐 Header 下沿；其余边沿保留一格 gorexGap。
// The card itself must NOT carry a Margin: a filled child
// overflows its box by the margin (mygo fills the parent's content box
// and shifts, it does not shrink), which painted every card through the
// right/bottom gutter and glued adjacent cards together (2026-10-07
// spacing report, pixel-measured).
func (s *Shell) terminalCanvas(c *ui.Context) {
	k := gorexColorsOf(c.Theme().Dark)
	canvas := ui.Box(c).Grow(1).MinWidth(0).MinHeight(0)
	if ground, ok := paneGround(c.Theme().Dark); ok {
		canvas.Background(ground)
	}
	bounds := canvas.Bounds()
	s.terminalCanvasBounds = bounds
	surfaces := s.visibleSurfaces()
	// Absolute children position relative to the canvas, while Bounds()
	// reports window coordinates: only the size carries over.
	inner := gorexInsetRect(ui.Rect{W: bounds.W, H: bounds.H}, gorexCardsPad)
	// 顶行卡片贴齐 Header 下沿；只移除画布上沿的半格 gutter。
	inner.Y = 0
	inner.H += gorexCardsPad
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	canvas.Children(func() {
		for _, surface := range surfaces {
			surface := surface
			left, top, width, height := surfacePercent(surface.area, surface.rect)
			topInset := float32(gorexFrameInset)
			if top == 0 {
				topInset = 0
			}
			w := inner.W*width/100 - 2*gorexFrameInset
			h := inner.H*height/100 - topInset - gorexFrameInset
			if w <= 0 || h <= 0 {
				continue
			}
			frame := ui.Box(c.Key(surface.key)).
				Transition(ui.ElementTransition{Position: true, Duration: motionHandOver}).
				Absolute().
				Left(inner.X + inner.W*left/100 + gorexFrameInset).
				Top(inner.Y + inner.H*top/100 + topInset).
				Width(w).
				Height(h)
			frame.Children(func() {
				s.paneCard(c, k, surface)
			})
		}
		if layout, ok := layoutForTab(s.projection, s.selectedTabID); ok && !layout.Zoomed && len(surfaces) > 1 && len(layout.Splits) > 0 {
			s.renderSplitDividers(c, inner, layout)
		}
	})
}

// gorexInsetRect shrinks r by d on every side (DIPs).
func gorexInsetRect(r ui.Rect, d float32) ui.Rect {
	return ui.Rect{X: r.X + d, Y: r.Y + d, W: r.W - 2*d, H: r.H - 2*d}
}

// renderSplitDividers draws the draggable handles between split panes.
// Herdr stays the layout authority: a drag never moves anything locally.
// Each gesture resolves into pane.resize RPCs that move exactly the grabbed
// divider, and the returned projection repaints the cards. The daemon
// semantics were probed against the live session (2026-10-12): pane.resize
// moves the target pane's nearest boundary in `direction` by `amount`, a
// ratio delta of the boundary's controlling split, clamped to [0.1, 0.9].
func (s *Shell) renderSplitDividers(c *ui.Context, inner ui.Rect, layout herdr.Layout) {
	area := layout.Area
	if area.Width == 0 || area.Height == 0 || len(layout.Splits) == 0 {
		return
	}
	const grip = float32(10)
	type splitDivider struct {
		split         herdr.LayoutSplit
		x, y, w, h    float32
		vertical      bool
		dx, dy        float32
		held, clicked bool
	}
	dividers := make([]splitDivider, 0, len(layout.Splits))
	for _, split := range layout.Splits {
		if split.Rect.Width == 0 || split.Rect.Height == 0 {
			continue
		}
		var d splitDivider
		switch split.Direction {
		case "right":
			ratioX := (float32(split.Rect.X) + float32(split.Rect.Width)*float32(split.Ratio) - float32(area.X)) / float32(area.Width)
			topPct := float32(split.Rect.Y-area.Y) / float32(area.Height)
			hPct := float32(split.Rect.Height) / float32(area.Height)
			topInset := float32(gorexFrameInset)
			if topPct == 0 {
				topInset = 0
			}
			d = splitDivider{split: split, vertical: true,
				x: inner.X + inner.W*ratioX - grip/2,
				y: inner.Y + inner.H*topPct + topInset,
				w: grip,
				h: inner.H*hPct - topInset - gorexFrameInset,
			}
		case "down":
			ratioY := (float32(split.Rect.Y) + float32(split.Rect.Height)*float32(split.Ratio) - float32(area.Y)) / float32(area.Height)
			leftPct := float32(split.Rect.X-area.X) / float32(area.Width)
			wPct := float32(split.Rect.Width) / float32(area.Width)
			d = splitDivider{split: split,
				x: inner.X + inner.W*leftPct + gorexFrameInset,
				y: inner.Y + inner.H*ratioY - grip/2,
				w: inner.W*wPct - 2*gorexFrameInset,
				h: grip,
			}
		default:
			continue
		}
		if d.w <= 0 || d.h <= 0 {
			continue
		}
		dividers = append(dividers, d)
	}
	if len(dividers) == 0 {
		return
	}

	// Build every handle and read its gesture state in one pass, then
	// process the drags afterwards: a sibling handle that is not under the
	// pointer must not zero an active drag's accumulator (two vertical
	// splits are enough to break per-divider resets).
	anyVerticalHeld, anyHorizontalHeld := false, false
	for i := range dividers {
		d := &dividers[i]
		handle := ui.Box(c.Key("split-div-" + d.split.ID)).
			Absolute().
			Left(d.x).
			Top(d.y).
			Width(d.w).
			Height(d.h).
			Label("Resize pane split")
		if d.vertical {
			handle = handle.Cursor(ui.CursorResizeEW)
		} else {
			handle = handle.Cursor(ui.CursorResizeNS)
		}
		d.dx, d.dy, d.held = handle.Dragged()
		d.clicked = handle.DoubleClicked()
		if d.held {
			if d.vertical {
				anyVerticalHeld = true
			} else {
				anyHorizontalHeld = true
			}
		}
		hovered := handle.Hovered()
		handle.Children(func() {
			line := ui.Box(c).Absolute()
			if d.vertical {
				line = line.Left((grip - 2) / 2).Width(2).
					Top(0).Bottom(0)
			} else {
				line = line.Top((grip - 2) / 2).Height(2).
					Left(0).Right(0)
			}
			line.Radius(1).PassThrough()
			if hovered || d.held {
				line.Background(c.Theme().Accent).Shadow(0, 0, 6, 0, c.Theme().Accent.Alpha(0.6))
			}
		})
	}

	// Double click brings the grabbed split back to an even 50/50.
	for i := range dividers {
		if dividers[i].clicked {
			s.resetSplitRatio(layout, dividers[i].split)
		}
	}

	// Fold this frame's drag deltas into the per-axis ratio accumulators.
	// Deltas keep accumulating while a previous pane.resize round trip is
	// still in flight (mutate drops concurrent mutations), so a fast drag
	// coalesces and catches up instead of losing the gesture.
	var dragged []*splitDivider
	for i := range dividers {
		d := &dividers[i]
		if !d.held {
			continue
		}
		amount := splitResizeAmount(d.split, area, inner.W, inner.H, d.dx, d.dy)
		if d.vertical {
			s.accumulatedResizeX = clampAbs(s.accumulatedResizeX+float32(amount), resizeDispatchCap)
		} else {
			s.accumulatedResizeY = clampAbs(s.accumulatedResizeY+float32(amount), resizeDispatchCap)
		}
		dragged = append(dragged, d)
	}
	if !anyVerticalHeld {
		s.accumulatedResizeX = 0
	}
	if !anyHorizontalHeld {
		s.accumulatedResizeY = 0
	}
	for _, d := range dragged {
		accumulated := s.accumulatedResizeY
		if d.vertical {
			accumulated = s.accumulatedResizeX
		}
		if math.Abs(float64(accumulated)) < float64(resizeGestureFloor) {
			continue
		}
		paneID, direction := splitGrowTarget(layout.Panes, d.split, accumulated)
		if paneID == "" {
			continue
		}
		if s.resizePane(paneID, direction, math.Abs(float64(accumulated))) {
			if d.vertical {
				s.accumulatedResizeX = 0
			} else {
				s.accumulatedResizeY = 0
			}
		}
	}
}

// splitResizeAmount converts one frame of canvas-pixel drag deltas into the
// pane.resize ratio delta for the grabbed split. Herdr's amount is a ratio
// of the split's own extent, so nested splits scale by their own rect, not
// the whole canvas: dragging the divider of a half-width sub-split moves
// twice as many ratio points per pixel as the root split's divider.
func splitResizeAmount(split herdr.LayoutSplit, area herdr.LayoutRect, innerW, innerH, dx, dy float32) float64 {
	switch split.Direction {
	case "right":
		if innerW <= 0 || split.Rect.Width == 0 {
			return 0
		}
		return float64(dx) * float64(area.Width) / (float64(innerW) * float64(split.Rect.Width))
	case "down":
		if innerH <= 0 || split.Rect.Height == 0 {
			return 0
		}
		return float64(dy) * float64(area.Height) / (float64(innerH) * float64(split.Rect.Height))
	}
	return 0
}

// splitGrowTarget resolves which pane and direction a divider gesture must
// target so Herdr moves exactly this divider. polarity > 0 means the
// divider moved right/down (the earlier side grows); < 0 means left/up.
// Growing the earlier side targets the pane whose far edge is the divider
// (resize right/down); growing the later side targets the pane whose near
// edge is the divider (resize left/up). Targeting the wrong side would
// move an outer boundary instead (probed 2026-10-12).
func splitGrowTarget(panes []herdr.LayoutPane, split herdr.LayoutSplit, polarity float32) (string, string) {
	horizontal := split.Direction == "right"
	switch split.Direction {
	case "right":
		cell := int(math.Round(float64(split.Rect.X) + float64(split.Rect.Width)*split.Ratio))
		if polarity >= 0 {
			return paneEndingAt(panes, split.Rect, cell, horizontal), "right"
		}
		return paneStartingAt(panes, split.Rect, cell, horizontal), "left"
	case "down":
		cell := int(math.Round(float64(split.Rect.Y) + float64(split.Rect.Height)*split.Ratio))
		if polarity >= 0 {
			return paneEndingAt(panes, split.Rect, cell, horizontal), "down"
		}
		return paneStartingAt(panes, split.Rect, cell, horizontal), "up"
	}
	return "", ""
}

// paneEndingAt returns the pane whose right (horizontal) or bottom edge
// sits at cell inside span, with a 1-cell tolerance for integer rounding;
// this is the pane whose boundary Herdr moves when a divider's earlier
// side grows. Prefer the widest match when rounding produces ties.
func paneEndingAt(panes []herdr.LayoutPane, span herdr.LayoutRect, cell int, horizontal bool) string {
	return paneAtEdge(panes, span, cell, horizontal, true)
}

// paneStartingAt returns the pane whose left (horizontal) or top edge sits
// at cell inside span: the pane Herdr moves when a divider's later side
// grows.
func paneStartingAt(panes []herdr.LayoutPane, span herdr.LayoutRect, cell int, horizontal bool) string {
	return paneAtEdge(panes, span, cell, horizontal, false)
}

func paneAtEdge(panes []herdr.LayoutPane, span herdr.LayoutRect, cell int, horizontal, ending bool) string {
	const cellSlack = 1
	bestID := ""
	bestExtent := -1
	for _, p := range panes {
		r := p.Rect
		if horizontal {
			if r.Y+r.Height <= span.Y || r.Y >= span.Y+span.Height {
				continue
			}
		} else if r.X+r.Width <= span.X || r.X >= span.X+span.Width {
			continue
		}
		var edge, extent int
		if horizontal {
			edge, extent = int(r.X), int(r.Width)
		} else {
			edge, extent = int(r.Y), int(r.Height)
		}
		if ending {
			edge += extent
		}
		if absInt(edge-cell) > cellSlack {
			continue
		}
		if extent > bestExtent {
			bestID, bestExtent = p.PaneID, extent
		}
	}
	return bestID
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func clampAbs(v, limit float32) float32 {
	if v > limit {
		return limit
	}
	if v < -limit {
		return -limit
	}
	return v
}

// resetSplitRatio brings the grabbed split back to an even 50/50 with one
// pane.resize: grow whichever side is smaller by the exact missing ratio.
func (s *Shell) resetSplitRatio(layout herdr.Layout, split herdr.LayoutSplit) {
	delta := 0.5 - split.Ratio
	if math.Abs(delta) < 0.001 {
		return
	}
	polarity := float32(1)
	if delta < 0 {
		polarity = -1
	}
	paneID, direction := splitGrowTarget(layout.Panes, split, polarity)
	if paneID == "" {
		return
	}
	s.resizePane(paneID, direction, math.Abs(delta))
}

// resizePane submits one pane.resize against Herdr's authoritative layout.
// It reports whether the mutation was dispatched: while another mutation is
// in flight the drag keeps accumulating and retries on a later frame.
func (s *Shell) resizePane(paneID, direction string, amount float64) bool {
	if s.loading || s.activeInstance == "" || paneID == "" {
		return false
	}
	instance := s.activeInstance
	s.mutate(func() (herdr.Projection, error) {
		return s.runtime.ResizePane(instance, paneID, direction, amount)
	})
	return true
}

// paneCard draws one attached Herdr Pane as a card: a rounded, opaque
// surface with a hairline border — accent-tinted when the Pane is the
// selected one — the pane header over its terminal. The gutter lives in the
// frame (see terminalCanvas); the card fills its frame exactly.
func (s *Shell) paneCard(c *ui.Context, k *gorexColors, surface *terminalSurface) {
	focused := surface.focused
	card := ui.Column(c).Fill().Radius(gorexCardR).Clip().MinWidth(0).MinHeight(0)
	// 纸色、边和阴影都由 paneCardFaceOf 提供。选中卡片只增加中性细边，阴影与同组一致。
	// The
	// Transparent-terminal preference swaps in a deliberately see-through
	// paper — the user's opt-in look.
	paneCardFaceOf(c.Theme().Dark, focused, s.settings.Terminal.Transparent).dress(card)
	var dropAction paneCardDropAction
	var dropLabel string
	if dragged, over := ui.DragOver[treeNodeDrag](card); over && s.canDropOnPaneCard(dragged, surface.paneID, s.selectedTabID) {
		// Native drag ghost follows the pointer; preview action (swap vs split)
		// without resizing the live PTY.
		px, py, _ := card.PointerPosition()
		bounds := card.Bounds()
		dropAction, dropLabel = resolvePaneCardDropAction(dragged, surface.paneID, s.selectedTabID, px, py, bounds.W, bounds.H)
		card.Border(2, c.Theme().Accent).Background(c.Theme().Accent.Alpha(0.08))
	}
	if dragged, dropped := ui.Drop[treeNodeDrag](card); dropped && s.canDropOnPaneCard(dragged, surface.paneID, s.selectedTabID) {
		px, py, _ := card.PointerPosition()
		bounds := card.Bounds()
		action, _ := resolvePaneCardDropAction(dragged, surface.paneID, s.selectedTabID, px, py, bounds.W, bounds.H)
		s.handlePaneCardDrop(dragged, surface.paneID, s.selectedTabID, action)
	}
	card.Transition(ui.ElementTransition{Colors: true, Duration: motionPanel})
	hovered := card.Hovered()
	card.Children(func() {
		s.paneCardHeader(c, k, surface, focused, hovered)
		body := ui.Box(c).Grow(1).MinHeight(0).Padding(0, 5, 6, 5)
		body.Children(func() {
			// Real-terminal semantics (F144, routing in attach_mouse.go):
			// the view owns every pointer event — no overlay child. It
			// reports clicks, wheel and drags back through the conn;
			// Shift+drag selects locally (⌘C copies). Right click is a
			// program event; the pane menu lives on the card header, the
			// sidebar rows and the title-bar Actions menu.
			view := terminal.View(c, surface.term).Fill()
			if surface.focused {
				view.AutoFocus()
			}
			if view.Focused() {
				if surface.paneID != s.selectedPaneID {
					s.selectPane(surface.paneID)
				}
				s.markAgentReviewedForPane(surface.paneID)
			}
			s.takenOverBanner(c, surface)
		})
		if dropAction != paneCardDropNone {
			ui.Row(c.Key("card-drop-target")).Absolute().Right(12).Top(gorexHeaderH+12).
				Padding(6, 10).Radius(7).Background(c.Theme().Surface).
				Border(1, c.Theme().Accent.Alpha(0.5)).
				PassThrough().Transition(ui.ElementTransition{
				Enter: &ui.Motion{Y: 6}, Exit: &ui.Motion{Y: -4}, Duration: motionRow,
			}).Children(func() {
				ui.Text(c, dropLabel).FontSize(Typography().BodySmall).FontWeight(WeightSemibold).
					TextColor(c.Theme().Accent).PassThrough()
			})
		}
	})
}

// paneCardHeader is a gorex pane header: the terminal glyph, the Pane's
// label and working directory, its Agent's status badge, and — focused or
// hovered — the pane action buttons. The buttons call the same shared
// actions as the pane menu (splitPaneAction and friends), never a second
// implementation.
// paneHeaderVisuals resolves the visual mark, primary title, and subtitle/detail
// for one attached terminal surface. If the pane runs an Agent, it prioritizes
// the Agent's brand icon and the shared agent title (agentCardTitleForPane:
// session title first, generated names never); if it runs a recognizable dev
// tool/server, it uses the program's brand and friendly name; otherwise it
// defaults to the terminal glyph, pane label, and shortened directory.
func (s *Shell) paneHeaderVisuals(surface *terminalSurface, dark bool) (visualMark, string, string) {
	if a := s.paneAgent(surface.paneID); a != nil {
		mark, _ := s.agentMarkForPane(surface.paneID, dark)
		title, titled := s.agentCardTitleForPane(surface.paneID)
		if !titled {
			title = a.Name
			if title == "" || agent.IsGeneratedAgentName(title, a.Kind) {
				provider := agentProvider(a)
				if provider != "" {
					title = provider.DisplayName()
				} else {
					title = "Agent"
				}
			}
		}
		detail := shortDir(surface.cwd)
		return mark, title, detail
	}

	if act, ok := s.serviceIndex.Panes[surface.paneID]; ok && act.Proc != "" && !isShell(act.Proc) {
		if mark, ok := s.procMarkForPaneWithTheme(surface.paneID, dark); ok {
			title := procFriendlyName(act.Proc)
			detail := shortDir(surface.cwd)
			return mark, title, detail
		}
	}

	title := surface.label
	if title == "" {
		title = "Terminal"
	}
	detail := shortDir(surface.cwd)
	// Shell labels are already "host:cwd" — repeating the same path in the
	// dim slot only doubles the truncation (F168).
	if detail != "" && titleSpellsOutDir(title, surface.cwd) {
		detail = ""
	}
	return visualMark{svg: iconTerminal}, title, detail
}

// titleSpellsOutDir reports whether a pane label already carries its working
// directory: in full, from ~, or by the tail shortDir keeps. Matching only
// shortDir's own output (F168) missed every deep path — its leading "…"
// never appears in the label that spells the path out.
func titleSpellsOutDir(title, cwd string) bool {
	if cwd == "" {
		return false
	}
	for _, form := range []string{cwd, tildePath(cwd), shortDir(cwd), strings.TrimPrefix(shortDir(cwd), "…")} {
		if form != "" && strings.Contains(title, form) {
			return true
		}
	}
	return false
}

// paneStackInfo reports whether surface's pane belongs to a multi-pane split
// tab currently maximized (zoomed), along with total pane count in that tab.
func (s *Shell) paneStackInfo(paneID string) (bool, int) {
	pane := paneByID(s.projection, paneID)
	if pane == nil {
		return false, 1
	}
	layout, ok := layoutForTab(s.projection, pane.TabID)
	if !ok || len(layout.Panes) <= 1 {
		return false, 1
	}
	return layout.Zoomed, len(layout.Panes)
}

// paneLogoView renders the pane card's visual identity mark. When a split
// tab is maximized (zoomed, stacking the other panes beneath it), the mark
// carries a count badge with the number of Panes in the Tab (2026-10-13
// badge round: no overlapping stacked art — the number is the indicator),
// plus an informative tooltip, click-to-select and double-click unzoom.
func (s *Shell) paneLogoView(c *ui.Context, k *gorexColors, surface *terminalSurface, mark visualMark, isStacked bool, stackCount int) {
	if !isStacked {
		ui.Box(c).Size(15, 15).Shrink(0).Children(func() {
			markView(c, mark, 14.5, k.text)
		})
		return
	}

	tooltip := fmt.Sprintf("%d panes in this tab • Double-click title to restore", stackCount)
	box := tipped(c, ui.Box(c).Size(15, 15).Shrink(0).Cursor(ui.CursorPointer), tooltip)
	if box.DoubleClicked() {
		s.selectPane(surface.paneID)
		s.zoomPaneAction(surface.paneID)
	} else if box.Clicked() {
		s.selectPane(surface.paneID)
	}
	box.Children(func() {
		markView(c, mark, 14.5, k.text)
		// The count badge floats at the mark's top-right corner (titlebar
		// activity badge idiom): theme status color, inverted numeral.
		countBadge(c, stackCount, ToneInfo).Left(6).Top(-2)
	})
}

// paneCardCaption is what a card's header says about its Pane:
// paneHeaderVisuals, less the directory when the card stands alone. The title
// bar's second row already shows the selected Pane's directory (2026-10-14
// duplicate audit), so a lone card — which is that Pane — repeats it only
// when siblings need telling apart from each other.
func (s *Shell) paneCardCaption(surface *terminalSurface, dark bool) (visualMark, string, string) {
	mark, title, detail := s.paneHeaderVisuals(surface, dark)
	if len(s.terminals) == 1 {
		detail = ""
	}
	return mark, title, detail
}

func (s *Shell) paneCardHeader(c *ui.Context, k *gorexColors, surface *terminalSurface, focused, hovered bool) {
	dark := c.Theme().Dark
	mark, title, detail := s.paneCardCaption(surface, dark)
	isStacked, stackCount := s.paneStackInfo(surface.paneID)
	h := ui.Row(c).Height(gorexHeaderH).Padding(0, gorexGap, 0, 12).Gap(7).AlignItems(ui.Center).MinWidth(0)
	// The pane menu lives here since the F144 real-terminal round: the
	// terminal surface's right click is a program event now, so the card
	// header is the pointer-owned pane-menu affordance (with the sidebar
	// rows and the title-bar Actions menu; one paneOverflowItems builder).
	h.ContextMenu(func(m *ui.Menu) {
		s.terminalPaneMenu(c, m, surface)
	})
	if h.DoubleClicked() {
		s.selectPane(surface.paneID)
		s.zoomPaneAction(surface.paneID)
	} else if h.Clicked() {
		s.selectPane(surface.paneID)
	}
	h.Children(func() {
		s.paneLogoView(c, k, surface, mark, isStacked, stackCount)
		caption := ui.Row(c).Grow(1).MinWidth(0).Gap(5).AlignItems(ui.Center).ClipX()
		if caption.DoubleClicked() {
			s.selectPane(surface.paneID)
			s.zoomPaneAction(surface.paneID)
		} else if caption.Clicked() {
			s.selectPane(surface.paneID)
		}
		if s.canDragPaneCard(surface.paneID) {
			tipped(c, caption.Drag(treeNodeDrag{
				Instance: s.activeInstance,
				Kind:     "pane",
				ParentID: s.selectedTabID,
				ID:       surface.paneID,
			}).Cursor(ui.CursorGrab), "Drag to move or swap pane")
			if caption.Dragging() {
				caption.Opacity(0.4).Cursor(ui.CursorGrabbing)
			}
		}
		caption.Children(func() {
			ui.Text(c, title).FontSize(Typography().Body).FontWeight(WeightSemibold).TextColor(k.text).
				SingleLine().Ellipsis("…").Shrink(0).MaxWidthPercent(80)
			// The Agent's state follows the title (2026-11-08), the same
			// indicator the sidebar rows use.
			if s.paneHeaderShowsStatus(surface) {
				statusIndicator(c, normalizeRuntimeStatus(surface.agentStatus))
			}
			if detail != "" {
				ui.Text(c, detail).FontSize(Typography().BodySmall).TextColor(k.textMuted).
					SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			}
			// The pane menu trigger sits against the title (2026-10-06 F148,
			// user request): the terminal surface's right click is a program
			// event now, so the pane menu needs a visible, always-there home
			// on the pane itself — same paneOverflowItems builder as the
			// sidebar rows and the title-bar Actions menu, never a second menu. A
			// chevron after the title, as a native title pop-up reads.
			// Always rendered (not hover-gated): a menu anchor that vanishes
			// when the pointer enters the dropdown would be a trap.
			more := gorexIconButton(c, k, iconChevronDown, "Pane menu", "Pane menu", false, gorexPaneBtn, 12)
			more.Menu(func(m *ui.Menu) {
				s.terminalPaneMenu(c, m, surface)
			})
		})
		show := focused || hovered
		ui.Row(c).Gap(1).AlignItems(ui.Center).Shrink(0).
			Opacity(map[bool]float32{true: 1, false: 0}[show]).Children(func() {
			if !show {
				return
			}
			// The project command control leads the row: the pane's own
			// Run/Stop for project-defined commands, before the pane
			// chrome actions.
			s.paneRunCommandControl(c, k, surface)
			if surface.paneID == s.selectedPaneID {
				// A detail set aside (the card's back button keeps it) is one
				// click away on the selected pane's card, as the Chat switch is.
				s.detailReturnButton(c, k, gorexPaneBtn, gorexPaneGlyph)
			}
			if s.paneAgent(surface.paneID) != nil {
				// N19 #7: the single Terminal⇄Chat switch lives here, in the
				// card header, not in the window header.
				s.surfaceToggle(c, k, WorkspaceSurfaceTerminal)
			}
			if gorexIconButton(c, k, iconSplit, "Split Right", "Split Right", false, gorexPaneBtn, gorexPaneGlyph).Clicked() {
				s.selectPane(surface.paneID)
				s.splitPaneAction(surface.paneID, "right")
			}
			if gorexIconButton(c, k, iconSplitDown, "Split Down", "Split Down", false, gorexPaneBtn, gorexPaneGlyph).Clicked() {
				s.selectPane(surface.paneID)
				s.splitPaneAction(surface.paneID, "down")
			}
			if gorexIconButton(c, k, iconZoom, "Zoom / Unzoom", "Zoom / Unzoom", false, gorexPaneBtn, gorexPaneGlyph).Clicked() {
				s.selectPane(surface.paneID)
				s.zoomPaneAction(surface.paneID)
			}
			if gorexIconButton(c, k, iconClose, "Close Pane", "Close Pane", false, gorexPaneBtn, gorexPaneGlyph).Clicked() {
				s.closePaneAction(surface.paneID)
			}
		})
	})
}

// paneHeaderShowsStatus is U12 (2026-11-08, T3 review round 2): a Pane's
// header carries its Agent's state only when something else would not
// already say it. A lone card in the default layout is the whole Tab, and the
// Tab bar's chip (the rolled-up state of the Tab) is that same state; split
// cards need telling apart, and with Tabs listed in the sidebar there is no
// chip above the Terminal, so the card keeps its own. A Pane without an Agent
// has no state at all.
func (s *Shell) paneHeaderShowsStatus(surface *terminalSurface) bool {
	if surface.agentStatus == "" {
		return false
	}
	return len(s.terminals) > 1 || !s.settings.General.TabsInTitleBar()
}

func (s *Shell) syncTerminals(projection herdr.Projection) error {
	started := time.Now()
	spawned, closed := 0, 0
	desired := visiblePaneGeometry(projection, s.selectedTabID, s.selectedPaneID)
	for key, surface := range s.terminals {
		if _, ok := desired[key]; !ok {
			slog.Debug("detach native terminal", "instance", s.activeInstance, "pane_id", surface.paneID, "terminal_id", key)
			// Visibility detach is not a Herdr Pane deletion and must not
			// erase OSC records (done/error survive a shell prompt too).
			_ = surface.term.Close()
			delete(s.terminals, key)
			closed++
		}
	}
	for key, geometry := range desired {
		if surface := s.terminals[key]; surface != nil {
			if want := localDragSelectFor(geometry); want != surface.dragSelect {
				// The classification flipped (fresh Agent pane gained its
				// scroll metrics, or its scrollback emptied): the option is
				// fixed at New, so reattach once rather than behaving
				// wrongly until the next natural reattach.
				_ = surface.term.Close()
				delete(s.terminals, key)
			} else {
				surface.area = geometry.area
				surface.rect = geometry.rect
				surface.focused = geometry.focused
				surface.label = geometry.pane.Label
				surface.cwd = geometry.pane.CWD
				surface.agent = geometry.pane.Agent
				surface.agentStatus = geometry.pane.AgentStatus
				s.reconcileSurfaceScroll(surface, geometry.pane)
				continue
			}
		}
		if s.win == nil {
			// Headless (tests): there is no surface to host an attachment,
			// and spawning a real `herdr terminal attach` child from a unit
			// test would leak processes into the developer's runtime.
			continue
		}
		command, err := s.runtime.TerminalAttachCommand(s.activeInstance, geometry.pane.TerminalID)
		if s.takeBack[key] { // the user's explicit "Take Back": the only attach that takes over
			delete(s.takeBack, key)
			command, err = s.runtime.TerminalTakeoverCommand(s.activeInstance, geometry.pane.TerminalID)
		}
		if err != nil {
			return err
		}
		slog.Info("attach native terminal", "instance", s.activeInstance, "pane_id", geometry.pane.ID, "terminal_id", geometry.pane.TerminalID)
		options := terminalOptionsFromSettings(s.settings.Terminal)
		options.LocalDragSelect = localDragSelectFor(geometry)
		// The gorex palettes (2026-10-06), or the pinned Ghostty theme.
		applyGorexTerminalTheme(&options, s.settings.General.Appearance, s.settings.Terminal.ThemeName)
		// Prefer the app-owned pty Conn: it carries the real-terminal mouse
		// semantics (F144) — Write routes reported SGR pointer events into
		// routeAttachMouse. Without a local pty, fall back to the plugin's
		// own spawn (no router: the daemon drops reported events, the view
		// still Shift+drag-selects locally).
		var conn *attachConn
		if c, err := newAttachConn(command, s.activeInstance, geometry.pane.ID, geometry.pane.TerminalID); err == nil {
			conn = c
			options.Conn = conn
			options.Command = nil
		} else {
			slog.Warn("native attach pty unavailable; unfiltered attach", "error", err)
			options.Command = command
		}
		surface := &terminalSurface{
			key:         key,
			paneID:      geometry.pane.ID,
			label:       geometry.pane.Label,
			area:        geometry.area,
			rect:        geometry.rect,
			focused:     geometry.focused,
			cwd:         geometry.pane.CWD,
			agent:       geometry.pane.Agent,
			agentStatus: geometry.pane.AgentStatus,
			dragSelect:  options.LocalDragSelect,
			conn:        conn,
		}
		// The terminal invokes callbacks from its reader goroutine. Marshal
		// every mutation onto the window lane; reject late callbacks from
		// a detached/replaced attachment (or another Herdr instance).
		instance := s.activeInstance
		options.OnProgramStatus = func(body []byte) {
			s.applyOnWindow(func() {
				if s.activeInstance == instance && s.terminals[key] == surface {
					s.applyProgramStatus(surface.paneID, body)
				}
			})
		}
		options.OnProgramStatusControl = func(control terminal.ProgramStatusControl) {
			s.applyOnWindow(func() {
				if s.activeInstance == instance && s.terminals[key] == surface {
					s.applyProgramStatusControl(surface.paneID, control)
				}
			})
		}
		options.OnExit = func(int) {
			if conn == nil {
				return
			}
			// The farewell line and the exit race: give the reader a moment to deliver it.
			for i := 0; i < 20 && !conn.takenOver.Load(); i++ {
				time.Sleep(25 * time.Millisecond)
			}
			if !conn.takenOver.Load() {
				return
			}
			s.applyOnWindow(func() {
				if s.activeInstance == instance && s.terminals[key] == surface {
					surface.takenOver = true
					surface.heldElsewhere = conn.heldElsewhere.Load()
				}
			})
		}
		if conn != nil {
			conn.onMouse = func(seq string) {
				s.applyOnWindow(func() { s.routeAttachMouse(surface, seq) })
			}
		}
		term, err := terminal.New(options)
		if err != nil {
			return fmt.Errorf("open Pane %s terminal: %w", geometry.pane.ID, err)
		}
		surface.term = term
		if ps := geometry.pane.Scroll; ps != nil {
			surface.scrollOffset = ps.OffsetFromBottom
			surface.scrollMax = ps.MaxOffsetFromBottom
		}
		s.terminals[key] = surface
		spawned++
	}
	s.prunePaneScrolls(desired)
	s.pruneAgentMouse(desired)
	// Attachment churn is the switching-lag suspect; one bounded line per
	// churn sync (timings per attach come from attachFrameTiming).
	if spawned > 0 || closed > 0 {
		slog.Info("native terminals synced", "instance", s.activeInstance, "spawned", spawned, "closed", closed, "duration_ms", time.Since(started).Milliseconds())
	}
	return nil
}

func visiblePaneGeometry(projection herdr.Projection, selectedTabID, selectedPaneID string) map[string]paneGeometry {
	result := make(map[string]paneGeometry)
	paneByID := make(map[string]herdr.Pane, len(projection.Panes))
	for _, pane := range projection.Panes {
		paneByID[pane.ID] = pane
	}
	layout, ok := layoutForTab(projection, selectedTabID)
	if ok && len(layout.Panes) > 0 && layout.Area.Width > 0 && layout.Area.Height > 0 {
		if layout.Zoomed {
			if pane, exists := paneByID[layout.FocusedPaneID]; exists && pane.TerminalID != "" {
				key := pane.TerminalID
				result[key] = paneGeometry{pane: pane, area: layout.Area, rect: layout.Area, focused: true}
			}
			return result
		}
		for _, lp := range layout.Panes {
			pane, exists := paneByID[lp.PaneID]
			if !exists || pane.TerminalID == "" {
				continue
			}
			key := pane.TerminalID
			result[key] = paneGeometry{
				pane: pane, area: layout.Area, rect: lp.Rect,
				focused: pane.ID == selectedPaneID,
			}
		}
		return result
	}
	if pane, exists := paneByID[selectedPaneID]; exists && pane.TerminalID != "" && pane.TabID == selectedTabID {
		unit := herdr.LayoutRect{Width: 1, Height: 1}
		result[pane.TerminalID] = paneGeometry{pane: pane, area: unit, rect: unit, focused: true}
	}
	return result
}

func (s *Shell) visibleSurfaces() []*terminalSurface {
	visible := make([]*terminalSurface, 0, len(s.terminals))
	for _, surface := range s.terminals {
		visible = append(visible, surface)
	}
	return visible
}

func surfacePercent(area, rect herdr.LayoutRect) (left, top, width, height float32) {
	if area.Width == 0 || area.Height == 0 {
		return 0, 0, 100, 100
	}
	left = float32(int(rect.X)-int(area.X)) / float32(area.Width) * 100
	top = float32(int(rect.Y)-int(area.Y)) / float32(area.Height) * 100
	width = float32(rect.Width) / float32(area.Width) * 100
	height = float32(rect.Height) / float32(area.Height) * 100
	return
}

func (s *Shell) closeAllTerminals() {
	if len(s.terminals) > 0 {
		slog.Debug("close native terminal attachments", "instance", s.activeInstance, "count", len(s.terminals))
	}
	for key, surface := range s.terminals {
		_ = surface.term.Close()
		delete(s.terminals, key)
	}
}
