package nativeui

// [INPUT]: 依赖 shell.go 的 Shell（setWindowTitle/windowTitleShown/selectedTabID/settings）、dialogs.go 的 openConfirm 通道、router.go 的 currentRouteTitle
// [OUTPUT]: 对外提供 syncWindowTitle（每帧幂等的路由驱动原生窗口标题，含恢复到内页的启动）、CloseTabFromMenu（菜单栏 ⌘W 关闭当前 Tab）、KeepWindowOnTopFromMenu（Window 菜单的置顶勾选项，2026-10-31 自标题栏图钉迁入）
// [POS]: window_actions 的原生窗口行为补齐层：F103/F104 的落地文件，避免触碰并行会话持有的 shell.go/titlebar.go
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

import (
	"fmt"

	"github.com/egoist/mygo"
)

// syncWindowTitle mirrors the current route into the native window title.
// The in-titlebar route text (F17) fixed the in-app chrome only; Mission
// Control, ⌘Tab and window pickers kept reading the static launch title.
// The workspace route keeps the plain app name so the window does not
// advertise a project that changed underneath it. It runs every frame and
// only hands the window a title that changed: a launch that restores an inner
// page never sees a route change, and used to keep the launch title.
func (s *Shell) syncWindowTitle() {
	if s.setWindowTitle == nil {
		return
	}
	title := "Solco"
	if route := s.currentRouteTitle(); route != "" && route != "Workspace" && route != "Solco" {
		title = fmt.Sprintf("%s — %s", "Solco", route)
	}
	if title == s.windowTitleShown {
		return
	}
	s.windowTitleShown = title
	s.setWindowTitle(title)
}

// CloseTabFromMenu is the menu-bar ⌘W target (F103): the Window role menu
// used to bind ⌘W to closing the whole window while the app model is
// tab-centric. Reuse the same guarded confirm channel as the sidebar's
// Close Tab so the action family stays single-sourced.
func (s *Shell) CloseTabFromMenu() {
	if s.selectedTabID == "" || s.loading {
		return
	}
	s.openConfirm("close-tab", s.selectedTabID, "Close Tab?", "This closes the selected Herdr Tab and all Panes in it.")
}

// NewTabFromMenu opens a Tab in the selected Workspace (⌘T) with the kind
// the Tab bar's "+" would start (the one used last, Terminal by default);
// the Workspace row's "+" stays a Terminal Tab.
func (s *Shell) NewTabFromMenu() {
	s.applyOnWindow(func() {
		if project := s.selectedProject(); project != nil && !s.loading {
			s.startNewTab(*project, s.lastLaunchKind(s.launchKinds))
		}
	})
}

// SelectTabFromMenu selects the nth Tab of the selected Workspace in Herdr's
// order (⌥⌘1–8); lastTab (⌥⌘9) means the last one, as in a browser. A Tab
// that does not exist is a no-op.
func (s *Shell) SelectTabFromMenu(n int) {
	s.applyOnWindow(func() {
		project := s.selectedProject()
		if project == nil || s.loading {
			return
		}
		tabs := tabsForProject(s.projection, project.ID)
		if n == lastTab {
			n = len(tabs)
		}
		if n < 1 || n > len(tabs) {
			return
		}
		if s.holdCommitNavigation() {
			return
		}
		s.selectTab(tabs[n-1].ID)
	})
}

// ShowShortcutsFromMenu opens the Keyboard Shortcuts sheet (⌘/).
func (s *Shell) ShowShortcutsFromMenu() {
	s.applyOnWindow(func() { s.shortcutsOpen = true })
}

// KeepWindowOnTopFromMenu applies the Window menu's checkbox (2026-10-31,
// the titlebar pin's successor): MyGo flips the check before the Click runs,
// so the item's own state is the always-on-top level to set. The window
// comes from the click and is nil in headless sessions, where the platform
// call stays a guarded no-op.
func (s *Shell) KeepWindowOnTopFromMenu(it *mygo.MenuItem, w *mygo.Window) {
	if w != nil {
		w.SetAlwaysOnTop(it.IsChecked())
	}
}
