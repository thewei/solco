package nativeui

import "github.com/egoist/mygo/ui"

/**
 * [INPUT]: 依赖 github.com/egoist/mygo/ui 的 MustParseSVG；字形来自 icons_gen.go（design/icons.json 生成）
 * [OUTPUT]: 对外提供 icon() 绘制器与 iconStroke；全部 iconXxx 变量定义在 icons_gen.go
 * [POS]: nativeui 的图标入口，被 titlebar/sidebar/terminal/breadcrumb 等呈现组件消费；字形的唯一来源是 design/icons.json（Tabler outline），本文件不得再写内联路径
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// iconStroke is the one stroke weight of every outline icon (24-unit grid):
// a light, SF-Symbols-like line. Heavier strokes made small glyphs read as
// clip-art, so weight is tuned here and nowhere else. It equals the pointer
// tier of design/icons.json (a test pins them).
const iconStroke = "1.5"

func icon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="` + iconStroke + `" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}
