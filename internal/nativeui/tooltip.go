package nativeui

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: 依赖 mygo/ui 的 TooltipBase（出现时机与翻转/夹紧由它负责）/Painter/Path 与主题 Inverse 色, design_system 的 Typography/Spacing/Radius/Controls 令牌
 * [OUTPUT]: 对外提供 tippedCard（多事实的悬停卡：主题 Surface+边框+Card 圆角+柔和阴影，深浅色同款，Tab chip 用）、tipped（全应用唯一的单行 tooltip 入口：主题 Surface 卡片面——1px Border、圆角 Radius.Row、内边距 6×12、柔和阴影、无箭头，深浅色跟随主题）与 tipKeys/tipIcon/tipMaxWidth 选项；tipGap/tipKeyHeight 常量
 * [POS]: nativeui tooltip 外观的唯一所有者；业务代码不直接调用 Element.Tooltip 或 ui.TooltipBase（TestTooltipsHaveOneFace 守卫），快捷键提示的键帽来自 shortcuts.go 的有效绑定
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// The tooltip face is the theme's own card (2026-11-02, replacing the inverted
// nova tip of 2026-10-29 at the user's request: it ignored light/dark and did
// not read as native): the Surface fill, a 1px Border, the row radius, a soft
// shadow, theme text and Kbd chips washed from the text colour, no arrow.
// MyGo keeps the timing (0.6 s rest, 0.4 s warm window), the placement above
// the anchor or below it where there is no room, and the pass-through
// pointer; only the look is ours.
const (
	tipDefaultWidth = 320 // max-w-xs
	tipGap          = 6   // between the anchor and the card
	tipKeyHeight    = 20  // Kbd h-5, and its min-w-5
)

type tipSpec struct {
	keys     []string
	icon     *ui.SVG
	maxWidth float32
}

// tipOption adds to the plain text tooltip.
type tipOption func(*tipSpec)

// tipKeys names the shortcut of the anchor's action as keycap chips after
// the text; shortcuts.go spells them from the binding that really fires.
func tipKeys(keys []string) tipOption { return func(s *tipSpec) { s.keys = keys } }

// tipIcon puts a glyph before the text.
func tipIcon(svg *ui.SVG) tipOption { return func(s *tipSpec) { s.icon = svg } }

// tipMaxWidth lets the text run wider than max-w-xs before it wraps — for
// what is long by nature, a directory.
func tipMaxWidth(w float32) tipOption { return func(s *tipSpec) { s.maxWidth = w } }

// tipColors is the tooltip's fill and ink: the theme's own surface and text,
// so the card follows light and dark.
func tipColors(t *ui.Theme) (fill, ink ui.Color) { return t.Surface, t.Text }

// tipShadow is the soft drop under every card, stronger on a dark page.
func tipShadow(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(0, 0, 0, 0.5)
	}
	return ui.RGBA(0, 0, 0, 0.16)
}

// tipped gives anchor the app's tooltip and returns it: text in the nova
// face, with the options' shortcut chips, glyph and width. An empty text
// shows nothing. Like Element.Tooltip it also describes the anchor to
// assistive technology, the shortcut spelled out.
func tipped(c *ui.Context, anchor ui.Element, text string, opts ...tipOption) ui.Element {
	if text == "" {
		return anchor
	}
	spec := tipSpec{maxWidth: tipDefaultWidth}
	for _, opt := range opts {
		opt(&spec)
	}
	description := text
	if len(spec.keys) > 0 {
		description += " (" + strings.Join(spec.keys, "") + ")"
	}
	anchor.Description(description)

	t := c.Theme()
	sp := Spacing()
	fill, ink := tipColors(t)
	ui.TooltipBase(c, anchor, func(tip ui.Element) {
		// A Kbd shortens the right padding (has-kbd:pr-1.5).
		right := sp.L
		if len(spec.keys) > 0 {
			right = sp.S
		}
		tip.MaxWidth(spec.maxWidth).Padding(sp.S, right, sp.S, sp.L).Radius(Radius().Row).
			Background(fill).Border(1, t.Border).Shadow(0, 4, 14, 0, tipShadow(t)).
			TextColor(ink).FontSize(Typography().Body)
		tip.AttachTo(anchor, ui.AnchorTop, ui.AnchorBottom).Margin(tipGap, 0, tipGap, 0)
		tip.Children(func() {
			if spec.icon == nil && len(spec.keys) == 0 {
				ui.Text(c, text)
				return
			}
			ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
				if spec.icon != nil {
					glyph := Controls().IconSmall
					ui.Icon(c, spec.icon).Size(glyph, glyph).TextColor(ink).Shrink(0)
				}
				ui.Text(c, text).Shrink(1).MinWidth(0)
				tipKeycaps(c, spec.keys, t.Dark, ink)
			})
		})
	})
	return anchor
}

// tipCardSpec is what a hover card says: a title, a muted line, a state with
// its dot, and the shortcut's keycaps.
type tipCardSpec struct {
	Title, Meta, State string
	Tone               StatusTone
	HasTone            bool
	Keys               []string
}

// tippedCard gives anchor a hover card: MyGo's tooltip timing and placement
// (below the anchor, above where there is no room), but a card in the
// theme's own surface — border, Card radius, a soft shadow, theme text — so
// it reads the same on light and dark, where the nova tooltip is inverted.
// For anchors with several facts to say (a Tab chip); one-line hints stay
// `tipped`.
func tippedCard(c *ui.Context, anchor ui.Element, spec tipCardSpec) ui.Element {
	if spec.Title == "" {
		return anchor
	}
	description := spec.Title
	for _, part := range []string{spec.Meta, spec.State} {
		if part != "" {
			description += ", " + part
		}
	}
	anchor.Description(description)

	t := c.Theme()
	sp := Spacing()
	fill, ink := tipColors(t)
	ui.TooltipBase(c, anchor, func(tip ui.Element) {
		tip.MinWidth(180).MaxWidth(300).Padding(sp.M, sp.L).Radius(Radius().Card).
			Background(fill).Border(1, t.Border).Shadow(0, 6, 20, 0, tipShadow(t)).TextColor(ink)
		tip.AttachTo(anchor, ui.AnchorBottom, ui.AnchorTop).Margin(tipGap, 0, tipGap, 0)
		tip.Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, spec.Title).FontSize(Typography().Body).FontWeight(WeightSemibold).TextColor(t.Text)
				if spec.Meta != "" {
					ui.Text(c, spec.Meta).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
				}
				if spec.State != "" {
					ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
						if spec.HasTone {
							tipCardDot(c, spec.Tone)
						}
						ui.Text(c, spec.State).FontSize(Typography().BodySmall).TextColor(t.Text).Shrink(1).MinWidth(0)
					})
				}
				if len(spec.Keys) > 0 {
					ui.Box(c).Height(sp.XS).PassThrough()
					tipKeycaps(c, spec.Keys, t.Dark, t.Text)
				}
			})
		})
	})
	return anchor
}

// tipCardDot is a state's marker inside a hover card: the shared status
// colors, a small spinner for work in progress, no tooltip of its own.
func tipCardDot(c *ui.Context, tone StatusTone) {
	t := c.Theme()
	color := designTokens(t.Dark).StatusColor(tone, t.Dark)
	if indicatorShapeFor(tone) == shapeSpinner {
		ui.Spinner(c).Size(12, 12).TextColor(color).Shrink(0)
		return
	}
	ui.Box(c).Size(7, 7).Radius(3.5).Background(color).Shrink(0)
}

// tipKeycaps is shadcn's Kbd group: one chip per key, 20 high and at least
// 20 wide, on a wash of the text colour — a fifth of it on a light card, a
// tenth on a dark one.
func tipKeycaps(c *ui.Context, keys []string, dark bool, ink ui.Color) {
	if len(keys) == 0 {
		return
	}
	sp := Spacing()
	wash := ink.Alpha(0.1)
	if !dark {
		wash = ink.Alpha(0.07)
	}
	ui.Row(c).Gap(sp.XS).AlignItems(ui.Center).Shrink(0).Children(func() {
		for _, key := range keys {
			ui.Box(c).Height(tipKeyHeight).MinWidth(tipKeyHeight).Padding(0, sp.XS).Shrink(0).Center().
				Radius(Radius().Control).Background(wash).Children(func() {
				ui.Text(c, key).FontWeight(WeightMedium)
			})
		}
	})
}
