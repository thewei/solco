package nativeui

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: 依赖 mygo/ui 的 TooltipBase（出现时机与翻转/夹紧由它负责）/Painter/Path 与主题 Inverse 色, design_system 的 Typography/Spacing/Radius/Controls 令牌
 * [OUTPUT]: 对外提供 tipped（全应用唯一的 tooltip 入口：shadcn nova 面——圆角 Radius.Row、内边距 6×12、无阴影、箭头指向锚点）与 tipKeys/tipIcon/tipMaxWidth 选项；tipArrowRise/tipKeyHeight 常量
 * [POS]: nativeui tooltip 外观的唯一所有者；业务代码不直接调用 Element.Tooltip 或 ui.TooltipBase（TestTooltipsHaveOneFace 守卫），快捷键提示的键帽来自 shortcuts.go 的有效绑定
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// The tooltip face is shadcn's nova (2026-10-29): bg-foreground with
// text-background, rounded-md, px-3 py-1.5, text-xs, no shadow, a 10-point
// arrow, Kbd chips for shortcuts. MyGo keeps the timing (0.6 s rest, 0.4 s
// warm window), the placement above the anchor or below it where there is no
// room, and the pass-through pointer; only the look is ours.
const (
	tipDefaultWidth = 320 // max-w-xs
	tipArrowBase    = 10  // the nub's width where it leaves the tip (size-2.5)
	tipArrowRise    = 5   // how far it stands out of the tip
	tipGap          = tipArrowRise + 3
	tipKeyHeight    = 20 // Kbd h-5, and its min-w-5
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

// tipColors is the tooltip's fill and ink: the theme turned over, as
// MyGo's own tooltip takes them.
func tipColors(t *ui.Theme) (fill, ink ui.Color) {
	fill, ink = t.Inverse, t.InverseText
	if fill == ui.Transparent {
		fill = t.Text
	}
	if ink == ui.Transparent {
		ink = t.Background
	}
	return fill, ink
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
		// The anchor's box of the last frame: the arrow aims at its middle
		// wherever the tip ends up, above or below, pushed along or not.
		aim := anchor.Bounds()
		tip.MaxWidth(spec.maxWidth).Padding(sp.S, right, sp.S, sp.L).Radius(Radius().Row).
			Background(fill).TextColor(ink).FontSize(Typography().Body)
		tip.AttachTo(anchor, ui.AnchorTop, ui.AnchorBottom).Margin(tipGap, 0, tipGap, 0)
		tip.Draw(func(p *ui.Painter, r ui.Rect) { tipArrow(p, r, aim, fill) })
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

// tipKeycaps is shadcn's Kbd group: one chip per key, 20 high and at least
// 20 wide, on a wash of the ink — a fifth of it on the dark tip of a light
// theme, a tenth on the light tip of a dark one.
func tipKeycaps(c *ui.Context, keys []string, dark bool, ink ui.Color) {
	if len(keys) == 0 {
		return
	}
	sp := Spacing()
	wash := ink.Alpha(0.2)
	if dark {
		wash = ink.Alpha(0.1)
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

// tipArrow paints the nub that points the tip at its anchor: out of the
// tip's foot when the tip sits above the anchor, out of its head when below,
// at the anchor's middle but no nearer a corner than the tip's own radius.
// It is painted beside the tip's box, in the margin MyGo keeps between the
// tip and the anchor, so it flips and slides with the tip.
func tipArrow(p *ui.Painter, tip, anchor ui.Rect, fill ui.Color) {
	half := float32(tipArrowBase) / 2
	room := Radius().Row + half
	if tip.W < 2*room {
		return
	}
	cx := min(max(anchor.X+anchor.W/2, tip.X+room), tip.X+tip.W-room)
	edge, out := tip.Y+tip.H, float32(1)
	if tip.Y+tip.H/2 > anchor.Y+anchor.H/2 {
		edge, out = tip.Y, -1
	}
	// A half point under the tip's own fill keeps a seam from showing, and
	// the apex is rounded off by a point and a half, as the arrow's 2-point
	// corner is.
	const lap, round = 0.5, 1.4
	var nub ui.Path
	nub.MoveTo(cx-half, edge-out*lap)
	nub.LineTo(cx-round, edge+out*(tipArrowRise-round))
	nub.QuadTo(cx, edge+out*tipArrowRise, cx+round, edge+out*(tipArrowRise-round))
	nub.LineTo(cx+half, edge-out*lap)
	nub.Close()
	p.FillPath(&nub, fill)
}
