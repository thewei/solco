package remotehost

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/herdr"
)

func (h *harness) post(path, token, body string) (int, string) {
	h.t.Helper()
	req, _ := http.NewRequest("POST", h.http.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out.Error.Code + "|" + out.Error.Message
}

func (h *harness) setStatus(status string) {
	proj := fixtureProjection()
	proj.Panes[0].AgentStatus = status
	h.back.set(proj)
}

func (h *harness) sent() ([]sentInput, []sentInput) {
	h.back.mu.Lock()
	defer h.back.mu.Unlock()
	return append([]sentInput(nil), h.back.prompts...), append([]sentInput(nil), h.back.agentKeys...)
}

const sendPath = "/api/v3/panes/w1:p1/chat/send?instance=default"

func TestChatSendDeliversOnlyThroughHerdrPromptWhenReady(t *testing.T) {
	h := newHarness(t)
	h.setStatus("idle")
	if code, _ := h.post(sendPath, h.token, `{"text":"  run the tests  "}`); code != 200 {
		t.Fatalf("status = %d", code)
	}
	prompts, keys := h.sent()
	if len(prompts) != 1 || prompts[0].pane != "w1:p1" || prompts[0].text != "run the tests" || len(keys) != 0 {
		t.Fatalf("prompts=%v keys=%v", prompts, keys)
	}
}

func TestChatSendRefusalsNeverReachHerdr(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct {
		status, body, want string
		http               int
		pane               string
	}{
		{"working", `{"text":"x"}`, "busy|", 409, "w1:p1"},
		{"blocked", `{"text":"x"}`, "needs_terminal|", 409, "w1:p1"},
		{"failed", `{"text":"x"}`, "needs_terminal|", 409, "w1:p1"},
		{"unknown", `{"text":"x"}`, "not_ready|", 409, "w1:p1"},
		{"idle", `{"text":"   "}`, "bad_request|", 400, "w1:p1"},
		{"idle", `not json`, "bad_request|", 400, "w1:p1"},
		{"idle", `{"text":"x"}`, "no_agent|", 409, "w1:p2"}, // a plain shell
		{"idle", `{"text":"x"}`, "unknown_pane|", 404, "ghost"},
	} {
		h.setStatus(c.status)
		code, got := h.post("/api/v3/panes/"+c.pane+"/chat/send?instance=default", h.token, c.body)
		if code != c.http || got != c.want {
			t.Errorf("%s %s: %d %q, want %d %q", c.status, c.body, code, got, c.http, c.want)
		}
	}
	if code, _ := h.post(sendPath, "", `{"text":"x"}`); code != 401 {
		t.Errorf("unauthenticated send = %d", code)
	}
	if prompts, keys := h.sent(); len(prompts) != 0 || len(keys) != 0 {
		t.Fatalf("a refused send reached Herdr: %v %v", prompts, keys)
	}
}

func TestChatSendInterruptCutsInInOrder(t *testing.T) {
	h := newHarness(t)
	h.setStatus("working")
	if code, got := h.post(sendPath, h.token, `{"text":"stop and do this","interrupt":true}`); code != 200 {
		t.Fatalf("status = %d %s", code, got)
	}
	prompts, keys := h.sent()
	if len(keys) != 2 || keys[0].keys[0] != "esc" || keys[1].keys[0] != "ctrl+c" || len(prompts) != 1 || prompts[0].text != "stop and do this" {
		t.Fatalf("prompts=%v keys=%v", prompts, keys)
	}
}

func TestChatSendRelaysHerdrsRefusalAndNeverRetries(t *testing.T) {
	h := newHarness(t)
	h.setStatus("idle")
	h.back.mu.Lock()
	h.back.promptErr = errors.New("agent is busy")
	h.back.mu.Unlock()
	code, got := h.post(sendPath, h.token, `{"text":"x"}`)
	if code != 409 || got != "refused|agent is busy" {
		t.Fatalf("refusal = %d %q", code, got)
	}
	h.back.mu.Lock()
	h.back.promptErr = &herdr.DeliveryUncertainError{Cause: errors.New("eof")}
	h.back.mu.Unlock()
	if code, got := h.post(sendPath, h.token, `{"text":"x"}`); code != 504 || !strings.HasPrefix(got, "delivery_uncertain") {
		t.Fatalf("uncertain = %d %q", code, got)
	}
	if prompts, _ := h.sent(); len(prompts) != 2 {
		t.Fatalf("each request must reach Herdr exactly once, got %d", len(prompts))
	}
}

func TestChatStopSendsOneEscape(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.post("/api/v3/panes/w1:p1/chat/stop?instance=default", h.token, ""); code != 200 {
		t.Fatalf("stop = %d", code)
	}
	if _, keys := h.sent(); len(keys) != 1 || keys[0].pane != "w1:p1" || keys[0].keys[0] != "esc" {
		t.Fatalf("keys = %v", keys)
	}
	if code, _ := h.post("/api/v3/panes/w1:p2/chat/stop?instance=default", h.token, ""); code != 409 {
		t.Fatalf("stop on a shell = %d", code)
	}
}
