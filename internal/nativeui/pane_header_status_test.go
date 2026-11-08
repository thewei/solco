package nativeui

import "testing"

// TestPaneHeaderShowsStatusOnlyWhereNothingElseSaysIt pins U12 (2026-11-08):
// a lone Pane under the Tab bar leaves its header without a state slot (the
// Tab chip is that state), split Panes and any Pane while Tabs live in the
// sidebar keep one, and a Pane with no Agent never has one.
func TestPaneHeaderShowsStatusOnlyWhereNothingElseSaysIt(t *testing.T) {
	agent := func(status string) *terminalSurface { return &terminalSurface{paneID: "p1", agentStatus: status} }
	one := map[string]*terminalSurface{"t1": agent("working")}
	two := map[string]*terminalSurface{"t1": agent("working"), "t2": agent("idle")}
	for _, tc := range []struct {
		name     string
		surfaces map[string]*terminalSurface
		sidebar  bool
		status   string
		want     bool
	}{
		{"single pane, tab bar", one, false, "working", false},
		{"split panes, tab bar", two, false, "working", true},
		{"single pane, tabs in sidebar", one, true, "working", true},
		{"no agent, split", two, false, "", false},
		{"no agent, sidebar", one, true, "", false},
	} {
		s := NewShell()
		s.terminals = tc.surfaces
		if tc.sidebar {
			s.settings.General.TabsPlacement = "sidebar"
		}
		if got := s.paneHeaderShowsStatus(agent(tc.status)); got != tc.want {
			t.Errorf("%s: shows status = %v, want %v", tc.name, got, tc.want)
		}
	}
}
