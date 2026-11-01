package nativeui

import (
	"context"
	"testing"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/remotehost"
)

// The phone's Sessions list is the desktop catalog with the desktop's own
// running match: a session an Agent runs carries its instance, pane and state
// and is listed first; nothing else does. The snapshot is what the Host
// goroutines read, so the test publishes it exactly as the window lane does.
func TestHostSessionsMarkRunningWithTheDesktopMatch(t *testing.T) {
	shell, _, meta := newContinueTestShell(t)
	live := selectionFixture("w1", "t1", "p1")
	live.Agents = []herdr.Agent{{
		TerminalID: "term2", PaneID: "p2", ProjectID: "w2", TabID: "t2", Status: "working",
		AgentSession: &herdr.AgentSessionIdentity{Agent: "codex", Kind: "id", Value: meta.ID},
	}}
	shell.projection = live

	serve := shell.hostSessions(shell.hist.service)
	if serve == nil {
		t.Fatal("no Sessions seam over a history service")
	}
	before, err := serve(context.Background(), remotehost.SessionsQuery{Limit: 50})
	if err != nil || len(before.Sessions) == 0 {
		t.Fatalf("list = %+v, %v", before, err)
	}
	for _, row := range before.Sessions {
		if row.Running != nil {
			t.Fatalf("running before any snapshot was published: %+v", row)
		}
	}

	shell.publishRemoteLiveSessions()
	after, err := serve(context.Background(), remotehost.SessionsQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	first := after.Sessions[0]
	if first.ID != meta.Key || first.Running == nil || first.Running.PaneID != "p2" || first.Running.State != "working" || first.Running.Instance != "main" {
		t.Fatalf("running row = %+v, want %s running on main/p2 (working) and listed first", first, meta.Key)
	}
	for _, row := range after.Sessions[1:] {
		if row.Running != nil {
			t.Fatalf("a session no Agent runs is marked: %+v", row)
		}
	}
}

func TestHostSessionsAbsentWithoutAHistoryService(t *testing.T) {
	if got := NewShell().hostSessions(nil); got != nil {
		t.Fatal("a Shell without history must offer no Sessions seam")
	}
}

func TestWireSessionStateWords(t *testing.T) {
	for state, want := range map[operationalState]string{opWorking: "working", opNeedsAttention: "attention", opReadyForReview: "review", opIdle: "idle"} {
		if got := wireSessionState(state); got != want {
			t.Errorf("wireSessionState(%v) = %q, want %q", state, got, want)
		}
	}
}
