package remotehost

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const subagentFixture = "" +
	`{"type":"user","cwd":"/work/solco","timestamp":"2026-08-01T01:00:10Z","message":{"content":"Find the parser"}}` + "\n" +
	`{"type":"assistant","cwd":"/work/solco","timestamp":"2026-08-01T01:00:11Z","message":{"id":"s1","model":"claude-haiku","content":[{"type":"text","text":"It is in parse.go."}]}}` + "\n"

// subagentHarness gives pane w1:p1's Claude session one sub-agent, spawned by tool call call_1.
func subagentHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h, session := chatHarness(t)
	parent := `{"type":"assistant","cwd":"/work/solco","timestamp":"2026-08-01T01:00:02Z","message":{"id":"m2","content":[{"type":"tool_use","id":"call_1","name":"Agent","input":{"description":"Find the parser","subagent_type":"Explore","prompt":"find it"}}]}}` + "\n"
	f, err := os.OpenFile(session, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(parent); err != nil {
		t.Fatal(err)
	}
	f.Close()
	dir := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := `{"agentType":"Explore","description":"Find the parser","toolUseId":"call_1"}`
	if err := os.WriteFile(filepath.Join(dir, "agent-abc123.meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-abc123.jsonl"), []byte(subagentFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return h, session
}

func TestChatListsASubagentAndTheDrawerReadsItsOwnTranscript(t *testing.T) {
	h, _ := subagentHarness(t)
	var chat struct {
		Subagents []struct {
			ID, Type string
		} `json:"subagents"`
	}
	if err := json.NewDecoder(h.get("/api/v3/panes/w1:p1/chat?instance=default", h.token, nil).Body).Decode(&chat); err != nil || len(chat.Subagents) != 1 {
		t.Fatalf("chat subagents %+v err=%v", chat, err)
	}
	id := chat.Subagents[0].ID
	resp := h.get("/api/v3/panes/w1:p1/chat/subagents/"+id+"?instance=default", h.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var view struct {
		Subagent struct{ ID, Type, Status string } `json:"subagent"`
		Turns    []struct {
			User      bool
			Text      string
			Narration string
		} `json:"turns"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if view.Subagent.ID != id || view.Subagent.Type != "Explore" || view.Error != "" || len(view.Turns) != 2 ||
		!view.Turns[0].User || view.Turns[0].Text != "Find the parser" || view.Turns[1].Text+view.Turns[1].Narration != "It is in parse.go." {
		t.Fatalf("view %+v", view)
	}
}

func TestSubagentRefusalsAreNamedAndNeverTakeAPath(t *testing.T) {
	h, session := subagentHarness(t)
	for _, c := range []struct {
		name, path, token string
		status            int
		code              string
	}{
		{"no token", "/api/v3/panes/w1:p1/chat/subagents/abc123?instance=default", "", 401, "unauthorized"},
		{"unknown pane", "/api/v3/panes/nope/chat/subagents/abc123?instance=default", h.token, 404, "unknown_pane"},
		{"unknown subagent", "/api/v3/panes/w1:p1/chat/subagents/zzz?instance=default", h.token, 404, "unknown_subagent"},
		{"a path is not an id", "/api/v3/panes/w1:p1/chat/subagents/..%2F..%2Fsession?instance=default", h.token, 404, "unknown_subagent"},
		{"another path form", "/api/v3/panes/w1:p1/chat/subagents/" + strings.ReplaceAll(session, "/", "%2F") + "?instance=default", h.token, 404, "unknown_subagent"},
	} {
		resp := h.get(c.path, c.token, nil)
		if resp.StatusCode != c.status || errCode(t, resp) != c.code {
			t.Fatalf("%s: status %d", c.name, resp.StatusCode)
		}
	}
}
