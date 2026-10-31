package nativeui

// [INPUT]: 依赖 shell.go 的菜单接缝（Navigate/ShowWorkspaceFromMenu/ShowChangesFromMenu/ShowFilesFromMenu/OpenCommandCenterFromMenu）、window_actions.go 的 CloseTabFromMenu 与 MyGo 菜单 API
// [OUTPUT]: 对外提供 MenuBar（macOS 菜单栏：应用菜单 + Go（⌘1–5）+ Tab（⌘T/⌘W/⌥⌘1–9）+ Edit + Window + Help（⌘/ 快捷键速查表））
// [POS]: 菜单栏的单一来源（原内联在 main.go，现可测试）：页面入口与侧栏视图标签、⌘P 命令面板同名同序（TestLaunchersAgreeOnViewDestinations）
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

import "github.com/egoist/mygo"

// MenuBar builds the macOS menu bar. checkForUpdates is the updater plugin's
// item — a plugin main.go owns — and sits after About, where macOS apps put it.
//
// The application menu is the stock one (About, Settings…, Hide, Quit); Go
// carries the app's own destinations in the order of the sidebar's view
// tabs, and accelerators only on idempotent destinations (Router.Push
// dedupes; the Terminal view keeps the Terminal), never on a toggle, so a
// menu accelerator and an in-app shortcut that fire together stay harmless.
func (s *Shell) MenuBar(checkForUpdates *mygo.MenuItem) *mygo.Menu {
	nav := func(label, accelerator, path string) *mygo.MenuItem {
		return &mygo.MenuItem{Label: label, Accelerator: accelerator, Click: func(*mygo.MenuItem, *mygo.Window) {
			s.Navigate(path)
		}}
	}
	do := func(label, accelerator string, fn func()) *mygo.MenuItem {
		return &mygo.MenuItem{Label: label, Accelerator: accelerator, Click: func(*mygo.MenuItem, *mygo.Window) { fn() }}
	}

	appMenu := []*mygo.MenuItem{{Role: mygo.RoleAbout}}
	if checkForUpdates != nil {
		appMenu = append(appMenu, checkForUpdates)
	}
	appMenu = append(appMenu,
		mygo.Separator(),
		nav("Settings…", accelSettings, routeSettings),
		mygo.Separator(),
		&mygo.MenuItem{Role: mygo.RoleHide},
		&mygo.MenuItem{Role: mygo.RoleHideOthers},
		&mygo.MenuItem{Role: mygo.RoleUnhide},
		mygo.Separator(),
		&mygo.MenuItem{Role: mygo.RoleQuit},
	)

	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Solco", Submenu: appMenu},
		{Label: "Go", Submenu: []*mygo.MenuItem{
			do("Workspace", accelWorkspace, s.ShowWorkspaceFromMenu),
			do("Changes", accelChanges, s.ShowChangesFromMenu),
			nav("Sessions", accelSessions, routeHistory),
			do("Files", accelFiles, s.ShowFilesFromMenu),
			nav("Hosts", accelHosts, routeHosts),
			mygo.Separator(),
			nav("Set Up Agents", "", "/settings/providers"),
			do("Command Center", accelCommandCenter, s.OpenCommandCenterFromMenu),
		}},
		// Tab (2026-10-30 UX audit): the Tab bar's actions with their keys.
		// Select Tab 1–8 and Last Tab take ⌥⌘1–9 — ⌘1–5 are the pages, and
		// ⌥⌘ digits are free in a terminal.
		{Label: "Tab", Submenu: s.tabMenu(do)},
		{Role: mygo.RoleEditMenu},
		// Custom Window menu (F103): Minimize/Zoom without the stock
		// "Close Window ⌘W" item so the accelerator stays tab-scoped.
		// Keep Window on Top (2026-10-31) is the titlebar pin's successor;
		// no accelerator, so it can never double-fire with the state it
		// toggles.
		{Label: "Window", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleMinimize},
			{Role: mygo.RoleZoom},
			mygo.Separator(),
			{Label: "Keep Window on Top", Type: mygo.MenuItemCheckbox, Click: s.KeepWindowOnTopFromMenu},
		}},
		{Role: mygo.RoleHelp, Submenu: []*mygo.MenuItem{
			do("Keyboard Shortcuts", accelShortcuts, s.ShowShortcutsFromMenu),
		}},
	})
}

// tabMenu lists the Tab actions. ⌘W belongs to the tab model, not the window
// (F103): the stock Window role bound it to closing the whole window in a
// tab-centric app. The guarded confirm keeps the action family single-sourced
// with the sidebar.
func (s *Shell) tabMenu(do func(label, accelerator string, fn func()) *mygo.MenuItem) []*mygo.MenuItem {
	items := []*mygo.MenuItem{
		do("New Tab", accelNewTab, s.NewTabFromMenu),
		do("Close Tab", accelCloseTab, s.CloseTabFromMenu),
		mygo.Separator(),
	}
	for n := 1; n < lastTab; n++ {
		n := n
		items = append(items, do("Select Tab "+itoa(n), selectTabAccelerator(n), func() { s.SelectTabFromMenu(n) }))
	}
	return append(items, do("Last Tab", selectTabAccelerator(lastTab), func() { s.SelectTabFromMenu(lastTab) }))
}
