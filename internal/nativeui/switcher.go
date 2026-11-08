package nativeui

import (
	"fmt"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
)

// SwitchAgentFromMenu is the Window menu's Switch Agent (⌃⇥, backwards ⌃⇧⇥)
// — the Agent MRU switcher (0.5 §13): the first press opens the overlay on the
// live MRU order, each press after it cycles (delta -1 backwards), and Enter or
// a click commits local navigation, Esc cancels. The keys are menu accelerators
// because no other way reaches them: AppKit takes Control-Tab as the key-view
// loop before any view's keyDown runs, so the in-view c.Shortcut this switcher
// used to listen on never fired (2026-11-08 probe; the 2026-10-06 audit's F43
// "cannot be verified" was this). MyGo exposes no view-visible Control-key
// release either, so release-to-commit is adapted to Enter/click; the deviation
// is recorded in the 0.5 audit.
func (s *Shell) SwitchAgentFromMenu(delta int) {
	s.applyOnWindow(func() {
		// Startup and the first-run guide own the canvas: no overlays, and no
		// key mutating state behind them.
		if s.bootstrapPending || s.bootstrapFailed || s.router.Path() == routeOnboarding {
			return
		}
		if s.workbench.switcherOpen {
			s.stepAgentSwitcher(delta)
			return
		}
		s.openAgentSwitcher()
		if s.workbench.switcherOpen && delta < 0 {
			s.stepAgentSwitcher(delta) // ⌃⇧⇥ from closed starts from the other end
		}
	})
}

// stepAgentSwitcher moves the highlight by delta through the snapshot-order
// list, wrapping.
func (s *Shell) stepAgentSwitcher(delta int) {
	cards := s.AgentMRUList()
	if len(cards) == 0 {
		s.workbench.switcherOpen = false
		return
	}
	s.workbench.switcherIndex = ((s.workbench.switcherIndex+delta)%len(cards) + len(cards)) % len(cards)
}

// openAgentSwitcher snapshots the current live MRU ordering.
func (s *Shell) openAgentSwitcher() {
	cards := s.AgentMRUList()
	if len(cards) == 0 {
		return
	}
	s.workbench.switcherOpen = true
	s.workbench.switcherIndex = 0
	s.workbench.switcherFresh = true
	s.workbench.switcherPointer = switcherPointer{}
	if card, ok := s.workbench.directory.Get(selectedAgentKeyOf(s)); ok {
		s.workbench.switcherOrigin = card.Key
		s.workbench.switcherHad = true
	} else {
		s.workbench.switcherHad = false
	}
}

func selectedAgentKeyOf(s *Shell) agent.AgentKey {
	if pane := s.selectedPane(); pane != nil {
		return agent.AgentKey{InstanceID: s.activeInstance, TerminalID: pane.TerminalID}
	}
	return agent.AgentKey{}
}

func (s *Shell) closeAgentSwitcher(restore bool) {
	s.workbench.switcherOpen = false
	if restore && s.workbench.switcherHad {
		if card, ok := s.workbench.directory.Get(s.workbench.switcherOrigin); ok {
			s.openAgentCard(card)
		}
	}
}

func (s *Shell) commitAgentSwitcher() {
	cards := s.AgentMRUList()
	if s.workbench.switcherIndex < 0 || s.workbench.switcherIndex >= len(cards) {
		s.closeAgentSwitcher(false)
		return
	}
	card := cards[s.workbench.switcherIndex]
	s.workbench.switcherOpen = false
	s.openAgentCard(card)
}

// agentSwitcherOverlay renders the MRU overlay while it is open (0.5 §13):
// provider mark, title, compact status per row; hover or arrows highlight;
// click or Enter commits; Esc cancels.
func (s *Shell) agentSwitcherOverlay(c *ui.Context) {
	if !s.workbench.switcherOpen {
		return
	}
	cards := s.AgentMRUList()
	if len(cards) == 0 {
		s.workbench.switcherOpen = false
		return
	}
	sp := Spacing()
	fresh := s.workbench.switcherFresh
	s.workbench.switcherFresh = false
	ui.Modal(c, &s.workbench.switcherOpen, func() {
		// Cycle/commit/cancel keys are handled inside the modal context:
		// shortcuts registered outside it are not delivered while a modal
		// is up.
		s.handleSwitcherKeys(c)
		// Liquid Glass (MyGo 0.2.15), matching the Command Center palette.
		// The dialog takes the keyboard as it opens (no ring: it is no
		// control): a modal with nothing focusable in it leaves the focus
		// where it was, and Enter then goes to whatever holds it — a
		// sidebar row opened the Agent again instead of committing the
		// highlighted one (2026-11-08, real run), a focused button would
		// be pressed. Closing hands the focus back to where it was.
		ui.Column(c).Width(460).Padding(sp.L).Gap(sp.XS).
			Radius(Radius().Card).Material(glass.Glass{}).
			Border(1, designTokens(c.Theme().Dark).BorderSubtle).
			Focusable().FocusRing(false).AutoFocus().Children(func() {
			ui.Text(c, "Switch Agent").FontSize(Typography().Section).FontWeight(WeightSemibold)
			ui.Text(c, fmt.Sprintf("Ctrl-Tab cycle · Enter commit · Esc cancel (%d agent%s)", len(cards), pluralS(len(cards)))).
				FontSize(Typography().Caption).TextColor(c.Theme().TextMuted)
			for index, card := range cards {
				index, card := index, card
				highlighted := index == s.workbench.switcherIndex
				var row ui.Element
				ui.Box(c).Children(func() {
					row = ui.Row(c).FillWidth().Padding(sp.S, sp.M).Gap(sp.S).
						AlignItems(ui.Center).Radius(Radius().Control).Children(func() {
						providerBadge(c, card.Provider)
						ui.Text(c, card.Title).FontSize(Typography().Body).Grow(1).SingleLine()
						statusPill(c, agent.OperationalLabel(card.Attention), operationalTone(card.Attention))
					})
				})
				if highlighted {
					row.Background(designTokens(c.Theme().Dark).StatusBackground(ToneInfo, c.Theme().Dark))
				}
				if x, y, over := row.PointerPosition(); over {
					s.hoverSwitcherRow(index, x, y, fresh)
				}
				if row.Clicked() {
					s.workbench.switcherIndex = index
					s.commitAgentSwitcher()
				}
			}
		})
	})
}

// switcherPointer is the place the pointer was last seen over the overlay: the
// row and the spot in it, in the row's own coordinates.
type switcherPointer struct {
	seen bool
	row  int
	x, y float32
}

// hoverSwitcherRow lets the pointer choose a row only once it moves. The
// overlay opens in the middle of the window, where the pointer often rests, and
// a row that stays hovered would put the highlight back on itself every frame,
// undoing each ⌃⇥ (2026-11-08: with the pointer at rest over a row the
// keyboard could not leave it). A pointer already over a row as the overlay
// builds for the first time is the one at rest; any other, and any move after,
// chooses the row it is on.
func (s *Shell) hoverSwitcherRow(index int, x, y float32, fresh bool) {
	here := switcherPointer{seen: true, row: index, x: x, y: y}
	p := &s.workbench.switcherPointer
	if fresh && !p.seen {
		*p = here
		return
	}
	if *p == here {
		return
	}
	*p = here
	s.workbench.switcherIndex = index
}

// handleSwitcherKeys runs inside the switcher modal: Enter commits, Esc
// cancels. Cycling is the Window menu's ⌃⇥ / ⌃⇧⇥ (SwitchAgentFromMenu).
func (s *Shell) handleSwitcherKeys(c *ui.Context) {
	if len(s.AgentMRUList()) == 0 {
		s.workbench.switcherOpen = false
		return
	}
	switch {
	case c.Shortcut(0, ui.KeyEscape):
		s.closeAgentSwitcher(true)
	case c.Shortcut(0, ui.KeyEnter):
		s.commitAgentSwitcher()
	}
}
