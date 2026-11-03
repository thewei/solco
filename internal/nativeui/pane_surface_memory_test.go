package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: Depends on Shell, workspaceSurfaceState, paneSurfaceMemo and captureSession
 * [OUTPUT]: Tests for the per-pane surface memory (2026-10-31): navigation back to a pane restores its remembered Chat/Terminal surface
 * [POS]: Tree pane switch, Tab switch, Chat-leaving flip, dead-Agent fallback and SaveSession persistence regressions
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func surfaceMemoryShell(t *testing.T) *Shell {
	t.Helper()
	shell := NewShell()
	shell.loading = false
	shell.activeInstance = "default"
	shell.projection = herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "demo", CWD: "/tmp"}},
		Tabs: []herdr.Tab{
			{ID: "t1", ProjectID: "w1", Label: "main"},
			{ID: "t2", ProjectID: "w1", Label: "second"},
		},
		Panes: []herdr.Pane{
			{ID: "p1", ProjectID: "w1", TabID: "t1", TerminalID: "term-1"},
			{ID: "p2", ProjectID: "w1", TabID: "t1", TerminalID: "term-2"},
			{ID: "p3", ProjectID: "w1", TabID: "t2", TerminalID: "term-3"},
		},
	}
	shell.workbench.directory = agent.NewAgentDirectory()
	shell.workbench.directory.Replace([]agent.AgentCardModel{
		{Key: agent.AgentKey{InstanceID: "default", TerminalID: "term-1"}, PaneID: "p1", TabID: "t1", ProjectID: "w1", Title: "Scout", Provider: history.AgentCodex},
		{Key: agent.AgentKey{InstanceID: "default", TerminalID: "term-2"}, PaneID: "p2", TabID: "t1", ProjectID: "w1", Title: "Ranger", Provider: history.AgentClaudeCode},
	})
	shell.selectedProjectID = "w1"
	shell.selectedTabID = "t1"
	shell.selectedPaneID = "p1"
	shell.surface.resetToTerminal(shell.workspaceContext())
	return shell
}

func TestPaneSurfaceMemoryRestoresChatOnNavigation(t *testing.T) {
	shell := surfaceMemoryShell(t)

	// The user opens p1's Chat; the pane's surface memory records Chat.
	shell.showSurface(WorkspaceSurfaceChat)
	if shell.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("setup surface = %v, want Chat", shell.surface.current())
	}

	// A tree click on the sibling pane lands on Terminal (never used in Chat).
	shell.selectPane("p2")
	if shell.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("pane switch surface = %v, want Terminal", shell.surface.current())
	}

	// Switching back to p1 returns its remembered Chat, rebound to p1.
	shell.selectPane("p1")
	if shell.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("pane return surface = %v, want Chat", shell.surface.current())
	}
	if shell.chatPaneID != "p1" {
		t.Fatalf("chatPaneID = %q, want p1", shell.chatPaneID)
	}

	// Another Tab: p3 has no memory, so it lands on Terminal…
	shell.selectTab("t2")
	if shell.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("fresh tab surface = %v, want Terminal", shell.surface.current())
	}
	// …and back to t1, whose first pane (p1) remembers Chat.
	shell.selectTab("t1")
	if shell.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("tab return surface = %v, want Chat", shell.surface.current())
	}
	if shell.chatPaneID != "p1" {
		t.Fatalf("tab return chatPaneID = %q, want p1", shell.chatPaneID)
	}
}

func TestPaneSurfaceMemoryLeavesChatForTerminalPane(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.showSurface(WorkspaceSurfaceChat)
	// p2 was last used on Terminal: leaving p1's Chat for it must switch.
	shell.rememberPaneSurface("p2", WorkspaceSurfaceTerminal)

	shell.selectPane("p2")
	if shell.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("surface = %v, want Terminal", shell.surface.current())
	}
}

func TestPaneSurfaceMemoryFallsBackWhenChatUnavailable(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.showSurface(WorkspaceSurfaceChat) // p1 remembers Chat
	// The Agent is gone (card pruned): the memory must not fake a Chat.
	shell.workbench.directory.Replace(nil)

	shell.selectTab("t2")
	if shell.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("fresh tab surface = %v, want Terminal", shell.surface.current())
	}
	shell.selectTab("t1")
	if shell.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("return without an Agent = %v, want Terminal", shell.surface.current())
	}
}

func TestPaneSurfaceMemoryPersistsInSession(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.showSurface(WorkspaceSurfaceChat)
	shell.rememberPaneSurface("p2", WorkspaceSurfaceTerminal)

	snapshot := shell.captureSession()
	if snapshot.PaneSurfaces["p1"] != string(WorkspaceSurfaceChat) {
		t.Fatalf("p1 surface = %q, want chat", snapshot.PaneSurfaces["p1"])
	}
	if snapshot.PaneSurfaces["p2"] != string(WorkspaceSurfaceTerminal) {
		t.Fatalf("p2 surface = %q, want terminal", snapshot.PaneSurfaces["p2"])
	}
}

// Settings → General → Agent view = Chat is only the default for a pane with
// no memory: an Agent pane opens in Chat, a plain shell stays on the
// Terminal, and a pane the user switched to Terminal keeps that choice.
func TestDefaultAgentViewChatOpensFreshAgentPanesInChat(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.settings.General.AgentView = "chat"

	shell.selectPane("p2") // an Agent pane, never used
	if shell.surface.current() != WorkspaceSurfaceChat || shell.chatPaneID != "p2" {
		t.Fatalf("fresh Agent pane = %v/%q, want Chat on p2", shell.surface.current(), shell.chatPaneID)
	}
	if !shell.panePrefersChat("p2") || shell.panePrefersChat("p3") {
		t.Fatal("only an Agent pane may prefer Chat; p3 is a plain shell")
	}

	shell.showSurface(WorkspaceSurfaceTerminal) // the user's own switch wins from now on
	shell.selectPane("p1")
	shell.selectPane("p2")
	if shell.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("a pane switched to Terminal came back as %v", shell.surface.current())
	}
}

func TestDefaultAgentViewTUIKeepsTheTerminal(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.selectPane("p2")
	if shell.surface.current() != WorkspaceSurfaceTerminal || shell.panePrefersChat("p2") {
		t.Fatalf("default setting must stay Terminal, got %v", shell.surface.current())
	}
}

// 2026-11-03 surface-owner unification: the header home tab (and every
// other "return to the workspace" launcher) is navigation — it must land
// the selected pane's remembered surface and never rewrite the pane-surface
// memory. A Chat-pinned pane stays Chat when the user pages away and comes
// back through it.
func TestHomeTabKeepsAPinnedChatPaneOnChat(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.showSurface(WorkspaceSurfaceChat) // the user pinned p1 on Chat
	tester := ui.NewTester(shell.View, 1200, 800)
	tester.Frame()

	if err := tester.Click("Terminal view"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if shell.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("home tab landed a Chat-pinned pane on %v", shell.surface.current())
	}
	if got := shell.captureSession().PaneSurfaces["p1"]; got != string(WorkspaceSurfaceChat) {
		t.Fatalf("home tab rewrote p1's memory to %q", got)
	}
}

func TestFilesTabExitKeepsAPinnedChatPaneOnChat(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.showSurface(WorkspaceSurfaceChat)
	shell.showViewFiles()
	if shell.router.Path() != routeFiles {
		t.Fatalf("setup route = %q, want Files", shell.router.Path())
	}

	shell.showViewFiles() // the second click is the way back
	if shell.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("leaving Files landed a Chat-pinned pane on %v", shell.surface.current())
	}
	if got := shell.captureSession().PaneSurfaces["p1"]; got != string(WorkspaceSurfaceChat) {
		t.Fatalf("the Files exit rewrote p1's memory to %q", got)
	}
}

// landSelectedPane — the shared owner behind the home tab, ⌘1 / Go-menu
// Workspace, the palette Workspace action and the view-tab exits — lands the
// remembered surface directly.
func TestLandSelectedPaneRestoresTheRememberedSurface(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.showSurface(WorkspaceSurfaceChat)
	shell.showViewFiles()

	shell.landSelectedPane()
	if shell.router.Path() != routeWorkspace || shell.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("home landing = %s/%v, want the workspace on Chat", shell.router.Path(), shell.surface.current())
	}
}

// The landing is navigation: when Chat cannot open (the Agent card is
// pruned) the pane falls to Terminal without erasing the Chat memory, so a
// later reconciliation can restore it.
func TestLandSelectedPaneKeepsChatMemoryWhenChatUnavailable(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.showSurface(WorkspaceSurfaceChat) // p1 pinned on Chat
	shell.workbench.directory.Replace(nil)  // the Agent is gone
	shell.surface.resetToTerminal(shell.workspaceContext())

	shell.landSelectedPane()
	if shell.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("surface without an Agent = %v, want Terminal", shell.surface.current())
	}
	if !shell.panePrefersChat("p1") {
		t.Fatal("landing Terminal must not erase p1's Chat memory")
	}
}

// A plain shell has no Chat preference: home lands Terminal and records
// nothing — incidental navigation never writes the memory.
func TestLandSelectedPaneRecordsNothingForAPlainPane(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.selectPane("p3")

	shell.landSelectedPane()
	if shell.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("plain pane surface = %v, want Terminal", shell.surface.current())
	}
	if got := shell.captureSession().PaneSurfaces["p3"]; got != "" {
		t.Fatalf("plain pane memory written as %q, want none", got)
	}
}

// goToPane — the Sessions page's "Go to Pane" — rides the same owner: a
// Chat-pinned pane is never flipped to its terminal.
func TestGoToPaneKeepsAPinnedChatPaneOnChat(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.showSurface(WorkspaceSurfaceChat)
	shell.selectPane("p2") // p2 is on Terminal
	shell.rememberPaneSurface("p1", WorkspaceSurfaceChat)

	shell.goToPane("p1")
	if shell.router.Path() != routeWorkspace || shell.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("go to pane = %s/%v, want the workspace on Chat", shell.router.Path(), shell.surface.current())
	}
	if got := shell.captureSession().PaneSurfaces["p1"]; got != string(WorkspaceSurfaceChat) {
		t.Fatalf("go to pane rewrote p1's memory to %q", got)
	}
}

// openChat's no-source fallback rides the same ownership: a pane pinned on
// Chat opens the invitation (the page's own empty state) and keeps its
// memory — the old fallback stamped Terminal without even showing one.
func TestOpenChatKeepsAPinnedSessionlessPaneOnItsInvitation(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.showSurface(WorkspaceSurfaceTerminal)
	shell.rememberPaneSurface("p1", WorkspaceSurfaceChat) // pinned, no session yet
	card, _ := shell.agentCardForPane("p1")

	shell.openChat(card)
	if shell.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("sessionless pinned pane = %v, want the Chat invitation", shell.surface.current())
	}
	if !shell.panePrefersChat("p1") {
		t.Fatal("the sessionless fallback must not rewrite the Chat memory")
	}
}

// Without a Chat preference the announced Terminal landing stands — through
// the shared owner, so incidental navigation writes no memory.
func TestOpenChatLandsASessionlessPlainPreferenceOnTerminal(t *testing.T) {
	shell := surfaceMemoryShell(t)
	shell.showSurface(WorkspaceSurfaceDiff)
	card, _ := shell.agentCardForPane("p1") // no memory: settings default is Terminal

	shell.openChat(card)
	if shell.surface.current() != WorkspaceSurfaceTerminal {
		t.Fatalf("sessionless unpinned pane = %v, want Terminal", shell.surface.current())
	}
	if shell.pendingToast == "" {
		t.Fatal("the Terminal fallback must keep its hint")
	}
	if got := shell.captureSession().PaneSurfaces["p1"]; got != "" {
		t.Fatalf("the Terminal fallback wrote p1's memory as %q, want none", got)
	}
}
