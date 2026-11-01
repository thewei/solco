package nativeui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

// TestAgentCardLabelLeadsWithTheSessionTitle pins the 2026-10-30 contract
// (F118 reversed): the session title is the card's primary name; only
// several visible cards of one project append the project for
// disambiguation (F169), a lone card keeps the bare title.
func TestAgentCardLabelLeadsWithTheSessionTitle(t *testing.T) {
	cards := []agent.AgentCardModel{
		{ProjectName: "herdr-client", Title: "keytest"},
		{ProjectName: "herdr-client", Title: "test-claude"},
		{ProjectName: "solo-project", Title: "forgotten-agent"},
		{ProjectName: "", Title: "title-only"},
		{ProjectName: "self-named", Title: "self-named"},
	}
	dups := duplicateAgentProjectLabels(cards)
	if !dups["herdr-client"] || dups["solo-project"] || dups["self-named"] {
		t.Fatalf("dup set = %+v", dups)
	}
	want := []struct {
		label     string
		carriesPr bool
	}{
		{"keytest · herdr-client", true},
		{"test-claude · herdr-client", true},
		{"forgotten-agent", false},
		{"title-only", false},
		{"self-named", false},
	}
	for i, tc := range want {
		label, carries := agentCardLabel(cards[i], dups)
		if label != tc.label || carries != tc.carriesPr {
			t.Fatalf("card %d label = (%q, %v), want (%q, %v)", i, label, carries, tc.label, tc.carriesPr)
		}
	}
}

// TestSidebarAgentCardsShowTitleOverProject renders the sidebar's Agents
// section with two same-project agents: each card names its session title
// on the first line, and the project is named once by the Workspace group
// label above them (2026-10-30) — never the old project-first label, and
// never identical rows.
func TestSidebarAgentCardsShowTitleOverProject(t *testing.T) {
	s := NewShell()
	s.loading = false
	// reconcileWorkbench normally builds these from the first applied
	// projection; seed them directly for the headless render. agentItems
	// gates on the projection's agents, so it needs entries too.
	s.workbench.markers = agent.NewMarkerStore()
	s.workbench.directory = agent.NewAgentDirectory()
	s.projection.Agents = []herdr.Agent{{PaneID: "p1"}, {PaneID: "p2"}}
	s.workbench.directory.Replace([]agent.AgentCardModel{
		{
			Key:         agent.AgentKey{TerminalID: "term-1"},
			Provider:    history.AgentPi,
			Title:       "keytest",
			ProjectName: "herdr-client",
			PaneID:      "p1",
		},
		{
			Key:         agent.AgentKey{TerminalID: "term-2"},
			Provider:    history.AgentPi,
			Title:       "test-claude",
			ProjectName: "herdr-client",
			PaneID:      "p2",
		},
	})

	tester := ui.NewTester(s.View, 1200, 800)
	titles := map[string]int{}
	projects := 0
	order := map[string]int{}
	for index, text := range tester.Texts() {
		switch text {
		case "keytest", "test-claude":
			titles[text]++
			if _, seen := order[text]; !seen {
				order[text] = index
			}
		case "herdr-client":
			projects++
			if _, seen := order["herdr-client"]; !seen {
				order["herdr-client"] = index
			}
		}
	}
	if len(titles) != 2 || projects != 1 {
		t.Fatalf("titles = %v, project lines = %d; want each card title once and the project once, as the group label", titles, projects)
	}
	// The group label heads its cards.
	if order["keytest"] < order["herdr-client"] {
		t.Fatalf("group label painted after its card (title@%d, project@%d)", order["keytest"], order["herdr-client"])
	}
}

// TestAgentAgo pins the sidebar's short "last worked" tag.
func TestAgentAgo(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Time{}, ""},
		{now.Add(-20 * time.Second), "<1m"},
		{now.Add(-7 * time.Minute), "7m"},
		{now.Add(-3 * time.Hour), "3h"},
		{now.Add(-72 * time.Hour), "3d"},
	} {
		if got := agentAgo(tc.at, now); got != tc.want {
			t.Fatalf("agentAgo(%v) = %q, want %q", tc.at, got, tc.want)
		}
	}
}

// TestGroupAgentCardsKeepsUrgentWorkspaceFirst: groups follow the order their
// first (most urgent) card appears in, cards keep their order inside.
func TestGroupAgentCardsKeepsUrgentWorkspaceFirst(t *testing.T) {
	cards := []agent.AgentCardModel{
		{ProjectID: "b", ProjectName: "core", Title: "1"},
		{ProjectID: "a", ProjectName: "site", Title: "2"},
		{ProjectID: "b", ProjectName: "core", Title: "3"},
	}
	groups := groupAgentCards(cards)
	if len(groups) != 2 || groups[0].Name != "core" || len(groups[0].Cards) != 2 || groups[0].Cards[1].Title != "3" || groups[1].Name != "site" {
		t.Fatalf("groups = %+v", groups)
	}
}

// subagentSidebarShell is a sidebar with one Claude Code card that has the
// given sub-agents.
func subagentSidebarShell(subs []history.Subagent) *Shell {
	s := NewShell()
	s.loading = false
	s.workbench.markers = agent.NewMarkerStore()
	s.workbench.directory = agent.NewAgentDirectory()
	s.projection.Agents = []herdr.Agent{{PaneID: "p1"}}
	s.workbench.directory.Replace([]agent.AgentCardModel{{
		Key: agent.AgentKey{TerminalID: "term-1"}, Provider: history.AgentClaudeCode,
		Title: "pricing page", ProjectID: "w1", ProjectName: "website", PaneID: "p1", Subagents: subs,
	}})
	return s
}

// TestSidebarNestsSubagentsUnderTheirParent pins the tree: the parent's card
// carries nested rows (kind and task) whether the sub-agents still write or
// are done (2026-11-01: finished ones used to collapse into a ⤷N chip), and
// the parent's chevron folds and unfolds them.
func TestSidebarNestsSubagentsUnderTheirParent(t *testing.T) {
	now := time.Now()
	for name, age := range map[string]time.Duration{"running": 2 * time.Second, "finished": 5 * time.Minute} {
		subs := []history.Subagent{
			{ID: "a", Type: "Explore", Description: "scan pricing section", UpdatedAt: now.Add(-age)},
			{ID: "b", Type: "general-purpose", Description: "review copy", UpdatedAt: now.Add(-age)},
		}
		tester := ui.NewTester(subagentSidebarShell(subs).View, 1200, 800)
		if !tester.HasText("Explore") || !tester.HasText("scan pricing section") || !tester.HasText("general-purpose") {
			t.Fatalf("%s sub-agents are not nested; texts=%q", name, tester.Texts())
		}
		if tester.HasText("⤷2") {
			t.Fatalf("%s: the sidebar nests rows, it has no chip; texts=%q", name, tester.Texts())
		}
	}
}

// TestSidebarSubagentFoldToggles: the chevron on the parent hides and shows
// its nested rows without opening the Agent, and the fold persists.
func TestSidebarSubagentFoldToggles(t *testing.T) {
	s := subagentSidebarShell([]history.Subagent{
		{ID: "a", Type: "Explore", Description: "scan pricing section", UpdatedAt: time.Now().Add(-5 * time.Minute)},
	})
	tester := ui.NewTester(s.View, 1200, 800)
	if err := tester.Click("Toggle sub-agents of pricing page"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if tester.HasText("Explore") || !s.foldedAgents["sub:term-1"] {
		t.Fatalf("folding must hide the rows; folded=%v texts=%q", s.foldedAgents, tester.Texts())
	}
	if s.router.Path() != routeWorkspace || s.selectedPaneID == "p1" {
		t.Fatalf("the chevron must not open the Agent: path=%q pane=%q", s.router.Path(), s.selectedPaneID)
	}
	if got := s.captureSession().FoldedAgents; len(got) != 1 || got[0] != "sub:term-1" {
		t.Fatalf("fold not captured for persistence: %v", got)
	}
	if err := tester.Click("Toggle sub-agents of pricing page"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("Explore") || s.foldedAgents["sub:term-1"] {
		t.Fatalf("unfolding must show the rows again; folded=%v", s.foldedAgents)
	}
}

// The Header popover nests finished sub-agents too, and its height budget
// follows the same fold.
func TestQuickPanelSubagentRowsFollowTheFold(t *testing.T) {
	s := subagentSidebarShell([]history.Subagent{
		{ID: "a", Type: "Explore", UpdatedAt: time.Now().Add(-5 * time.Minute)},
		{ID: "b", Type: "Plan", UpdatedAt: time.Now().Add(-5 * time.Minute)},
	})
	entries := []StatusCenterEntry{{Card: s.workbenchCards()[0]}}
	if got := s.quickPanelSubagentRows(entries); got != 2 {
		t.Fatalf("finished sub-agents must count as rows; got %d", got)
	}
	s.toggleAgentFold(subagentFoldKey(entries[0].Card))
	if got := s.quickPanelSubagentRows(entries); got != 0 {
		t.Fatalf("a folded parent has no rows; got %d", got)
	}
}

// TestSidebarAgentGroupFolds: the Workspace label folds its cards; folded, it
// still tells the count and keeps the group's state visible.
func TestSidebarAgentGroupFolds(t *testing.T) {
	s := subagentSidebarShell(nil)
	tester := ui.NewTester(s.View, 1200, 800)
	if !tester.HasText("pricing page") {
		t.Fatalf("card missing before the fold; texts=%q", tester.Texts())
	}
	if err := tester.Click("Toggle website"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if tester.HasText("pricing page") || !s.foldedAgents["group:w1"] || !tester.HasText("website") || !tester.HasText("1") {
		t.Fatalf("folded group must hide its cards and keep label+count; folded=%v texts=%q", s.foldedAgents, tester.Texts())
	}
	if err := tester.Click("Toggle website"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("pricing page") {
		t.Fatalf("unfolding must bring the cards back; texts=%q", tester.Texts())
	}
}

func TestSubagentsChangedTreatsEmptyAsEqual(t *testing.T) {
	if subagentsChanged(nil, map[agent.AgentKey][]history.Subagent{}) {
		t.Fatal("nil vs empty is not a change")
	}
}

// An Agent that starts while the sidebar is on screen grows in; it does not
// appear whole.
func TestSidebarAgentRowGrowsIn(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.workbench.markers = agent.NewMarkerStore()
	s.workbench.directory = agent.NewAgentDirectory()
	card := func(n string) agent.AgentCardModel {
		return agent.AgentCardModel{Key: agent.AgentKey{TerminalID: "term-" + n}, Provider: history.AgentPi, Title: "title-" + n, ProjectName: "proj", PaneID: "p" + n}
	}
	s.projection.Agents = []herdr.Agent{{PaneID: "p1"}}
	s.workbench.directory.Replace([]agent.AgentCardModel{card("1")})
	tester := ui.NewTester(s.View, 1200, 800)
	tester.Frame()
	tester.Advance(2 * treeRowMotion.Duration)
	s.projection.Agents = []herdr.Agent{{PaneID: "p1"}, {PaneID: "p2"}}
	s.workbench.directory.Replace([]agent.AgentCardModel{card("1"), card("2")})
	tester.Frame()
	early, _ := tester.Find("title-2")
	tester.Advance(2 * treeRowMotion.Duration)
	settled, ok := tester.Find("title-2")
	if !ok {
		t.Fatalf("the new Agent never showed; texts=%q", tester.Texts())
	}
	if early == settled {
		t.Fatalf("the new row appeared whole: %+v", settled)
	}
	if first, ok := tester.Find("title-1"); ok && first.Y >= settled.Y {
		t.Fatalf("order changed: %+v vs %+v", first, settled)
	}
}

// The folded group's slot and the folded parent's toggle keep running
// sub-agents visible: needs-you states still win, otherwise a running
// sub-agent makes the group read as working.
func TestGroupIndicatorCountsRunningSubagents(t *testing.T) {
	now := time.Now()
	running := []history.Subagent{{ID: "a", UpdatedAt: now.Add(-time.Second)}}
	done := []history.Subagent{{ID: "a", UpdatedAt: now.Add(-time.Hour)}}
	idle := agent.AgentCardModel{Key: agent.AgentKey{TerminalID: "t1"}}
	if got := groupIndicatorState([]agent.AgentCardModel{idle}, now); got == opWorking {
		t.Fatalf("an idle card with no sub-agents must not spin; got %v", got)
	}
	idle.Subagents = done
	if got := groupIndicatorState([]agent.AgentCardModel{idle}, now); got == opWorking {
		t.Fatalf("finished sub-agents must not spin; got %v", got)
	}
	idle.Subagents = running
	if got := groupIndicatorState([]agent.AgentCardModel{idle}, now); got != opWorking {
		t.Fatalf("a running sub-agent must make the folded group work; got %v", got)
	}
	needs := agent.AgentCardModel{Key: agent.AgentKey{TerminalID: "t0"}, Attention: agent.OpNeedsAttention}
	if got := groupIndicatorState([]agent.AgentCardModel{needs, idle}, now); got != opNeedsAttention {
		t.Fatalf("what needs you outranks working sub-agents; got %v", got)
	}
}

// A list longer than the rows shown says so, and the row opens Chat.
func TestSidebarShowsAnEarlierRowWhenSubagentsAreCut(t *testing.T) {
	var subs []history.Subagent
	for i := 0; i < subagentRowsShown+2; i++ {
		subs = append(subs, history.Subagent{ID: itoa(i), Type: "T" + itoa(i), UpdatedAt: time.Now().Add(-time.Hour)})
	}
	s := subagentSidebarShell(subs)
	tester := ui.NewTester(s.View, 1200, 800)
	if !tester.HasText("+2 earlier") || tester.HasText("T0") || !tester.HasText("T7") {
		t.Fatalf("want the newest %d rows and a +2 earlier row; texts=%q", subagentRowsShown, tester.Texts())
	}
	s.toggleAgentFold(subagentFoldKey(s.workbenchCards()[0]))
	tester.Frame()
	if tester.HasText("+2 earlier") {
		t.Fatalf("a folded parent shows no earlier row; texts=%q", tester.Texts())
	}
}

// Fold All / Unfold All act on every group and every parent.
func TestSetAgentFoldsFoldsEverything(t *testing.T) {
	s := NewShell()
	groups := []agentGroup{
		{Key: "w1", Name: "site", Cards: []agent.AgentCardModel{{Key: agent.AgentKey{TerminalID: "t1"}, Subagents: []history.Subagent{{ID: "a"}}}}},
		{Key: "w2", Name: "core", Cards: []agent.AgentCardModel{{Key: agent.AgentKey{TerminalID: "t2"}}}},
	}
	s.setAgentFolds(groups, "group", true)
	if !s.foldedAgents["group:w1"] || !s.foldedAgents["group:w2"] || len(s.foldedAgents) != 2 {
		t.Fatalf("groups = %v", s.foldedAgents)
	}
	s.setAgentFolds(groups, "sub", true)
	if !s.foldedAgents["sub:t1"] || s.foldedAgents["sub:t2"] {
		t.Fatalf("only parents with sub-agents fold: %v", s.foldedAgents)
	}
	s.setAgentFolds(groups, "group", false)
	s.setAgentFolds(groups, "sub", false)
	if len(s.foldedAgents) != 0 {
		t.Fatalf("unfold all must clear every fold: %v", s.foldedAgents)
	}
}

// The Agents list answers the arrows like the other sidebar lists: ←/→ fold
// and unfold a parent's sub-agents, ↓ steps into them, ← climbs back to the
// parent and on to the Workspace label, whose ← folds the group.
func TestSidebarAgentsListIsKeyboardDriven(t *testing.T) {
	s := subagentSidebarShell([]history.Subagent{
		{ID: "a", Type: "Explore", Description: "scan", UpdatedAt: time.Now().Add(-time.Hour)},
	})
	tester := ui.NewTester(s.View, 1200, 800)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	settleFrames(tester, 3)
	if err := tester.Click("pricing page"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 3)
	wantCursor(t, tester, "pricing page")
	pressKey(tester, 0, ui.KeyLeft)
	if tester.HasText("Explore") || !s.foldedAgents["sub:term-1"] {
		t.Fatalf("← on an open parent must fold its sub-agents; folded=%v texts=%q", s.foldedAgents, tester.Texts())
	}
	pressKey(tester, 0, ui.KeyRight)
	if !tester.HasText("Explore") {
		t.Fatalf("→ on a folded parent must unfold; texts=%q", tester.Texts())
	}
	pressKey(tester, 0, ui.KeyDown)
	wantCursor(t, tester, "Explore scan")
	pressKey(tester, 0, ui.KeyLeft)
	wantCursor(t, tester, "pricing page") // ← on a sub-agent climbs to its parent
	pressKey(tester, 0, ui.KeyLeft)       // fold the parent again, then climb
	pressKey(tester, 0, ui.KeyLeft)
	wantCursor(t, tester, "Toggle website")
	pressKey(tester, 0, ui.KeyLeft)
	if tester.HasText("pricing page") || !s.foldedAgents["group:w1"] {
		t.Fatalf("← on the Workspace label must fold the group; folded=%v texts=%q", s.foldedAgents, tester.Texts())
	}
}

// Folds of Workspaces and agent runs that are gone do not pile up in the
// session file; before a projection is known nothing is dropped.
func TestPersistableAgentFoldsDropsWhatIsGone(t *testing.T) {
	s := subagentSidebarShell(nil)
	s.foldedAgents = map[string]bool{"group:w1": true, "group:gone": true, "sub:term-1": true, "sub:dead": true}
	if got := s.persistableAgentFolds(); len(got) != 4 {
		t.Fatalf("without a known Workspace nothing may be dropped: %v", got)
	}
	s.projection.Projects = []herdr.Project{{ID: "w1"}}
	got := s.persistableAgentFolds()
	if len(got) != 2 || got[0] != "group:w1" || got[1] != "sub:term-1" {
		t.Fatalf("want only the live folds, got %v", got)
	}
}
