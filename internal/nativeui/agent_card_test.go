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

// TestSidebarNestsSubagentsUnderTheirParent pins the 2026-10-30 tree: while
// a sub-agent still writes, the parent's card carries nested rows (kind and
// task); once all are done the rows fold into a ⤷N chip on the parent.
func TestSidebarNestsSubagentsUnderTheirParent(t *testing.T) {
	render := func(subs []history.Subagent) []string {
		s := NewShell()
		s.loading = false
		s.workbench.markers = agent.NewMarkerStore()
		s.workbench.directory = agent.NewAgentDirectory()
		s.projection.Agents = []herdr.Agent{{PaneID: "p1"}}
		s.workbench.directory.Replace([]agent.AgentCardModel{{
			Key: agent.AgentKey{TerminalID: "term-1"}, Provider: history.AgentClaudeCode,
			Title: "pricing page", ProjectID: "w1", ProjectName: "website", PaneID: "p1", Subagents: subs,
		}})
		tester := ui.NewTester(s.View, 1200, 800)
		return tester.Texts()
	}
	has := func(texts []string, want string) bool {
		for _, text := range texts {
			if text == want {
				return true
			}
		}
		return false
	}
	now := time.Now()
	running := []history.Subagent{
		{ID: "a", Type: "Explore", Description: "scan pricing section", UpdatedAt: now.Add(-2 * time.Second)},
		{ID: "b", Type: "general-purpose", Description: "review copy", UpdatedAt: now.Add(-5 * time.Minute)},
	}
	texts := render(running)
	if !has(texts, "Explore") || !has(texts, "scan pricing section") || !has(texts, "general-purpose") {
		t.Fatalf("running sub-agents are not nested; texts=%q", texts)
	}
	if has(texts, "⤷2") {
		t.Fatalf("a running parent must show rows, not the chip; texts=%q", texts)
	}
	for i := range running {
		running[i].UpdatedAt = now.Add(-5 * time.Minute)
	}
	texts = render(running)
	if has(texts, "Explore") || !has(texts, "⤷2") {
		t.Fatalf("finished sub-agents must fold into ⤷2; texts=%q", texts)
	}
}

func TestSubagentsChangedTreatsEmptyAsEqual(t *testing.T) {
	if subagentsChanged(nil, map[agent.AgentKey][]history.Subagent{}) {
		t.Fatal("nil vs empty is not a change")
	}
}
