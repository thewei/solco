package nativeui

import "github.com/egoist/mygo/ui"

/**
 * [INPUT]: 依赖 mygo/ui 的 Button/PrimaryButton/TextInput/TextArea/SearchField/Combobox/Select/SegmentedBase，ds_components.go 的 segmentedTrack/segmentFace
 * [OUTPUT]: 对外提供控件脸的唯一入口：regularButton/regularPrimaryButton（与 ds_components.go 的 compactButton/compactPrimaryButton 同档同高）、textField/textArea/searchField/comboField/selectField、segmentedControl
 * [POS]: nativeui 控件外观的唯一所有者；业务代码不直接调用 ui.Button/PrimaryButton/TextInput/TextArea/SearchField/Combobox/Select/Segmented（TestControlsHaveOneFace 守卫），圆角来自主题 Radius（= Radius().Row，theme.go 设一次）；焦点环是 MyGo 自己的纤细环，这里既不画也不关（TestControlsDrawNoFocusRing）——2026-10-29 试过 shadcn nova 的 3px 贴边光晕，用户更喜欢原来的，已回退；ui.NumberInput 是三个盒子（输入框与 −/+ 钮）的 MyGo 复合件，保持 MyGo 原貌，只跟随主题圆角
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// The control faces are shadcn's nova (2026-10-29), without its focus ring:
// every control rounds to the theme radius, and every button variant carries
// the 1px border, so a primary is as tall as the outline button beside it.
// Focus is MyGo's own — a 2px ring one pixel off the edge of what the
// keyboard reaches, a recoloured border on a field — which the app keeps over
// nova's flush 3px halo, so nothing here draws a ring or turns MyGo's off.

// regularButton is the outline button of the regular tier.
func regularButton(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label)
}

// regularPrimaryButton is regularButton's accent-filled form. MyGo gives a
// primary no border, which makes it two points shorter than the button beside
// it; nova's carries one, transparent, and so does this.
func regularPrimaryButton(c *ui.Context, label string) ui.Element {
	return ui.PrimaryButton(c, label).Border(1, ui.Transparent)
}

// textField is the single-line text input.
func textField(c *ui.Context, value *string) ui.Element {
	return ui.TextInput(c, value)
}

// textArea is the multi-line text input.
func textArea(c *ui.Context, value *string) ui.Element {
	return ui.TextArea(c, value)
}

// searchField is the field for searching and filtering: a magnifier, the text
// and a clear button around a text input.
func searchField(c *ui.Context, query *string) ui.Element {
	return ui.SearchField(c, query)
}

// comboField is the field whose text filters the options below it.
func comboField(c *ui.Context, selected *string, options []string) ui.Element {
	return ui.Combobox(c, selected, options)
}

// selectField is the drop-down choosing one of options.
func selectField(c *ui.Context, selected *string, options []string) ui.Element {
	return ui.Select(c, selected, options)
}

// segmentedControl is the switch between a few views, labels only; icons and
// counts take segmentedTrack/segmentFace around ui.SegmentedBase directly.
// Both wear ui.Segmented's own look, which TestSegmentedControlMatchesTheOfficialFace
// pins pixel for pixel. A segment is named by its text, as ui.Segmented's are,
// so it takes no label of its own.
func segmentedControl(c *ui.Context, selected *int, labels ...string) ui.Element {
	tabs := ui.SegmentedBase(c, selected, len(labels))
	segmentedTrack(c, tabs, "")
	tabs.Track.Children(func() {
		for i, label := range labels {
			segmentFace(c, tabs, i, i == *selected, "").Children(func() {
				ui.Text(c, label).SingleLine()
			})
		}
	})
	return tabs.Track
}
