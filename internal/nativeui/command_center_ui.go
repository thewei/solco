package nativeui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/commandcenter"
)

// Command Center (0.9 P1) over the shared Action Registry (0.9 P2): the
// fast navigation/actions palette. Opening it performs zero filesystem,
// network or Herdr RPC work — every action is built from application
// snapshot facts and every result carries a typed stable target that the
// executor revalidates before acting. It is the app's single search
// surface: the standalone Search page was removed (2026-10-06 product
// decision) and ⌘K/⌘P/⌘⇧P all land here.

// Palette layout constants (DIP): mainstream command palettes run a
// 560-680 panel, a tall query field, and a capped scrolling list.
const (
	commandCenterWidth     = 560
	commandCenterListCap   = 384
	commandCenterMaxShown  = 100
	commandCenterPageSize  = 8
	commandCenterNoReveals = -1
)

// commandCenterChips are the ⌘K scope chips (2026-10-30 UX audit): All, then
// one chip per kind of thing the palette indexes. Files and Sessions are not
// chips on purpose — finding them takes filesystem/catalog reads, and opening
// the palette performs none (they keep their own search fields). Sections
// are the registry sections each chip keeps.
var commandCenterChips = []struct {
	Label    string
	Sections []string
}{
	{"All", nil},
	{"Agents", []string{"Agents"}},
	{"Workspaces", []string{"Navigation"}},
	{"Commands", []string{"App", "Devices"}},
}

func commandCenterChipLabels() []string {
	labels := make([]string, len(commandCenterChips))
	for i, chip := range commandCenterChips {
		labels[i] = chip.Label
	}
	return labels
}

// commandCenterResults ranks the query inside the scope and the chosen chip.
// The ⌘P navigation scope is already one kind and shows no chips.
func (s *Shell) commandCenterResults() []commandcenter.Result {
	results := s.commandCenterIndexCache.Query(s.commandCenterQuery, s.commandCenterScope)
	if s.commandCenterScope != commandcenter.ScopeAll || s.commandCenterChip <= 0 || s.commandCenterChip >= len(commandCenterChips) {
		return results
	}
	keep := commandCenterChips[s.commandCenterChip].Sections
	kept := results[:0]
	for _, result := range results {
		for _, section := range keep {
			if result.Action.Section == section {
				kept = append(kept, result)
				break
			}
		}
	}
	return kept
}

// commandCenterSectionTitle names a registry section as its chip does.
func commandCenterSectionTitle(section string) string {
	if section == "Navigation" {
		return "Workspaces"
	}
	return section
}

// commandCenterKind is the quiet right-hand label that replaced the title
// prefixes ("Agent: ", "Device: "): what kind of thing the row is.
func commandCenterKind(action commandcenter.Action) string {
	switch action.Kind {
	case commandcenter.TargetProject:
		return "Workspace"
	case commandcenter.TargetTab:
		return "Tab"
	case commandcenter.TargetPane:
		return "Pane"
	case commandcenter.TargetAgent:
		return "Agent"
	}
	switch action.Section {
	case "Devices":
		return "Device"
	case "App":
		return "Command"
	}
	return ""
}

// handleCommandCenterShortcuts opens the palette: Cmd/Ctrl+Shift+P → All,
// Cmd/Ctrl+P → Navigation.
func (s *Shell) handleCommandCenterShortcuts(c *ui.Context) {
	if s.commandCenterOpen {
		return
	}
	switch {
	case c.Shortcut(ui.Super|ui.Shift, ui.KeyP):
		s.openCommandCenter(commandcenter.ScopeAll)
	case c.Shortcut(ui.Super, ui.KeyP):
		s.openCommandCenter(commandcenter.ScopeNavigation)
	}
}

// openCommandCenter snapshots the palette index from application state and
// opens the overlay.
func (s *Shell) openCommandCenter(scope commandcenter.Scope) {
	if s.holdCommitNavigation() {
		return
	}
	s.commandCenterIndexCache = commandcenter.Build(s.buildCommandCenterActions())
	s.commandCenterScope = scope
	s.commandCenterQuery = ""
	s.commandCenterChip = 0
	s.commandCenterSelected = 0
	s.commandCenterReveal = commandCenterNoReveals
	s.commandCenterOpen = true
}

func (s *Shell) closeCommandCenter() {
	s.commandCenterOpen = false
	s.commandCenterQuery = ""
	s.commandCenterSelected = 0
	s.commandCenterReveal = commandCenterNoReveals
}

// buildCommandCenterActions derives the Action Registry from the current
// application snapshot and capabilities (0.9 P2): the single metadata source
// shared by the Command Center, the shortcut reference and native menus.
func (s *Shell) buildCommandCenterActions() []commandcenter.Action {
	actions := make([]commandcenter.Action, 0, 32)

	// Navigation: projects → tabs → panes, straight from the projection.
	for _, project := range s.projection.Projects {
		actions = append(actions, commandcenter.Action{
			ID: "nav.project." + project.ID, Title: project.Label,
			Section: "Navigation", Keywords: []string{"project", "workspace"}, Kind: commandcenter.TargetProject, TargetID: project.ID,
			ScopeFilter: commandcenter.ScopeNavigation, Available: true,
		})
	}
	for _, tab := range s.projection.Tabs {
		actions = append(actions, commandcenter.Action{
			ID: "nav.tab." + tab.ID, Title: tab.Label,
			Section: "Navigation", Keywords: []string{"tab"}, Kind: commandcenter.TargetTab, TargetID: tab.ID,
			ScopeFilter: commandcenter.ScopeNavigation, Available: true,
		})
	}
	for _, pane := range s.projection.Panes {
		label := pane.Label
		if label == "" {
			label = pane.ID
		}
		actions = append(actions, commandcenter.Action{
			ID: "nav.pane." + pane.ID, Title: label,
			Section: "Navigation", Keywords: []string{"pane"}, Kind: commandcenter.TargetPane, TargetID: pane.ID,
			ScopeFilter: commandcenter.ScopeNavigation, Available: pane.TerminalID != "",
		})
	}

	// Agents from the reconciled workbench.
	for _, card := range s.workbenchCards() {
		actions = append(actions, commandcenter.Action{
			ID: "agent.open." + card.Key.TerminalID, Title: card.Title,
			Section: "Agents", Keywords: []string{"agent", "open agent", card.Provider.DisplayName()},
			Kind: commandcenter.TargetAgent, TargetID: card.Key.TerminalID,
			Available: true,
		})
	}

	// Devices: the saved SSH machines and the way to add one, the same
	// switcher the sidebar footer opens. Built from the instance list already
	// in memory, so opening the palette stays free of network work.
	actions = append(actions, s.deviceCommandActions()...)

	// Recent History from the already-loaded management cache (no IO).

	// App actions.
	// One Sessions entry: /history-projects serves the same page for
	// decode compatibility, so a second palette command only duplicated it
	// (F162, same shape as the F16 Settings pair).
	//
	// Every page the sidebar's view tabs and the Go menu reach is here under
	// the same name (TestLaunchersAgreeOnViewDestinations). The palette lists
	// a section alphabetically, so the order below is only reading order.
	actions = append(actions,
		commandcenter.Action{ID: "app.workspace", Title: "Workspace", Section: "App", Shortcut: acceleratorLabel(accelWorkspace),
			Keywords: []string{"terminal"}, Kind: commandcenter.TargetRoute, TargetID: "action:open-workspace", Available: true},
		commandcenter.Action{ID: "app.changes", Title: "Changes", Section: "App", Shortcut: acceleratorLabel(accelChanges),
			Keywords: []string{"git", "diff", "review", "commit", "staged"}, Kind: commandcenter.TargetRoute, TargetID: "action:open-changes", Available: true},
		commandcenter.Action{ID: "app.history", Title: "Sessions", Section: "App", Shortcut: acceleratorLabel(accelSessions),
			Keywords: []string{"history", "conversations"}, Kind: commandcenter.TargetRoute, TargetID: "/history", Available: true},
		commandcenter.Action{ID: "app.open-files", Title: "Files", Section: "App", Shortcut: acceleratorLabel(accelFiles),
			Keywords: []string{"browse files", "directory", "tree", "file preview"}, Kind: commandcenter.TargetRoute, TargetID: "action:open-files", Available: true},
		commandcenter.Action{ID: "app.quota", Title: "Quota", Section: "App",
			Keywords: []string{"usage", "limits", "tokens", "claude", "codex", "cursor", "copilot"}, Kind: commandcenter.TargetRoute, TargetID: routeQuota, Available: true},
		commandcenter.Action{ID: "app.hosts", Title: "Hosts", Section: "App", Shortcut: acceleratorLabel(accelHosts),
			Keywords: []string{"ssh", "machines", "servers"}, Kind: commandcenter.TargetRoute, TargetID: "/hosts", Available: true},
		// One Settings entry: the old Appearance/General pair both pushed
		// /settings/general and read as two identical commands (F16).
		commandcenter.Action{ID: "app.settings", Title: "Settings", Section: "App", Shortcut: acceleratorLabel(accelSettings),
			Keywords: []string{"preferences", "appearance", "theme", "density", "language"}, Kind: commandcenter.TargetSetting, TargetID: "/settings/general", Available: true},
		commandcenter.Action{ID: "app.hook-doctor", Title: "Set Up Agents", Section: "App",
			Keywords: []string{"agents", "hooks", "integration", "install", "update", "claude", "antigravity"}, Kind: commandcenter.TargetRoute, TargetID: "/settings/providers", Available: true},
		// The com.whstudio.solco.automations plugin: derived from the plugin
		// gate like the work board entry, so it exists only while the plugin
		// is installed, enabled and in the verified 0.12.x window.
		commandcenter.Action{ID: "app.automations", Title: "Automations", Section: "App", Shortcut: acceleratorLabel(accelAutomations),
			Keywords: []string{"scheduled", "cron", "定时", "runs"}, Kind: commandcenter.TargetRoute, TargetID: routeAutomations, Available: s.automationsAvailable()},
		commandcenter.Action{ID: "app.assistants", Title: "Assistants", Section: "App",
			Keywords: []string{"assistant", "goal", "task", "coordinator", "memory", "助手"}, Kind: commandcenter.TargetRoute, TargetID: routeAssistants, Available: s.assistantsAvailable()},
	)
	return actions
}

// commandCenterGroup is one titled block of the palette list.
type commandCenterGroup struct {
	Section string
	Results []commandcenter.Result
}

// groupCommandCenterResults splits the ranked results into consecutive
// section groups: Query already sorts by section inside a match class, so
// one pass closes a group wherever the section changes.
func groupCommandCenterResults(results []commandcenter.Result) []commandCenterGroup {
	groups := make([]commandCenterGroup, 0, 4)
	for _, result := range results {
		if n := len(groups); n > 0 && groups[n-1].Section == result.Action.Section {
			groups[n-1].Results = append(groups[n-1].Results, result)
			continue
		}
		groups = append(groups, commandCenterGroup{
			Section: result.Action.Section,
			Results: []commandcenter.Result{result},
		})
	}
	return groups
}

// commandCenterOverlay renders the palette while open (0.9 P1): one
// DialogBase panel styled once — glass, a single corner radius, one
// border — carrying the query field, the grouped results and the key
// hints. No nested panel inside it: the old Modal-in-Column pair drew two
// rounded containers.
func (s *Shell) commandCenterOverlay(c *ui.Context) {
	if !s.commandCenterOpen {
		return
	}
	sp := Spacing()
	winW, winH := c.Size()
	ui.DialogBase(c, &s.commandCenterOpen, func(back, panel ui.Element) {
		back.Background(ui.RGBA(0, 0, 0, 0.4))
		// Liquid Glass (MyGo 0.2.15): the palette frosts the dimmed page
		// under it instead of painting a flat panel — the macOS-26 overlay
		// look. glass.Regular keeps rows readable over any content.
		panel.Width(commandCenterWidth).MaxWidth(winW-2*sp.XL).MaxHeight(winH-2*sp.XL).
			Padding(sp.L).Gap(sp.S).Radius(Radius().Card).
			Material(glass.Glass{}).
			Border(1, designTokens(c.Theme().Dark).BorderSubtle).
			Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3))
		// AutoFocus: the palette is keyboard-first from open (WIX-021) —
		// the field hands the focus to its editor (mygo field-focus seam).
		field := searchField(c, &s.commandCenterQuery).Label("Command Center query").
			MinHeight(Controls().PaletteField).AutoFocus()
		s.commandCenterQueryFocused = field.FocusWithin()
		if field.Changed() {
			s.commandCenterSelected = 0
			s.commandCenterReveal = 0
		}
		if s.commandCenterScope == commandcenter.ScopeAll {
			// ⇥ / ⇧⇥ walk the chips without leaving the field.
			n := len(commandCenterChips)
			switch {
			case field.Shortcut(0, ui.KeyTab):
				s.commandCenterChip = (s.commandCenterChip + 1) % n
				s.commandCenterSelected, s.commandCenterReveal = 0, 0
			case field.Shortcut(ui.Shift, ui.KeyTab):
				s.commandCenterChip = (s.commandCenterChip - 1 + n) % n
				s.commandCenterSelected, s.commandCenterReveal = 0, 0
			}
			before := s.commandCenterChip
			segmentedControl(c, &s.commandCenterChip, commandCenterChipLabels()...).FillWidth()
			if s.commandCenterChip != before {
				s.commandCenterSelected, s.commandCenterReveal = 0, 0
			}
		}
		results := s.commandCenterResults()
		if s.commandCenterSelected >= len(results) {
			s.commandCenterSelected = len(results) - 1
		}
		if s.commandCenterSelected < 0 {
			s.commandCenterSelected = 0
		}
		s.commandCenterListKeys(field, len(results))
		if field.Submitted() {
			// The submitted action recomputes from the CURRENT query: an
			// IME commit or fast typing may land in the same pass.
			s.commandCenterSubmit(s.commandCenterResults())
		}
		total := len(results)
		if total > commandCenterMaxShown {
			results = results[:commandCenterMaxShown]
		}
		if total == 0 {
			ui.Text(c, "No matching commands").FontSize(Typography().Body).TextColor(c.Theme().TextMuted).FillWidth().TextAlign(ui.Center).Padding(sp.XL, sp.L)
			return
		}
		// The grouped list scrolls past the cap; the selection reveals
		// itself only when the keys or a new query moved it — never while
		// the pointer wanders (ScrollIntoView every frame would fight the
		// user's scrolling).
		ui.Scroll(c).FillWidth().MaxHeight(commandCenterListCap).Children(func() {
			flat := 0
			for gi, group := range groupCommandCenterResults(results) {
				if gi > 0 {
					ui.Box(c).FillWidth().Height(1).Margin(0, sp.M).
						Background(designTokens(c.Theme().Dark).BorderSubtle)
				}
				ui.Row(c).FillWidth().Padding(sp.S, sp.M).Children(func() {
					ui.Text(c, strings.ToUpper(commandCenterSectionTitle(group.Section))).
						FontSize(Typography().Micro).TextColor(c.Theme().TextMuted)
				})
				kinds := map[string]bool{}
				for _, result := range group.Results {
					kinds[commandCenterKind(result.Action)] = true
				}
				// A group of one kind already says it in its header (AGENTS,
				// APP); the right-hand label returns where kinds mix.
				for _, result := range group.Results {
					s.commandCenterRow(c, flat, result, len(kinds) > 1)
					flat++
				}
			}
		})
		shown := fmt.Sprintf("%d result%s", total, pluralS(total))
		if total > len(results) {
			shown = fmt.Sprintf("%d of %d results", len(results), total)
		}
		hint := "↑↓ select · ⇥ scope · Enter open · Esc close"
		if s.commandCenterScope != commandcenter.ScopeAll {
			hint = "↑↓ select · Enter open · Esc close"
		}
		if s.commandCenterScope == commandcenter.ScopeNavigation {
			hint = "Navigation — " + hint
		}
		ui.Text(c, shown+" · "+hint).FontSize(Typography().Caption).TextColor(c.Theme().TextMuted)
	})
}

// commandCenterRowFade is how a result row appears under the list that was
// already open: the list's scope changed, or a longer list grew.
var commandCenterRowFade = ui.ElementTransition{Duration: motionRow, Enter: &ui.Motion{}}

// commandCenterRow builds one result row: hover selects it, click runs
// it, and the row the keyboard moved to reveals itself in the list.
func (s *Shell) commandCenterRow(c *ui.Context, i int, result commandcenter.Result, showKind bool) {
	sp := Spacing()
	// Keyed by chip and position: switching scope builds new rows, which fade
	// in (commandCenterRowFade); typing keeps its rows and only changes them.
	row := ui.Row(c.Key(fmt.Sprintf("cc-row:%d:%d", s.commandCenterChip, i))).FillWidth().Padding(sp.S, sp.M).Gap(sp.S).AlignItems(ui.Center).
		Radius(Radius().Control).Transition(commandCenterRowFade)
	row.Children(func() {
		// Truncated titles stay recoverable via tooltip (F157, same
		// contract as the commits list).
		tipped(c, ui.Text(c, result.Action.Title).FontSize(Typography().Body).Grow(1).SingleLine(), result.Action.Title)
		if result.Action.Shortcut != "" {
			ui.Text(c, result.Action.Shortcut).FontSize(Typography().Caption).TextColor(c.Theme().TextMuted)
		}
		if kind := commandCenterKind(result.Action); kind != "" && showKind {
			ui.Text(c, kind).FontSize(Typography().Caption).TextColor(c.Theme().TextMuted).SingleLine().Shrink(0)
		}
	})
	if i == s.commandCenterSelected {
		row.Background(designTokens(c.Theme().Dark).StatusBackground(ToneInfo, c.Theme().Dark))
	}
	if i == s.commandCenterReveal {
		row.ScrollIntoView()
		s.commandCenterReveal = commandCenterNoReveals
	}
	if row.Hovered() {
		s.commandCenterSelected = i
	}
	if row.Clicked() {
		s.executeCommandCenterResult(result)
	}
}

// commandCenterListKeys claims the palette's list keys on the search
// field: the field owns the keyboard from open, so the claims ride its
// focus chain (the Combobox pattern) — the window-level fallback the old
// version used would never fire past a focused editor. Arrows wrap and
// PgUp/PgDn jump; every move reveals its row. Home/End stay with the
// caret: the editor takes them unconditionally, as mainstream palettes
// do while their input holds the focus.
func (s *Shell) commandCenterListKeys(field ui.Element, n int) {
	if n == 0 {
		return
	}
	down := field.Shortcut(0, ui.KeyDown)
	up := field.Shortcut(0, ui.KeyUp)
	pageDown := field.Shortcut(0, ui.KeyPageDown)
	pageUp := field.Shortcut(0, ui.KeyPageUp)
	move := func(i int) {
		s.commandCenterSelected = i
		s.commandCenterReveal = i
	}
	switch {
	case down:
		move((s.commandCenterSelected + 1) % n)
	case up:
		move((s.commandCenterSelected - 1 + n) % n)
	case pageDown:
		move(min(s.commandCenterSelected+commandCenterPageSize, n-1))
	case pageUp:
		move(max(s.commandCenterSelected-commandCenterPageSize, 0))
	}
}

// commandCenterSubmit commits the selected result (the search field's
// Submitted path).
func (s *Shell) commandCenterSubmit(results []commandcenter.Result) {
	if s.commandCenterSelected < len(results) {
		s.executeCommandCenterResult(results[s.commandCenterSelected])
	}
}

// executeCommandCenterResult revalidates the typed stable target against
// the application snapshot, then navigates client-locally. A stale target
// surfaces recoverable text instead of acting (0.9 P1).
func (s *Shell) executeCommandCenterResult(result commandcenter.Result) {
	action := result.Action
	s.closeCommandCenter()
	switch action.Kind {
	case commandcenter.TargetPane:
		if paneByID(s.projection, action.TargetID) == nil {
			s.status = "That pane is gone — pick another from the Command Center."
			return
		}
		s.selectPane(action.TargetID)
		s.router.Push(routeWorkspace)
	case commandcenter.TargetTab:
		if !s.projectionHasTab(action.TargetID) {
			s.status = "That tab is gone — pick another from the Command Center."
			return
		}
		s.selectTab(action.TargetID)
		s.router.Push(routeWorkspace)
	case commandcenter.TargetProject:
		if !s.projectionHasProject(action.TargetID) {
			s.status = "That workspace is gone — pick another from the Command Center."
			return
		}
		s.selectProject(action.TargetID)
		s.router.Push(routeWorkspace)
	case commandcenter.TargetAgent:
		if card, ok := s.workbench.directory.Get(agent.AgentKey{InstanceID: s.activeInstance, TerminalID: action.TargetID}); ok {
			s.openAgentCard(card)
			return
		}
		s.status = "That agent is no longer live."
	case commandcenter.TargetHistory:
		s.router.Push("/history/" + action.TargetID)
	default:
		if name, ok := strings.CutPrefix(action.TargetID, deviceActionPrefix); ok {
			s.switchDeviceAction(name)
			return
		}
		// Command actions have no Context during execution. The native window
		// size supplies the same geometry that View gets from c.Size(); in
		// headless mode a zero width means geometry is not yet established.
		switch action.TargetID {
		case "action:add-device":
			s.openDeviceAdd("")
		case "action:open-workspace":
			s.landSelectedPane()
		case "action:open-changes":
			s.goChanges()
		case "action:open-files":
			s.goFiles()
		default:
			if action.TargetID != "" {
				s.router.Push(action.TargetID)
			}
		}
	}
}

// projectionHasTab / projectionHasProject revalidate navigation targets
// against the current snapshot (pure reads, no RPC).
func (s *Shell) projectionHasTab(tabID string) bool {
	for _, tab := range s.projection.Tabs {
		if tab.ID == tabID {
			return true
		}
	}
	return false
}

func (s *Shell) projectionHasProject(projectID string) bool {
	for _, project := range s.projection.Projects {
		if project.ID == projectID {
			return true
		}
	}
	return false
}

// deviceActionPrefix leads the target of a "switch to this device" action; the
// rest is the instance key, or "local" for this Mac.
const deviceActionPrefix = "action:switch-device:"

// deviceCommandActions is the palette's Devices section. A device already in
// use and answering is not offered (picking it would do nothing); one that was
// lost is, as the way to reconnect. This Mac is offered only while a device is
// in use.
func (s *Shell) deviceCommandActions() []commandcenter.Action {
	machines := s.machineInstances()
	actions := make([]commandcenter.Action, 0, len(machines)+2)
	if len(machines) > 0 && s.activeIsRemote() {
		actions = append(actions, commandcenter.Action{
			ID: "device.switch.local", Title: "This Mac", Section: "Devices",
			Keywords: []string{"device", "switch device", "local", "mac"},
			Kind:     commandcenter.TargetRoute, TargetID: deviceActionPrefix + "local", Available: true,
		})
	}
	for _, instance := range machines {
		inUse := instance.Name == s.activeInstance && s.devices.lostFor(instance.Name) == nil
		actions = append(actions, commandcenter.Action{
			ID: "device.switch." + instance.Name, Title: instance.DisplayName, Section: "Devices",
			Keywords: []string{"device", "switch device", "ssh", "remote", instance.Machine.Target},
			Kind:     commandcenter.TargetRoute, TargetID: deviceActionPrefix + instance.Name,
			Available: instance.Machine.Enabled && !inUse,
		})
	}
	return append(actions, commandcenter.Action{
		ID: "device.add", Title: "Add Device", Section: "Devices",
		Keywords: []string{"ssh", "remote", "machine", "connect", "host"},
		Kind:     commandcenter.TargetRoute, TargetID: "action:add-device", Available: true,
	})
}

// switchDeviceAction runs a Devices action, revalidating its target against
// the instance list first like every other palette target.
func (s *Shell) switchDeviceAction(name string) {
	if name == "local" {
		s.returnToThisMac()
		return
	}
	if inst := s.instanceByName(name); inst == nil || inst.Machine == nil {
		s.status = "That device is gone — pick another from the Command Center."
		return
	}
	s.switchToDevice(name)
}
