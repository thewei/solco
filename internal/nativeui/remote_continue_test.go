package nativeui

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/remotehost"
)

func continuerFor(t *testing.T) (*remoteContinuer, string) {
	t.Helper()
	shell, _, meta := newContinueTestShell(t)
	lookup, ok := shell.hist.service.(sessionLookup)
	if !ok {
		t.Fatal("the production history service must offer Session lookup")
	}
	return &remoteContinuer{launch: &app.LaunchService{}, lookup: lookup, service: shell.hist.service, live: &shell.remoteLive}, meta.Key
}

func TestRemoteContinueUnknownSessionIsRefused(t *testing.T) {
	c, _ := continuerFor(t)
	_, err := c.Continue(context.Background(), "main", "codex:nope")
	var le *remotehost.LaunchError
	if !errors.As(err, &le) || le.Code != "unknown_session" || le.Status != http.StatusNotFound {
		t.Fatalf("err = %v", err)
	}
}

// A session an Agent already runs is located, never started a second time —
// the same exact-identity rule as the desktop's Continue.
func TestRemoteContinueLocatesARunningSession(t *testing.T) {
	shell, _, meta := newContinueTestShell(t)
	lookup := shell.hist.service.(sessionLookup)
	c := &remoteContinuer{launch: &app.LaunchService{}, lookup: lookup, service: shell.hist.service, live: &shell.remoteLive}
	live := selectionFixture("w1", "t1", "p1")
	live.Agents = []herdr.Agent{{
		TerminalID: "term2", PaneID: "p2", ProjectID: "w2", TabID: "t2", Status: "idle",
		AgentSession: &herdr.AgentSessionIdentity{Agent: "codex", Kind: "id", Value: meta.ID},
	}}
	shell.projection = live
	shell.publishRemoteLiveSessions()

	got, err := c.Continue(context.Background(), "main", meta.Key)
	if err != nil || got.PaneID != "p2" || !got.AlreadyLive {
		t.Fatalf("got %+v, %v; want the running pane p2", got, err)
	}
	if ids := c.liveIdentities("other"); len(ids) != 0 {
		t.Fatalf("identities of another Herdr session leaked: %+v", ids)
	}
}

func TestContinueLaunchErrorCodes(t *testing.T) {
	cases := map[string]struct {
		kind   string
		code   string
		status int
	}{
		"project":   {"project-unavailable", "path_unavailable", http.StatusBadRequest},
		"conflict":  {"operation-conflict", "launch_conflict", http.StatusConflict},
		"uncertain": {"agent-start-uncertain", "launch_uncertain", http.StatusGatewayTimeout},
		"other":     {"runtime-create", "launch_failed", http.StatusBadGateway},
	}
	for name, tc := range cases {
		got := continueLaunchError(&agent.LaunchFailure{Kind: tc.kind})
		if got.Code != tc.code || got.Status != tc.status {
			t.Errorf("%s: %s/%d, want %s/%d", name, got.Code, got.Status, tc.code, tc.status)
		}
	}
	if got := continueLaunchError(errors.New("x")); got.Code != "launch_failed" {
		t.Errorf("plain error -> %s", got.Code)
	}
}

func TestHostContinuerAbsentWithoutLaunchService(t *testing.T) {
	shell, _, _ := newContinueTestShell(t)
	shell.launch = nil
	if got := shell.hostContinuer(shell.hist.service); got != nil {
		t.Fatal("no launch service must mean no Continue seam")
	}
}
