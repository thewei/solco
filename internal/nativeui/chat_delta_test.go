/**
 * [INPUT]: ChatDelta suffix patch builder and ChatBridgeState timeline projection
 * [OUTPUT]: bounded suffix, metadata/prefix identity checks, rolling-window fallback and empty history tests
 * [POS]: nativeui protocol regression; no MyGo window or external Agent
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"context"
	"fmt"
	"testing"

	"github.com/thewei/solco/internal/chatsession"
)

func TestChatDeltaPatchSendsBoundedSuffixWithSequence(t *testing.T) {
	base := ChatBridgeState{Seq: 41, ConversationID: "live:omp:terminal"}
	for i := 0; i < 80; i++ {
		base.Turns = append(base.Turns, chatsession.ChatTurn{Text: fmt.Sprintf("%d", i)})
	}
	next := base
	next.Seq = 42
	next.Turns = append([]chatsession.ChatTurn(nil), base.Turns...)
	next.Turns[79].Text = "updated"
	delta, ok := chatDeltaPatch(base, next)
	if !ok || delta.BaseSeq != 41 || delta.Seq != 42 || delta.ConversationID != next.ConversationID ||
		delta.Start != 77 || len(delta.Turns) != 3 || delta.Turns[0].Text != "77" || delta.Turns[2].Text != "updated" {
		t.Fatalf("long window patch (ok=%v): %+v", ok, delta)
	}
	base = ChatBridgeState{Seq: 43, ConversationID: "small", Turns: []chatsession.ChatTurn{{Text: "first"}}}
	next = ChatBridgeState{Seq: 44, ConversationID: "small", Turns: []chatsession.ChatTurn{{Text: "first"}, {Text: "second"}}}
	delta, ok = chatDeltaPatch(base, next)
	if !ok || delta.Start != 0 || len(delta.Turns) != 2 {
		t.Fatalf("short window patch: %+v %v", delta, ok)
	}
	empty := ChatBridgeState{Seq: 45, ConversationID: "small"}
	delta, ok = chatDeltaPatch(next, empty)
	if !ok || delta.Start != 0 || len(delta.Turns) != 0 {
		t.Fatalf("empty patch: %+v %v", delta, ok)
	}
}

func TestChatBridgeSnapshotBecomesNextDeltaBaseline(t *testing.T) {
	shell := NewShell()
	shell.chatConversationID = "live:omp:term"
	state, err := NewChatBridge(shell).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if shell.chatLastPublishedState == nil || shell.chatLastPublishedState.Seq != state.Seq || shell.chatLastPublishedState.ConversationID != state.ConversationID {
		t.Fatalf("RPC snapshot was not recorded as delivered baseline: %+v", shell.chatLastPublishedState)
	}
}

func TestChatDeltaPatchRejectsShiftedPrefixAndMetadata(t *testing.T) {
	base := ChatBridgeState{Seq: 10, ConversationID: "live:omp:term"}
	for i := 0; i < 30; i++ {
		base.Turns = append(base.Turns, chatsession.ChatTurn{Text: fmt.Sprintf("turn-%d", i)})
	}
	clone := func() ChatBridgeState {
		next := base
		next.Seq = 11
		next.Turns = append([]chatsession.ChatTurn(nil), base.Turns...)
		return next
	}
	rolling := clone()
	rolling.Turns = append(rolling.Turns[1:], chatsession.ChatTurn{Text: "new"})
	if _, ok := chatDeltaPatch(base, rolling); ok {
		t.Fatal("rolling-window shifted prefix accepted")
	}

	earlyEdit := clone()
	earlyEdit.Turns[9].Text = "changed earlier transcript"
	if _, ok := chatDeltaPatch(base, earlyEdit); ok {
		t.Fatal("changed prefix accepted")
	}

	metadata := clone()
	metadata.Outcome = "permission required"
	if _, ok := chatDeltaPatch(base, metadata); ok {
		t.Fatal("metadata-only update accepted")
	}

	different := clone()
	different.ConversationID = "another-session"
	if _, ok := chatDeltaPatch(base, different); ok {
		t.Fatal("cross-conversation update accepted")
	}

	tail := clone()
	tail.Turns[28].Text = "refreshed"
	if _, ok := chatDeltaPatch(base, tail); !ok {
		t.Fatal("tail-only update unexpectedly rejected")
	}
}
