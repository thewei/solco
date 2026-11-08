package nativeui

/**
 * [INPUT]: main shell routes (the retired inspector's texts must never come back)
 * [OUTPUT]: assert retired right rail has zero presentation on all routes
 * [POS]: removal regression for the former shared contextual inspector
 * [PROTOCOL]: update header and check CLAUDE.md
 */

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestRetiredRightRailDoesNotRenderOnAnyRoute(t *testing.T) {
	s := rightPanelTestShell(t, t.TempDir())
	tt := ui.NewTester(s.View, 1200, 800)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	for _, path := range []string{routeWorkspace, routeHistory, "/history/b", routeHosts, routeSettings} {
		s.router.Replace(path)
		tt.Frame()
		for _, removed := range []string{"History details", "Workspace Tools", "Listening Ports & Local Preview", "Review Inspector"} {
			if tt.HasText(removed) {
				t.Fatalf("retired right rail leaked %q on %s", removed, path)
			}
		}
	}
}
