package nativeui

/**
 * [INPUT]: Shell 假投影（herdr.Project/Panes/Agents）与假 newTabStarter（fakeStarter）、sidebarSectionHeader 的 Menu 型 Action 常驻槽、ui.Tester 的悬停/菜单模拟
 * [OUTPUT]: Agents 区排序回归——头部排序槽点击开菜单、By Workspace/By Time/By Status 三种分组模式、默认按 Pane 顺序与组内 Sort by Updated Time（含 Newest/Oldest First）切换、各模式组行折叠与展开、会话跨重启持久化
 * [POS]: 2026-11-02 用户要求（Agents 标题排序菜单 + Workspace/Time/Status 三维度分组 + 组内按更新时间勾选排序）；悬停条件菜单会丢选择（探针实证），故槽按钮键控常驻
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"reflect"
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
// worked two hours ago, the "site" agent worked last. Both Workspaces and Panes
// are in the projection, so the groups carry their "+" and default pane order.
func agentsSortShell(t *testing.T) (*Shell, *ui.Tester) {
	t.Helper()
	s := NewShell()
	s.loading = false
	s.workbench.markers = agent.NewMarkerStore()
	s.workbench.directory = agent.NewAgentDirectory()
	late := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	s.clockFn = func() time.Time { return late }
	s.projection.Agents = []herdr.Agent{{PaneID: "p1"}, {PaneID: "p2"}}
	s.projection.Projects = []herdr.Project{
		{ID: "w-core", Label: "core", CWD: "/core"},
		{ID: "w-site", Label: "site", CWD: "/site"},
	}
	s.projection.Panes = []herdr.Pane{
		{ID: "p1", ProjectID: "w-core"},
		{ID: "p2", ProjectID: "w-site"},
	}
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
	for _, want := range []string{"By Workspace", "By Time", "By Status", "Sort by Updated Time"} {
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
	if _, ok := tester.Find("Toggle Today"); !ok {
		t.Fatal("time mode must show time group labels like Today")
	}
	if _, ok := tester.Find("Toggle core"); ok {
		t.Fatal("time mode must not show workspace group labels")
	}
}

// TestAgentsSortMenuChecksTheCurrentMode cycles the remaining modes through
// the same menu, ending back at the default.
func TestAgentsSortMenuChecksTheCurrentMode(t *testing.T) {
	s, tester := agentsSortShell(t)
	for _, tc := range []struct {
		item string
		want agentSortMode
	}{{"By Status", agentSortStatus}, {"By Workspace", agentSortWorkspace}} {
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

// TestAgentsSortOrders pins each mode's list shape: grouped by Workspace by
// default, grouped by Time, or grouped by Status.
func TestAgentsSortOrders(t *testing.T) {
	for _, tc := range []struct {
		mode     agentSortMode
		sequence []string
	}{
		{agentSortWorkspace, []string{"Toggle core", "core-agent", "Toggle site", "site-agent"}},
		{agentSortTime, []string{"Toggle Today", "core-agent", "site-agent"}},
		{agentSortStatus, []string{"Toggle Needs Attention", "core-agent", "Toggle Idle", "site-agent"}},
	} {
		s, tester := agentsSortShell(t)
		s.setAgentsSort(tc.mode)
		tester.Frame()
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

// TestAgentsSortByTimeToggle tests the orthogonal "Sort by Updated Time"
// checkbox: when enabled, newer cards precede older cards; when toggled to
// Oldest First, older cards precede newer cards; when toggled off, cards revert
// to default Pane order.
func TestAgentsSortByTimeToggle(t *testing.T) {
	s, tester := agentsSortShell(t)
	s.setAgentsSort(agentSortTime)
	tester.Frame()

	// Default: Pane order (core-agent in p1 leads site-agent in p2)
	texts := tester.Texts()
	if firstIndex(texts, "core-agent") > firstIndex(texts, "site-agent") {
		t.Fatal("default pane order should place core-agent before site-agent")
	}

	// Toggle "Sort by Updated Time" on
	if err := tester.Click("Sort Agents"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("Sort by Updated Time"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !s.agentsSortByTime {
		t.Fatal("Sort by Updated Time should be active")
	}

	// Now newest active (site-agent, late) leads (core-agent, late - 2h)
	texts = tester.Texts()
	if firstIndex(texts, "site-agent") > firstIndex(texts, "core-agent") {
		t.Fatal("with Sort by Updated Time active (newest first), site-agent should precede core-agent")
	}

	// Switch to Oldest First
	if err := tester.Click("Sort Agents"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("Oldest First"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !s.agentsSortAscending {
		t.Fatal("Oldest First should be active")
	}
	texts = tester.Texts()
	if firstIndex(texts, "core-agent") > firstIndex(texts, "site-agent") {
		t.Fatal("with Oldest First active, core-agent should precede site-agent")
	}

	// Toggle "Sort by Updated Time" off -> reverts to Pane order
	if err := tester.Click("Sort Agents"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("Sort by Updated Time"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if s.agentsSortByTime {
		t.Fatal("Sort by Updated Time should be inactive after toggle")
	}
	texts = tester.Texts()
	if firstIndex(texts, "core-agent") > firstIndex(texts, "site-agent") {
		t.Fatal("reverting to default pane order should place core-agent before site-agent")
	}
}

// TestGroupPlusOpensTheKindMenu hovers a group row, clicks its "+" and
// expects the Tab bar's own menu — Terminal first, then the Agents with the
// last-used one marked — whose choice (made with the pointer off the row)
// starts that kind in that Workspace, with no fold toggle from the same press.
func TestGroupPlusOpensTheKindMenu(t *testing.T) {
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
	if rect, ok := tester.Find("New Pane in core"); !ok || rect.W == 0 {
		t.Fatal("hovering the group row shows no +")
	}
	if err := tester.Click("New Pane in core"); err != nil {
		t.Fatal(err)
	}
	menu := tester.Menu()
	if want := []string{"Terminal", "-", "Codex  ·  last used", "Claude Code", "Pi"}; !reflect.DeepEqual(menu, want) {
		t.Fatalf("group + menu = %q, want %q", menu, want)
	}
	tester.Move(1150, 780) // the pointer is in the popup, off the row
	if err := tester.ChooseMenuItem("Codex  ·  last used"); err != nil {
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
	first.agentsSortByTime = true
	first.agentsSortAscending = true
	first.SaveSession()

	second := NewShell(WithSettings(app.NewSettingsService(store)))
	if second.agentsSortMode() != agentSortStatus {
		t.Fatalf("restored sort = %q, want status", second.agentsSortMode())
	}
	if !second.agentsSortByTime {
		t.Fatal("restored agentsSortByTime = false, want true")
	}
	if !second.agentsSortAscending {
		t.Fatal("restored agentsSortAscending = false, want true")
	}
	if got := normalizeAgentSort("bogus"); got != agentSortWorkspace {
		t.Fatalf("normalizeAgentSort(%q) = %q, want the workspace default", "bogus", got)
	}
	if got := normalizeAgentSort(""); got != agentSortWorkspace {
		t.Fatalf("normalizeAgentSort(\"\") = %q, want the workspace default", got)
	}
}
