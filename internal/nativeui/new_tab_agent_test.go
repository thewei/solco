package nativeui

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: Shell, tabBarShell, newTabStarter 假实现
 * [OUTPUT]: Tab 条「+ ▾」的 MRU 排序、+ 复用上次种类、▾ 菜单选择启动 Agent 的回归
 * [POS]: 2026-11-01 新建 Tab 选 Agent（议题 7）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type fakeStarter struct {
	kinds []app.SessionKind
	specs []app.SessionSpec
	pane  string
	err   error
}

func (f *fakeStarter) Kinds() []app.SessionKind { return f.kinds }
func (f *fakeStarter) StartSession(_ context.Context, _ string, spec app.SessionSpec) (app.SessionResult, error) {
	f.specs = append(f.specs, spec)
	return app.SessionResult{PaneID: f.pane}, f.err
}

var testKinds = []app.SessionKind{
	{ID: "terminal", Label: "Terminal"},
	{ID: "claude-code", Label: "Claude Code"},
	{ID: "codex", Label: "Codex"},
	{ID: "pi", Label: "Pi"},
}

func ids(kinds []app.SessionKind) []string {
	var out []string
	for _, k := range kinds {
		out = append(out, k.ID)
	}
	return out
}

func TestOrderLaunchKindsPutsRecentFirstAndTerminalLast(t *testing.T) {
	got := ids(orderLaunchKinds(testKinds, []string{"codex", "terminal", "gone", "codex"}))
	if want := []string{"codex", "claude-code", "pi", "terminal"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if got := ids(orderLaunchKinds(testKinds, nil)); got[len(got)-1] != "terminal" {
		t.Fatalf("Terminal must stay last with no history: %v", got)
	}
}

func TestRememberLaunchKindIsMRUAndBounded(t *testing.T) {
	recent := []string{"a", "b", "c"}
	if got := rememberLaunchKind(recent, "c"); !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Fatalf("got %v", got)
	}
	long := []string{"1", "2", "3", "4", "5", "6", "7", "8"}
	if got := rememberLaunchKind(long, "9"); len(got) != recentLaunchKindsKept || got[0] != "9" {
		t.Fatalf("bounded list = %v", got)
	}
}

func TestLastKindIsWhatShortcutsStartAndDefaultsToTerminal(t *testing.T) {
	fake := &fakeStarter{kinds: testKinds}
	s := tabBarShell(2, "t1")
	s.newTabStarter = fake
	s.launchKinds = testKinds
	s.recentLaunchKinds = []string{"codex"}
	s.startNewTab(s.projection.Projects[0], s.lastLaunchKind(s.launchKinds))
	if len(fake.specs) != 1 || fake.specs[0].Agent != history.AgentCodex || fake.specs[0].WorkspaceID != "w1" {
		t.Fatalf("specs = %+v, want one Codex launch in w1", fake.specs)
	}
	if s.recentLaunchKinds[0] != "codex" {
		t.Fatalf("recent = %v", s.recentLaunchKinds)
	}

	s.recentLaunchKinds = nil
	if got := s.lastLaunchKind(testKinds); got.ID != "terminal" {
		t.Fatalf("no history must default to Terminal, got %+v", got)
	}
	if got := (&Shell{recentLaunchKinds: []string{"gone"}}).lastLaunchKind(testKinds); got.ID != "terminal" {
		t.Fatalf("an unavailable kind must fall back to Terminal, got %+v", got)
	}
}

func TestStartNewTabAgentRemembersKind(t *testing.T) {
	fake := &fakeStarter{kinds: testKinds}
	s := tabBarShell(1, "t1")
	s.newTabStarter = fake
	s.startNewTab(s.projection.Projects[0], app.SessionKind{ID: "pi", Label: "Pi"})
	if len(fake.specs) != 1 || fake.specs[0].Agent != history.AgentPi || s.recentLaunchKinds[0] != "pi" {
		t.Fatalf("specs=%+v recent=%v", fake.specs, s.recentLaunchKinds)
	}
}

func TestCmdTFollowsTheLastKind(t *testing.T) {
	fake := &fakeStarter{kinds: testKinds}
	s := tabBarShell(1, "t1")
	s.newTabStarter = fake
	s.launchKinds = testKinds
	s.recentLaunchKinds = []string{"claude-code"}
	s.NewTabFromMenu()
	if len(fake.specs) != 1 || fake.specs[0].Agent != history.AgentClaudeCode {
		t.Fatalf("⌘T specs = %+v, want one Claude Code launch", fake.specs)
	}
}

func TestPendingAgentOpenDropsWhenTheUserMovesOn(t *testing.T) {
	s := tabBarShell(1, "t1")
	s.pendingAgentOpen = &pendingAgentOpen{PaneID: "gone", At: time.Now()}
	s.completeAgentOpen()
	if s.pendingAgentOpen != nil {
		t.Fatal("a pane that is no longer selected must not be pulled into Chat")
	}
}

func TestPendingAgentOpenLandsOnChatOnceTheCardExistsWithoutAnIdentity(t *testing.T) {
	s := tabBarShell(2, "t2")
	s.pendingAgentOpen = &pendingAgentOpen{PaneID: "pt2", At: time.Now()}
	s.completeAgentOpen()
	if s.pendingAgentOpen != nil {
		t.Fatal("the pending open must be consumed once the card exists")
	}
	if s.surface.current() != WorkspaceSurfaceChat || s.paneSurfaceMemo["pt2"] != WorkspaceSurfaceChat {
		t.Fatalf("surface=%v memo=%v, want Chat", s.surface.current(), s.paneSurfaceMemo["pt2"])
	}
}

func TestStartNewTabAgentLandsOnChatWhenTheLaunchShipsTheCard(t *testing.T) {
	fake := &fakeStarter{kinds: testKinds, pane: "p-new"}
	s := tabBarShell(1, "t1")
	s.newTabStarter = fake
	s.settings.General.AgentView = "chat"
	// Hermetic shell: drop whatever selected pane / surface memo the
	// machine's persisted session state restored, so the assertion reads
	// only this test's launch.
	s.clearLocalSelection()
	s.paneSurfaceMemo = nil
	s.selectedProjectID, s.selectedTabID, s.selectedPaneID = "w1", "t1", "pt1"
	s.surface.resetToTerminal(s.workspaceContext())
	// A real launch fetches the projection that already carries the new
	// pane's agent entry (2026-11-02 surface-flash loop); with no runtime the
	// launch reads THIS shell's projection, so the pane and its agent ride it
	// — the same shape reconcileWorkbench rebuilds the workbench directory
	// from, so the card survives the rebuild during the launch.
	s.runtime = nil
	s.projection.Panes = append(s.projection.Panes, herdr.Pane{ID: "p-new", ProjectID: "w1", TabID: "t1"})
	s.projection.Agents = append(s.projection.Agents, herdr.Agent{PaneID: "p-new", Kind: "pi", TerminalID: "term-new", Status: "working"})
	s.startNewTab(s.projection.Projects[0], app.SessionKind{ID: "pi", Label: "Pi"})
	if s.selectedPaneID != "p-new" {
		t.Fatalf("selection = %q, want the launched pane", s.selectedPaneID)
	}
	if s.surface.current() != WorkspaceSurfaceChat {
		t.Fatalf("surface = %v, want Chat: the launch intent with Agent view = chat must land Chat the moment the pane ships its card", s.surface.current())
	}
}
