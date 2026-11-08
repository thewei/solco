/**
 * [INPUT]: chat_stream.go, existing Shell live binding and chatstream event model
 * [OUTPUT]: exact pane/session/provider fence, replay guards and rebind clearing
 * [POS]: nativeui stream acceptance contract; no real provider, socket or window
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thewei/solco/internal/apphook"
	"github.com/thewei/solco/internal/chatstream"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

// The fixture owns a real private Socket; it does not start a provider or
// touch the user's Herdr runtime. It proves Socket -> exact Pane gate ->
// ChatBridgeState, including the fail-closed loss of a token frame.
func TestChatStreamSocketToChatBridgeProjection(t *testing.T) {
	shell := NewShell()
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	shell.chatPaneID = "stream-pane"
	shell.chatLiveProvider = history.AgentOMP
	shell.chatLivePath = path
	shell.chatConversationID = "live:omp:stream-pane"
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("sc-%d.sock", time.Now().UnixNano()))
	defer os.Remove(sock)
	got := make(chan struct{}, 8)
	server := apphook.NewServer(sock, apphook.Handler{OnChatStream: func(e chatstream.Event) {
		shell.handleAppChatStream(e)
		got <- struct{}{}
	}})
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	send := func(seq uint64, kind, delta string) {
		t.Helper()
		e := chatstream.Event{Agent: "omp", PaneID: "stream-pane", SessionPath: path, Epoch: "0000000000001-probe", Seq: seq, Kind: kind, MessageID: "m1", Delta: delta}
		wire, err := json.Marshal(map[string]any{"id": seq, "method": "chat.stream", "params": e})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(append(wire, '\n')); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := reader.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		select {
		case <-got:
		case <-time.After(time.Second):
			t.Fatal("no callback")
		}
	}
	send(1, "message_start", "")
	send(2, "text_delta", "partial")
	if view := shell.chatBridgeState().Turns; len(view) != 1 || view[0].Narration != "partial" {
		t.Fatalf("Socket delta not rendered: %+v", view)
	}
	send(4, "text_delta", "invalid suffix") // missing seq=3
	if view := shell.chatBridgeState().Turns; len(view) != 0 || !shell.chatStream.Desynced {
		t.Fatalf("lost token rendered as complete message: %+v", view)
	}
}

// A run may finish while its Pane is not the visible Chat. The extension
// still runs in Herdr, so a later visit must not inherit stale Solco/OSC
// working just because the Chat bridge was bound somewhere else.
func TestBackgroundAgentCompletionRetiresStaleWorkingBeforeChatOpens(t *testing.T) {
	s := NewShell()
	path := filepath.Join(t.TempDir(), "background.jsonl")
	s.projection = herdr.Projection{
		Panes: []herdr.Pane{
			{ID: "visible", TerminalID: "term-visible"},
			{ID: "background", TerminalID: "term-bg", AgentStatus: "working"},
		},
		Agents: []herdr.Agent{{
			PaneID: "background", TerminalID: "term-bg", Kind: "omp", Status: "working",
			AgentSession: &herdr.AgentSessionIdentity{Agent: "omp", Kind: "path", Value: path},
		}},
	}
	s.chatPaneID = "visible" // User is inspecting another Chat or Terminal.
	s.chatLiveProvider = history.AgentPi
	s.chatLivePath = "/tmp/visible-pi.jsonl"
	s.statusStore.ApplySolcoHookStatus("background", "working", time.Now())
	event := chatstream.Event{Agent: "omp", PaneID: "background", SessionPath: path, Epoch: "0001", MessageID: "bg-message", Seq: 1, Kind: "message_start"}
	s.handleAppChatStream(event)
	event.Seq, event.Kind = 2, "message_end"
	s.handleAppChatStream(event)
	if s.projection.Agents[0].Status != "working" {
		t.Fatal("message_end incorrectly retired a live background agent")
	}
	event.Seq, event.Kind = 3, "agent_end"
	for _, tamper := range []func(*chatstream.Event){
		func(e *chatstream.Event) { e.Agent = "pi" },
		func(e *chatstream.Event) { e.PaneID = "visible" },
		func(e *chatstream.Event) { e.SessionPath += "-other" },
		func(e *chatstream.Event) { e.Epoch = "0000" },
		func(e *chatstream.Event) { e.MessageID = "previous-run" },
	} {
		wrong := event
		tamper(&wrong)
		s.handleAppChatStream(wrong)
		if got := s.projection.Agents[0].Status; got != "working" {
			t.Fatalf("foreign/delayed agent_end completed background Pane: %s", got)
		}
	}
	s.handleAppChatStream(event)
	if got := s.projection.Agents[0].Status; got != "idle" {
		t.Fatalf("background agent remains stale after its real run ended: %s", got)
	}
	if got := s.statusStore.Display("background"); got != "idle" {
		t.Fatalf("status store remained working after background completion: %s", got)
	}
	// A genuinely new message transitions back to working; replayed old
	// agent_end must not retire this newer turn.
	event.Seq, event.Kind, event.MessageID = 4, "message_start", "next-run"
	s.handleAppChatStream(event)
	if got := s.projection.Agents[0].Status; got != "working" {
		t.Fatalf("new background prompt did not enter working: %s", got)
	}
	delayed := event
	delayed.Seq, delayed.Kind, delayed.MessageID = 5, "agent_end", "bg-message"
	s.handleAppChatStream(delayed)
	if got := s.projection.Agents[0].Status; got != "working" {
		t.Fatalf("late completion of an old run retired newer turn: %s", got)
	}
}

func TestFinalAgentTurnRetiresStaleSolcoWorkingStatus(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p1", TerminalID: "term-1", AgentStatus: "working"}},
		Agents: []herdr.Agent{{PaneID: "p1", TerminalID: "term-1", Kind: "omp", Status: "working"}},
	}
	s.chatPaneID = "p1"
	s.chatLiveProvider = history.AgentOMP
	s.chatLivePath = "/tmp/test-omp-completed.jsonl"
	s.chatConversationID = "live:omp:term-1"
	s.statusStore.ApplySolcoHookStatus("p1", "working", time.Now())
	e := chatstream.Event{Agent: "omp", PaneID: "p1", SessionPath: s.chatLivePath, Epoch: "0001", MessageID: "m1", Seq: 1, Kind: "message_start"}
	s.handleAppChatStream(e)
	e.Seq, e.Kind, e.Delta = 2, "text_delta", "The answer is done."
	s.handleAppChatStream(e)
	e.Seq, e.Kind, e.Delta = 3, "message_end", ""
	s.handleAppChatStream(e)
	if s.projection.Agents[0].Status != "working" {
		t.Fatal("message_end alone is NOT the end of an agent turn")
	}
	if before := s.chatBridgeState(); before.Disposition != "queue" || len(before.Turns) == 0 || !before.Turns[len(before.Turns)-1].Live {
		t.Fatalf("pre-completion UI should still show Stop and live caret: %+v", before)
	}
	e.Seq, e.Kind = 4, "agent_end"
	s.handleAppChatStream(e)
	if s.projection.Agents[0].Status != "idle" || s.statusStore.Display("p1") != "idle" {
		t.Fatalf("stale working/Stop persisted after agent_end: status=%q hook=%q", s.projection.Agents[0].Status, s.statusStore.Display("p1"))
	}
	if after := s.chatBridgeState(); after.Disposition != "ready" || len(after.Turns) == 0 || after.Turns[len(after.Turns)-1].Live {
		t.Fatalf("finished UI kept Stop or the blinking caret: %+v", after)
	}
	e.Seq, e.Kind, e.MessageID = 5, "message_start", "m2"
	s.handleAppChatStream(e)
	if s.projection.Agents[0].Status != "working" {
		t.Fatal("next turn must restore working")
	}
}

func TestFinalAgentTurnRetiresStaleOSCProgramWorking(t *testing.T) {
	s := NewShell()
	s.projection = herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p1", TerminalID: "term-1", AgentStatus: "working"}},
		Agents: []herdr.Agent{{PaneID: "p1", TerminalID: "term-1", Kind: "omp", Status: "working"}},
	}
	s.chatPaneID = "p1"
	s.chatLiveProvider = history.AgentOMP
	s.chatLivePath = "/tmp/test-osc-ended.jsonl"
	s.chatConversationID = "live:omp:term-1"
	s.applyProgramStatus("p1", []byte("state=working:app=omp"))
	evt := chatstream.Event{Agent: "omp", PaneID: "p1", SessionPath: s.chatLivePath, Epoch: "0001", MessageID: "m1", Seq: 1, Kind: "message_start"}
	s.handleAppChatStream(evt)
	evt.Seq, evt.Kind, evt.Delta = 2, "text_delta", "finished"
	s.handleAppChatStream(evt)
	evt.Seq, evt.Kind, evt.Delta = 3, "message_end", ""
	s.handleAppChatStream(evt)
	evt.Seq, evt.Kind = 4, "agent_end"
	s.handleAppChatStream(evt)
	if state := s.chatBridgeState(); state.Disposition != "ready" || state.Turns[len(state.Turns)-1].Live {
		t.Fatalf("OSC outlived actual provider run: status=%q hook=%q projection=%+v", state.Disposition, s.statusStore.Display("p1"), state.Turns)
	}
}

func TestPiChatStreamOnlyAffectsBoundPiConversation(t *testing.T) {
	s := &Shell{chatPaneID: "p1", chatLiveProvider: history.AgentPi, chatLivePath: "/tmp/pi-session.jsonl"}
	start := chatstream.Event{Agent: "pi", PaneID: "p1", SessionPath: "/tmp/pi-session.jsonl", Epoch: "000001-pi", Seq: 1, Kind: "message_start", MessageID: "m1"}
	other := start
	other.Agent = "omp"
	s.handleAppChatStream(other)
	if s.chatStream != nil {
		t.Fatal("OMP event was rendered in Pi Chat")
	}
	s.handleAppChatStream(start)
	start.Seq, start.Kind, start.Delta = 2, "text_delta", "streaming pi"
	s.handleAppChatStream(start)
	if s.chatStream == nil || s.chatStream.Text != "streaming pi" {
		t.Fatalf("Pi Chat lost delta: %+v", s.chatStream)
	}
}

func TestChatStreamOnlyAffectsBoundOMPConversation(t *testing.T) {
	s := &Shell{chatPaneID: "p1", chatLiveProvider: history.AgentOMP, chatLivePath: "/tmp/session.jsonl"}
	makeEvent := func(seq uint64, kind, delta string) chatstream.Event {
		return chatstream.Event{Agent: "omp", PaneID: "p1", SessionPath: "/tmp/session.jsonl", Epoch: "run-1", Seq: seq, Kind: kind, MessageID: "msg-1", Delta: delta}
	}
	wrongPane := makeEvent(1, "message_start", "")
	wrongPane.PaneID = "p2"
	s.handleAppChatStream(wrongPane)
	if s.chatStream != nil {
		t.Fatal("cross-pane event accepted")
	}
	wrongSession := makeEvent(1, "message_start", "")
	wrongSession.SessionPath = "/tmp/another.jsonl"
	s.handleAppChatStream(wrongSession)
	if s.chatStream != nil {
		t.Fatal("cross-session event accepted")
	}
	s.handleAppChatStream(makeEvent(1, "message_start", ""))
	s.handleAppChatStream(makeEvent(2, "text_delta", "Hello"))
	s.handleAppChatStream(makeEvent(2, "text_delta", " duplicate"))
	if s.chatStream == nil || s.chatStream.Text != "Hello" {
		t.Fatalf("overlay %+v", s.chatStream)
	}
	s.stopLiveTail()
	if s.chatStream != nil {
		t.Fatal("overlay outlived conversation rebind")
	}
	s.chatLiveProvider = history.AgentClaudeCode
	s.chatLivePath = "/tmp/session.jsonl"
	s.handleAppChatStream(makeEvent(1, "message_start", ""))
	if s.chatStream != nil {
		t.Fatal("OMP event accepted for Claude chat")
	}
}

// An omp pane's chat stream says "working" at every message_start; when the
// agent then parks on an ask, Herdr's snapshot says blocked. The overlay used
// to keep Solco's working, so no Question card ever surfaced (2026-11-06).
func TestRuntimeBlockedSurfacesTranscriptQuestionDespiteStreamWorking(t *testing.T) {
	s := NewShell()
	s.loading = false
	s.chatPaneID = "p1"
	s.chatLiveProvider = history.AgentOMP
	s.chatLivePath = "/tmp/ask-mask.jsonl"
	s.chatConversationID = "live:omp:term-1"
	input := `{"questions":[{"question":"Authorize?","options":[{"label":"A"},{"label":"B"}]}]}`
	s.chatTurns = []conversation.TimelineTurn{{Steps: []conversation.TimelineStep{{Kind: "tool", Tool: &conversation.ConversationToolCall{ID: "c1", Name: "ask", Input: &input}}}}}
	s.projection = herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p1", TerminalID: "term-1", AgentStatus: "working"}},
		Agents: []herdr.Agent{{PaneID: "p1", TerminalID: "term-1", Kind: "omp", Status: "working"}},
	}
	s.handleAppChatStream(chatstream.Event{Agent: "omp", PaneID: "p1", SessionPath: s.chatLivePath, Epoch: "0001", MessageID: "m1", Seq: 1, Kind: "message_start"})

	blocked := s.projection
	blocked.Agents = []herdr.Agent{{PaneID: "p1", TerminalID: "term-1", Kind: "omp", Status: "blocked"}}
	blocked.Panes = []herdr.Pane{{ID: "p1", TerminalID: "term-1", AgentStatus: "blocked"}}
	s.applyProjection(blocked, false)
	if got := s.projection.Agents[0].Status; got != "blocked" {
		t.Fatalf("Herdr says blocked, Solco shows %q", got)
	}
	if len(s.interactionBroker.PendingForPane("p1")) == 0 {
		t.Fatal("a blocked omp pane must surface its ask as a Question card")
	}

	// The answer: Herdr leaves blocked, Solco's own working returns, the card retires.
	working := s.projection
	working.Agents = []herdr.Agent{{PaneID: "p1", TerminalID: "term-1", Kind: "omp", Status: "working"}}
	working.Panes = []herdr.Pane{{ID: "p1", TerminalID: "term-1", AgentStatus: "working"}}
	s.applyProjection(working, false)
	if got := s.projection.Agents[0].Status; got != "working" {
		t.Fatalf("after the answer the status must fall back to working, got %q", got)
	}
	if len(s.interactionBroker.PendingForPane("p1")) != 0 {
		t.Fatal("the card must retire once Herdr leaves blocked")
	}
}

func edgeSnapshot(status string, seq uint64) herdr.Projection {
	return herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p1", TerminalID: "term-1", AgentStatus: status}},
		Agents: []herdr.Agent{{PaneID: "p1", TerminalID: "term-1", Kind: "omp", Status: status, StateChangeSeq: seq}},
	}
}

func edgeShell(t *testing.T) *Shell {
	t.Helper()
	s := NewShell()
	s.loading = false
	s.chatPaneID = "p1"
	s.chatLiveProvider = history.AgentOMP
	s.chatLivePath = "/tmp/edge.jsonl"
	s.chatConversationID = "live:omp:term-1"
	s.projection = edgeSnapshot("idle", 10)
	s.applyProjection(edgeSnapshot("idle", 10), false) // the baseline observation
	return s
}

// A run whose agent_end never arrived (aborted, extension died) must not keep
// Chat on Stop once Herdr measures the pane idle (2026-11-06 follow-up).
func TestHerdrIdleEdgeRetiresStreamWorkingWhenAgentEndIsLost(t *testing.T) {
	s := edgeShell(t)
	s.handleAppChatStream(chatstream.Event{Agent: "omp", PaneID: "p1", SessionPath: s.chatLivePath, Epoch: "0001", MessageID: "m1", Seq: 1, Kind: "message_start"})
	s.applyProjection(edgeSnapshot("working", 11), false)
	if got := s.projection.Agents[0].Status; got != "working" {
		t.Fatalf("mid-run status = %q", got)
	}
	s.applyProjection(edgeSnapshot("idle", 12), false)
	if got := s.projection.Agents[0].Status; got != "idle" {
		t.Fatalf("Herdr measured idle after the run, Solco still shows %q", got)
	}
}

// After agent_end, a run the stream never announced must not read idle.
func TestHerdrWorkingEdgeRetiresStreamIdleAfterAgentEnd(t *testing.T) {
	s := edgeShell(t)
	for i, kind := range []string{"message_start", "message_end", "agent_end"} {
		s.handleAppChatStream(chatstream.Event{Agent: "omp", PaneID: "p1", SessionPath: s.chatLivePath, Epoch: "0001", MessageID: "m1", Seq: uint64(i + 1), Kind: kind})
	}
	if got := s.projection.Agents[0].Status; got != "idle" {
		t.Fatalf("after agent_end = %q", got)
	}
	s.applyProjection(edgeSnapshot("idle", 10), false) // baseline for the idle guess
	s.applyProjection(edgeSnapshot("working", 13), false)
	if got := s.projection.Agents[0].Status; got != "working" {
		t.Fatalf("Herdr measured a new run, Solco still shows %q", got)
	}
}
