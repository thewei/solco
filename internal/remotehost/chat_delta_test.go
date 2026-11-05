package remotehost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/chatstream"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: canonical Host Chat frames and OMP apphook events.
 * [OUTPUT]: suffix-only framing plus identity, capacity, desync and shifted-window gates.
 * [POS]: SC-06 remote Host Chat delta unit regression.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestChatSuffixDeltaAndCanonicalFallback(t *testing.T) {
	base := chatWireFrame{T: "chat", PaneID: "w1:p1", AgentStatus: "working", Seq: 41,
		State: chatsession.State{Provider: "omp"},
	}
	for i := 0; i < 40; i++ {
		base.State.Turns = append(base.State.Turns, chatsession.ChatTurn{Narration: fmt.Sprintf("turn-%d", i)})
	}
	next := base
	next.Seq = 42
	next.State.Turns = append([]chatsession.ChatTurn(nil), base.State.Turns...)
	next.State.Turns[39].Narration = "Hello!"
	delta, ok := chatSuffixDelta(base, next)
	if !ok || delta.BaseSeq != 41 || delta.Seq != 42 || delta.Start != 37 || len(delta.Turns) != 3 || delta.Turns[2].Narration != "Hello!" {
		t.Fatalf("patch = %+v ok=%v", delta, ok)
	}
	changed := next
	changed.State.Turns = append([]chatsession.ChatTurn(nil), next.State.Turns...)
	changed.State.Turns = append(changed.State.Turns[1:], chatsession.ChatTurn{Narration: "new"})
	if _, ok := chatSuffixDelta(base, changed); ok {
		t.Fatal("rolled window accepted")
	}
	changed = next
	changed.State.Turns = []chatsession.ChatTurn{{Narration: "new short conversation"}}
	if _, ok := chatSuffixDelta(base, changed); ok {
		t.Fatal("40-turn window replaced by short result as a suffix")
	}
	changed = next
	changed.State.Model = "new model"
	if _, ok := chatSuffixDelta(base, changed); ok {
		t.Fatal("metadata changed without full frame")
	}
	changed = next
	changed.PaneID = "w2:p1"
	if _, ok := chatSuffixDelta(base, changed); ok {
		t.Fatal("pane switch accepted")
	}
	changed = next
	changed.AgentStatus = "blocked"
	if _, ok := chatSuffixDelta(base, changed); ok {
		t.Fatal("agent status changed without full frame")
	}
	changed = next
	changed.State.Turns = append([]chatsession.ChatTurn(nil), next.State.Turns...)
	changed.State.Turns[5].Narration = "prior turn changed"
	if _, ok := chatSuffixDelta(base, changed); ok {
		t.Fatal("early turn changed without full frame")
	}
}

func TestPiChatStreamReachesAuthenticatedWebSocket(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "pi.jsonl")
	var content string
	content += `{"type":"session","version":3,"id":"pi-smoke","timestamp":"2026-11-05T08:00:00Z","cwd":"/tmp"}` + "\n"
	for i := range 16 {
		content += fmt.Sprintf(`{"type":"message","timestamp":"2026-11-05T08:00:01Z","message":{"role":"user","content":[{"type":"text","text":"before %d"}]}}`, i) + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := fixtureProjection()
	proj.Agents = []herdr.Agent{{TerminalID: "term_a", PaneID: "w1:p1", Kind: "pi", Status: "working",
		AgentSession: &herdr.AgentSessionIdentity{Agent: "pi", Kind: "path", Value: path}}}
	h.back.set(proj)
	conn, _, err := h.dial("/api/v3/panes/w1:p1/chat/stream?instance=default", true)
	if err != nil {
		t.Fatal(err)
	}
	baseline := readJSON(t, conn)
	if baseline["t"] != "chat" {
		t.Fatalf("missing baseline: %+v", baseline)
	}
	for seq, kind := range []string{"message_start", "text_delta"} {
		e := chatstream.Event{Agent: "pi", PaneID: "w1:p1", SessionPath: path, Epoch: "00001-pi", Seq: uint64(seq + 1), Kind: kind, MessageID: "pi-1"}
		if kind == "text_delta" {
			e.Delta = "Hi from Pi"
		}
		h.srv.IngestOMPChatStream(e)
	}
	patch := readJSON(t, conn)
	if patch["t"] != "chat_delta" || patch["baseSeq"] != baseline["seq"] {
		t.Fatalf("Pi did not get suffix frame: %+v", patch)
	}
	list, _ := patch["turns"].([]any)
	if len(list) != 3 {
		t.Fatalf("Pi suffix size = %d", len(list))
	}
	tail, _ := list[len(list)-1].(map[string]any)
	if tail["narration"] != "Hi from Pi" {
		raw, _ := json.Marshal(tail)
		t.Fatalf("Pi live text not delivered: %s", raw)
	}
}

func TestPiAndOMPStreamCannotCrossProviderFence(t *testing.T) {
	var server Server
	path := filepath.Join(t.TempDir(), "session.jsonl")
	canonical := chatsession.State{Provider: "pi", Turns: []chatsession.ChatTurn{{User: true, Text: "one"}}}
	for _, agent := range []history.AgentID{history.AgentPi, history.AgentOMP} {
		start := chatstream.Event{Agent: string(agent), PaneID: "p1", SessionPath: path, Epoch: "000001-probe", Seq: 1, Kind: "message_start", MessageID: "m1"}
		server.IngestOMPChatStream(start)
		start.Seq, start.Kind, start.Delta = 2, "text_delta", string(agent)
		server.IngestOMPChatStream(start)
	}
	for _, agent := range []history.AgentID{history.AgentPi, history.AgentOMP} {
		got := server.chatStreamOverlay("p1", chatTarget{Provider: agent, Path: path}, canonical)
		if len(got.Turns) != 2 || got.Turns[1].Narration != string(agent) {
			t.Fatalf("cross provider stream: %s %+v", agent, got.Turns)
		}
	}
}

func TestOMPStreamFeedExactPaneAndSessionFence(t *testing.T) {
	s := &Server{}
	home := t.TempDir()
	path := filepath.Join(home, "conversation.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	evt := chatstream.Event{
		Agent: "omp", PaneID: "w1:p1", SessionPath: path,
		Epoch: "0000000000001-probe", Seq: 1, Kind: "message_start", MessageID: "m1",
	}
	s.IngestOMPChatStream(evt)
	evt.Seq = 2
	evt.Kind = "text_delta"
	evt.Delta = "partial"
	s.IngestOMPChatStream(evt)
	original := chatsession.State{Provider: "omp", Turns: []chatsession.ChatTurn{{User: true, Text: "hello"}}}
	matched := s.chatStreamOverlay("w1:p1", chatTarget{Provider: history.AgentOMP, Path: path}, original)
	if len(matched.Turns) != 2 || matched.Turns[1].Narration != "partial" {
		t.Fatalf("expected overlay: %+v", matched.Turns)
	}
	for _, bad := range []chatTarget{{Provider: history.AgentClaudeCode, Path: path}, {Provider: history.AgentOMP, Path: path + "-other"}} {
		if got := s.chatStreamOverlay("w1:p1", bad, original); len(got.Turns) != 1 {
			t.Fatalf("cross session/provider rendered: %+v", got.Turns)
		}
	}
	if got := s.chatStreamOverlay("w2:p1", chatTarget{Provider: history.AgentOMP, Path: path}, original); len(got.Turns) != 1 {
		t.Fatalf("cross Pane overlay rendered: %+v", got.Turns)
	}
	evt.Seq = 4 // missing seq=3 → provisional text is invalid
	evt.Delta = " unsafe"
	s.IngestOMPChatStream(evt)
	if got := s.chatStreamOverlay("w1:p1", chatTarget{Provider: history.AgentOMP, Path: path}, original); len(got.Turns) != 1 {
		t.Fatalf("lost event not fail-closed: %+v", got.Turns)
	}
}
