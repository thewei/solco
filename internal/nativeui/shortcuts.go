package nativeui

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/thewei/solco/internal/commandcenter"
	"github.com/thewei/solco/internal/settings"
)

type shortcutAction uint8

const (
	shortcutSearch shortcutAction = iota + 1
	shortcutSettings
	shortcutRefresh
	shortcutBack
	shortcutForward
)

type shortcutBinding struct {
	Modifiers ui.Modifiers
	Key       ui.Key
	Action    shortcutAction
}

var defaultShortcutBindings = []shortcutBinding{
	{Modifiers: ui.Cmd, Key: ui.KeyK, Action: shortcutSearch},
	{Modifiers: ui.Cmd, Key: ui.KeyComma, Action: shortcutSettings},
	{Modifiers: ui.Cmd, Key: ui.KeyR, Action: shortcutRefresh},
	{Modifiers: ui.Cmd, Key: ui.KeyBracketLeft, Action: shortcutBack},
	{Modifiers: ui.Cmd, Key: ui.KeyBracketRight, Action: shortcutForward},
}

// The three bindings the Shell routes beside the configurable set. The key
// handlers and the tooltips that name them read the same value, so a hint
// cannot name a key that does nothing. ⌥⌘B is the right panel's, as T3 Code
// binds it (designs/desktop-t3-unify, the right panel's rules): ⌘B is the sidebar's,
// and Shortcut matches the modifiers exactly, so the two never fire together.
var (
	sidebarToggleBinding   = shortcutBinding{Modifiers: ui.Super, Key: ui.KeyB}
	sidePanelToggleBinding = shortcutBinding{Modifiers: ui.Alt | ui.Super, Key: ui.KeyB}
	chatSwitchBinding      = shortcutBinding{Modifiers: ui.Cmd | ui.Shift, Key: ui.KeyC}
)

// The menu bar's accelerators, one place for the menu and the Keyboard
// Shortcuts sheet (shortcuts_sheet.go) so a listed key is a bound key. The
// pages take ⌘1, ⌘3, ⌘5 and ⌘6 (⌘6 only exists while the automations gate is
// open); ⌘2 and ⌘4 went with the Git and Files pages (2026-11-08) and the other
// keys kept their places. Tabs take ⌥⌘1–9.
const (
	accelWorkspace     = "CmdOrCtrl+1"
	accelSessions      = "CmdOrCtrl+3"
	accelHosts         = "CmdOrCtrl+5"
	accelNewTab        = "CmdOrCtrl+T"
	accelCloseTab      = "CmdOrCtrl+W"
	accelCommandCenter = "CmdOrCtrl+Shift+P"
	accelShortcuts     = "CmdOrCtrl+/"
	accelAutomations   = "CmdOrCtrl+6"
	accelSettings      = "CmdOrCtrl+,"

	// ⌃⇥ and ⌃⇧⇥ are the Agent switcher's, and they are menu accelerators
	// because they have to be: AppKit takes Control-Tab as the key-view loop
	// before any view's keyDown runs (a bare NSView probe, 2026-11-08), so an
	// in-view c.Shortcut for it never fires. Visible items only — a hidden
	// item's key equivalent does not fire either (real-machine run, same day).
	accelSwitchAgent     = "Ctrl+Tab"
	accelSwitchAgentBack = "Ctrl+Shift+Tab"

	// lastTab is the pseudo index of "the last Tab" (⌥⌘9).
	lastTab = 9
)

// selectTabAccelerator is the accelerator of Select Tab n (⌥⌘n).
func selectTabAccelerator(n int) string { return "CmdOrCtrl+Alt+" + itoa(n) }

// acceleratorKeycaps spells a menu accelerator as keycaps in the macOS order
// (⌃⌥⇧⌘ then the key), as shortcutBinding.keycaps does for a binding.
func acceleratorKeycaps(accelerator string) []string {
	parts := strings.Split(accelerator, "+")
	key := parts[len(parts)-1]
	var caps []string
	for _, modifier := range []struct{ name, cap string }{
		{"Ctrl", "⌃"}, {"Alt", "⌥"}, {"Shift", "⇧"}, {"CmdOrCtrl", "⌘"}, {"Cmd", "⌘"},
	} {
		for _, part := range parts[:len(parts)-1] {
			if part == modifier.name {
				caps = append(caps, modifier.cap)
			}
		}
	}
	if key == "Tab" {
		key = "⇥"
	}
	return append(caps, strings.ToUpper(key))
}

// acceleratorLabel is the accelerator as one string of keycaps ("⌘1"), the
// shape the Command Center prints beside a command.
func acceleratorLabel(accelerator string) string {
	return strings.Join(acceleratorKeycaps(accelerator), "")
}

// keycaps spells the binding as a tooltip shows it: the modifiers in the
// macOS order (⌃⌥⇧⌘), then the key. A key with no known cap yields nil — no
// hint rather than a wrong one.
func (b shortcutBinding) keycaps() []string {
	key := keycapName(b.Key)
	if key == "" {
		return nil
	}
	var caps []string
	for _, modifier := range []struct {
		flag ui.Modifiers
		cap  string
	}{{ui.Ctrl, "⌃"}, {ui.Alt, "⌥"}, {ui.Shift, "⇧"}, {ui.Super, "⌘"}} {
		if b.Modifiers&modifier.flag != 0 {
			caps = append(caps, modifier.cap)
		}
	}
	return append(caps, key)
}

var keycapNames = map[ui.Key]string{
	ui.KeyComma: ",", ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]",
	ui.KeyEnter: "↩", ui.KeyEscape: "⎋", ui.KeyTab: "⇥", ui.KeySpace: "Space",
	ui.KeyUp: "↑", ui.KeyDown: "↓", ui.KeyLeft: "←", ui.KeyRight: "→",
}

// keycapName is the cap of a key, the reverse of parseKey's spellings.
func keycapName(key ui.Key) string {
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		return string(rune('A' + key - ui.KeyA))
	case key >= ui.Key0 && key <= ui.Key9:
		return string(rune('0' + key - ui.Key0))
	}
	if name, ok := keycapNames[key]; ok {
		return name
	}
	for n := 1; n <= 12; n++ {
		name := "F" + strconv.Itoa(n)
		if known, ok := parseKey(name); ok && known == key {
			return name
		}
	}
	return ""
}

// shortcutKeycaps is the keycaps of the route shortcut as it is bound now,
// from the same resolution handleRouteShortcuts fires.
func (s *Shell) shortcutKeycaps(action shortcutAction) []string {
	for _, binding := range shortcutBindings(s.settings.Shortcuts) {
		if binding.Action == action {
			return binding.keycaps()
		}
	}
	return nil
}

// shortcutBindings resolves the effective route shortcuts: the persisted
// settings are authoritative (they were stored but never consumed before
// 2026-10-06), falling back per-action to the default when unset or
// unparsable, so a broken edit can only lose one binding.
func shortcutBindings(value settings.ShortcutSettings) []shortcutBinding {
	bindings := make([]shortcutBinding, 0, len(defaultShortcutBindings))
	appendBinding := func(action shortcutAction, configured string, fallback shortcutBinding) {
		if binding, ok := parseShortcut(configured); ok {
			binding.Action = action
			bindings = append(bindings, binding)
			return
		}
		bindings = append(bindings, fallback)
	}
	appendBinding(shortcutSearch, value.Search, defaultShortcutBindings[0])
	appendBinding(shortcutSettings, value.Settings, defaultShortcutBindings[1])
	appendBinding(shortcutRefresh, value.Refresh, defaultShortcutBindings[2])
	appendBinding(shortcutBack, value.Back, defaultShortcutBindings[3])
	appendBinding(shortcutForward, value.Forward, defaultShortcutBindings[4])
	return bindings
}

// parseShortcut reads the persisted spelling ("Cmd+Shift+N", "Ctrl+K",
// "Cmd+Comma") into modifiers and a key.
func parseShortcut(value string) (shortcutBinding, bool) {
	var binding shortcutBinding
	if strings.TrimSpace(value) == "" {
		return binding, false
	}
	for _, part := range strings.Split(value, "+") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "cmd", "super":
			binding.Modifiers |= ui.Cmd
		case "ctrl", "control":
			binding.Modifiers |= ui.Ctrl
		case "shift":
			binding.Modifiers |= ui.Shift
		case "alt", "option":
			binding.Modifiers |= ui.Alt
		default:
			key, ok := parseKey(part)
			if !ok {
				return shortcutBinding{}, false
			}
			if binding.Key != 0 {
				return shortcutBinding{}, false
			}
			binding.Key = key
		}
	}
	if binding.Key == 0 {
		return binding, false
	}
	return binding, true
}

func parseKey(value string) (ui.Key, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "comma":
		return ui.KeyComma, true
	case "[", "bracketleft":
		return ui.KeyBracketLeft, true
	case "]", "bracketright":
		return ui.KeyBracketRight, true
	case "enter", "return":
		return ui.KeyEnter, true
	case "escape", "esc":
		return ui.KeyEscape, true
	case "tab":
		return ui.KeyTab, true
	case "space":
		return ui.KeySpace, true
	}
	name := strings.ToUpper(strings.TrimSpace(value))
	if len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z' {
		// MyGo letter keys are an enum run (KeyA + offset), not ASCII codes.
		return ui.KeyA + ui.Key(name[0]-'A'), true
	}
	if len(name) == 1 && name[0] >= '0' && name[0] <= '9' {
		return ui.Key0 + ui.Key(name[0]-'0'), true
	}
	if key, ok := map[string]ui.Key{
		"UP": ui.KeyUp, "DOWN": ui.KeyDown, "LEFT": ui.KeyLeft, "RIGHT": ui.KeyRight,
		"F1": ui.KeyF1, "F2": ui.KeyF2, "F3": ui.KeyF3, "F4": ui.KeyF4,
		"F5": ui.KeyF5, "F6": ui.KeyF6, "F7": ui.KeyF7, "F8": ui.KeyF8,
		"F9": ui.KeyF9, "F10": ui.KeyF10, "F11": ui.KeyF11, "F12": ui.KeyF12,
	}[name]; ok {
		return key, true
	}
	return 0, false
}

func (s *Shell) handleRouteShortcuts(c *ui.Context) {
	for _, binding := range shortcutBindings(s.settings.Shortcuts) {
		if c.Shortcut(binding.Modifiers, binding.Key) {
			s.runShortcut(binding.Action)
		}
	}
}

// handleSurfaceShortcuts routes surface-aware shortcuts (plan §33): the
// active WorkspacePrimarySurface owns Cmd/Ctrl+F and Cmd/Ctrl+Enter; hidden
// surfaces never receive them.
func (s *Shell) handleSurfaceShortcuts(c *ui.Context) {
	if s.router.Path() != routeWorkspace {
		return
	}
	if c.Shortcut(chatSwitchBinding.Modifiers, chatSwitchBinding.Key) {
		s.toggleTerminalChat()
		return
	}
	switch s.surface.current() {
	case WorkspaceSurfaceChat:
		// Esc closes a floating right panel first (sidePanelFloating); only with
		// none up does it leave Chat.
		if !s.sidePanelOverlayUp() && c.Shortcut(0, ui.KeyEscape) {
			s.showSurface(WorkspaceSurfaceTerminal)
		}
	case WorkspaceSurfaceDetail:
		// Esc steps back to the Terminal or Chat the card came from, the
		// detail kept open; a floating right panel takes the Esc first, and
		// so does a find bar the file's formatted view has open.
		if !s.sidePanelOverlayUp() && c.Shortcut(0, ui.KeyEscape) && !s.closePreviewFind() {
			s.leaveDetail()
		}
	case WorkspaceSurfaceCommit:
		if c.Shortcut(ui.Cmd, ui.KeyEnter) {
			if selected := s.selectedCommitPaths(); len(selected) > 0 &&
				strings.TrimSpace(s.surface.commit.Subject) != "" && s.surface.commit.Captured {
				s.submitCommit(strings.TrimSpace(s.surface.commit.Subject),
					s.surface.commit.Body, selected)
			}
		}
	case WorkspaceSurfaceTerminal:
		// Terminal surface Cmd+F opens the Terminal find bar driven by the
		// live-verified Herdr CopySearch adapter (GWB-010/§31).
		if c.Shortcut(ui.Cmd, ui.KeyF) {
			s.openTerminalFind()
		}
	}
}

// openTerminalFind surfaces the Terminal find bar over the visible Terminal
// surface; the adapter executes against the selected pane only.
func (s *Shell) openTerminalFind() {
	if len(s.terminals) == 0 {
		return
	}
	if pane := s.selectedPane(); pane != nil && pane.TerminalID != "" {
		if surface := s.terminals[pane.TerminalID]; surface != nil {
			s.terminalFind.open = true
			s.terminalFind.paneID = pane.ID
			s.terminalFind.terminalID = pane.TerminalID
		}
	}
}

func (s *Shell) runShortcut(action shortcutAction) {
	switch action {
	case shortcutSearch:
		// ⌘K is the one search surface: the palette jumps to the matching
		// project/tab/pane/agent/session destination; there is no
		// standalone search page to route to.
		s.openCommandCenter(commandcenter.ScopeAll)
	case shortcutSettings:
		if s.holdCommitNavigation() {
			return
		}
		s.router.Push(routeSettings)
	case shortcutRefresh:
		if s.router.Path() == routeWorkspace {
			s.reloadInstances(false)
		}
	case shortcutBack:
		if !s.holdCommitNavigation() {
			s.router.Back()
		}
	case shortcutForward:
		if !s.holdCommitNavigation() {
			s.router.Forward()
		}
	}
}
