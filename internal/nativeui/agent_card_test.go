package nativeui

import (
	"testing"

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
// on the first line and carries the project on the shared second line —
// never the old project-first label, and never identical rows.
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
	if len(titles) != 2 || projects != 2 {
		t.Fatalf("titles = %v, project lines = %d; want each card title once and the project on both second lines", titles, projects)
	}
	// The title is painted before its project line: first title index
	// precedes the first project line.
	if order["keytest"] > order["herdr-client"] {
		t.Fatalf("project line painted before the title (title@%d, project@%d)", order["keytest"], order["herdr-client"])
	}
}
