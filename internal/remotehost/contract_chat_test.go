package remotehost

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRealHerdrChatContract is the P2/P3 end-to-end against a real Herdr and a
// real omp agent in a throw-away session: wait for the agent's own session
// identity, send a prompt through the Host (agent.prompt) and read the answer
// back through the shared chat projection. Opt-in because it starts a Herdr
// server and spends one short model turn:
//
//	SOLCO_REMOTEHOST_CONTRACT_CHAT=1 GOTOOLCHAIN=auto go test -run RealHerdrChat -v ./internal/remotehost/
func TestRealHerdrChatContract(t *testing.T) {
	if os.Getenv("SOLCO_REMOTEHOST_CONTRACT_CHAT") != "1" {
		t.Skip("set SOLCO_REMOTEHOST_CONTRACT_CHAT=1 to run against a real herdr and omp")
	}
	if _, err := exec.LookPath("omp"); err != nil {
		t.Skip("omp is not installed")
	}
	session, m, _ := startRealHerdr(t)
	proj, err := m.Projection(session)
	if err != nil || len(proj.Panes) == 0 {
		t.Fatalf("no pane: %v", err)
	}
	paneID := proj.Panes[0].ID
	cmd := exec.Command("herdr", "agent", "start", "chat-contract", "--kind", "omp", "--pane", paneID)
	cmd.Env = append(os.Environ(), "HERDR_SESSION="+session)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("agent start: %v\n%s", err, out)
	}

	srv, err := New(Options{Config: Config{Mode: ModeLoopback, Port: 7788}, Dir: t.TempDir(), Backend: NewManagerBackend(m)})
	if err != nil {
		t.Fatal(err)
	}
	_, token, _ := srv.Devices().Add("contract")
	ts := httptest.NewServer(srv.Handler())
	defer func() { srv.Close(); ts.Close() }()
	h := &harness{t: t, srv: srv, http: ts, token: token}
	base := "/api/v3/panes/" + paneID
	q := "?instance=" + session

	// a fresh omp names its session file only with the first prompt: 409 until then
	if resp := h.get(base+"/chat"+q, token, nil); resp.StatusCode != 409 {
		t.Logf("before the first prompt chat answered %d (409 expected for a fresh agent)", resp.StatusCode)
	}
	var status string
	waitForT(t, 60*time.Second, func() bool {
		resp := h.get(base+"/chat"+q, token, nil)
		var b struct {
			AgentStatus string `json:"agentStatus"`
		}
		if resp.StatusCode == 409 {
			// idle agent with no conversation: the Host must still accept a prompt
			p, _ := m.Projection(session)
			for _, pane := range p.Panes {
				if pane.ID == paneID {
					status = pane.AgentStatus
				}
			}
			return status == "idle"
		}
		_ = json.NewDecoder(resp.Body).Decode(&b)
		status = b.AgentStatus
		return status == "idle"
	})
	// omp's own UI takes a few seconds to start accepting a submit after Herdr
	// already calls the pane an idle agent: wait for its input prompt to settle.
	waitForT(t, 60*time.Second, func() bool {
		screen, _ := m.ReadPane(context.Background(), session, paneID, "visible")
		return strings.Contains(screen, "MCP finished") || strings.Contains(screen, "GLM") || strings.Contains(screen, "·")
	})
	time.Sleep(4 * time.Second)
	if code, got := h.post(base+"/chat/send"+q, token, `{"text":"Reply with exactly the single word: pong"}`); code != 200 {
		t.Fatalf("send = %d %s", code, got)
	}
	defer func() {
		if t.Failed() {
			screen, _ := m.ReadPane(context.Background(), session, paneID, "visible")
			p, _ := m.Projection(session)
			t.Logf("agents=%+v\nscreen:\n%s", p.Agents, screen)
		}
	}()
	waitForT(t, 90*time.Second, func() bool {
		resp := h.get(base+"/chat"+q, token, nil)
		if resp.StatusCode != 200 {
			return false
		}
		var b struct {
			AgentStatus string `json:"agentStatus"`
			Turns       []struct {
				User      bool   `json:"user"`
				Narration string `json:"narration"`
				Text      string `json:"text"`
			} `json:"turns"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&b)
		for _, turn := range b.Turns {
			if !turn.User && strings.Contains(strings.ToLower(turn.Narration+turn.Text), "pong") {
				return true
			}
		}
		return false
	})
}

func waitForT(t *testing.T, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the real agent")
}
