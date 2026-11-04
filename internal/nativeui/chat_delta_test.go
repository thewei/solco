/**
 * [INPUT]: ChatDelta suffix patch builder and ChatBridgeState timeline projection
 * [OUTPUT]: bounded patch shape, empty and long conversation cases
 * [POS]: nativeui protocol regression; no MyGo window or external Agent
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"fmt"
	"testing"

	"github.com/thewei/solco/internal/chatsession"
)

func TestChatDeltaPatchSendsBoundedSuffixWithSequence(t *testing.T) {
	state := ChatBridgeState{Seq: 42, ConversationID: "live:omp:terminal"}
	for i := 0; i < 80; i++ {
		state.Turns = append(state.Turns, chatsession.ChatTurn{Text: fmt.Sprintf("%d", i)})
	}
	delta := chatDeltaPatch(state, 41)
	if delta.BaseSeq != 41 || delta.Seq != 42 || delta.ConversationID != state.ConversationID ||
		delta.Start != 77 || len(delta.Turns) != 3 || delta.Turns[0].Text != "77" || delta.Turns[2].Text != "79" {
		t.Fatalf("long window patch: %+v", delta)
	}
	state.Turns = state.Turns[:2]
	delta = chatDeltaPatch(state, 42)
	if delta.Start != 0 || len(delta.Turns) != 2 {
		t.Fatalf("short window patch: %+v", delta)
	}
	state.Turns = nil
	delta = chatDeltaPatch(state, 43)
	if delta.Start != 0 || len(delta.Turns) != 0 {
		t.Fatalf("empty patch: %+v", delta)
	}
}
