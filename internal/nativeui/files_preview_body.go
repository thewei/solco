package nativeui

/**
 * [INPUT]: rightPanel 预览分级状态（selectedFilePath/previewPlanned）、nativePreviewBody（files_native_preview.go）
 * [OUTPUT]: filesPreviewBody（右侧面板 Files 页为所选文件挑好的预览体：探测中 = 转圈，其余交给 nativePreviewBody——渲染后的 Markdown、原生 editor、原生图片、AVIF/ICO 的小页、真实失败态）
 * [POS]: 原 /files 整页（page_files.go）于 2026-11-08 G5 退役后留下的唯一一块：预览体由面板 Files 页与 Detail 卡单点消费
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"github.com/egoist/mygo/ui"
)

// filesPreviewBody is the view the plan chose for the open file.
func (s *Shell) filesPreviewBody(c *ui.Context, selected string) {
	if s.rightPanel.previewPlanned != selected {
		// The size probe is still landing.
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Spinner(c).Size(16, 16)
		})
		return
	}
	s.nativePreviewBody(c)
}
