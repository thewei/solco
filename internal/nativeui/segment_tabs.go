package nativeui

/**
 * [INPUT]: 共享分段控件（segmentedTrack/segmentFace）、gorex 面板图标尺寸
 * [OUTPUT]: segmentTab / segmentTabs——带图标的二选一分段开关（可选计数徽标），各段从自身内容宽度（segmentTabNeed）起按余量均分，计数不被挤成省略号
 * [POS]: 浅色下轨道为浅灰底、无可见描边、选中段为白色芯片（2026-11-08），深色沿用共享脸。右侧面板顶部的 Changes | Files 与 Files 搜索下的 Files | Content 共用；原先寄居在 Git 参考侧栏文件里（git_sidebar_tree.go，2026-11-08 G5 随 Git 整页退役，这一块是活的，搬出来）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"github.com/egoist/mygo/ui"
)

// segmentTab is one segment of segmentTabs: a glyph and a label.
type segmentTab struct {
	label string
	svg   *ui.SVG
}

// segmentTabs is a two-way switch — Changes | Files at the top of the right
// panel, Files | Content under the Files search: the shared segmented face
// with an icon on each segment. badge, when given, returns a count shown after a
// segment's label (0 shows none). It reports whether the choice changed.
func segmentTabs(c *ui.Context, name string, chosen *int, items []segmentTab, badge func(i int) int) bool {
	t := c.Theme()
	tabs := ui.SegmentedBase(c, chosen, len(items))
	segmentedTrack(c, tabs, name)
	// On the light panel the shared white track with its dark outline reads as
	// a bright frame (2026-11-08): the track is the soft grey ground instead,
	// outline invisible (kept, so the control's height is unchanged), and the
	// chosen segment the white chip. The dark window keeps the shared face.
	light := !t.Dark
	if light {
		tabs.Track.Background(designRoles(false).Grouped).Border(1, ui.RGBA(0, 0, 0, 0))
	}
	tabs.Track.FillWidth().Children(func() {
		for i, tab := range items {
			count := 0
			if badge != nil {
				count = badge(i)
			}
			seg := segmentFace(c, tabs, i, i == *chosen, tab.label).
				Basis(segmentTabNeed(c, tab.label, count)).Grow(1).Justify(ui.Center)
			if light && i == *chosen {
				seg.Background(t.Surface)
			}
			col := t.TextMuted
			if i == *chosen || seg.Hovered() {
				col = t.Text
			}
			seg.Children(func() {
				ui.Icon(c, tab.svg).Size(gorexPaneGlyph, gorexPaneGlyph).TextColor(col).Shrink(0)
				ui.Text(c, tab.label).SingleLine().TextColor(col)
				if count > 0 {
					ui.Text(c, itoa(count)).FontSize(Typography().BodySmall).TextColor(t.TextMuted).SingleLine()
				}
			})
		}
	})
	return tabs.Track.Changed()
}

// segmentTabNeed is the width one segment's own content asks for: its padding,
// glyph, label and count. The segments grow from it — the spare room is shared
// evenly, but a label with a count is never squeezed down to a bare label's
// share (growing from nothing, as flex: 1 does, clipped "Changes 330" to
// "Chan… 3…" in a 400-point panel).
func segmentTabNeed(c *ui.Context, label string, count int) float32 {
	gap := Spacing().S
	w, _ := c.MeasureText(0, ui.Span{Text: label})
	w += 2*c.Theme().Space(3) + gorexPaneGlyph + gap
	if count > 0 {
		cw, _ := c.MeasureText(0, ui.Span{Text: itoa(count), Size: Typography().BodySmall})
		w += gap + cw
	}
	// A pixel or two of slack: text measured to the dot would still clip.
	return w + 2
}
