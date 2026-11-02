package nativeui

/**
 * [INPUT]: Shell 假投影（herdr.Project/Agents）与假 newTabStarter（fakeStarter）、sidebarSectionHeader 的 Menu 型 Action 常驻槽、ui.Tester 的悬停/菜单模拟
 * [OUTPUT]: Agents 区排序回归——头部排序槽点击开菜单、指针离开行后选项仍必达（Menu 型 Action 的存在理由）、time/status 扁平顺序与分组默认、组行悬停 "+" 走 startNewTab 上次种类且不折叠分组、AgentsSort 随会话捕获/恢复
 * [POS]: 2026-11-02 用户要求（Agents 标题排序菜单 + 分组行新建 Pane）；悬停条件菜单会丢选择（探针实证），故槽按钮键控常驻
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/settings"
)

// agentsSortShell seeds two Workspace groups: the "core" agent is blocked and
// worked two hours ago, the "site" agent worked last. Both Workspaces are in
// the projection, so the groups carry their "+".
func agentsSortShell(t *testing.T) (*Shell, *ui.Tester) {
	t.Helper()
	s := NewShell()
	s.loading = false
	s.workbench.markers = agent.NewMarkerStore()
	s.workbench.directory = agent.NewAgentDirectory()
	s.projection.Agents = []herdr.Agent{{PaneID: "p1"}, {PaneID: "p2"}}
	s.projection.Projects = []herdr.Project{
		{ID: "w-core", Label: "core", CWD: "/core"},
		{ID: "w-site", Label: "site", CWD: "/site"},
	}
	late := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	s.workbench.directory.Replace([]agent.AgentCardModel{
		{
			Key: agent.AgentKey{TerminalID: "term-core"}, Provider: history.AgentPi,
			Title: "core-agent", ProjectID: "w-core", ProjectName: "core", PaneID: "p1",
			RuntimePhase: agent.PhaseBlocked, LastActive: late.Add(-2 * time.Hour),
		},
		{
			Key: agent.AgentKey{TerminalID: "term-site"}, Provider: history.AgentPi,
			Title: "site-agent", ProjectID: "w-site", ProjectName: "site", PaneID: "p2",
			LastActive: late,
		},
	})
	tester := ui.NewTester(s.View, 1200, 800)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	settleFrames(tester, 2)
	return s, tester
}

// firstIndex returns where a text first appears in the frame's reading order.
func firstIndex(texts []string, want string) int {
	for i, text := range texts {
		if text == want {
			return i
		}
	}
	return -1
}

// TestAgentsSortMenuDeliversAwayFromTheRow opens the header's sort menu and
// chooses an item after the pointer has left the row — the popup flow, where
// a hover-conditional menu's element would already be gone and the choice
// would drop (probed 2026-11-02). The list reorders and the header click
// must not have folded the section.
func TestAgentsSortMenuDeliversAwayFromTheRow(t *testing.T) {
	s, tester := agentsSortShell(t)
	if _, ok := tester.Find("Sort Agents"); !ok {
		t.Fatal("the Agents header carries no sort slot")
	}
	if err := tester.Click("Sort Agents"); err != nil {
		t.Fatal(err)
	}
	menu := tester.Menu()
	for _, want := range []string{"By Group", "By Time", "By Status"} {
		if !containsText(menu, want) {
			t.Fatalf("sort menu %q missing %q", menu, want)
		}
	}
	// The pointer travels into the popup, off the header row.
	tester.Move(1150, 780)
	if err := tester.ChooseMenuItem("By Time"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if s.agentsSortMode() != agentSortTime {
		t.Fatalf("sort after the away-choice = %q, want time", s.agentsSortMode())
	}
	if !s.agentsOpen {
		t.Fatal("opening the sort menu must not fold the section")
	}
	texts := tester.Texts()
	if first, last := firstIndex(texts, "site-agent"), firstIndex(texts, "core-agent"); first < 0 || first > last {
		t.Fatalf("time order wrong: site-agent@%d core-agent@%d", first, last)
	}
	if _, ok := tester.Find("Toggle core"); ok {
		t.Fatal("time mode must drop the group labels")
	}
}

// TestAgentsSortMenuChecksTheCurrentMode cycles the remaining modes through
// the same menu, ending back at the default.
func TestAgentsSortMenuChecksTheCurrentMode(t *testing.T) {
	s, tester := agentsSortShell(t)
	for _, tc := range []struct {
		item string
		want agentSortMode
	}{{"By Status", agentSortStatus}, {"By Group", agentSortGroup}} {
		tester.Move(20, 12)
		tester.Frame()
		if err := tester.Click("Sort Agents"); err != nil {
			t.Fatal(err)
		}
		if err := tester.ChooseMenuItem(tc.item); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
		if s.agentsSortMode() != tc.want {
			t.Fatalf("after choosing %q sort = %q, want %q", tc.item, s.agentsSortMode(), tc.want)
		}
	}
}

// TestAgentsSortOrders pins each mode's list shape: grouped by default with
// the urgent Workspace leading, time flat and newest first, status flat in
// the directory's needs-you-first order.
func TestAgentsSortOrders(t *testing.T) {
	for _, tc := range []struct {
		mode     agentSortMode
		grouped  bool
		sequence []string
	}{
		{agentSortGroup, true, []string{"Toggle core", "core-agent", "Toggle site", "site-agent"}},
		{agentSortTime, false, []string{"site-agent", "core-agent"}},
		{agentSortStatus, false, []string{"core-agent", "site-agent"}},
	} {
		s, tester := agentsSortShell(t)
		s.setAgentsSort(tc.mode)
		tester.Frame()
		_, hasGroups := tester.Find("Toggle core")
		if hasGroups != tc.grouped {
			t.Fatalf("mode %q grouped = %v, want %v", tc.mode, hasGroups, tc.grouped)
		}
		texts := tester.Texts()
		last := -1
		for _, want := range tc.sequence {
			at := firstIndex(texts, want)
			if at < 0 {
				t.Fatalf("mode %q never shows %q; texts=%q", tc.mode, want, texts)
			}
			if at < last {
				t.Fatalf("mode %q order wrong: %q after the previous row; texts=%q", tc.mode, want, texts)
			}
			last = at
		}
	}
}

// TestGroupPlusStartsTheLastKind hovers a group row, clicks its "+" and
// expects the Tab bar's own flow: the last-used kind started in that
// Workspace — and no fold toggle from the same press.
func TestGroupPlusStartsTheLastKind(t *testing.T) {
	s, tester := agentsSortShell(t)
	s.activeInstance = "main" // startNewTab refuses to launch without one
	fake := &fakeStarter{kinds: testKinds}
	s.newTabStarter = fake
	s.launchKinds = testKinds
	s.recentLaunchKinds = []string{"codex"}
	head, ok := tester.Find("Toggle core")
	if !ok {
		t.Fatal("no group row")
	}
	tester.Move(head.X+head.W/2, head.Y+head.H/2)
	tester.Frame()
	if _, ok := tester.Find("New Pane in core"); !ok {
		t.Fatal("hovering the group row shows no +")
	}
	if err := tester.Click("New Pane in core"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if len(fake.specs) != 1 {
		t.Fatalf("the group + started %d sessions, want one", len(fake.specs))
	}
	spec := fake.specs[0]
	if spec.WorkspaceID != "w-core" || spec.CWD != "/core" || spec.Agent != history.AgentCodex {
		t.Fatalf("spec = %+v, want the Codex kind in w-core", spec)
	}
	if s.foldedAgents["group:w-core"] {
		t.Fatal("the + press must not fold the group")
	}
	if _, ok := tester.Find("core-agent"); !ok {
		t.Fatal("the group's cards vanished with the + press")
	}
	// A Workspace the runtime no longer reports carries no "+".
	s.projection.Projects = nil
	tester.Frame()
	tester.Move(head.X+head.W/2, head.Y+head.H/2)
	tester.Frame()
	if _, ok := tester.Find("New Pane in core"); ok {
		t.Fatal("a gone Workspace must not offer the +")
	}
}

// TestAgentsSortPersistsAcrossLaunch round-trips the mode through the
// session snapshot; an unknown stored word reads as the default.
func TestAgentsSortPersistsAcrossLaunch(t *testing.T) {
	store := settings.NewMemoryStore(settings.Default())
	first := NewShell(WithSettings(app.NewSettingsService(store)))
	first.activeInstance = "main"
	first.setAgentsSort(agentSortStatus)
	first.SaveSession()

	second := NewShell(WithSettings(app.NewSettingsService(store)))
	if second.agentsSortMode() != agentSortStatus {
		t.Fatalf("restored sort = %q, want status", second.agentsSortMode())
	}
	if got := normalizeAgentSort("bogus"); got != agentSortGroup {
		t.Fatalf("normalizeAgentSort(%q) = %q, want the group default", "bogus", got)
	}
	if got := normalizeAgentSort(""); got != agentSortGroup {
		t.Fatalf("normalizeAgentSort(\"\") = %q, want the group default", got)
	}
}
