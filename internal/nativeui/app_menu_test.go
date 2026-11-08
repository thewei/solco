package nativeui

// [INPUT]: 依赖 app_menu.go 的 MenuBar、command_center_ui.go 的命令注册表、sidebar_test.go 的 sidebarViewTabLabels
// [OUTPUT]: 菜单栏结构（macOS 惯例）、三个启动器（侧栏标签 / ⌘P / Go 菜单）同名同目的地、"点名当前页保持不动"的回归
// [POS]: 菜单栏与命令面板的守卫；菜单结构不在 main.go，所以这里能测
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/commandcenter"
	"github.com/thewei/solco/internal/herdr"
)

// menuLabels lists a submenu's labels, separators as "-".
func menuLabels(items []*mygo.MenuItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.Type == mygo.MenuItemSeparator {
			out = append(out, "-")
			continue
		}
		out = append(out, it.Label)
	}
	return out
}

func menuNamed(t *testing.T, menu *mygo.Menu, label string) *mygo.MenuItem {
	t.Helper()
	for _, it := range menu.Items() {
		if it.Label == label {
			return it
		}
	}
	t.Fatalf("no %q menu in the bar", label)
	return nil
}

func menuItemNamed(items []*mygo.MenuItem, label string) *mygo.MenuItem {
	for _, it := range items {
		if it.Label == label {
			return it
		}
	}
	return nil
}

// TestMenuBarFollowsTheMacConventions pins the bar's shape: the application
// menu leads with Check for Updates…, carries Settings… (⌘,) before Hide,
// and closes with About Solco after Quit (2026-11-01, the user's
// call) — and every command lives in exactly one menu.
func TestMenuBarFollowsTheMacConventions(t *testing.T) {
	s := NewShell()
	menu := s.MenuBar(&mygo.MenuItem{Label: "Check for Updates…"})

	var bar []string
	for _, it := range menu.Items() {
		bar = append(bar, it.Label)
	}
	if want := []string{"Solco", "Go", "Tab", "Edit", "Window", "Help"}; !slices.Equal(bar, want) {
		t.Fatalf("menu bar = %q, want %q", bar, want)
	}

	appMenu := menuNamed(t, menu, "Solco").Submenu
	labels := menuLabels(appMenu)
	// About, Hide and Quit carry the app's name: match by prefix.
	wantApp := []string{"Check for Updates…", "-", "Settings…", "-", "Hide ", "Hide Others", "Show All", "-", "Quit ", "-", "About Solco"}
	if len(labels) != len(wantApp) {
		t.Fatalf("application menu = %q, want the shape %q", labels, wantApp)
	}
	for i, want := range wantApp {
		if !strings.HasPrefix(labels[i], want) {
			t.Fatalf("application menu item %d = %q, want %q… (menu %q)", i, labels[i], want, labels)
		}
	}
	if settings := menuItemNamed(appMenu, "Settings…"); settings.Accelerator != "CmdOrCtrl+," {
		t.Fatalf("Settings… accelerator = %q, want CmdOrCtrl+,", settings.Accelerator)
	}
	if hide := appMenu[4]; hide.Accelerator != "Cmd+H" {
		t.Fatalf("%q accelerator = %q — ⌘H must hide the app", hide.Label, hide.Accelerator)
	}

	// The Go menu's accelerators are muscle memory: the pages keep the keys
	// they had in the sidebar's tab order (⌘2 and ⌘4 went with the Git and
	// Files pages and are not reused), ⇧⌘P is the Command Center.
	goMenu := menuNamed(t, menu, "Go").Submenu
	for label, want := range map[string]string{
		"Workspace": "CmdOrCtrl+1", "Sessions": "CmdOrCtrl+3",
		"Hosts": "CmdOrCtrl+5", "Command Center": "CmdOrCtrl+Shift+P",
	} {
		if item := menuItemNamed(goMenu, label); item == nil || item.Accelerator != want {
			t.Errorf("Go ▸ %s accelerator = %+v, want %s", label, item, want)
		}
	}
	// The Tab menu: ⌘T new, ⌘W close (tab-scoped, never the window — F103),
	// ⌥⌘1–8 select by position, ⌥⌘9 the last.
	tabMenu := menuNamed(t, menu, "Tab").Submenu
	for label, want := range map[string]string{
		"New Tab": "CmdOrCtrl+T", "Close Tab": "CmdOrCtrl+W", "Select Tab 1": "CmdOrCtrl+Alt+1",
		"Select Tab 8": "CmdOrCtrl+Alt+8", "Last Tab": "CmdOrCtrl+Alt+9",
	} {
		if item := menuItemNamed(tabMenu, label); item == nil || item.Accelerator != want {
			t.Errorf("Tab ▸ %s accelerator = %+v, want %s", label, item, want)
		}
	}
	if help := menuItemNamed(menuNamed(t, menu, "Help").Submenu, "Keyboard Shortcuts"); help == nil || help.Accelerator != "CmdOrCtrl+/" {
		t.Errorf("Help ▸ Keyboard Shortcuts = %+v, want CmdOrCtrl+/", help)
	}

	seen := map[string]string{}
	for _, name := range []string{"Solco", "Go", "Tab"} {
		for _, it := range menuNamed(t, menu, name).Submenu {
			if it.Type == mygo.MenuItemSeparator {
				continue
			}
			label := strings.TrimSuffix(it.Label, "…")
			if other, dup := seen[label]; dup {
				t.Errorf("%q is in the %s menu and the %s menu — a command lives in one", it.Label, other, name)
			}
			seen[label] = name
		}
	}
}

// TestWindowMenuCarriesTheAlwaysOnTopCheckbox pins the 2026-10-31 move: the
// titlebar pin is gone and the Window menu holds the toggle. MyGo's checkbox
// flips its own check before the Click runs, so the test replays that order;
// the headless session's nil window keeps the platform call a guarded no-op.
func TestWindowMenuCarriesTheAlwaysOnTopCheckbox(t *testing.T) {
	s := NewShell()
	item := menuItemNamed(menuNamed(t, s.MenuBar(nil), "Window").Submenu, "Keep Window on Top")
	if item == nil {
		t.Fatal("no Keep Window on Top in the Window menu")
	}
	if item.Type != mygo.MenuItemCheckbox {
		t.Fatalf("Keep Window on Top is %q, want a checkbox", item.Type)
	}
	if item.IsChecked() {
		t.Fatal("window must start unpinned")
	}
	item.SetChecked(true)
	item.Click(item, nil)
	if !item.IsChecked() {
		t.Fatal("click lost the checked state")
	}
}

// homeDestination is the name the palette and the Go menu give the Terminal's
// page. The sidebar's way there is the Back foot of Sessions and Hosts (the
// view-tab strip went, 2026-11-08).
var homeDestination = struct{ control, name string }{"Back", "Workspace"}

// viewDestinations pairs each page with the sidebar control that opens it (""
// where the sidebar has none) and the name the other launchers — the palette
// and the Go menu — give it. Hosts has no sidebar control any more: the
// Command Center pins it (U11).
var viewDestinations = []struct{ control, name string }{
	{"History view", "Sessions"},
	{"", "Hosts"},
}

// TestLaunchersAgreeOnViewDestinations pins the rule that a page reachable
// from the sidebar is reachable from the palette and the menu bar under one
// name: the Changes view was once a sidebar-only page, and the palette once
// had no way back to the Terminal. The Go menu also keeps the sidebar tabs'
// order after Workspace; the palette lists a section alphabetically, so only
// its names are pinned. Changes and Files left the strip with the Git and Files
// pages (2026-11-08): the right panel reaches them, and TestRetiredPagesLeaveNoEntry
// pins that nothing names them any more.
func TestLaunchersAgreeOnViewDestinations(t *testing.T) {
	want := []string{homeDestination.name}
	for _, d := range viewDestinations {
		want = append(want, d.name)
	}

	s := NewShell()
	palette := map[string]bool{}
	for _, a := range s.buildCommandCenterActions() {
		if a.Section == "App" && a.Available {
			palette[a.Title] = true
		}
	}
	for _, name := range want {
		if !palette[name] {
			t.Errorf("the Command Center has no App action %q", name)
		}
	}
	var goMenu []string
	for _, label := range menuLabels(menuNamed(t, s.MenuBar(nil), "Go").Submenu) {
		if slices.Contains(want, label) {
			goMenu = append(goMenu, label)
		}
	}
	if !slices.Equal(goMenu, want) {
		t.Errorf("Go menu lists the destinations as %q, want %q", goMenu, want)
	}
}

// launch runs a destination through one launcher: the Go menu item or the
// Command Center App action of the same name.
func launch(t *testing.T, s *Shell, via, name string) {
	t.Helper()
	if via == "menu" {
		item := menuItemNamed(menuNamed(t, s.MenuBar(nil), "Go").Submenu, name)
		if item == nil {
			t.Fatalf("no %q in the Go menu", name)
		}
		item.Click(item, nil)
		return
	}
	for _, a := range s.buildCommandCenterActions() {
		if a.Title == name && a.Section == "App" {
			s.executeCommandCenterResult(commandcenter.Result{Action: a})
			return
		}
	}
	t.Fatalf("no %q in the Command Center", name)
}

// TestNamingTheCurrentViewKeepsIt pins the destination form of the view
// launchers: the palette and the Go menu name a page, so choosing the one
// already open stays on it — only the sidebar tab toggles back to the Terminal.
func TestNamingTheCurrentViewKeepsIt(t *testing.T) {
	landing := func(s *Shell) string {
		return s.router.Path() + "|" + string(s.surface.current())
	}
	// The pages leave the Workspace surface as the shell had it.
	want := func(s *Shell, name string) string {
		if name == "Sessions" {
			return routeHistory + "|" + string(s.surface.current())
		}
		return routeHosts + "|" + string(s.surface.current())
	}
	for _, via := range []string{"palette", "menu"} {
		for _, d := range viewDestinations {
			s := gitSurfaceTestShell(t, t.TempDir())
			s.hosts.loaded = true // never read the developer's real ~/.ssh/config
			choose := func() { launch(t, s, via, d.name) }
			expected := want(s, d.name)
			choose()
			first := landing(s)
			if first != expected {
				t.Errorf("%s %q landed on %q, want %q", via, d.name, first, expected)
			}
			choose()
			if again := landing(s); again != first {
				t.Errorf("%s %q a second time went to %q — naming the open page keeps it (was %q)", via, d.name, again, first)
			}
		}
	}
}

// TestWorkspaceLandsTheTerminalFromAnySurface pins Go ▸ Workspace (⌘1) and
// the palette's Workspace: from the Workspace's own Chat surface a bare route
// push was a no-op (same route), so the item looked dead. Both land the
// Terminal like Back does, and choosing again keeps the Terminal.
func TestWorkspaceLandsTheTerminalFromAnySurface(t *testing.T) {
	starts := map[string]func(s *Shell){
		"Chat":     func(s *Shell) { s.surface.openChat(s.workspaceContext()) },
		"Sessions": func(s *Shell) { s.router.Push(routeHistory) },
		"Hosts":    func(s *Shell) { s.router.Push(routeHosts) },
	}
	for _, via := range []string{"palette", "menu"} {
		for name, start := range starts {
			s := gitSurfaceTestShell(t, t.TempDir())
			start(s)
			launch(t, s, via, "Workspace")
			if got := s.router.Path(); got != routeWorkspace || s.surface.current() != WorkspaceSurfaceTerminal {
				t.Errorf("%s Workspace from %s landed on %s showing %q, want the Terminal at %s", via, name, got, s.surface.current(), routeWorkspace)
			}
			launch(t, s, via, "Workspace")
			if got := s.router.Path(); got != routeWorkspace || s.surface.current() != WorkspaceSurfaceTerminal {
				t.Errorf("%s Workspace a second time (from %s) left the Terminal: %s showing %q", via, name, got, s.surface.current())
			}
		}
	}
}

// TestShortcutSheetListsTheBoundKeys pins the sheet to the real bindings: each
// accelerator the Go, Tab and Window menus carry appears in the sheet under the same
// keycaps, and the route shortcuts show the keys the Shell really fires.
func TestShortcutSheetListsTheBoundKeys(t *testing.T) {
	s := NewShell()
	left, right := s.shortcutSections()
	listed := map[string]string{}
	for _, sections := range [][]shortcutSection{left, right} {
		for _, section := range sections {
			for _, r := range section.Rows {
				if len(r.Keys) == 0 {
					t.Errorf("sheet row %q has no keys — a row without a key is not a shortcut", r.Title)
				}
				listed[strings.Join(r.Keys, "")] = r.Title
			}
		}
	}
	menu := s.MenuBar(nil)
	for _, name := range []string{"Go", "Tab", "Window"} {
		for _, it := range menuNamed(t, menu, name).Submenu {
			// A role item (Minimize, Zoom) is the system's, its key not ours to list.
			if it.Accelerator == "" || it.Role != "" || strings.HasPrefix(it.Label, "Select Tab ") || it.Label == "Last Tab" {
				continue
			}
			if _, ok := listed[strings.Join(acceleratorKeycaps(it.Accelerator), "")]; !ok {
				t.Errorf("%s ▸ %s (%s) is not in the shortcut sheet", name, it.Label, it.Accelerator)
			}
		}
	}
	if got := listed["⌥⌘1–9"]; got != "Select Tab" {
		t.Errorf("sheet lists ⌥⌘1–9 as %q, want Select Tab", got)
	}
	if got, want := strings.Join(s.shortcutKeycaps(shortcutSearch), ""), "⌘K"; got != want {
		t.Errorf("Search keycaps = %q, want %q", got, want)
	}
}

// TestSelectTabKeycaps pins which Tab chips name a key: ⌥⌘1–8 by position,
// ⌥⌘9 on the last Tab only when it is past the eighth.
func TestSelectTabKeycaps(t *testing.T) {
	for _, c := range []struct {
		n, count int
		want     string
	}{{1, 3, "⌥⌘1"}, {8, 12, "⌥⌘8"}, {9, 9, "⌥⌘9"}, {9, 12, ""}, {12, 12, "⌥⌘9"}} {
		if got := strings.Join(selectTabKeycaps(c.n, c.count), ""); got != c.want {
			t.Errorf("selectTabKeycaps(%d,%d) = %q, want %q", c.n, c.count, got, c.want)
		}
	}
}

// TestTabMenuSelectsTabsByPosition pins ⌥⌘1–8 and ⌥⌘9: the nth Tab of the
// selected Workspace in Herdr's order, "Last Tab" the final one, and a
// position past the end changes nothing.
func TestTabMenuSelectsTabsByPosition(t *testing.T) {
	s := tabBarShell(12, "t1")
	choose := func(label string) {
		t.Helper()
		item := menuItemNamed(menuNamed(t, s.MenuBar(nil), "Tab").Submenu, label)
		if item == nil {
			t.Fatalf("no %q in the Tab menu", label)
		}
		item.Click(item, nil)
	}
	choose("Select Tab 3")
	if s.selectedTabID != "t3" {
		t.Fatalf("Select Tab 3 landed on %q, want t3", s.selectedTabID)
	}
	choose("Last Tab")
	if s.selectedTabID != "t12" {
		t.Fatalf("Last Tab landed on %q, want t12", s.selectedTabID)
	}
	few := tabBarShell(2, "t1")
	item := menuItemNamed(menuNamed(t, few.MenuBar(nil), "Tab").Submenu, "Select Tab 5")
	item.Click(item, nil)
	if few.selectedTabID != "t1" {
		t.Fatalf("Select Tab 5 of 2 moved the selection to %q", few.selectedTabID)
	}
}

// TestRetiredPagesLeaveNoEntry pins G5 (2026-11-08): the Git and Files pages
// left every entry point — the Go menu, the accelerators ⌘2 and ⌘4, the
// Keyboard Shortcuts sheet, the Command Center, the sidebar's view strip and
// the workspace's own menu — while the right panel stays reachable from the
// palette under both of the words the pages went by.
func TestRetiredPagesLeaveNoEntry(t *testing.T) {
	s := NewShell()
	menu := s.MenuBar(nil)

	retired := []string{"Changes", "Files"}
	for _, top := range menu.Items() {
		for _, item := range top.Submenu {
			if slices.Contains(retired, item.Label) {
				t.Errorf("%s ▸ %s: a retired page still has its menu item", top.Label, item.Label)
			}
			if item.Accelerator == "CmdOrCtrl+2" || item.Accelerator == "CmdOrCtrl+4" {
				t.Errorf("%s ▸ %s carries %s, the key of a retired page", top.Label, item.Label, item.Accelerator)
			}
		}
	}

	left, right := s.shortcutSections()
	for _, section := range append(left, right...) {
		for _, row := range section.Rows {
			if slices.Contains(retired, row.Title) {
				t.Errorf("shortcuts sheet ▸ %s ▸ %s: a retired page is still listed", section.Title, row.Title)
			}
		}
	}

	for _, action := range s.buildCommandCenterActions() {
		if action.Section == "App" && slices.Contains(retired, action.Title) {
			t.Errorf("Command Center lists %q as a page", action.Title)
		}
		if strings.Contains(action.TargetID, "/files") {
			t.Errorf("Command Center action %q still targets the retired route %q", action.ID, action.TargetID)
		}
	}
	panel, ok := commandCenterAction(s, "app.right-panel")
	if !ok {
		t.Fatal("the Command Center lost the right panel's entry")
	}
	for _, word := range []string{"changes", "files", "git"} {
		if !slices.Contains(panel.Keywords, word) {
			t.Errorf("the panel's entry no longer answers to %q", word)
		}
	}

	s.loading = false
	s.instances = []herdr.Instance{{Name: "default", DisplayName: "Default", Running: true, Default: true}}
	s.activeInstance = "default"
	s.projection = herdr.Projection{Protocol: 22}
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	for _, label := range []string{"Search", "History view"} {
		if !tester.HasText(label) {
			t.Errorf("the sidebar's top row lost %q; texts=%q", label, tester.Texts())
		}
	}
	for _, gone := range []string{"Changes view", "Files view", "Terminal view", "Hosts view", "Automations view"} {
		if tester.HasText(gone) {
			t.Errorf("the sidebar still carries the retired %q", gone)
		}
	}
}

// TestWindowMenuCarriesSwitchAgentOnControlTab pins the 2026-11-08 repair of
// the Agent switcher's key. AppKit gives Control-Tab to no view (a bare NSView
// probe saw performKeyEquivalent and no keyDown), so the in-view shortcut the
// switcher used to listen on never fired; a menu accelerator is the one
// carrier, and it must be a visible item — a hidden item's key equivalent does
// not fire either (real-machine run, same day).
func TestWindowMenuCarriesSwitchAgentOnControlTab(t *testing.T) {
	s := workbenchTestShell(t)
	for _, terminal := range []string{"term-2", "term-1"} { // visit both Agents: the switcher lists the visited
		card, ok := s.workbench.directory.Get(agent.AgentKey{InstanceID: "default", TerminalID: terminal})
		if !ok {
			t.Fatalf("agent card %s missing", terminal)
		}
		s.openAgentCard(card)
	}
	window := menuNamed(t, s.MenuBar(nil), "Window").Submenu
	forward := menuItemNamed(window, "Switch Agent")
	backward := menuItemNamed(window, "Switch Agent Backwards")
	if forward == nil || backward == nil {
		t.Fatalf("Window menu has no Switch Agent items: %q", menuLabels(window))
	}
	if forward.Accelerator != "Ctrl+Tab" || backward.Accelerator != "Ctrl+Shift+Tab" {
		t.Errorf("accelerators = %q / %q, want Ctrl+Tab / Ctrl+Shift+Tab", forward.Accelerator, backward.Accelerator)
	}
	if forward.Hidden || backward.Hidden || forward.Disabled || backward.Disabled {
		t.Errorf("the Switch Agent items must be visible and enabled: %+v / %+v", forward, backward)
	}

	// Choosing the item is what the key does: it opens the overlay, and again it cycles.
	forward.Click(forward, nil)
	if !s.workbench.switcherOpen {
		t.Fatal("Window ▸ Switch Agent did not open the switcher")
	}
	forward.Click(forward, nil)
	if s.workbench.switcherIndex != 1 {
		t.Errorf("the second Switch Agent did not cycle: index=%d, want 1", s.workbench.switcherIndex)
	}
	backward.Click(backward, nil)
	if s.workbench.switcherIndex != 0 {
		t.Errorf("Switch Agent Backwards did not step back: index=%d, want 0", s.workbench.switcherIndex)
	}

	// No item of the bar may be hidden with a key: AppKit never fires it.
	var walk func(items []*mygo.MenuItem, path string)
	walk = func(items []*mygo.MenuItem, path string) {
		for _, it := range items {
			if it.Hidden && it.Accelerator != "" {
				t.Errorf("%s%q is hidden with a key equivalent (%s)", path, it.Label, it.Accelerator)
			}
			walk(it.Submenu, path+it.Label+" ▸ ")
		}
	}
	for _, menu := range s.MenuBar(nil).Items() {
		walk(menu.Submenu, menu.Label+" ▸ ")
	}
}
