package nativeui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

// staticHistory is a HistoryListView with a fixed List answer.
type staticHistory struct{ rows []history.SessionSummary }

func (h staticHistory) List(context.Context, history.HistoryQuery) ([]history.SessionSummary, error) {
	return h.rows, nil
}
func (h staticHistory) Open(context.Context, history.ConversationWindowRequest) (history.TranscriptWindow, error) {
	return history.TranscriptWindow{}, nil
}
func (h staticHistory) Recent(context.Context, int) ([]history.SessionSummary, error) {
	return h.rows, nil
}
func (h staticHistory) Scan(context.Context) (history.ScanResult, error) {
	return history.ScanResult{}, nil
}

// sourcelessLinkShell is a Shell whose selected pane is an agent without a
// session identity, with a history catalog offering three sessions: one
// recent, one older than the window, one archived.
func sourcelessLinkShell(t *testing.T) (*Shell, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "recent.jsonl")
	if err := os.WriteFile(file, []byte(`{"type":"user","uuid":"u1","timestamp":"2026-10-31T00:00:00Z","message":{"role":"user","content":"hello"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	shell := NewShell()
	shell.loading = false
	shell.selectedPaneID = "p1"
	shell.projection = herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p1", TerminalID: "term-1", CWD: "/work/demo"}},
		Agents: []herdr.Agent{{TerminalID: "term-1", PaneID: "p1", Status: "idle"}},
	}
	shell.workbench.directory = agent.NewAgentDirectory()
	shell.workbench.directory.Replace([]agent.AgentCardModel{{
		Key:      agent.AgentKey{InstanceID: "inst", TerminalID: "term-1"},
		PaneID:   "p1",
		Provider: history.AgentClaudeCode,
	}})
	shell.hist.service = staticHistory{rows: []history.SessionSummary{
		{Meta: history.SessionMeta{Key: "claude:recent", Agent: history.AgentClaudeCode, Title: "Recent work", FilePath: file, UpdatedAt: now - int64(5*time.Minute/time.Millisecond), MessageCount: 4}},
		{Meta: history.SessionMeta{Key: "claude:old", Agent: history.AgentClaudeCode, Title: "Last week", FilePath: file, UpdatedAt: now - int64(72*time.Hour/time.Millisecond)}},
		{Meta: history.SessionMeta{Key: "claude:archived", Agent: history.AgentClaudeCode, Title: "Gone", FilePath: file, UpdatedAt: now, Archived: true}},
	}}
	shell.rebindWorkspaceChat()
	if !shell.chatPaneSourceless() {
		t.Fatal("setup: the pane must be sourceless")
	}
	return shell, file
}

// The invitation offers only recent, unarchived sessions of the pane's
// provider and folder — and nothing once the pane has a chat source.
func TestSourcelessPaneOffersLinkCandidates(t *testing.T) {
	shell, _ := sourcelessLinkShell(t)
	state := shell.chatBridgeState()
	if len(state.LinkCandidates) != 1 || state.LinkCandidates[0].Key != "claude:recent" || state.LinkCandidates[0].Title != "Recent work" {
		t.Fatalf("candidates = %+v, want the one recent unarchived session", state.LinkCandidates)
	}
	shell.chatConversationID = "live:claude:term-1"
	if got := shell.chatBridgeState().LinkCandidates; len(got) != 0 {
		t.Fatalf("a bound pane offers no candidates: %+v", got)
	}
}

// Linking is the user's click: a key outside the offered list binds nothing;
// a good one tails the file, and the measured identity later takes over.
func TestLinkSessionBindsOnlyAnOfferedCandidate(t *testing.T) {
	shell, _ := sourcelessLinkShell(t)
	bridge := &ChatBridge{shell: shell}

	state, err := bridge.LinkSession(context.Background(), "claude:old")
	if err != nil {
		t.Fatal(err)
	}
	if shell.chatSessionLinked() || shell.chatConversationID != "" || state.Outcome == "" {
		t.Fatalf("an unoffered key must bind nothing: linked=%v conv=%q outcome=%q", shell.chatSessionLinked(), shell.chatConversationID, state.Outcome)
	}

	state, err = bridge.LinkSession(context.Background(), "claude:recent")
	if err != nil {
		t.Fatal(err)
	}
	if !shell.chatSessionLinked() || shell.chatConversationID == "" || state.Waiting || state.Outcome != "" {
		t.Fatalf("link did not bind: linked=%v conv=%q waiting=%v outcome=%q", shell.chatSessionLinked(), shell.chatConversationID, state.Waiting, state.Outcome)
	}

	// The agent finally reports its own session: the guess yields.
	shell.projection.Agents[0].AgentSession = &herdr.AgentSessionIdentity{Agent: "claude", Kind: "session_id", Value: "abc"}
	shell.rebindWorkspaceChat()
	if shell.chatSessionLinked() {
		t.Fatal("a hand-linked session must yield to the measured identity")
	}
}

// Right after a restart the pane is not in any projection yet: the page must
// say "loading", not invite a new conversation over an existing one — and the
// invitation returns once the runtime has described the pane and it truly has
// no session.
func TestChatLoadsInsteadOfInvitingBeforeTheFirstProjection(t *testing.T) {
	shell := NewShell()
	shell.selectedPaneID = "p1"
	state := shell.chatBridgeState()
	if !state.Loading || state.Waiting {
		t.Fatalf("before any projection: loading=%v waiting=%v, want loading only", state.Loading, state.Waiting)
	}

	shell, _ = sourcelessLinkShell(t)
	state = shell.chatBridgeState()
	if state.Loading || !state.Waiting {
		t.Fatalf("a described pane without a session: loading=%v waiting=%v, want the invitation", state.Loading, state.Waiting)
	}
}

// A pane the surface has not bound yet (Terminal showing, or the first
// frames after a restart) is undecided, not new: the page loads until the
// surface binds or latches the pane sourceless, and every projection is
// numbered so a late one can be dropped.
func TestUnboundKnownPaneLoadsAndProjectionsAreNumbered(t *testing.T) {
	shell, _ := sourcelessLinkShell(t)
	shell.chatSourcelessPaneID = "" // never bound by a Chat frame
	shell.chatPaneID = ""
	first := shell.chatBridgeState()
	if !first.Loading || first.Waiting {
		t.Fatalf("unbound known pane: loading=%v waiting=%v, want loading only", first.Loading, first.Waiting)
	}
	shell.rebindWorkspaceChat() // the Chat frame binds: no source → the invitation is now true
	second := shell.chatBridgeState()
	if second.Loading || !second.Waiting {
		t.Fatalf("after the bind: loading=%v waiting=%v, want the invitation", second.Loading, second.Waiting)
	}
	if second.Seq <= first.Seq {
		t.Fatalf("seq did not increase: %d then %d", first.Seq, second.Seq)
	}
}
