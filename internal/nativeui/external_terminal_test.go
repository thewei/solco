package nativeui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTerminalURL(t *testing.T) {
	long := "/" + strings.Repeat("a", terminalCWDMax)
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"solco://terminal?cwd=%2FUsers%2Fme%2Fapp", "/Users/me/app", true},
		{"solco://terminal?cwd=%2FUsers%2Fme%2Fapp%2F..%2Fdocs%2F", "/Users/me/docs", true},
		{"solco://terminal?cwd=%2Ftmp%2Fa%20b", "/tmp/a b", true},
		{"solco://terminal?cwd=%2Ftmp&command=rm", "/tmp", true}, // a command is never read
		{"solco://terminal", "", false},
		{"solco://terminal?cwd=", "", false},
		{"solco://terminal?cwd=relative%2Fpath", "", false},
		{"solco://terminal?cwd=%2Ftmp%00x", "", false},
		{"solco://terminal?cwd=" + long, "", false},
		{"solco://reveal?cwd=%2Ftmp", "", false},
		{"https://terminal?cwd=%2Ftmp", "", false},
		{"::not a url", "", false},
	}
	for _, tc := range cases {
		got, ok := parseTerminalURL(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseTerminalURL(%.60q) = %q, %v; want %q, %v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

// terminalShell is a Shell on a projection whose Project w2 is rooted at dir.
func terminalRoutingShell(t *testing.T) (*Shell, string) {
	t.Helper()
	s := revealShell()
	dir := t.TempDir()
	s.projection.Projects[1].CWD = dir
	return s, dir
}

func TestOpenTerminalAtShowsTheWorkspaceRootedThere(t *testing.T) {
	s, dir := terminalRoutingShell(t)
	s.createFolderWorkspace = func(string) { t.Error("a Workspace already roots there: nothing is created") }

	s.openTerminalAt(dir)

	if s.router.Path() != routeWorkspace || s.selectedProjectID != "w2" {
		t.Fatalf("route=%q project=%q, want the workspace page on w2", s.router.Path(), s.selectedProjectID)
	}
	if s.pendingTerminal != nil {
		t.Fatalf("request left pending: %+v", s.pendingTerminal)
	}
}

func TestOpenTerminalAtFollowsASymlinkedFolder(t *testing.T) {
	s, dir := terminalRoutingShell(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skip("no symlinks: ", err)
	}
	s.createFolderWorkspace = func(string) { t.Error("the symlink names the same folder") }
	s.openTerminalAt(link)
	if s.selectedProjectID != "w2" {
		t.Fatalf("project=%q, want w2", s.selectedProjectID)
	}
}

func TestOpenTerminalAtCreatesAWorkspaceForAFolderNobodyRoots(t *testing.T) {
	s, _ := terminalRoutingShell(t)
	other := t.TempDir()
	var made []string
	s.createFolderWorkspace = func(cwd string) { made = append(made, cwd) }

	s.openTerminalAt(other)

	if len(made) != 1 || made[0] != other {
		t.Fatalf("created %q, want one Workspace at %q", made, other)
	}
	if s.router.Path() != routeWorkspace || s.selectedProjectID == "w2" {
		t.Errorf("route=%q project=%q: the workspace page, and not on a Workspace that is somewhere else", s.router.Path(), s.selectedProjectID)
	}
}

func TestOpenTerminalAtRefusesAFolderThatIsGone(t *testing.T) {
	s, _ := terminalRoutingShell(t)
	s.createFolderWorkspace = func(string) { t.Error("nothing is created for a missing folder") }
	s.openTerminalAt(filepath.Join(t.TempDir(), "gone"))
	if s.pendingTerminal != nil || s.pendingToast == "" || s.router.Path() == routeWorkspace {
		t.Errorf("a missing folder says so and goes nowhere: pending=%v toast=%q route=%q", s.pendingTerminal != nil, s.pendingToast, s.router.Path())
	}
}

func TestOpenTerminalAtWaitsForTheSessionAndExpires(t *testing.T) {
	s, dir := terminalRoutingShell(t)
	s.activeInstance = ""
	s.createFolderWorkspace = func(string) { t.Error("no creation before a Session is there") }
	s.openTerminalAt(dir)
	if s.pendingTerminal == nil || s.router.Path() == routeWorkspace {
		t.Fatal("with no Session the request waits")
	}

	s.activeInstance = "default"
	s.applyProjection(s.projection, false)
	if s.pendingTerminal != nil || s.selectedProjectID != "w2" {
		t.Fatalf("the projection resumes it: pending=%v project=%q", s.pendingTerminal != nil, s.selectedProjectID)
	}

	s.activeInstance = ""
	s.openTerminalAt(dir)
	s.pendingTerminal.at = s.pendingTerminal.at.Add(-2 * revealPendingTTL)
	s.activeInstance = "default"
	s.selectedProjectID = "w1"
	s.applyProjection(s.projection, false)
	if s.pendingTerminal != nil || s.selectedProjectID != "w1" {
		t.Fatalf("an expired request is dropped, not acted on: pending=%v project=%q", s.pendingTerminal != nil, s.selectedProjectID)
	}
}
