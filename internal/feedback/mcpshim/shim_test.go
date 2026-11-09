package mcpshim

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/thewei/solco/internal/feedback"
)

func setup(t *testing.T) (*feedback.Client, *feedback.Store) {
	t.Helper()
	dir := t.TempDir()
	token, _ := feedback.EnsureToken(dir)
	store := feedback.NewMemory()
	srv := httptest.NewServer(feedback.NewHandler(&feedback.Broker{Store: store, Token: token}))
	t.Cleanup(srv.Close)
	ep, _ := json.Marshal(map[string]any{"addr": srv.Listener.Addr().String(), "pid": 1})
	if err := os.WriteFile(filepath.Join(dir, "broker.json"), ep, 0o600); err != nil {
		t.Fatal(err)
	}
	return feedback.NewClient(dir), store
}

func connect(t *testing.T, c *feedback.Client) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := NewServer(c, "test").Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cl := mcp.NewClient(&mcp.Implementation{Name: "cursor-vscode", Version: "9.9"}, nil)
	sess, err := cl.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func text(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatal("no text content")
	return ""
}

func TestToolsAreListedWithOldNames(t *testing.T) {
	c, _ := setup(t)
	sess := connect(t, c)
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"interactive_feedback", "interactive_feedback_start", "interactive_feedback_wait", "recall_decisions"} {
		if !got[want] {
			t.Fatalf("missing tool %s in %v", want, got)
		}
	}
}

// The tool descriptions are what every connected Agent reads: they must ask for one open, blocking call and name the
// fallback for a client that cuts it short (2026-11-09: the user may need hours to answer).
func TestDescriptionsAskForABlockingLongCall(t *testing.T) {
	c, _ := setup(t)
	sess := connect(t, c)
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"interactive_feedback":       {"BLOCK", "ONE call open", "do not poll", "assumed answer", "interactive_feedback_start"},
		"interactive_feedback_start": {"never stop waiting", "interactive_feedback_wait"},
		"interactive_feedback_wait":  {"wait_seconds", "per-call limit"},
	}
	for _, tool := range res.Tools {
		for _, w := range want[tool.Name] {
			if !strings.Contains(tool.Description, w) {
				t.Errorf("%s description lacks %q", tool.Name, w)
			}
		}
	}
}

func TestAskBlocksUntilTheUserAnswers(t *testing.T) {
	c, store := setup(t)
	sess := connect(t, c)
	repo := t.TempDir()
	go func() {
		// the user, in Solco
		for i := 0; i < 100; i++ {
			rows := store.Threads(feedback.Filter{Waiting: true})
			if len(rows) == 1 {
				th, _ := store.Thread(rows[0].ID)
				_ = store.Resolve(th.Asks[0].ID, map[string]feedback.Answer{"q1": {Selected: []string{"ship"}}}, nil, nil)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "interactive_feedback", Arguments: map[string]any{
		"caller_info": "Cursor Agent - release", "project_root": repo,
		"questions": []map[string]any{{"id": "q1", "text": "Ship?", "type": "single", "options": []string{"ship", "hold"}}},
	}})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	var out struct {
		Status  string                     `json:"status"`
		Answers map[string]feedback.Answer `json:"answers"`
	}
	if err := json.Unmarshal([]byte(text(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "answered" || out.Answers["q1"].Selected[0] != "ship" {
		t.Fatalf("result: %s", text(t, res))
	}
	// The handshake client name reached the thread.
	rows := store.Threads(feedback.Filter{})
	if rows[0].Client != "Cursor" {
		t.Fatalf("client: %+v", rows[0])
	}
}

func TestStartThenWaitAndRecall(t *testing.T) {
	c, store := setup(t)
	sess := connect(t, c)
	ctx := context.Background()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "interactive_feedback_start", Arguments: map[string]any{
		"caller_info": "Codex", "thread_id": "T1",
		"questions": []map[string]any{{"id": "a", "text": "Which database?", "type": "text"}},
	}})
	if err != nil || res.IsError {
		t.Fatalf("start: %v", err)
	}
	var started struct {
		Data struct {
			SessionID string `json:"session_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(text(t, res)), &started)
	id := started.Data.SessionID
	if id == "" {
		t.Fatalf("no session id: %s", text(t, res))
	}
	w, _ := sess.CallTool(ctx, &mcp.CallToolParams{Name: "interactive_feedback_wait", Arguments: map[string]any{"session_id": id, "wait_seconds": 1}})
	if !strings.Contains(text(t, w), `"pending"`) {
		t.Fatalf("expected pending: %s", text(t, w))
	}
	_ = store.Resolve(id, map[string]feedback.Answer{"a": {Custom: "Postgres"}}, nil, []feedback.ContextRef{{Kind: "skill", Name: "think", Path: "/Users/me/.agents/skills/think/SKILL.md"}})
	w, _ = sess.CallTool(ctx, &mcp.CallToolParams{Name: "interactive_feedback_wait", Arguments: map[string]any{"session_id": id, "wait_seconds": 5}})
	if !strings.Contains(text(t, w), `"answered"`) || !strings.Contains(text(t, w), "Postgres") {
		t.Fatalf("expected answer: %s", text(t, w))
	}
	if !strings.Contains(text(t, w), `"context"`) || !strings.Contains(text(t, w), "SKILL.md") || !strings.Contains(text(t, w), "context_note") {
		t.Fatalf("the attached skill must reach the Agent with its path and a note: %s", text(t, w))
	}
	r, _ := sess.CallTool(ctx, &mcp.CallToolParams{Name: "recall_decisions", Arguments: map[string]any{"query": "database"}})
	if !strings.Contains(text(t, r), "Postgres") || !strings.Contains(text(t, r), `"count": 1`) {
		t.Fatalf("recall: %s", text(t, r))
	}
	if bad, _ := sess.CallTool(ctx, &mcp.CallToolParams{Name: "interactive_feedback_wait", Arguments: map[string]any{"session_id": "nope"}}); !strings.Contains(text(t, bad), "not_found") {
		t.Fatalf("unknown id: %s", text(t, bad))
	}
}

// Instructions reach the Agent's system prompt; annotations keep clients from asking approval for a read-only recall.
func TestServerCarriesInstructionsAndAnnotations(t *testing.T) {
	c, _ := setup(t)
	sess := connect(t, c)
	if got := sess.InitializeResult().Instructions; !strings.Contains(got, "ONE call open") || !strings.Contains(got, "project_root") || !strings.Contains(got, `"context"`) {
		t.Fatalf("instructions: %q", got)
	}
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Annotations == nil || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Errorf("%s: annotations %+v", tool.Name, tool.Annotations)
		}
		if tool.Name == "recall_decisions" && !tool.Annotations.ReadOnlyHint {
			t.Error("recall_decisions is read-only")
		}
	}
}

// A cancelled call leaves the question answerable: it expires at once instead of being ended.
func TestCancelledAskExpiresAndStaysAnswerable(t *testing.T) {
	c, store := setup(t)
	sess := connect(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = sess.CallTool(ctx, &mcp.CallToolParams{Name: "interactive_feedback", Arguments: map[string]any{
			"caller_info": "Cursor Agent", "project_root": t.TempDir(),
			"questions": []map[string]any{{"id": "q1", "text": "Ship?", "type": "single", "options": []string{"a"}}},
		}})
	}()
	var askID string
	for i := 0; i < 100 && askID == ""; i++ {
		if rows := store.Threads(feedback.Filter{Waiting: true}); len(rows) == 1 {
			th, _ := store.Thread(rows[0].ID)
			askID = th.Asks[0].ID
		}
		time.Sleep(50 * time.Millisecond)
	}
	if askID == "" {
		t.Fatal("ask never appeared")
	}
	cancel()
	<-done
	for i := 0; i < 100; i++ {
		th, _ := store.Thread(store.Threads(feedback.Filter{})[0].ID)
		if th.Asks[0].State == feedback.StateExpired {
			if err := store.Resolve(askID, map[string]feedback.Answer{"q1": {Selected: []string{"a"}}}, nil, nil); err != nil {
				t.Fatalf("late answer refused: %v", err)
			}
			return
		}
		if th.Asks[0].State == feedback.StateEnded {
			t.Fatal("a cancelled call must not end the question")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("ask never expired")
}

func TestLostSessionKeepsTheSessionID(t *testing.T) {
	res := lostSession("ask_123", "Solco feedback broker is not reachable")
	if !res.IsError || !strings.Contains(text(t, res), `"session_id": "ask_123"`) {
		t.Fatalf("result: %s", text(t, res))
	}
}

func TestSuggestContextIsKeptWhenRealAndProjectDefaultsToTheLaunchRepo(t *testing.T) {
	c, store := setup(t)
	sess := connect(t, c)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// project_root left out on purpose: the launch folder is a repository, so the thread files under it.
	if _, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "interactive_feedback_start", Arguments: map[string]any{
		"caller_info":     "Cursor Agent",
		"suggest_context": []map[string]any{{"kind": "file", "name": "main.go"}, {"kind": "file", "name": "ghost.go"}},
		"questions":       []map[string]any{{"id": "q1", "text": "Which?", "type": "text"}},
	}}); err != nil {
		t.Fatal(err)
	}
	rows := store.Threads(feedback.Filter{})
	if len(rows) != 1 {
		t.Fatalf("threads: %+v", rows)
	}
	th, _ := store.Thread(rows[0].ID)
	if got := th.Asks[0].Suggest; len(got) != 1 || got[0] != (feedback.ContextRef{Kind: "file", Name: "main.go"}) {
		t.Fatalf("suggest: %+v", got)
	}
	if th.ProjectRoot == "" {
		t.Fatal("project root was not defaulted to the launch folder")
	}
}
