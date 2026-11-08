package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thewei/solco/plugins/solco-automations/internal/personalagent"
)

// submitHerdr installs a herdr whose `agent prompt` answers promptError (a
// JSON error object) and whose `agent get` reports status, logging every call.
func submitHerdr(t *testing.T, promptError, status string) (logFile string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	logFile = filepath.Join(dir, "calls")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$HERDR_TEST_LOG"
case "$*" in
*"agent prompt"*) printf '%s\n' "$HERDR_TEST_PROMPT_ERROR"; exit 1;;
*"agent get"*) printf '%s\n' '{"result":{"type":"agent_info","agent":{"pane_id":"w1:p1","agent":"omp","agent_status":"'"$HERDR_TEST_STATUS"'","interactive_ready":true}}}';;
*"pane run"*) printf '%s\n' '{"result":{"type":"ok"}}';;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", bin)
	t.Setenv("HERDR_TEST_LOG", logFile)
	t.Setenv("HERDR_TEST_PROMPT_ERROR", promptError)
	t.Setenv("HERDR_TEST_STATUS", status)
	return logFile
}

const identityRefusal = `{"error":{"code":"agent_not_ready","message":"agent w1:p1 is no longer the pane foreground process"}}`

func TestGroupDeliveryTypesIntoThePaneWhenHerdrRefusesTheAgentsIdentity(t *testing.T) {
	logFile := submitHerdr(t, identityRefusal, "idle")
	if err := submitToAgent("work", "w1:p1", "hello group"); err != nil {
		t.Fatalf("a live idle agent behind an identity refusal must take the message, got %v", err)
	}
	raw, _ := os.ReadFile(logFile)
	if n := strings.Count(string(raw), "pane run w1:p1 hello group"); n != 1 {
		t.Fatalf("typed %d times, want once; calls:\n%s", n, raw)
	}
}

func TestGroupDeliveryNeverTypesIntoABusyAgentBehindAnIdentityRefusal(t *testing.T) {
	logFile := submitHerdr(t, identityRefusal, "working")
	err := submitToAgent("work", "w1:p1", "hello group")
	if !errors.Is(err, personalagent.ErrSubmitRefused) {
		t.Fatalf("a working agent must be retried later, got %v", err)
	}
	if raw, _ := os.ReadFile(logFile); strings.Contains(string(raw), "pane run") {
		t.Fatalf("typed into a working agent:\n%s", raw)
	}
}

func TestGroupDeliveryLeavesOtherRefusalsToTheRetryLadder(t *testing.T) {
	logFile := submitHerdr(t, `{"error":{"code":"agent_pane_busy","message":"pane is busy"}}`, "idle")
	err := submitToAgent("work", "w1:p1", "hello group")
	if !errors.Is(err, personalagent.ErrSubmitRefused) {
		t.Fatalf("a busy pane stays a retryable refusal, got %v", err)
	}
	raw, _ := os.ReadFile(logFile)
	if strings.Contains(string(raw), "pane run") || strings.Contains(string(raw), "agent get") {
		t.Fatalf("only the two identity refusals may reach for the pane:\n%s", raw)
	}
}
