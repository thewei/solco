package nativeui

/**
 * [INPUT]: 依赖主窗口 mygo.WebView、assistants_state 的门与 WebView 句柄、webviewBackground
 * [OUTPUT]: routeAssistants（/assistants）、Shell.assistantsWebviewFor（惰性创建 + 失败锁存）、Shell.assistantsPage（路由体）
 * [POS]: 产品决策 2026-11-03：Assistants 页是同一嵌入 SPA 的 /assistants 路由，经 `assistants` 桥读写；页面自己轮询桥（不依赖原生推送），原生只负责路由、门与 WebView 的显隐；创建失败显示真实空态
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"log/slog"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// assistantsWebviewFor returns the lazily created page WebView, or nil when no
// window exists or creation failed once (latched). Call on the UI lane.
func (s *Shell) assistantsWebviewFor() *mygo.WebView {
	st := &s.assistants
	if s.win == nil || st.broken {
		return nil
	}
	if st.webview == nil {
		view, err := s.win.NewWebView(mygo.WebViewOptions{URL: "/assistants", BackgroundColor: webviewBackground})
		if err != nil {
			st.broken = true
			slog.Warn("create assistants webview failed", "error", err)
			return nil
		}
		view.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
			e.PreventDefault()
			if pageNavigationDecision(e.URL, e.UserInitiated) {
				go func() { _ = mygo.Shell.OpenExternal(e.URL) }()
			}
		})
		st.webview = view
	}
	return st.webview
}

// assistantsPage is the body of /assistants.
func (s *Shell) assistantsPage(c *ui.Context) {
	if !s.assistantsAvailable() {
		emptyState(c, "Assistants aren't available", "Install or update the Solco Automations plugin to turn them on.")
		return
	}
	view := s.assistantsWebviewFor()
	if view == nil {
		emptyState(c, "Assistants could not start", "Restart Solco to try again.")
		return
	}
	ui.WebView(c, view).Grow(1)
}
