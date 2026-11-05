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
