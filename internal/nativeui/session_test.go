package nativeui

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/settings"
)

func TestPersistableRouteNormalization(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{routeWorkspace, routeWorkspace},
		{routeHistory, routeHistory},
		{"/history-projects", "/history-projects"},
		{"/settings/terminal", "/settings/terminal"},
		// Context-scoped detail routes normalize to the workspace page.
		{"/history/session-42", routeWorkspace},
		{"/inspector/pane-7", routeWorkspace},
		{"/chat", routeWorkspace},
		// Routes removed from the app normalize to the workspace too, so a
		// stale persisted route never renders a Not Found page.
		{"/new-task", routeWorkspace},
		{"/status-center", routeWorkspace},
		{"/agents", routeWorkspace},
	}
	for _, tc := range cases {
		if got := persistableRoute(tc.path); got != tc.want {
			t.Fatalf("persistableRoute(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestSessionCaptureRestoreRoundTrip(t *testing.T) {
	store := settings.NewMemoryStore(settings.Default())
	first := NewShell(WithSettings(app.NewSettingsService(store)))
	first.activeInstance = "main"
	first.selectedProjectID = "proj-1"
	first.selectedTabID = "tab-1"
	first.selectedPaneID = "pane-1"
	first.projectsOpen = false
	first.agentsOpen = false
	first.pinsOpen = false
	first.tasksOpen = false
	first.expandedProjects["proj-a"] = true
	first.expandedProjects["proj-b"] = false // cleared entries are not captured
	first.expandedTabs["tab-9"] = true
	first.rightPanel.open = true
	first.rightPanel.surface = SurfaceFiles
	first.rightPanel.width = 460
	first.router.Replace(routeHistory)
	first.SaveSession()

	second := NewShell(WithSettings(app.NewSettingsService(store)))
	if second.visibleRoute != routeHistory || second.router.Path() != routeHistory {
		t.Fatalf("route not restored: visible=%q path=%q", second.visibleRoute, second.router.Path())
	}
	if second.activeInstance != "main" {
		t.Fatalf("instance hint = %q, want main", second.activeInstance)
	}
	if second.selectedProjectID != "proj-1" || second.selectedTabID != "tab-1" || second.selectedPaneID != "pane-1" {
		t.Fatalf("selection hints = %q/%q/%q", second.selectedProjectID, second.selectedTabID, second.selectedPaneID)
	}
	if second.projectsOpen || second.agentsOpen || second.pinsOpen || second.tasksOpen {
		t.Fatal("sidebar section visibility not restored")
	}
	if !second.expandedProjects["proj-a"] || second.expandedProjects["proj-b"] || !second.expandedTabs["tab-9"] {
		t.Fatalf("tree expansion not restored: %+v %+v", second.expandedProjects, second.expandedTabs)
	}
	if !second.rightPanel.open || second.rightPanel.surface != SurfaceFiles || second.rightPanel.width != 460 {
		t.Fatalf("right panel not restored: open=%v surface=%q width=%d", second.rightPanel.open, second.rightPanel.surface, second.rightPanel.width)
	}
}

func TestRestoreSessionKeepsFirstLaunchDefaults(t *testing.T) {
	s := NewShell()
	if s.visibleRoute != routeWorkspace || s.router.Path() != routeWorkspace {
		t.Fatalf("fresh shell route = %q/%q, want workspace", s.visibleRoute, s.router.Path())
	}
	if !s.projectsOpen || !s.agentsOpen || !s.pinsOpen || !s.tasksOpen {
		t.Fatal("fresh shell sidebar sections must stay at their first-launch defaults")
	}
	if s.rightPanel.open {
		t.Fatal("fresh shell panels must stay closed")
	}
}

// TestRestoreSessionDropsRemovedRoutes pins the 2026-10-07 guard: a route
// persisted by an older build (the deleted New Task / Status Center pages)
// must not render a Not Found page at launch — restore falls back to the
// workspace.
func TestRestoreSessionDropsRemovedRoutes(t *testing.T) {
	store := settings.NewMemoryStore(settings.Default())
	store.Save(context.Background(), func() settings.Settings {
		v := settings.Default()
		v.Session = settings.SessionSettings{Route: "/status-center"}
		return v
	}())
	s := NewShell(WithSettings(app.NewSettingsService(store)))
	if s.router.Path() != routeWorkspace || s.visibleRoute != routeWorkspace {
		t.Fatalf("removed route restored as %q/%q, want the workspace", s.visibleRoute, s.router.Path())
	}
}

// A launch that restores an inner page sets visibleRoute itself, so no route
// change ever titled the window: Mission Control and ⌘Tab read "Solco"
// over the Files page (found on the real device, 2026-10-14). The title now
// follows the route from the first frame, and is only handed over when it
// changes.
func TestRestoredRouteTitlesTheNativeWindow(t *testing.T) {
	store := settings.NewMemoryStore(settings.Default())
	store.Save(context.Background(), func() settings.Settings {
		v := settings.Default()
		v.Session = settings.SessionSettings{Route: routeFiles}
		return v
	}())
	s := NewShell(WithSettings(app.NewSettingsService(store)))
	var titles []string
	s.setWindowTitle = func(title string) { titles = append(titles, title) }

	tester := ui.NewTester(s.View, 1100, 760)
	tester.Frame()
	if len(titles) != 1 || titles[0] != "Solco — Files" {
		t.Fatalf("titles after the first frame = %q, want [Solco — Files]", titles)
	}
	tester.Frame()
	if len(titles) != 1 {
		t.Fatalf("an unchanged route re-titled the window: %q", titles)
	}
	s.router.Replace(routeHosts)
	tester.Frame()
	s.router.Replace(routeWorkspace)
	tester.Frame()
	if want := []string{"Solco — Files", "Solco — Hosts", "Solco"}; len(titles) != 3 || titles[1] != want[1] || titles[2] != want[2] {
		t.Fatalf("titles = %q, want %q (the workspace keeps the bare app name)", titles, want)
	}
}

func TestRestoreSessionClampsPanelWidth(t *testing.T) {
	store := settings.NewMemoryStore(settings.Default())
	store.Save(context.Background(), func() settings.Settings {
		v := settings.Default()
		v.Session = settings.SessionSettings{
			Instance:        "main",
			RightPanelOpen:  true,
			RightPanelWidth: 9999,
		}
		return v
	}())
	s := NewShell(WithSettings(app.NewSettingsService(store)))
	if s.rightPanel.width != MaxRightPanelWidth {
		t.Fatalf("restored width = %d, want clamped to %d", s.rightPanel.width, MaxRightPanelWidth)
	}
}

func TestPreferredInstanceName(t *testing.T) {
	instances := []herdr.Instance{
		{Name: "aux"},
		{Name: "main", Default: true},
	}
	cases := []struct {
		name string
		hint string
		want string
	}{
		{"listed hint wins", "aux", "aux"},
		{"stale hint falls back to default", "gone", "main"},
		{"empty hint picks default", "", "main"},
	}
	for _, tc := range cases {
		if got := preferredInstanceName(instances, tc.hint); got != tc.want {
			t.Fatalf("%s: preferredInstanceName(%q) = %q, want %q", tc.name, tc.hint, got, tc.want)
		}
	}
	if got := preferredInstanceName(instances[:1], "gone"); got != "aux" {
		t.Fatalf("no-default fallback = %q, want aux", got)
	}
	if got := preferredInstanceName(nil, "main"); got != "" {
		t.Fatalf("empty instance list = %q, want empty", got)
	}
}

// Startup always opens this Mac: a saved device is never the launch instance,
// whether the session hint names it or Herdr lists it first.
func TestPreferredInstanceNameSkipsDevices(t *testing.T) {
	device := herdr.Instance{Name: herdr.MachineInstanceKey("0123456789abcdef0123456789abcdef"), DisplayName: "Build box", Machine: &herdr.Machine{ID: "0123456789abcdef0123456789abcdef", Label: "Build box", Enabled: true}}
	instances := []herdr.Instance{device, {Name: "aux"}, {Name: "main", Default: true}}
	if got := preferredInstanceName(instances, device.Name); got != "main" {
		t.Fatalf("device hint = %q, want the local default main", got)
	}
	if got := preferredInstanceName(instances, ""); got != "main" {
		t.Fatalf("empty hint = %q, want the local default main", got)
	}
	if got := preferredInstanceName([]herdr.Instance{device, {Name: "aux"}}, ""); got != "aux" {
		t.Fatalf("no local default = %q, want the first local instance aux", got)
	}
	if got := preferredInstanceName([]herdr.Instance{device}, device.Name); got != "" {
		t.Fatalf("devices only = %q, want empty: startup never dials a device", got)
	}
}
