package remotehost

import (
	"testing"
	"time"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

func titledProjection() herdr.Projection {
	return herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "solco"}},
		Tabs:     []herdr.Tab{{ID: "t1", ProjectID: "w1", Label: "main"}},
		Panes: []herdr.Pane{
			{ID: "p1", TabID: "t1", TerminalID: "term_a", Agent: "claude", AgentStatus: "working"},
			{ID: "p2", TabID: "t1", TerminalID: "term_b", Agent: "omp", AgentStatus: "idle"},
			{ID: "p3", TabID: "t1", TerminalID: "term_c", Label: "zsh"},
		},
	}
}

func TestTreeAgentTitleFollowsTheDesktopChain(t *testing.T) {
	last := time.UnixMilli(1785546001000)
	tree := buildTree("default", titledProjection(), map[string]AgentFact{
		"term_a": {Title: "  Fix the parser ", LastActive: last},
	})
	panes := tree.Workspaces[0].Tabs[0].Panes
	if got := panes[0].Agent; got.Title != "Fix the parser" || got.LastActiveMs != last.UnixMilli() {
		t.Fatalf("resolved agent = %+v", got)
	}
	// no fact (another Herdr session, or not resolved yet): the provider's display name, no time
	if got := panes[1].Agent; got.Title != history.AgentOMP.DisplayName() || got.LastActiveMs != 0 {
		t.Fatalf("fallback agent = %+v", got)
	}
	if panes[2].Agent != nil {
		t.Fatal("a shell pane must carry no agent")
	}
}

func TestTreeTitleChangeMovesTheRevisionHash(t *testing.T) {
	a := buildTree("default", titledProjection(), nil)
	b := buildTree("default", titledProjection(), map[string]AgentFact{"term_a": {Title: "Renamed"}})
	if a.hash() == b.hash() {
		t.Fatal("a new title must change the tree hash so phones refetch")
	}
}
