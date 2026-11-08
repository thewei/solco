package remotehost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const permissionScreen = `● Bash(rm -rf build)

Do you want to proceed?
❯ 1. Yes
  2. No

Enter to select · ↑/↓ to navigate · Esc to cancel
`

const chatPath = "/api/v3/panes/w1:p1/chat?instance=default"
const answerPath = "/api/v3/panes/w1:p1/chat/answer?instance=default"

// blockedHarness is chatHarness with the pane blocked and the TUI showing screen.
func blockedHarness(t *testing.T, screen string) (*harness, string) {
	t.Helper()
	answerTextSettle = time.Millisecond
	h, path := chatHarness(t)
	proj := fixtureProjection()
	proj.Panes[0].AgentStatus = "blocked"
	proj.Agents = h.back.proj.Agents
	h.back.set(proj)
	h.back.mu.Lock()
	h.back.screen = screen
	h.back.mu.Unlock()
	return h, path
}

// setBlockedStatus changes the pane's status without dropping its agent identity.
func (h *harness) setBlockedStatus(status string) {
	h.back.mu.Lock()
	agents := h.back.proj.Agents
	h.back.mu.Unlock()
	proj := fixtureProjection()
	proj.Panes[0].AgentStatus = status
	proj.Agents = agents
	h.back.set(proj)
}

func askTool(id, questions string) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(questions)); err != nil {
		panic(err)
	}
	questions = compact.String()
	return fmt.Sprintf(`{"type":"assistant","cwd":"/work/solco","timestamp":"2026-08-01T01:00:09Z","message":{"id":"m2","model":"claude-sonnet-4-5","content":[{"type":"tool_use","id":%q,"name":"ask","input":{"questions":%s}}]}}`+"\n", id, questions)
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

type card struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Prompt      string   `json:"prompt"`
	Options     []string `json:"options"`
	Multi       bool     `json:"multi"`
	TextOptions []string `json:"textOptions"`
	Step        int      `json:"step"`
	Steps       int      `json:"steps"`
}

func (h *harness) card() *card {
	h.t.Helper()
	resp := h.get(chatPath, h.token, nil)
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("chat = %d", resp.StatusCode)
	}
	var body struct {
		AgentStatus string `json:"agentStatus"`
		Interaction *card  `json:"interaction"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		h.t.Fatal(err)
	}
	return body.Interaction
}

func TestChatShowsAndAnswersAScreenPromptOnce(t *testing.T) {
	h, _ := blockedHarness(t, permissionScreen)
	c := h.card()
	if c == nil || c.Kind != "permission" || c.Prompt != "Do you want to proceed?" || strings.Join(c.Options, "|") != "Yes|No" {
		t.Fatalf("card = %+v", c)
	}
	if again := h.card(); again == nil || again.ID != c.ID {
		t.Fatalf("the id must be stable across reads: %+v vs %+v", again, c)
	}
	for _, bad := range []struct{ body, want string }{
		{`{"interactionId":"stale","option":"No"}`, "interaction_gone|"},
		{`{"interactionId":"` + c.ID + `","option":"Maybe"}`, "unknown_option|"},
		{`{"interactionId":"` + c.ID + `","option":"No","text":"hm"}`, "bad_request|"},
		{`{"option":"No"}`, "bad_request|"},
	} {
		if code, got := h.post(answerPath, h.token, bad.body); code < 400 || got != bad.want {
			t.Errorf("%s: %d %q, want %q", bad.body, code, got, bad.want)
		}
	}
	if _, keys := h.sent(); len(keys) != 0 {
		t.Fatalf("a refused answer reached Herdr: %v", keys)
	}
	if code, got := h.post(answerPath, h.token, `{"interactionId":"`+c.ID+`","option":"No"}`); code != 200 {
		t.Fatalf("answer = %d %s", code, got)
	}
	if _, keys := h.sent(); len(keys) != 1 || keys[0].pane != "w1:p1" || strings.Join(keys[0].keys, ",") != "down,enter" {
		t.Fatalf("keys = %v", keys)
	}
	// The TUI still draws the card for a beat: the second answer loses, and the card is hidden.
	if code, got := h.post(answerPath, h.token, `{"interactionId":"`+c.ID+`","option":"Yes"}`); code != 409 || got != "interaction_gone|" {
		t.Fatalf("second answer = %d %q", code, got)
	}
	if h.card() != nil {
		t.Fatal("a just-answered card must not come back while the screen still shows it")
	}
	if _, keys := h.sent(); len(keys) != 1 {
		t.Fatalf("the losing answer reached Herdr: %v", keys)
	}
}

// A failed screen read names its cause in the log (2026-11-07: one undiagnosable `screen read failed`) — the error
// only, never the screen text it could not read.
func TestChatLogsWhyTheScreenReadFailed(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	h, _ := blockedHarness(t, "SECRET-SCREEN-BODY\n")
	h.back.mu.Lock()
	h.back.readErr = errors.New("socket closed")
	h.back.mu.Unlock()
	if h.card() != nil {
		t.Fatal("an unreadable screen must not invent a card")
	}
	logged := buf.String()
	if !strings.Contains(logged, "screen read failed") || !strings.Contains(logged, "socket closed") {
		t.Fatalf("the failed read must name its cause:\n%s", logged)
	}
	if strings.Contains(logged, "SECRET-SCREEN-BODY") {
		t.Fatalf("screen text reached the log:\n%s", logged)
	}
}

func TestChatAnswerNeedsABlockedAgentAndRelaysDeliveryFailure(t *testing.T) {
	h, _ := blockedHarness(t, permissionScreen)
	c := h.card()
	h.back.mu.Lock()
	h.back.keysErr = errors.New("not an active named agent")
	h.back.mu.Unlock()
	code, got := h.post(answerPath, h.token, `{"interactionId":"`+c.ID+`","option":"Yes"}`)
	if code != 409 || got != "refused|not an active named agent" {
		t.Fatalf("refusal = %d %q", code, got)
	}
	if h.card() == nil {
		t.Fatal("an undelivered answer must leave the card in place")
	}
	h.setBlockedStatus("idle")
	if code, got := h.post(answerPath, h.token, `{"interactionId":"`+c.ID+`","option":"Yes"}`); code != 409 || got != "interaction_gone|" {
		t.Fatalf("answer to an unblocked agent = %d %q", code, got)
	}
	if code, _ := h.post(answerPath, "", `{}`); code != 401 {
		t.Fatalf("unauthenticated = %d", code)
	}
	if h.card() != nil {
		t.Fatal("no card while the agent is not blocked")
	}
}

func TestChatWalksAMultiQuestionAskStepByStep(t *testing.T) {
	h, path := blockedHarness(t, "")
	appendTo(t, path, askTool("toolu_ask", `[
	  {"header":"Framework","question":"Which framework?","recommended":1,"options":[{"label":"pytest","description":"asserts"},{"label":"unittest"}]},
	  {"question":"Which areas?","multi":true,"options":[{"label":"auth"},{"label":"cache"},{"label":"logs"}]}]`))
	c := h.card()
	if c == nil || c.Step != 0 || c.Steps != 2 || c.Prompt != "Which framework?" || len(c.Options) != 3 || len(c.TextOptions) != 1 || c.TextOptions[0] != "Other (type your own)" {
		t.Fatalf("first card = %+v", c)
	}
	if code, got := h.post(answerPath, h.token, `{"interactionId":"`+c.ID+`","option":"Other (type your own)"}`); code != 400 {
		t.Fatalf("a typed row without text = %d %q", code, got)
	}
	// `recommended: 1` opens on unittest: unittest is Enter alone, pytest is up+Enter.
	if code, got := h.post(answerPath, h.token, `{"interactionId":"`+c.ID+`","option":"pytest"}`); code != 200 {
		t.Fatalf("answer = %d %s", code, got)
	}
	if _, keys := h.sent(); strings.Join(keys[0].keys, ",") != "up,enter" {
		t.Fatalf("keys = %v", keys)
	}
	next := h.card()
	if next == nil || next.Step != 1 || !next.Multi || next.Prompt != "Which areas?" || len(next.TextOptions) != 0 {
		t.Fatalf("second card = %+v", next)
	}
	if code, _ := h.post(answerPath, h.token, `{"interactionId":"`+next.ID+`","options":["auth","nope"]}`); code != 400 {
		t.Fatalf("an unknown tick = %d", code)
	}
	if code, got := h.post(answerPath, h.token, `{"interactionId":"`+next.ID+`","options":["cache","logs"]}`); code != 200 {
		t.Fatalf("multi answer = %d %s", code, got)
	}
	if _, keys := h.sent(); strings.Join(keys[1].keys, ",") != "down,space,down,space,enter" {
		t.Fatalf("multi keys = %v", keys[1])
	}
	if h.card() != nil {
		t.Fatal("after the last question the ask is done: no card until the screen shows another")
	}
}

func TestChatTypedAnswerSelectsTheRowThenTypesIt(t *testing.T) {
	h, path := blockedHarness(t, "")
	appendTo(t, path, askTool("toolu_one", `[{"question":"Which?","options":[{"label":"a"},{"label":"b"}]}]`))
	c := h.card()
	if code, got := h.post(answerPath, h.token, `{"interactionId":"`+c.ID+`","option":"Other (type your own)","text":"  something else "}`); code != 200 {
		t.Fatalf("typed answer = %d %s", code, got)
	}
	prompts, keys := h.sent()
	if len(keys) != 1 || strings.Join(keys[0].keys, ",") != "down,down,enter" || len(prompts) != 1 || prompts[0].text != "something else" {
		t.Fatalf("keys=%v prompts=%v", keys, prompts)
	}
	// A prompt the agent is blocked on refuses agent.prompt by design: the text is typed into the pane.
	h2, path2 := blockedHarness(t, "")
	appendTo(t, path2, askTool("toolu_two", `[{"question":"Which?","options":[{"label":"a"},{"label":"b"}]}]`))
	c2 := h2.card()
	h2.back.mu.Lock()
	h2.back.promptErr = errors.New("agent is blocked and requires interactive input")
	h2.back.mu.Unlock()
	if code, got := h2.post(answerPath, h2.token, `{"interactionId":"`+c2.ID+`","option":"Other (type your own)","text":"hi"}`); code != 200 {
		t.Fatalf("fallback answer = %d %s", code, got)
	}
	h2.back.mu.Lock()
	inputs := append([]sentInput(nil), h2.back.inputs...)
	h2.back.mu.Unlock()
	if len(inputs) != 1 || inputs[0].text != "hi" || strings.Join(inputs[0].keys, ",") != "enter" {
		t.Fatalf("pane input = %v", inputs)
	}
}

func TestChatStreamCarriesTheCardAndRetiresIt(t *testing.T) {
	h, _ := blockedHarness(t, permissionScreen)
	conn, _, err := h.dial("/api/v3/panes/w1:p1/chat/stream?instance=default", true)
	if err != nil {
		t.Fatal(err)
	}
	first := readJSON(t, conn)
	card, _ := first["interaction"].(map[string]any)
	if first["agentStatus"] != "blocked" || card["kind"] != "permission" || card["id"] == "" {
		t.Fatalf("first frame = %v", first)
	}
	h.setBlockedStatus("working")
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		next := readJSON(t, conn)
		if _, has := next["interaction"]; !has && next["agentStatus"] == "working" {
			return
		}
	}
	t.Fatal("the card did not retire when the agent left blocked")
}
