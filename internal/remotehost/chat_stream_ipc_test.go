package remotehost

/**
 * [INPUT]: Real private apphook Unix socket, fake Herdr projection, isolated OMP JSONL, authenticated Chat WS.
 * [OUTPUT]: Proves bounded, low-latency extension -> socket -> Host -> mobile delta, no duplicate OMP process.
 * [POS]: SC-06 end-to-end IPC regression; no user socket, real Herdr or network API.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/internal/apphook"
	"github.com/thewei/solco/internal/chatstream"
	"github.com/thewei/solco/internal/herdr"
)

func TestOMPAppHookSocketToAuthenticatedMobileChatDelta(t *testing.T) {
	h := newHarness(t)
	session := filepath.Join(t.TempDir(), "omp.jsonl")
	var body strings.Builder
	body.WriteString(`{"type":"session","version":3,"id":"omp-1","timestamp":"2026-11-05T08:00:00Z","cwd":"/tmp"}` + "\n")
	for i := 0; i < 24; i++ {
		fmt.Fprintf(&body, `{"type":"message","timestamp":"2026-11-05T08:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hello-%d"}]}}`+"\n", i)
	}
	if err := os.WriteFile(session, []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := fixtureProjection()
	proj.Agents = []herdr.Agent{{
		TerminalID: "term_a", PaneID: "w1:p1", Kind: "omp", Status: "working",
		AgentSession: &herdr.AgentSessionIdentity{Agent: "omp", Kind: "path", Value: session},
	}}
	h.back.set(proj)

	dir, err := os.MkdirTemp("/tmp", "sh-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "event.sock")
	ingest := apphook.NewServer(sock, apphook.Handler{OnChatStream: h.srv.IngestOMPChatStream})
	if err := ingest.Start(); err != nil {
		t.Fatal(err)
	}
	defer ingest.Close()

	mobile, _, err := h.dial("/api/v3/panes/w1:p1/chat/stream?instance=default", true)
	if err != nil {
		t.Fatal(err)
	}
	baseline := readJSON(t, mobile)
	if baseline["t"] != "chat" {
		t.Fatalf("first frame %v", baseline)
	}
	ipc, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer ipc.Close()
	writer := bufio.NewWriter(ipc)
	reader := bufio.NewReader(ipc)
	start := time.Now()
	for seq := uint64(1); seq <= 65; seq++ {
		e := chatstream.Event{Agent: "omp", PaneID: "w1:p1", SessionPath: session, Epoch: "00000000000001", MessageID: "m1", Seq: seq}
		if seq == 1 {
			e.Kind = "message_start"
		} else {
			e.Kind = "text_delta"
			e.Delta = "x"
		}
		raw, _ := json.Marshal(map[string]any{"method": "chat.stream", "id": seq, "params": e})
		if _, err := writer.Write(append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	_ = ipc.SetReadDeadline(time.Now().Add(3 * time.Second))
	for range 65 {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("apphook did not process all events: %v", err)
		}
		var reply struct {
			Result struct {
				OK bool `json:"ok"`
			} `json:"result"`
		}
		if err := json.Unmarshal(line, &reply); err != nil || !reply.Result.OK {
			t.Fatalf("invalid apphook ack %v %s", err, line)
		}
	}
	next := readJSON(t, mobile)
	if next["t"] != "chat_delta" {
		t.Fatalf("expected token suffix frame: %v", next["t"])
	}
	if time.Since(start) > 900*time.Millisecond {
		t.Fatalf("OMP IPC fell back to 1s polling: %v", time.Since(start))
	}
	delta, _ := next["turns"].([]any)
	if len(delta) != 3 {
		t.Fatalf("wanted last 3 turns only, got %d", len(delta))
	}
	tail, _ := delta[2].(map[string]any)
	if tail["narration"] != strings.Repeat("x", 64) {
		t.Fatalf("lost/doubled burst tokens: %+v", tail)
	}
}
