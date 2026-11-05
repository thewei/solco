package remotehost

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/chatstream"
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
