package nativeui

import (
	"testing"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
)

// TestHostAgentFactsPublishTheDesktopTitle pins the phone's title source: the
// Host gets exactly the title the desktop card shows (session title first), a
// Solco-minted plumbing name is dropped, and only the shown Herdr session has facts.
func TestHostAgentFactsPublishTheDesktopTitle(t *testing.T) {
	s := NewShell()
	s.activeInstance = "default"
	s.projection = herdr.Projection{
		Agents: []herdr.Agent{
			{TerminalID: "term-a", PaneID: "p1", Kind: "claude", Status: "idle", Name: "claude"},
			{TerminalID: "term-b", PaneID: "p2", Kind: "omp", Status: "idle", Name: "omp-3fa9c2"},
		},
	}
	mod := time.Unix(1785546001, 0)
	s.agentSessionTitle = map[agent.AgentKey]string{{InstanceID: "default", TerminalID: "term-a"}: "Fix the parser"}
	s.agentSessionMod = map[agent.AgentKey]time.Time{{InstanceID: "default", TerminalID: "term-a"}: mod}
	s.reconcileWorkbench()

	facts := s.hostAgentFacts("default")
	if got := facts["term-a"]; got.Title != "Fix the parser" || !got.LastActive.Equal(mod) {
		t.Fatalf("term-a fact = %+v", got)
	}
	if got := facts["term-b"]; got.Title != "" {
		t.Fatalf("a generated name must not be a title, got %q", got.Title)
	}
	if s.hostAgentFacts("other") != nil {
		t.Fatal("another Herdr session must get no facts")
	}
}
