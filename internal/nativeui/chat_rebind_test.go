package nativeui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

// TestRebindWorkspaceChatSourcelessClearsStaleTranscript pins the bug where
// toggling to Chat on a session-less Agent kept the previous pane's
// conversation on screen: the fallthrough branch must drop to the empty
// state, latch the pane so the render-path rebind exits early, and never
// keep a foreign conversation id.
func TestRebindWorkspaceChatSourcelessClearsStaleTranscript(t *testing.T) {
	shell := NewShell()
	shell.loading = false
	shell.selectedPaneID = "p-new"
	shell.chatPaneID = "p-old"
	shell.chatConversationID = "live:claude:term-old"
	shell.chatTurns = []conversation.TimelineTurn{{UserRow: true, Text: "stale"}}
	shell.projection = herdr.Projection{
		Agents: []herdr.Agent{
			{TerminalID: "term-new", PaneID: "p-new", Status: "idle"},
		},
	}

	shell.rebindWorkspaceChat()

	if shell.chatPaneID != "p-new" {
		t.Fatalf("chatPaneID = %q, want p-new", shell.chatPaneID)
	}
	if shell.chatConversationID != "" {
		t.Fatalf("stale conversation survived: %q", shell.chatConversationID)
	}
	if len(shell.chatTurns) != 0 {
		t.Fatalf("stale turns survived: %d", len(shell.chatTurns))
	}
	if shell.chatSourcelessPaneID != "p-new" {
		t.Fatalf("sourceless latch = %q, want p-new", shell.chatSourcelessPaneID)
	}
	// The page shows its own invitation (type the first prompt), so the
	// toast that used to announce "no chat stream" is gone.
	if shell.pendingToast != "" {
		t.Fatalf("sourceless pane must not toast: %q", shell.pendingToast)
	}
	if !shell.chatBridgeState().Waiting {
		t.Fatal("a sourceless pane projects the invitation (Waiting)")
	}

	// The render path re-enters for the same pane: the latch makes it a
	// cheap exit with no second toast churn.
	shell.pendingToast = ""
	shell.rebindWorkspaceChat()
	if shell.pendingToast != "" {
		t.Fatalf("latched rebind re-notified: %q", shell.pendingToast)
	}
	if shell.chatSourcelessPaneID != "p-new" {
		t.Fatalf("latch lost across rebind: %q", shell.chatSourcelessPaneID)
	}
}

// TestRebindWorkspaceChatUnlatchesWhenSourceAppears pins the latch
// semantics: while the pane has no chat source the latch exits early, but
// once the card carries a conversation identity (agent established a
// session), the rebind must retry — headless the binds fail and the
// cleanup re-runs, which is the observable proof the latch was left.
func TestRebindWorkspaceChatUnlatchesWhenSourceAppears(t *testing.T) {
	shell := NewShell()
	shell.loading = false
	shell.selectedPaneID = "p-new"
	shell.chatSourcelessPaneID = "p-new"
	shell.chatPaneID = "p-new"
	shell.projection = herdr.Projection{
		Agents: []herdr.Agent{
			{TerminalID: "term-new", PaneID: "p-new", Status: "idle"},
		},
	}

	// Still sourceless: the latch exits early, no notice churn.
	shell.pendingToast = ""
	shell.rebindWorkspaceChat()
	if shell.pendingToast != "" {
		t.Fatalf("latched rebind re-notified while sourceless: %q", shell.pendingToast)
	}

	// The agent establishes a session: the card gains a conversation id.
	shell.workbench.directory = agent.NewAgentDirectory()
	shell.workbench.directory.Replace([]agent.AgentCardModel{{
		Key:            agent.AgentKey{InstanceID: "inst", TerminalID: "term-new"},
		PaneID:         "p-new",
		ConversationID: "live:claude:term-new",
	}})
	shell.pendingToast = ""
	shell.rebindWorkspaceChat()
	if shell.chatSourcelessPaneID == "p-new" && shell.pendingToast == "" {
		t.Fatal("the rebind must retry once the pane has a chat source")
	}
}

// The first prompt typed into a sourceless pane echoes at once (user row +
// EchoPending, Waiting off), survives the latched re-entry, and a provider
// row after the live bind consumes it exactly once.
func TestSourcelessPaneEchoesFirstPrompt(t *testing.T) {
	shell := NewShell()
	shell.loading = false
	shell.selectedPaneID = "p-new"
	shell.projection = herdr.Projection{
		Agents: []herdr.Agent{{TerminalID: "term-new", PaneID: "p-new", Status: "idle"}},
	}
	shell.rebindWorkspaceChat()
	if !shell.chatPaneSourceless() {
		t.Fatal("setup: the pane must be sourceless")
	}

	shell.chatAfterPrompt("req-1", "hello agent", conversation.DispositionSentNow, nil)
	if shell.chatPendingEcho == nil || shell.chatPendingEcho.Baseline != 0 || shell.chatPendingEcho.PaneID != "p-new" {
		t.Fatalf("pending echo = %#v", shell.chatPendingEcho)
	}
	state := shell.chatBridgeState()
	if state.Waiting || !state.EchoPending {
		t.Fatalf("after send: Waiting=%v EchoPending=%v, want false/true", state.Waiting, state.EchoPending)
	}
	if len(state.Turns) != 1 || !state.Turns[0].User || state.Turns[0].Text != "hello agent" {
		t.Fatalf("echo row = %+v", state.Turns)
	}

	// Latched re-entry (render path) keeps the single echo row.
	shell.rebindWorkspaceChat()
	if shell.chatPendingEcho == nil || countUserRows(shell.chatTurns, "hello agent") != 1 {
		t.Fatalf("re-entry lost or duplicated the echo: %#v / %+v", shell.chatPendingEcho, shell.chatTurns)
	}

	// A different pane drops it.
	shell.retainPendingEchoForPane("p-other")
	if shell.chatPendingEcho != nil {
		t.Fatal("echo must not follow a pane switch")
	}
}

// ToolDone is the "result landed" flag: an empty result is still finished,
// only a call whose Output was never set reads as running.
func TestBridgeStepToolDone(t *testing.T) {
	empty := ""
	shell := NewShell()
	shell.chatTurns = []conversation.TimelineTurn{{
		Steps: []conversation.TimelineStep{
			{Kind: conversation.StepTool, Tool: &conversation.ConversationToolCall{Name: "read", Output: &empty}},
			{Kind: conversation.StepTool, Tool: &conversation.ConversationToolCall{Name: "edit"}},
		},
	}}
	steps := shell.chatBridgeState().Turns[0].Steps
	if len(steps) != 2 || !steps[0].ToolDone || steps[1].ToolDone {
		t.Fatalf("toolDone = %+v", steps)
	}
}

// A fresh omp reports the session file it is about to write, but the file
// appears only with the first prompt. Chat must open on its invitation (not
// "Loading conversation…" forever), keep the latch across renders, and bind
// for real once the file exists.
func TestFreshAgentWithUnwrittenSessionFileOpensOnInvitation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "session.jsonl")
	shell := NewShell()
	shell.loading = false
	shell.selectedPaneID = "p-fresh"
	shell.projection = herdr.Projection{
		Agents: []herdr.Agent{{
			TerminalID:   "term-fresh",
			PaneID:       "p-fresh",
			Status:       "idle",
			AgentSession: &herdr.AgentSessionIdentity{Agent: "omp", Kind: "path", Source: "herdr:omp", Value: path},
		}},
	}

	shell.rebindWorkspaceChat()
	state := shell.chatBridgeState()
	if state.Loading || !state.Waiting || !shell.chatPaneSourceless() {
		t.Fatalf("fresh session: Loading=%v Waiting=%v sourceless=%v, want false/true/true", state.Loading, state.Waiting, shell.chatPaneSourceless())
	}

	// The render path re-enters every frame: the latch must survive it.
	shell.rebindWorkspaceChat()
	if !shell.chatPaneSourceless() {
		t.Fatal("latch lost on re-entry while the file is still missing")
	}

	// The first prompt creates the file; the next rebind binds the tail.
	header := "{\"type\":\"session\",\"version\":3,\"id\":\"s1\",\"timestamp\":\"2026-10-31T05:00:00Z\",\"cwd\":\"/tmp\"}\n"
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	shell.rebindWorkspaceChat()
	if shell.chatPaneSourceless() || shell.chatConversationID == "" {
		t.Fatalf("file exists but chat did not bind: id=%q", shell.chatConversationID)
	}
	if shell.chatBridgeState().Loading {
		t.Fatal("a header-only session must not stay Loading")
	}
	shell.stopLiveTail()
}

// Switching agents inside one pane (claude exits, omp starts) never changes
// the pane id, so the rebind must notice the occupant change itself: the
// bound live transcript yields and the new agent's session binds
// (2026-10-31 agent-switch report).
func TestRebindWorkspaceChatYieldsWhenPaneAgentSwaps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "omp-session.jsonl")
	header := "{\"type\":\"session\",\"version\":3,\"id\":\"s2\",\"timestamp\":\"2026-10-31T06:00:00Z\",\"cwd\":\"/tmp\"}\n"
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := NewShell()
	shell.loading = false
	shell.selectedPaneID = "p1"
	shell.chatPaneID = "p1"
	shell.chatConversationID = "live:claude-code:term-1"
	shell.chatLiveFingerprint = "claude-fp"
	shell.chatLiveProvider = history.AgentClaudeCode
	shell.chatTurns = []conversation.TimelineTurn{{UserRow: true, Text: "claude answer"}}
	shell.projection = herdr.Projection{
		Agents: []herdr.Agent{{
			TerminalID:   "term-1",
			PaneID:       "p1",
			Status:       "idle",
			AgentSession: &herdr.AgentSessionIdentity{Agent: "omp", Kind: "path", Source: "herdr:omp", Value: path},
		}},
	}

	shell.rebindWorkspaceChat()

	if shell.chatConversationID != "live:omp:term-1" {
		t.Fatalf("conversation = %q, want live:omp:term-1", shell.chatConversationID)
	}
	if shell.chatLiveProvider != history.AgentOMP {
		t.Fatalf("provider = %q, want omp", shell.chatLiveProvider)
	}
	if shell.chatLiveFingerprint == "claude-fp" {
		t.Fatal("the binding kept the previous agent's fingerprint")
	}
	for _, turn := range shell.chatTurns {
		if turn.UserRow && turn.Text == "claude answer" {
			t.Fatal("the previous agent's transcript survived the switch")
		}
	}
	shell.stopLiveTail()
}

// The same agent restarting into a new session (claude --resume) changes the
// measured fingerprint without changing the pane: the tail must follow the
// new transcript, not keep the old one.
func TestRebindWorkspaceChatFollowsSessionRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "omp-restarted.jsonl")
	header := "{\"type\":\"session\",\"version\":3,\"id\":\"s3\",\"timestamp\":\"2026-10-31T07:00:00Z\",\"cwd\":\"/tmp\"}\n"
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := NewShell()
	shell.loading = false
	shell.selectedPaneID = "p1"
	shell.chatPaneID = "p1"
	shell.chatConversationID = "live:omp:term-1"
	shell.chatLiveFingerprint = "old-session-fp"
	shell.chatLiveProvider = history.AgentOMP
	shell.chatLivePath = "/old/session.jsonl"
	shell.projection = herdr.Projection{
		Agents: []herdr.Agent{{
			TerminalID:   "term-1",
			PaneID:       "p1",
			Status:       "idle",
			AgentSession: &herdr.AgentSessionIdentity{Agent: "omp", Kind: "path", Source: "herdr:omp", Value: path},
		}},
	}

	shell.rebindWorkspaceChat()

	if shell.chatLivePath != path {
		t.Fatalf("tail path = %q, want the restarted session %q", shell.chatLivePath, path)
	}
	if shell.chatLiveFingerprint == "old-session-fp" {
		t.Fatal("the binding kept the restarted session's old fingerprint")
	}
	shell.stopLiveTail()
}

// A swapped-in agent that has not established a session yet (no measured
// identity) still owns the pane: the stale transcript yields to the
// sourceless invitation instead of lingering until the first prompt.
func TestRebindWorkspaceChatYieldsToSourcelessOnProviderSwap(t *testing.T) {
	shell := NewShell()
	shell.loading = false
	shell.selectedPaneID = "p1"
	shell.chatPaneID = "p1"
	shell.chatConversationID = "live:claude-code:term-1"
	shell.chatLiveFingerprint = "claude-fp"
	shell.chatLiveProvider = history.AgentClaudeCode
	shell.chatTurns = []conversation.TimelineTurn{{UserRow: true, Text: "claude answer"}}
	shell.projection = herdr.Projection{
		Agents: []herdr.Agent{{TerminalID: "term-1", PaneID: "p1", Status: "idle", Kind: "omp"}},
	}

	shell.rebindWorkspaceChat()

	if shell.chatConversationID != "" {
		t.Fatalf("stale conversation survived: %q", shell.chatConversationID)
	}
	if len(shell.chatTurns) != 0 {
		t.Fatalf("stale turns survived: %d", len(shell.chatTurns))
	}
	if !shell.chatPaneSourceless() {
		t.Fatal("the pane must latch sourceless until omp writes its session")
	}
	if !shell.chatBridgeState().Waiting {
		t.Fatal("a swapped-in sourceless agent projects the invitation")
	}
}

// The steady state must not thrash: an unchanged occupant rebind is a cheap
// early return that keeps the bound transcript (the render path re-enters
// every frame).
func TestRebindWorkspaceChatKeepsCurrentLiveSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "omp-session.jsonl")
	header := "{\"type\":\"session\",\"version\":3,\"id\":\"s4\",\"timestamp\":\"2026-10-31T08:00:00Z\",\"cwd\":\"/tmp\"}\n"
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	identity := herdr.AgentSessionIdentity{Agent: "omp", Kind: "path", Source: "herdr:omp", Value: path}
	fingerprint := conversation.SessionIdentity{
		Provider: string(history.AgentOMP),
		Kind:     identity.Kind,
		Source:   identity.Source,
		Value:    identity.Value,
	}.Fingerprint()
	shell := NewShell()
	shell.loading = false
	shell.selectedPaneID = "p1"
	shell.chatPaneID = "p1"
	shell.chatConversationID = "live:omp:term-1"
	shell.chatLiveFingerprint = fingerprint
	shell.chatLiveProvider = history.AgentOMP
	shell.chatLivePath = path
	shell.chatTurns = []conversation.TimelineTurn{{UserRow: true, Text: "kept"}}
	shell.projection = herdr.Projection{
		Agents: []herdr.Agent{{TerminalID: "term-1", PaneID: "p1", Status: "idle", AgentSession: &identity}},
	}

	shell.rebindWorkspaceChat()

	if shell.chatLiveFingerprint != fingerprint || shell.chatLivePath != path {
		t.Fatal("steady-state rebind thrashed the live binding")
	}
	if countUserRows(shell.chatTurns, "kept") != 1 {
		t.Fatalf("steady-state rebind disturbed the transcript: %+v", shell.chatTurns)
	}
}
