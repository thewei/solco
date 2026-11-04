package remotehost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/thewei/solco/internal/chatstream"
	"github.com/thewei/solco/internal/herdr"
)

const claudeFixture = "" +
	`{"type":"user","cwd":"/work/solco","timestamp":"2026-08-01T01:00:00Z","message":{"content":"Fix the parser"}}` + "\n" +
	`{"type":"assistant","cwd":"/work/solco","timestamp":"2026-08-01T01:00:01Z","message":{"id":"m1","model":"claude-sonnet-4-5","content":[{"type":"text","text":"I found it."}]}}` + "\n"

// chatHarness binds pane w1:p1's agent to a Claude transcript in a temp dir.
func chatHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(claudeFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := fixtureProjection()
	proj.Agents = []herdr.Agent{{
		TerminalID: "term_a", PaneID: "w1:p1", Kind: "claude", Status: "working",
		AgentSession: &herdr.AgentSessionIdentity{Agent: "claude", Kind: "path", Value: path},
	}}
	h.back.set(proj)
	return h, path
}

func TestChatReadsTheAgentsTranscriptThroughTheSharedProjection(t *testing.T) {
	h, _ := chatHarness(t)
	resp := h.get("/api/v3/panes/w1:p1/chat?instance=default", h.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		PaneID   string `json:"paneId"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Turns    []struct {
			User bool   `json:"user"`
			Text string `json:"text"`
		} `json:"turns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.PaneID != "w1:p1" || body.Provider != "claude-code" && body.Provider != "claude" || len(body.Turns) != 2 {
		t.Fatalf("body = %+v", body)
	}
	if !body.Turns[0].User || body.Turns[0].Text != "Fix the parser" || body.Turns[1].User {
		t.Fatalf("turns = %+v", body.Turns)
	}
}

func TestChatRefusalsAreNamed(t *testing.T) {
	h, _ := chatHarness(t)
	for _, c := range []struct {
		path, token string
		status      int
		code        string
	}{
		{"/api/v3/panes/w1:p1/chat?instance=default", "", 401, "unauthorized"},
		{"/api/v3/panes/nope/chat?instance=default", h.token, 404, "unknown_pane"},
		{"/api/v3/panes/w1:p2/chat?instance=default", h.token, 409, "no_conversation"}, // a shell pane has no agent session
	} {
		resp := h.get(c.path, c.token, nil)
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		if resp.StatusCode != c.status || body.Error.Code != c.code {
			t.Errorf("%s: %d %q, want %d %q", c.path, resp.StatusCode, body.Error.Code, c.status, c.code)
		}
	}
}

func TestChatStreamOMPCompanionSendsBoundedDeltasAndRecoversSnapshot(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	body := `{"type":"session","version":3,"id":"omp-stream","timestamp":"2026-11-05T09:00:00Z","cwd":"/work/solco"}` + "\n"
	for i := range 18 {
		body += fmt.Sprintf(`{"type":"message","timestamp":"2026-11-05T09:00:%02dZ","message":{"role":"user","content":[{"type":"text","text":"prompt %d"}]}}`, i, i) + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := fixtureProjection()
	proj.Agents = []herdr.Agent{{TerminalID: "term_a", PaneID: "w1:p1", Kind: "omp", Status: "working",
		AgentSession: &herdr.AgentSessionIdentity{Agent: "omp", Kind: "path", Value: path}}}
	h.back.set(proj)
	conn, _, err := h.dial("/api/v3/panes/w1:p1/chat/stream?instance=default", true)
	if err != nil {
		t.Fatal(err)
	}
	first := readJSON(t, conn)
	state, _ := first["state"].(map[string]any)
	turns, _ := state["turns"].([]any)
	if first["t"] != "chat" || len(turns) < 10 {
		t.Fatalf("invalid baseline: %+v", first)
	}
	baseSeq, _ := first["seq"].(float64)
	e := chatstream.Event{Agent: "omp", PaneID: "w1:p1", SessionPath: path,
		Epoch: "0000000000001-real", MessageID: "m1", Kind: "message_start", Seq: 1}
	h.srv.IngestOMPChatStream(e)
	e.Seq = 2
	e.Kind = "text_delta"
	e.Delta = "Hello streaming from OMP!"
	start := time.Now()
	h.srv.IngestOMPChatStream(e)
	second := readJSON(t, conn)
	if got := second["t"]; got != "chat_delta" {
		t.Fatalf("expected compact incremental frame, got %v: %+v", got, second)
	}
	if second["baseSeq"] != baseSeq || second["seq"] != baseSeq+1 {
		t.Fatalf("incorrect delta sequence: %+v", second)
	}
	if time.Since(start) > 850*time.Millisecond {
		t.Fatalf("stream was delayed to file poll: %v", time.Since(start))
	}
	patch, _ := second["turns"].([]any)
	if len(patch) > 3 || len(patch) == 0 {
		t.Fatalf("unbounded/empty suffix: %+v", second)
	}
	last, _ := patch[len(patch)-1].(map[string]any)
	if last["narration"] != "Hello streaming from OMP!" {
		t.Fatalf("wrong overlay: %+v", last)
	}

	// When a sequence is lost, the Host must withdraw the provisional text
	// rather than render an incomplete answer or pretend it was committed.
	e.Seq = 4
	e.Delta = "unsafe"
	h.srv.IngestOMPChatStream(e)
	third := readJSON(t, conn)
	if third["seq"] != baseSeq+2 {
		t.Fatalf("missing desync recovery frame: %+v", third)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"t":"snapshot"}`)); err != nil {
		t.Fatal(err)
	}
	full := readJSON(t, conn)
	if full["t"] != "chat" || full["seq"] != baseSeq+3 {
		t.Fatalf("snapshot recovery failed: %+v", full)
	}
	fresh, _ := full["state"].(map[string]any)
	canonical, _ := fresh["turns"].([]any)
	if len(canonical) != len(turns) {
		t.Fatalf("desynced temporary turn leaked into snapshot: %d vs %d", len(canonical), len(turns))
	}
}

func TestChatStreamRebindSendsFullFrameAfterOMPSessionChange(t *testing.T) {
	h := newHarness(t)
	makeTranscript := func(content string) string {
		path := filepath.Join(t.TempDir(), "session.jsonl")
		body := `{"type":"session","version":3,"id":"omp-sess","timestamp":"2026-11-05T09:00:00Z","cwd":"/work/solco"}` + "\n" +
			fmt.Sprintf(`{"type":"message","message":{"role":"user","content":[{"type":"text","text":%q}]}}`, content) + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	oldPath, newPath := makeTranscript("old session"), makeTranscript("new session")
	bind := func(path string) {
		proj := fixtureProjection()
		proj.Agents = []herdr.Agent{{TerminalID: "term_a", PaneID: "w1:p1", Kind: "omp", Status: "working",
			AgentSession: &herdr.AgentSessionIdentity{Agent: "omp", Kind: "path", Value: path}}}
		h.back.set(proj)
	}
	bind(oldPath)
	conn, _, err := h.dial("/api/v3/panes/w1:p1/chat/stream?instance=default", true)
	if err != nil {
		t.Fatal(err)
	}
	first := readJSON(t, conn)
	if first["t"] != "chat" {
		t.Fatalf("initial frame: %v", first)
	}
	bind(newPath)
	// Host re-resolves the Herdr session every four polls; the first
	// frame for another source must be whole, never a suffix of old turns.
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("no rebind frame: %v", err)
		}
		var frame map[string]any
		if err := json.Unmarshal(raw, &frame); err != nil {
			t.Fatal(err)
		}
		if frame["t"] != "chat" {
			t.Fatalf("session switch sent a delta: %+v", frame)
		}
		state, _ := frame["state"].(map[string]any)
		turns, _ := state["turns"].([]any)
		if len(turns) != 1 {
			t.Fatalf("new session state: %+v", state)
		}
		row, _ := turns[0].(map[string]any)
		if row["text"] == "new session" {
			break
		}
	}
}

func TestChatStreamPushesWhenTheTranscriptGrows(t *testing.T) {
	h, path := chatHarness(t)
	conn, _, err := h.dial("/api/v3/panes/w1:p1/chat/stream?instance=default", true)
	if err != nil {
		t.Fatal(err)
	}
	turns := func(frame map[string]any) int {
		state, _ := frame["state"].(map[string]any)
		list, _ := state["turns"].([]any)
		return len(list)
	}
	first := readJSON(t, conn)
	if first["t"] != "chat" || turns(first) != 2 {
		t.Fatalf("first frame = %v", first)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"user","cwd":"/work/solco","timestamp":"2026-08-01T01:00:05Z","message":{"content":"And the lexer"}}` + "\n")
	f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("no second frame: %v", err)
		}
		var frame map[string]any
		_ = json.Unmarshal(data, &frame)
		if turns(frame) == 3 {
			return
		}
	}
}

func TestChatFallsBackToTheAppsHookIdentity(t *testing.T) {
	h, path := chatHarness(t)
	proj := fixtureProjection()
	proj.Agents = []herdr.Agent{{TerminalID: "term_a", PaneID: "w1:p1", Kind: "claude", Status: "idle"}} // no agent_session
	h.back.set(proj)
	if resp := h.get("/api/v3/panes/w1:p1/chat?instance=default", h.token, nil); resp.StatusCode != 409 {
		t.Fatalf("without any identity: %d, want 409", resp.StatusCode)
	}
	h.srv.opts.Identity = func(instance, paneID, terminalID, kind string) *herdr.AgentSessionIdentity {
		if paneID != "w1:p1" || terminalID != "term_a" || kind != "claude" {
			return nil
		}
		return &herdr.AgentSessionIdentity{Agent: "claude", Kind: "path", Value: path}
	}
	if resp := h.get("/api/v3/panes/w1:p1/chat?instance=default", h.token, nil); resp.StatusCode != 200 {
		t.Fatalf("with the hook identity: %d, want 200", resp.StatusCode)
	}
}

func TestChatStreamOlderAsksForThePreviousPageOnlyOnThatSocket(t *testing.T) {
	h := newHarness(t)
	var body string
	total := 130
	for i := 0; i < total; i++ {
		body += `{"type":"user","cwd":"/work/solco","timestamp":"2026-08-01T01:00:00Z","message":{"content":"m` + string(rune('a'+i%26)) + `"}}` + "\n"
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := fixtureProjection()
	proj.Agents = []herdr.Agent{{
		TerminalID: "term_a", PaneID: "w1:p1", Kind: "claude", Status: "idle",
		AgentSession: &herdr.AgentSessionIdentity{Agent: "claude", Kind: "path", Value: path},
	}}
	h.back.set(proj)
	turns := func(frame map[string]any) (int, bool) {
		state, _ := frame["state"].(map[string]any)
		list, _ := state["turns"].([]any)
		older, _ := state["hasOlder"].(bool)
		return len(list), older
	}
	conn, _, err := h.dial("/api/v3/panes/w1:p1/chat/stream?instance=default", true)
	if err != nil {
		t.Fatal(err)
	}
	if n, older := turns(readJSON(t, conn)); n != 60 || !older {
		t.Fatalf("first page: %d turns, hasOlder=%v", n, older)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"t":"older"}`)); err != nil {
		t.Fatal(err)
	}
	if n, older := turns(readJSON(t, conn)); n != 120 || !older {
		t.Fatalf("after older: %d turns, hasOlder=%v", n, older)
	}
	// another socket still opens at the newest page: paging is per stream
	other, _, err := h.dial("/api/v3/panes/w1:p1/chat/stream?instance=default", true)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := turns(readJSON(t, other)); n != 60 {
		t.Fatalf("a second stream opened at %d turns", n)
	}
	// noise is ignored
	if err := conn.Write(ctx, websocket.MessageText, []byte(`not json`)); err != nil {
		t.Fatal(err)
	}
}

// A phone that changed network leaves half-open streams behind: the new chat evicts the oldest one instead of being refused.
func TestChatStreamsOfOneDeviceEvictTheOldestInsteadOfRefusing(t *testing.T) {
	s := &Server{}
	evicted := make([]int, maxChatStreamsPerDev+2)
	releases := make([]func(), len(evicted))
	for i := range evicted {
		releases[i] = s.acquireChat("dev", func() { evicted[i]++ })
	}
	for i, n := range evicted {
		want := 0
		if i < 2 { // six opened, four allowed: the two oldest went
			want = 1
		}
		if n != want {
			t.Fatalf("stream %d evicted %d times, want %d", i, n, want)
		}
	}
	if got := len(s.chats["dev"]); got != maxChatStreamsPerDev {
		t.Fatalf("slots = %d, want %d", got, maxChatStreamsPerDev)
	}
	for _, release := range releases { // an evicted stream's own release must not free someone else's slot
		release()
	}
	if len(s.chats) != 0 {
		t.Fatalf("slots left after every release: %v", s.chats)
	}
}
