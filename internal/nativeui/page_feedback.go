package nativeui

/**
 * [INPUT]: 依赖 feedback_ui.go 的 feedbackWebviewFor/feedbackAvailable、emptyState
 * [OUTPUT]: Shell.feedbackPage（/feedback 的路由体）
 * [POS]: Feedback 页是同一嵌入 SPA 的 /feedback 路由，经 `feedback` 桥读写；页面自己轮询桥，原生只负责路由与 WebView 的显隐；创建失败显示真实空态
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import "github.com/egoist/mygo/ui"

// feedbackPage is the body of /feedback.
func (s *Shell) feedbackPage(c *ui.Context) {
	if !s.feedbackAvailable() {
		emptyState(c, "Feedback isn't available", "Solco could not find its data folder.")
		return
	}
	view := s.feedbackWebviewFor()
	if view == nil {
		emptyState(c, "Feedback could not start", "Restart Solco to try again.")
		return
	}
	ui.WebView(c, view).Grow(1)
}
