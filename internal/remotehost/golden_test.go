package remotehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/thewei/solco/internal/herdr"
)

// [INPUT]: server_test.go 的假 Backend 与 harness，真实的 handler / WebSocket 帧
// [OUTPUT]: testdata/api-v3/*.json|txt 黄金文件：手机端（solco-mobile 的 Zod 契约测试）逐字拷贝并解析
// [POS]: /api/v3 线格式的 Go 半边钉死：handler 输出与黄金文件逐字一致，漂移 = 两端同批修复；`go test ./internal/remotehost -run Golden -update` 重生成
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

var updateGolden = flag.Bool("update", false, "rewrite testdata/api-v3 from the live handlers")

const goldenDir = "testdata/api-v3"

var (
	goldenDeviceID = regexp.MustCompile(`dev_[0-9a-f]{16}`)
	goldenToken    = regexp.MustCompile(`"token": ?"[^"]+"`)
	goldenCode     = regexp.MustCompile(`c=[A-Z2-7]{26}`)
)

// golden compares one wire payload (JSON bytes) with its file, canonically
// indented so the file reads well and diffs cleanly.
func golden(t *testing.T, name string, raw []byte) {
	t.Helper()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, bytes.TrimSpace(raw), "", "  "); err != nil {
		t.Fatalf("%s: not JSON: %v\n%s", name, err, raw)
	}
	pretty.WriteByte('\n')
	goldenText(t, name, pretty.Bytes())
}

func goldenText(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join(goldenDir, name)
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s missing (run with -update): %v", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s drifted from the live handler (run with -update, then sync solco-mobile):\n--- want\n%s--- got\n%s", name, want, got)
	}
}

func readRaw(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("want a text frame, got %v", typ)
	}
	return data
}

func bodyOf(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGoldenAPIv3(t *testing.T) {
	h := newHarness(t)
	h.back.screen = "ls\nfoo bar\n│       │"

	// A listening Host, as pairing needs one (the harness serves the handler directly).
	const fp = "a1cc1d2af9c5a03eefb30bfcc11c5bae5554e2cee2ff007c8cde90e0a096ad57"
	h.srv.mu.Lock()
	h.srv.fingerprint, h.srv.listenHost, h.srv.listenPort = fp, "192.168.1.20", "7788"
	h.srv.mu.Unlock()
	h.srv.opts.HostName = "Test Mac"

	// pairing: the offer URL and the POST /pair answer
	offer, err := h.srv.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	goldenText(t, "pair-url.txt", []byte(goldenCode.ReplaceAllString(offer.URL, "c=AAAAAAAAAAAAAAAAAAAAAAAAAA")+"\n"))
	resp, err := http.Post(h.http.URL+"/api/v3/pair", "application/json",
		strings.NewReader(`{"code":"`+offer.Code+`","deviceName":"Test Phone"}`))
	if err != nil {
		t.Fatal(err)
	}
	pair := bodyOf(t, resp)
	pair = goldenToken.ReplaceAll(goldenDeviceID.ReplaceAll(pair, []byte("dev_0000000000000000")), []byte(`"token":"tok_0000000000000000000000000000000000000000000"`))
	golden(t, "pair.json", pair)

	// plain HTTP
	for name, path := range map[string]string{
		"bootstrap.json": "/api/v3/bootstrap",
		"tree.json":      "/api/v3/tree",
		"peek.json":      "/api/v3/panes/w1:p1/peek",
	} {
		golden(t, name, bodyOf(t, h.get(path, h.token, nil)))
	}
	golden(t, "error-unauthorized.json", bodyOf(t, h.get("/api/v3/tree", "", nil)))
	golden(t, "error-unknown-instance.json", bodyOf(t, h.get("/api/v3/tree?instance=ghost", h.token, nil)))

	// events: hello, then tree.changed after a visible change
	events, _, err := h.dial("/api/v3/events", true)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "events-hello.json", readRaw(t, events))
	src := h.back.source(0)
	if src == nil {
		t.Fatal("no event subscription")
	}
	proj := fixtureProjection()
	proj.Panes[0].AgentStatus = "blocked"
	h.back.set(proj)
	src.events <- herdr.RuntimeEvent{Event: "pane.updated"}
	golden(t, "events-tree-changed.json", readRaw(t, events))

	// terminal: frame header, ping, bad input, refused input
	term, _, err := h.dial("/api/v3/terminals/term_b/stream?cols=51&rows=19", true)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "terminal-frame-header.json", readRaw(t, term))
	readBinary(t, term)
	writeClient(t, term, map[string]any{"t": "ping"})
	golden(t, "terminal-pong.json", readRaw(t, term))
	writeClient(t, term, map[string]any{"t": "input", "keys": []string{"enter; rm -rf"}})
	golden(t, "terminal-input-error-bad.json", readRaw(t, term))
	h.back.mu.Lock()
	h.back.inputErr = errors.New("agent is busy")
	h.back.mu.Unlock()
	writeClient(t, term, map[string]any{"t": "input", "text": "ls", "keys": []string{"enter"}})
	golden(t, "terminal-input-error-refused.json", readRaw(t, term))

	// chat (P2, read-only): the shared chatsession projection of a Claude
	// transcript, as a one-shot read and as a stream frame
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(claudeFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	chatProj := fixtureProjection()
	chatProj.Agents = []herdr.Agent{{
		TerminalID: "term_a", PaneID: "w1:p1", Kind: "claude", Status: "working",
		AgentSession: &herdr.AgentSessionIdentity{Agent: "claude", Kind: "path", Value: path},
	}}
	h.back.set(chatProj)
	golden(t, "chat.json", bodyOf(t, h.get("/api/v3/panes/w1:p1/chat", h.token, nil)))
	golden(t, "error-no-conversation.json", bodyOf(t, h.get("/api/v3/panes/w1:p2/chat", h.token, nil)))
	chat, _, err := h.dial("/api/v3/panes/w1:p1/chat/stream", true)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "chat-frame.json", readRaw(t, chat))

	// chat send/stop (P3): the request body, the ack and a named refusal
	rawReq, _ := json.Marshal(chatSendRequest{Text: "run the tests", Interrupt: true})
	golden(t, "chat-client-send.json", rawReq)
	_, _ = h.post("/api/v3/panes/w1:p1/chat/stop", h.token, "")
	req, _ := http.NewRequest("POST", h.http.URL+"/api/v3/panes/w1:p1/chat/send", strings.NewReader(`{"text":"x"}`))
	req.Header.Set("Authorization", "Bearer "+h.token)
	busy, err := http.DefaultClient.Do(req) // the fixture agent is working → busy
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "error-busy.json", bodyOf(t, busy))
	chatProj.Panes[0].AgentStatus = "idle"
	h.back.set(chatProj)
	req, _ = http.NewRequest("POST", h.http.URL+"/api/v3/panes/w1:p1/chat/send", strings.NewReader(`{"text":"x"}`))
	req.Header.Set("Authorization", "Bearer "+h.token)
	sent, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "chat-send-ok.json", bodyOf(t, sent))

	// chat answer (P3): the card a blocked agent shows (screen prompt), the
	// answer body and the named refusal for a stale card
	chatProj.Panes[0].AgentStatus = "blocked"
	h.back.set(chatProj)
	h.back.mu.Lock()
	h.back.screen = permissionScreen
	h.back.mu.Unlock()
	blocked, _, err := h.dial("/api/v3/panes/w1:p1/chat/stream", true)
	if err != nil {
		t.Fatal(err)
	}
	rawCard := readRaw(t, blocked)
	golden(t, "chat-frame-interaction.json", rawCard)
	var cardFrame struct {
		Interaction struct{ ID string } `json:"interaction"`
	}
	if err := json.Unmarshal(rawCard, &cardFrame); err != nil || cardFrame.Interaction.ID == "" {
		t.Fatalf("no card in the blocked frame: %v %s", err, rawCard)
	}
	rawAnswer, _ := json.Marshal(chatAnswerRequest{InteractionID: cardFrame.Interaction.ID, Option: "No"})
	golden(t, "chat-client-answer.json", rawAnswer)
	req, _ = http.NewRequest("POST", h.http.URL+"/api/v3/panes/w1:p1/chat/answer", strings.NewReader(`{"interactionId":"stale","option":"No"}`))
	req.Header.Set("Authorization", "Bearer "+h.token)
	gone, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "error-interaction-gone.json", bodyOf(t, gone))

	// the three upstream messages the phone sends (what the Host accepts)
	for name, msg := range map[string]map[string]any{
		"terminal-client-input.json":  {"t": "input", "text": "ls", "keys": []string{"enter"}},
		"terminal-client-resize.json": {"t": "resize", "cols": 60, "rows": 22},
		"terminal-client-ping.json":   {"t": "ping"},
	} {
		raw, _ := json.Marshal(msg)
		golden(t, name, raw)
	}
}
