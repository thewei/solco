/**
 * [INPUT]: chat_stream.go, existing Shell live binding and chatstream event model
 * [OUTPUT]: exact pane/session/provider fence, replay guards and rebind clearing
 * [POS]: nativeui stream acceptance contract; no real provider, socket or window
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"testing"

	"github.com/thewei/solco/internal/chatstream"
	"github.com/thewei/solco/internal/history"
)

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
