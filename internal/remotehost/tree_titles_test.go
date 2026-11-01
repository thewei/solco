package remotehost

import (
	"sync"
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

func TestPokePushesATitleOnlyChange(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	title := "Old title"
	h.srv.opts.AgentFacts = func(string) map[string]AgentFact {
		mu.Lock()
		defer mu.Unlock()
		return map[string]AgentFact{"term_a": {Title: title}}
	}
	conn, _, err := h.dial("/api/v3/events", true)
	if err != nil {
		t.Fatal(err)
	}
	readJSON(t, conn) // hello
	if h.back.source(0) == nil {
		t.Fatal("no subscription")
	}
	mu.Lock()
	title = "New title"
	mu.Unlock()
	h.srv.Poke() // no Herdr event follows: the title is the only change
	if msg := readJSON(t, conn); msg["t"] != "tree.changed" {
		t.Fatalf("frame = %v", msg)
	}
}
