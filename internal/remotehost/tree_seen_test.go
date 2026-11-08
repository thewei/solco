package remotehost

import (
	"errors"
	"testing"
	"time"

	"github.com/thewei/solco/internal/herdr"
)

// stamps maps each agent terminal of the tree to the first-seen time its agent carries.
func stamps(tree Tree) map[string]int64 {
	out := map[string]int64{}
	for _, ws := range tree.Workspaces {
		for _, tab := range ws.Tabs {
			for _, pane := range tab.Panes {
				if pane.Agent != nil {
					out[pane.TerminalID] = pane.Agent.FirstSeenMs
				}
			}
		}
	}
	return out
}

// TestTreeStampsWhenTheHostFirstSawAnAgent pins the phone's only clock for a chat that never ran a turn
// (2026-11-07): the moment this Host first saw the terminal running an agent. It stays while the agent runs,
// starts afresh for the next agent in that terminal, survives a failed Herdr read, and a shell never gets one.
func TestTreeStampsWhenTheHostFirstSawAnAgent(t *testing.T) {
	h := newHarness(t)
	now := time.UnixMilli(1_785_546_000_000)
	h.srv.opts.Now = func() time.Time { return now }
	read := func() Tree {
		t.Helper()
		tree, err := h.srv.currentTree("default")
		if err != nil {
			t.Fatal(err)
		}
		return tree
	}
	start := now.UnixMilli()

	h.back.set(titledProjection()) // term_a claude, term_b omp, term_c a shell
	first := read()
	if got := stamps(first); len(got) != 2 || got["term_a"] != start || got["term_b"] != start {
		t.Fatalf("first read stamps = %v, want term_a and term_b at %d", got, start)
	}
	if first.Workspaces[0].Tabs[0].Panes[2].Agent != nil {
		t.Fatal("a shell pane must carry no agent, so no stamp")
	}

	// Asking again later changes nothing: it is when the agent was FIRST seen, so the revision stays put as well.
	now = now.Add(5 * time.Minute)
	if again := read(); again.Revision != first.Revision || stamps(again)["term_a"] != start {
		t.Fatalf("a later read moved the stamp or the revision: rev %d→%d, %v", first.Revision, again.Revision, stamps(again))
	}

	// A new agent terminal carries its own, later time.
	proj := titledProjection()
	proj.Panes = append(proj.Panes, herdr.Pane{ID: "p4", TabID: "t1", TerminalID: "term_d", Agent: "omp", AgentStatus: "idle"})
	h.back.set(proj)
	if got := stamps(read()); got["term_a"] != start || got["term_d"] != now.UnixMilli() {
		t.Fatalf("new terminal stamps = %v, want term_a %d and term_d %d", got, start, now.UnixMilli())
	}

	// A failed Herdr read surfaces and keeps what the Host knows.
	h.back.mu.Lock()
	h.back.projErr = errors.New("socket closed")
	h.back.mu.Unlock()
	if _, err := h.srv.currentTree("default"); err == nil {
		t.Fatal("a failed projection read must surface")
	}
	h.back.mu.Lock()
	h.back.projErr = nil
	h.back.mu.Unlock()
	now = now.Add(time.Minute)
	if got := stamps(read()); got["term_a"] != start {
		t.Fatalf("a failed read made the Host forget: %v", got)
	}

	// An agent that stops (a plain shell now) is forgotten; the next agent in that terminal is a new chat.
	stopped := titledProjection()
	stopped.Panes[1].Agent, stopped.Panes[1].AgentStatus = "", ""
	h.back.set(stopped)
	if got := stamps(read()); len(got) != 1 || got["term_a"] != start {
		t.Fatalf("after term_b stopped: %v", got)
	}
	now = now.Add(time.Hour)
	h.back.set(titledProjection())
	if got := stamps(read()); got["term_a"] != start || got["term_b"] != now.UnixMilli() {
		t.Fatalf("a new agent in term_b must stamp afresh: %v", got)
	}
}
