package nativeui

/**
 * [INPUT]: 依赖 MyGo Native UI 的按钮、拖放目标、位置/尺寸过渡与统一主题 token
 * [OUTPUT]: 提供包含原生拖动占位反馈的 treeRow 与通用导航组件
 * [POS]: nativeui 的基础视觉组件层；拖动规则由 reorder.go 注入而非组件自行决定
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// rowFade eases a row's background between rest, hover and selection, so
// the tint arrives and leaves instead of popping; every row built on
// rowTint carries it (colors only: no position or size motion).
var rowFade = ui.ElementTransition{Colors: true, Duration: motionColor}

// splitFileExt cuts a file name before its extension (".json", ".tsx") so a
// long name can give up the middle of its stem and keep the extension; names
// without one (and dotfiles) come back whole.
func splitFileExt(name string) (stem, ext string) {
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 || len(name)-dot > 8 {
		return name, ""
	}
	return name[:dot], name[dot:]
}

// fileNameLabel is a file row's name: when the row is too narrow the stem
// ends in an ellipsis and the extension stays, so session-index.json and
// session-index_test.json no longer both read as "session-in…".
func fileNameLabel(c *ui.Context, name, tooltip string) ui.Element {
	size := Typography().Body
	stem, ext := splitFileExt(name)
	if ext == "" {
		return tipped(c, ui.Text(c, name).FontSize(size).SingleLine().Grow(1).Shrink(1).MinWidth(0), tooltip)
	}
	row := ui.Row(c).Grow(1).Shrink(1).MinWidth(0).Label(name) // one name for screen readers and lookups
	row.Children(func() {
		ui.Text(c, stem).FontSize(size).SingleLine().Shrink(1).MinWidth(0)
		ui.Text(c, ext).FontSize(size).SingleLine().Shrink(0)
	})
	return tipped(c, row, tooltip)
}

// treeRowMotion is rowFade plus the folder motion: a row appearing under an
// already-built list grows in, and one that goes fades out.
var treeRowMotion = ui.ElementTransition{
	Colors:   true,
	Duration: motionRow,
	Enter:    &ui.Motion{Collapse: true},
	Exit:     &ui.Motion{},
}

// rowEnter is rowFade plus a fade-in for a row that joins a list already on
// screen (a search or filter that brings rows in). The list must not be
// virtualized: rows built by scrolling would fade too.
var rowEnter = ui.ElementTransition{Colors: true, Duration: motionRow, Enter: &ui.Motion{}}

// disclosureMotion is for a panel that opens in place under its row (the
// Sessions info card): it grows in and fades out.
var disclosureMotion = ui.ElementTransition{
	Duration: motionPanel,
	Enter:    &ui.Motion{Collapse: true},
	Exit:     &ui.Motion{},
}

// rowTint is the shared background policy for selectable rows: selection
// outranks hover, and an untinted row reports ok=false so callers keep the
// surface transparent.
func rowTint(tokens DesignTokens, selected, hovered bool) (ui.Color, bool) {
	switch {
	case selected:
		return tokens.SidebarSelect, true
	case hovered:
		return tokens.SidebarHover, true
	default:
		return ui.Color{}, false
	}
}

func navButton(c *ui.Context, glyph *ui.SVG, label, shortcut string, selected bool) ui.Element {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	b := ui.ButtonBase(c).
		FillWidth().
		Height(Controls().RowHeight).
		PaddingX(8).
		Gap(7).
		Radius(Radius().Control).
		Label(label)
	if tint, ok := rowTint(tokens, selected, b.Hovered()); ok {
		b.Background(tint)
	}
	b.Transition(rowFade)
	b.Children(func() {
		if glyph != nil {
			ui.Icon(c, glyph).Size(14, 14).TextColor(t.TextMuted)
		}
		ui.Text(c, label).FontSize(Typography().Body).Grow(1).SingleLine()
		if shortcut != "" {
			ui.Text(c, shortcut).FontSize(Typography().Micro).TextColor(t.TextMuted).SingleLine()
		}
	})
	return b
}

// navTileButton is a destination row led by a colored icon tile: the
// sidebar list of a multi-page surface such as Settings.
func navTileButton(c *ui.Context, glyph *ui.SVG, tone TileTone, label string, selected bool) ui.Element {
	return navTileButtonDetail(c, glyph, tone, label, "", selected)
}

// navTileButtonDetail is navTileButton with a quiet detail after the label
// (a search hit's page name).
func navTileButtonDetail(c *ui.Context, glyph *ui.SVG, tone TileTone, label, detail string, selected bool) ui.Element {
	tokens := designTokens(c.Theme().Dark)
	b := ui.ButtonBase(c).
		FillWidth().
		Height(Controls().RowHeight).
		PaddingX(8).
		Gap(8).
		Radius(6).
		Label(label)
	if tint, ok := rowTint(tokens, selected, b.Hovered()); ok {
		b.Background(tint)
	}
	b.Transition(rowFade)
	b.Children(func() {
		iconTile(c, glyph, tone, 20)
		ui.Text(c, label).FontSize(Typography().Body).Grow(1).SingleLine().PassThrough()
		if detail != "" {
			ui.Text(c, detail).FontSize(Typography().Micro).TextColor(c.Theme().TextMuted).SingleLine().Shrink(0).PassThrough()
		}
	})
	return b
}

func sectionLabel(c *ui.Context, label string) {
	ui.Text(c, label).
		Padding(7, 8, 4).
		FontSize(Typography().BodySmall).
		FontWeight(WeightMedium).
		TextColor(c.Theme().TextMuted)
}

// treeRowAction is a row's hover icon button. Tooltip defaults to Label.
// A Menu action turns the icon into a menu button instead of a click: the
// menu is attached to an element that is built every frame (the slot), so a
// chosen item always delivers — a hover-conditional element would be gone
// by the frame after the choice and the choice would be lost.
type treeRowAction struct {
	Label   string
	Tooltip string
	Glyph   *ui.SVG
	Run     func()
	// Menu, when set, is the icon's dropdown; Run stays unused.
	Menu func(*ui.Menu)
}

func (a treeRowAction) tooltip() string {
	if a.Tooltip != "" {
		return a.Tooltip
	}
	return a.Label
}

type treeRowSpec struct {
	Key               string
	Depth             int
	AncestorContinues []bool
	Last              bool
	Selected          bool
	Expandable        bool
	Expanded          bool
	Mark              visualMark
	Label             string
	Status            string
	// Dot renders the bound Agent's state in the row's trailing status slot
	// (agent Panes); opUnknown leaves the slot to the other facts.
	Dot operationalState
	// Activity rolls subtree state into the same slot on ancestor rows: a
	// service (green) or agents running below this row (2026-10-06 sidebar
	// service rollup), with a tooltip naming what runs.
	Activity sidebarActivity
	// StatusSlot reserves the trailing slot even when the row has nothing
	// to show, so the Workspace tree's rows keep one aligned column
	// (statusIndicator is the one drawing function; Git/Files trees leave
	// it off).
	StatusSlot bool
	// Tile, when set, replaces the Mark with a letter tile (the Workspace
	// row, 2026-10-30), Branch/Dirty add the repository facts after the
	// label in fixed columns (branch, then the pane tally): the branch muted,
	// amber when files changed — never a second dot.
	Tile   string
	Branch string
	Dirty  bool
	// Action is the row's one primary action, an icon button at the
	// trailing edge under the pointer (a project's "+"). Everything else
	// lives in the row's right-click menu — no row of dots.
	Action *treeRowAction
	// Count is the number of panes beneath a nesting row (2026-10-07 user
	// request): more than one shows as tiny muted digits at the trailing
	// edge, yielding to the hover controls. A project row passes it whether
	// the row is folded or expanded (2026-10-15 user request); a tab row
	// passes it only while collapsed — an expanded tab shows its children.
	// 0 and 1 render nothing.
	Count int
	// Drag enables a reorder affordance only for the runtime tree.
	Drag *treeRowDrag
	// Height overrides the row height; 0 uses the density default.
	Height float32
}

// designRowHeight resolves the row height token.
func designRowHeight(override float32) float32 {
	if override > 0 {
		return override
	}
	return 28
}

// sidebarRowHeight is the density-token row height of the sidebar's rows
// (GWB-063): compact 26 / default 30 / comfortable 34 DIP (2026-10-29
// Cursor-3 reskin lifted the default tier). The Git and Files trees take
// treeRowHeight, two tighter.
func (s *Shell) sidebarRowHeight() float32 {
	switch s.sidebarDensity() {
	case DensityCompact:
		return 26
	case DensityComfortable:
		return 34
	default:
		return 30
	}
}

// sidebarTopAction is one compact icon control in the sidebar top actions
// row (GWB-060): icon-only with accessible label + tooltip.
func sidebarTopAction(c *ui.Context, glyph *ui.SVG, label, shortcut string, selected bool) ui.Element {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	b := tipped(c, ui.ButtonBase(c).
		Size(28, 28).
		Radius(6).
		Label(label), strings.TrimSpace(label+" "+shortcut))
	if tint, ok := rowTint(tokens, selected, b.Hovered()); ok {
		b.Background(tint)
	}
	b.Transition(rowFade)
	b.Children(func() {
		ui.Icon(c, glyph).Size(14, 14).TextColor(t.TextMuted).PassThrough()
	})
	return b
}

// dockPanel is the Cursor-3-style floating prompt container (2026-10-29
// reskin): one raised surface with a hairline border that brightens while
// focus sits inside. Callers compose the field and its controls; the panel
// owns only the face, so the chat composer and the workspace home share it.
func dockPanel(c *ui.Context, build func()) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	sp := Spacing()
	panel := ui.Column(c).FillWidth().Radius(gorexCardR).
		Background(t.Surface).Border(1, tokens.BorderSubtle).
		Padding(sp.M, sp.L).Gap(sp.S).
		Transition(ui.ElementTransition{Colors: true, Duration: motionRow})
	if panel.FocusWithin() {
		panel.Border(1, tokens.BorderSubtle.Mix(t.Text, 0.35))
	}
	panel.Children(build)
}

// ghostPill is the quiet pill action (Cursor-3 quick actions): hairline
// border, transparent fill, muted label; hover fills the neutral selection
// tint and press one step stronger. run, when nil, renders an inert pill.
func ghostPill(c *ui.Context, key, label string, run func()) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	b := ui.ButtonBase(c.Key(key)).Height(28).Radius(14).Padding(0, 12).
		AlignItems(ui.Center).Label(label).Cursor(ui.CursorPointer).
		Border(1, tokens.BorderSubtle)
	switch {
	case b.Pressed():
		b.Background(tokens.SidebarSelectStrong)
	case b.Hovered():
		b.Background(tokens.SidebarSelect)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(Typography().Caption).TextColor(t.TextMuted).Shrink(0)
	})
	if run != nil && b.Clicked() {
		run()
	}
}

// indicator resolves the ONE state a row shows in its trailing status slot
// (2026-10-30: moved from the icon corner, the Agent's spinner and the dot
// share the slot): an Agent Pane's own state first (a hollow ring means
// idle, as in the Agents section), then the rolled-up activity (a service or
// agents running beneath an ancestor row), then the raw runtime Status.
func (spec treeRowSpec) indicator() (StatusTone, string, bool) {
	if tone, ok := indicatorToneFor(spec.Dot); ok {
		return tone, operationalLabel(spec.Dot), true
	}
	if spec.Activity.On {
		return spec.Activity.Tone, spec.Activity.Detail, true
	}
	if state := normalizeRuntimeStatus(spec.Status); state != opIdle && state != opUnknown {
		return operationalTone(state), operationalLabel(state), true
	}
	return ToneNeutral, "", false
}

// serviceSlot reports that the resolved state is a running service, not an
// Agent's: the slot then draws a rounded square (statusIndicatorService).
func (spec treeRowSpec) serviceSlot() bool {
	if _, ok := indicatorToneFor(spec.Dot); ok {
		return false
	}
	return spec.Activity.On && spec.Activity.Service
}

// treeRow draws a compact Finder/Xcode-style hierarchy row with connector
// guides. Children are pointer-pass-through so clicks/context menus belong to
// the whole row rather than only its empty padding.
func treeRow(c *ui.Context, spec treeRowSpec) ui.Element {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	var row ui.Element
	height := designRowHeight(spec.Height)
	container := ui.Box(c.Key(spec.Key)).FillWidth().Transition(ui.ElementTransition{Position: true, Size: true, Duration: motionPanel})
	container.Children(func() {
		// Reserve an animated, pointer-transparent insertion slot. It never
		// steals the drop from the row underneath the cursor.
		before := ui.Box(c).Height(0).PassThrough().Transition(ui.ElementTransition{Size: true, Duration: motionRow})
		row = ui.ButtonBase(c).
			FillWidth().
			Height(height).
			Radius(6).
			Padding(0, treeRowPad). // the same right edge as the Git/Files rows and section counts
			Label(spec.Label)
		if tint, ok := rowTint(tokens, spec.Selected, row.Hovered()); ok {
			row.Background(tint)
		}
		row.Transition(rowFade)
		if drag := spec.Drag; drag != nil {
			row.Drag(drag.Value).Cursor(ui.CursorGrab)
			if row.Dragging() {
				row.Opacity(0.38).Cursor(ui.CursorGrabbing)
			}
			value, over := ui.DragOver[treeNodeDrag](container)
			_, y, _ := container.PointerPosition()
			if over && drag.Accept(value) {
				row.Background(t.Accent.Alpha(0.13))
				if value.Kind == drag.Value.Kind && y < height/2 {
					before.Height(7).Background(t.Accent.Alpha(0.65))
				}
			}
			if value, dropped := ui.Drop[treeNodeDrag](container); dropped && drag.Accept(value) {
				drag.Drop(value, y < height/2)
			}
		}
		row.Draw(func(p *ui.Painter, r ui.Rect) {
			if spec.Depth <= 0 {
				return
			}
			const step float32 = 14
			const origin float32 = 9
			mid := r.Y + r.H/2
			for level, continues := range spec.AncestorContinues {
				if !continues {
					continue
				}
				x := r.X + origin + float32(level)*step
				p.Line(x, r.Y, x, r.Y+r.H, 1, tokens.TreeLine)
			}
			x := r.X + origin + float32(spec.Depth-1)*step
			bottom := r.Y + r.H
			if spec.Last {
				bottom = mid
			}
			p.Line(x, r.Y, x, bottom, 1, tokens.TreeLine)
			p.Line(x, mid, x+8, mid, 1, tokens.TreeLine)
		})
		row.Children(func() {
			// Indent only — the trailing chevron replaced the leading one,
			// so rows sit flush with the section edge (2026-10-05 round 2).
			ui.Box(c).Width(float32(spec.Depth) * 14).Shrink(0).PassThrough()
			if spec.Tile != "" {
				letterTile(c, spec.Tile, tileToneFor(spec.Label), 16)
				ui.Box(c).Width(6).Shrink(0).PassThrough()
			} else if spec.Mark.svg != nil || spec.Mark.bmp != nil {
				ui.Box(c).Size(16, 16).Shrink(0).PassThrough().Children(func() {
					markView(c, spec.Mark, 14, t.TextMuted)
				})
				// Breathe between the glyph and its label (2026-10-05).
				ui.Box(c).Width(5).Shrink(0).PassThrough()
			}
			ui.Text(c, spec.Label).FontSize(Typography().Body).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").PassThrough()
			hovered := row.Hovered()
			controls := func() {
				if spec.Expandable && hovered {
					glyph := iconChevron
					if spec.Expanded {
						glyph = iconChevronDown
					}
					ui.Icon(c, glyph).Size(11, 11).TextColor(t.TextMuted).Shrink(0).PassThrough()
				}
				if spec.Action != nil && hovered {
					action := gorexIconButton(c, gorexColorsOf(t.Dark), spec.Action.Glyph, spec.Action.Label, spec.Action.tooltip(), false, gorexRowBtn, gorexRowGlyph)
					if action.Clicked() {
						spec.Action.Run()
					}
				}
			}
			// The collapsed pane tally (2026-10-07): tiny muted digits at the
			// trailing edge; under the pointer it steps aside for the
			// chevron and the row's action.
			tally := func() {
				if spec.Count > 1 && !hovered {
					ui.Text(c, itoa(spec.Count)).Label(itoa(spec.Count) + " panes").
						FontSize(Typography().Micro).TextColor(t.TextMuted).
						Shrink(0).PassThrough()
				}
			}
			if spec.Tile != "" {
				// A Workspace row's meta is two fixed columns — branch, then the
				// pane tally — so every row's branch and count line up whatever
				// it holds (2026-10-30; they used to float against a variable
				// label and each other). The branch wears amber when the working
				// tree has uncommitted changes, so a row has no second dot: the
				// one dot at the edge is only ever the state slot. Under the
				// pointer the same box holds the chevron and the action, so
				// nothing shifts.
				ui.Row(c).Width(treeBranchColW + treeMetaGap + treeCountColW).Shrink(0).Gap(treeMetaGap).AlignItems(ui.Center).PassThrough().Children(func() {
					ui.Box(c).Grow(1).MinWidth(0).PassThrough()
					if hovered {
						controls()
						return
					}
					if spec.Branch != "" {
						ink, tip := t.TextMuted, spec.Branch
						if spec.Dirty {
							ink, tip = t.Warning, spec.Branch+" — uncommitted changes"
						}
						tipped(c, ui.Text(c, spec.Branch).FontSize(Typography().Micro).TextColor(ink).
							SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).MaxWidth(treeBranchColW).PassThrough(), tip)
					}
					ui.Row(c).Width(treeCountColW).Shrink(0).PassThrough().Children(func() {
						ui.Box(c).Grow(1).MinWidth(0).PassThrough()
						tally()
					})
				})
			} else {
				tally()
				controls()
			}
			// The state slot ends the row: spinner, dot or ring in one place.
			if tone, detail, ok := spec.indicator(); ok {
				if spec.serviceSlot() {
					statusIndicatorService(c, tone, detail)
				} else {
					statusIndicatorTone(c, tone, detail)
				}
			} else if spec.StatusSlot {
				statusSlotEmpty(c)
			}
		})
		if drag := spec.Drag; drag != nil {
			value, over := ui.DragOver[treeNodeDrag](container)
			_, y, _ := container.PointerPosition()
			afterHeight := float32(0)
			if over && drag.Accept(value) && y >= height/2 {
				afterHeight = 7
			}
			ui.Box(c).Height(afterHeight).PassThrough().
				Background(t.Accent.Alpha(0.65)).
				Transition(ui.ElementTransition{Size: true, Duration: motionRow})
		}
	})
	return row
}
