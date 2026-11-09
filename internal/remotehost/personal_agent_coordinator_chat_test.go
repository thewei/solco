package remotehost

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/history"
)

const coordinatorTranscript = `{"type":"session","version":3,"id":"coord-1","timestamp":"2026-11-06T10:00:00.000Z","cwd":"/plugin/personal/coordinators/` + paAssistantA + `"}` + "\n" +
	`{"type":"message","timestamp":"2026-11-06T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"You are the Coordinator of the Solco Assistant Ada. Standing brief."}]}}` + "\n" +
	`{"type":"message","timestamp":"2026-11-06T10:00:02.000Z","message":{"role":"user","content":[{"type":"text","text":"remember the launch plan"}]}}` + "\n" +
	`{"type":"message","timestamp":"2026-11-06T10:00:03.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Noted: the launch plan is on file."}]}}` + "\n"

// dropStandingBrief stands in for the desktop's rule: the brief row and the
// Agent's answer to it (the assistant rows up to the next user row) go.
func dropStandingBrief(turns []chatsession.ChatTurn) []chatsession.ChatTurn {
	kept, skip := []chatsession.ChatTurn{}, false
	for _, turn := range turns {
		if turn.User {
			skip = strings.HasPrefix(turn.Text, "You are the Coordinator of the Solco Assistant")
		}
		if !skip {
			kept = append(kept, turn)
		}
	}
	return kept
}

func coordinatorChatHarness(t *testing.T) (*harness, *struct{ asked, ran bool }) {
	t.Helper()
	h, _ := paHarness(t, true)
	path := filepath.Join(t.TempDir(), "coordinator.jsonl")
	if err := os.WriteFile(path, []byte(coordinatorTranscript), 0o600); err != nil {
		t.Fatal(err)
	}
	seen := &struct{ asked, ran bool }{}
	h.srv.opts.CoordinatorHistory = func(_ context.Context, id string, ran bool) (CoordinatorTranscript, bool) {
		seen.asked, seen.ran = true, ran
		if id != paAssistantA {
			return CoordinatorTranscript{}, false
		}
		return CoordinatorTranscript{Provider: history.AgentOMP, Path: path}, true
	}
	h.srv.opts.FilterTurns = dropStandingBrief
	return h, seen
}

// The phone reads the Coordinator's previous conversation as the desktop does:
// the same transcript, Solco's own standing brief left out, nothing written.
func TestCoordinatorChatReadsThePreviousConversation(t *testing.T) {
	h, seen := coordinatorChatHarness(t)
	resp := h.get("/api/v3/personal-agent/assistants/"+paAssistantA+"/coordinator/chat", h.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		AssistantID string `json:"assistantId"`
		Provider    string `json:"provider"`
		Turns       []struct {
			User bool   `json:"user"`
			Text string `json:"text"`
		} `json:"turns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AssistantID != paAssistantA || len(body.Turns) != 2 || !body.Turns[0].User || body.Turns[0].Text != "remember the launch plan" || body.Turns[1].User {
		t.Fatalf("body = %+v", body)
	}
	if !seen.ran {
		t.Fatal("a Coordinator the core says has run must ask the catalog to look again")
	}
}

func TestCoordinatorChatRefusals(t *testing.T) {
	h, _ := coordinatorChatHarness(t)
	base := "/api/v3/personal-agent/assistants/"
	if resp := h.get(base+paAssistantA+"/coordinator/chat", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token = %d", resp.StatusCode)
	}
	if resp := h.get(base+"not%20an%20id/coordinator/chat", h.token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed id = %d", resp.StatusCode)
	}
	if resp := h.get(base+paAssistantB+"/coordinator/chat", h.token, nil); resp.StatusCode != http.StatusConflict || errCode(t, resp) != "no_conversation" {
		t.Fatalf("an Assistant with no transcript = %d", resp.StatusCode)
	}
	// An unreadable file is the same answer as none: the phone shows the invitation, never an error screen.
	h.srv.opts.CoordinatorHistory = func(context.Context, string, bool) (CoordinatorTranscript, bool) {
		return CoordinatorTranscript{Provider: history.AgentOMP, Path: filepath.Join(t.TempDir(), "gone.jsonl")}, true
	}
	if resp := h.get(base+paAssistantA+"/coordinator/chat", h.token, nil); resp.StatusCode != http.StatusConflict || errCode(t, resp) != "no_conversation" {
		t.Fatalf("a vanished file = %d", resp.StatusCode)
	}
	// No catalog seam (the resident service) or a closed gate: no such surface.
	h.srv.opts.CoordinatorHistory = nil
	if resp := h.get(base+paAssistantA+"/coordinator/chat", h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "personal_agent_unavailable" {
		t.Fatalf("no seam = %d", resp.StatusCode)
	}
	closed, _ := paHarness(t, false)
	closed.srv.opts.CoordinatorHistory = func(context.Context, string, bool) (CoordinatorTranscript, bool) {
		t.Fatal("a closed gate reached the catalog")
		return CoordinatorTranscript{}, false
	}
	if resp := closed.get(base+paAssistantA+"/coordinator/chat", closed.token, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("closed gate = %d", resp.StatusCode)
	}
}

// Bootstrap advertises the read only where the desktop can answer it.
func TestCoordinatorChatCapability(t *testing.T) {
	has := func(h *harness) bool {
		var boot struct {
			Capabilities map[string]bool `json:"capabilities"`
		}
		if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&boot); err != nil {
			t.Fatal(err)
		}
		return boot.Capabilities["personalAgentCoordinatorChat"]
	}
	plain, _ := paHarness(t, true)
	if has(plain) {
		t.Fatal("advertised without a catalog seam")
	}
	h, _ := coordinatorChatHarness(t)
	if !has(h) {
		t.Fatal("not advertised with a catalog seam")
	}
}

func TestCoordinatorChatGolden(t *testing.T) {
	h, _ := coordinatorChatHarness(t)
	golden(t, "personal-agent-coordinator-chat.json", bodyOf(t, h.get("/api/v3/personal-agent/assistants/"+paAssistantA+"/coordinator/chat", h.token, nil)))
	golden(t, "error-personal-agent-no-conversation.json", bodyOf(t, h.get("/api/v3/personal-agent/assistants/"+paAssistantB+"/coordinator/chat", h.token, nil)))
}

// The same rows stay out of a live pane's chat, so handing the conversation
// from its history to the running Coordinator never makes the brief appear.
func TestFilterTurnsAppliesToEveryPaneChat(t *testing.T) {
	h, _ := chatHarness(t)
	turns := func() int {
		var body struct {
			Turns []json.RawMessage `json:"turns"`
		}
		if err := json.NewDecoder(h.get("/api/v3/panes/w1:p1/chat?instance=default", h.token, nil).Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return len(body.Turns)
	}
	if got := turns(); got != 2 {
		t.Fatalf("without FilterTurns = %d turns", got)
	}
	h.srv.opts.FilterTurns = func(in []chatsession.ChatTurn) []chatsession.ChatTurn { return in[1:] }
	if got := turns(); got != 1 {
		t.Fatalf("with FilterTurns = %d turns", got)
	}
}

// A long conversation opens on its newest page; `older=N` walks the window up N
// pages (a fresh Reader per request, nothing remembered), stops at the top, and
// refuses what is not a count.
func TestCoordinatorChatPagesOlderTurns(t *testing.T) {
	h, _ := coordinatorChatHarness(t)
	var long strings.Builder
	long.WriteString(strings.SplitN(coordinatorTranscript, "\n", 2)[0] + "\n")
	for i := 0; i < 2*history.WindowLimit; i++ {
		role, kind := "user", "q"
		if i%2 == 1 {
			role, kind = "assistant", "a"
		}
		long.WriteString(`{"type":"message","timestamp":"2026-11-06T10:01:00.000Z","message":{"role":"` + role + `","content":[{"type":"text","text":"` + kind + strconv.Itoa(i) + `"}]}}` + "\n")
	}
	path := filepath.Join(t.TempDir(), "long.jsonl")
	if err := os.WriteFile(path, []byte(long.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	h.srv.opts.CoordinatorHistory = func(context.Context, string, bool) (CoordinatorTranscript, bool) {
		return CoordinatorTranscript{Provider: history.AgentOMP, Path: path}, true
	}
	read := func(query string) (turns int, hasOlder bool, status int) {
		resp := h.get("/api/v3/personal-agent/assistants/"+paAssistantA+"/coordinator/chat"+query, h.token, nil)
		status = resp.StatusCode
		if status != http.StatusOK {
			return 0, false, status
		}
		var body struct {
			HasOlder bool              `json:"hasOlder"`
			Turns    []json.RawMessage `json:"turns"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return len(body.Turns), body.HasOlder, status
	}
	newest, more, _ := read("")
	if !more || newest == 0 {
		t.Fatalf("newest page: %d turns, hasOlder=%v", newest, more)
	}
	one, _, _ := read("?older=1")
	if one <= newest {
		t.Fatalf("older=1 gave %d turns, newest page had %d", one, newest)
	}
	lim15, _, _ := read("?limit=15")
	if lim15 > 15 {
		t.Fatalf("limit=15 gave %d turns, want <= 15", lim15)
	}
	lim15Older5, _, _ := read("?limit=15&older=5")
	if lim15Older5 <= lim15 {
		t.Fatalf("limit=15&older=5 gave %d turns, want > %d", lim15Older5, lim15)
	}
	all, top, _ := read("?older=99")
	if all < one || top {
		t.Fatalf("older=99 gave %d turns, hasOlder=%v; want the top of the transcript", all, top)
	}
	for _, bad := range []string{"?older=x", "?older=-1"} {
		if _, _, status := read(bad); status != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", bad, status)
		}
	}
}
