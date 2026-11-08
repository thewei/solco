/**
 * [INPUT]: apphook.Server chat.stream route and chatstream event contract
 * [OUTPUT]: Bounded socket/JSON-RPC integration contract: valid event observed, invalid rejected, legacy hook unaffected
 * [POS]: AppHook streaming adapter wire test
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package apphook

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thewei/solco/internal/chatstream"
)

func TestChatStreamOversizedSocketFrameFailsClosed(t *testing.T) {
	path := filepath.Join("/tmp", "sc-"+strconv.FormatInt(time.Now().UnixNano(), 36)+".sock")
	defer os.Remove(path)
	server := NewServer(path, Handler{OnChatStream: func(chatstream.Event) { t.Error("oversized frame dispatched") }})
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(strings.Repeat("x", 70*1024))); err != nil &&
		!errors.Is(err, syscall.EPIPE) && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatal(err)
	}
	// Early EPIPE is expected: the reader closes on the 64-KiB limit.
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("oversized frame was acknowledged")
	}
}

func TestChatStreamSocketDispatchRejectsInvalid(t *testing.T) {
	// macOS Unix sockets have a small sun_path limit; use a short path.
	path := filepath.Join("/tmp", "sc-"+strconv.FormatInt(time.Now().UnixNano(), 36)+".sock")
	defer os.Remove(path)
	got := make(chan chatstream.Event, 3)
	server := NewServer(path, Handler{OnChatStream: func(e chatstream.Event) { got <- e }})
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	send := func(params any) map[string]any {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"id": "test", "method": "chat.stream", "params": params})
		if _, err := c.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		line, err := bufio.NewReader(c).ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		resp := map[string]any{}
		if err := json.Unmarshal(line, &resp); err != nil {
			t.Fatal(err)
		}
		return resp["result"].(map[string]any)
	}
	valid := chatstream.Event{Agent: "omp", PaneID: "p", SessionPath: "/tmp/a.jsonl", Epoch: "process-1", Seq: 1, Kind: "message_start", MessageID: "m1"}
	if send(valid)["ok"] != true {
		t.Fatal("valid event rejected")
	}
	if send(map[string]any{"agent": "omp", "pane_id": "p", "kind": "text_delta"})["ok"] != false {
		t.Fatal("invalid accepted")
	}
	select {
	case v := <-got:
		if v.PaneID != "p" || v.Seq != 1 {
			t.Fatalf("dispatched %+v", v)
		}
	case <-time.After(time.Second):
		t.Fatal("event not dispatched")
	}
	select {
	case <-got:
		t.Fatal("invalid event dispatched")
	default:
	}
}
