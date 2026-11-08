package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/settings"
)

// gateTestStarter builds a SessionStarter over a fixed live kind list and a
// per-test toggle snapshot, the seams the gate reads.
func gateTestStarter(gate settings.AgentsSettings) *SessionStarter {
	service := &LaunchService{registry: agent.NewLaunchRegistry()}
	service.AttachAgentGate(func() settings.AgentsSettings { return gate })
	return &SessionStarter{
		service:   service,
		runtime:   func(string) agent.LaunchRuntime { return &fakeAppRuntime{} },
		listKinds: func(context.Context) ([]string, error) { return []string{"claude", "pi", "agy"}, nil },
		now:       time.Now,
	}
}

func kindIDs(starter *SessionStarter) []string {
	var out []string
	for _, kind := range starter.Kinds() {
		out = append(out, kind.ID)
	}
	return out
}

func TestKindsFiltersDisabledAgents(t *testing.T) {
	if got := kindIDs(gateTestStarter(settings.AgentsSettings{})); !equalStrings(got, []string{"terminal", "claude-code", "pi", "antigravity"}) {
		t.Fatalf("without opt-outs: got %v", got)
	}
	gate := settings.AgentsSettings{Enabled: map[string]bool{"claude-code": false, "antigravity": false}}
	if got := kindIDs(gateTestStarter(gate)); !equalStrings(got, []string{"terminal", "pi"}) {
		t.Fatalf("with opt-outs: got %v", got)
	}
}

func TestStartSessionRefusesDisabledAgent(t *testing.T) {
	gate := settings.AgentsSettings{Enabled: map[string]bool{"claude-code": false}}
	starter := gateTestStarter(gate)
	starter.run = func(ctx context.Context, rt agent.LaunchRuntime, intent agent.LaunchIntent) (agent.LaunchOutcome, *agent.LaunchFailure) {
		return agent.RunLaunchTransaction(ctx, rt, Preparer{}, intent, agent.TransactionTimings{})
	}
	_, err := starter.StartSession(context.Background(), "default", SessionSpec{RequestID: "req_gate_off", CWD: t.TempDir(), Agent: "claude-code"})
	var sessionErr *SessionError
	if err == nil || !errors.As(err, &sessionErr) || sessionErr.Code != SessionAgentDisabled {
		t.Fatalf("err = %v, want agent_disabled", err)
	}
	// Terminal is not an agent: the toggle never fences it.
	if pane, err := starter.StartSession(context.Background(), "default", SessionSpec{RequestID: "req_gate_tty", CWD: t.TempDir()}); err != nil || pane.PaneID == "" {
		t.Fatalf("terminal refused: pane %+v err %v", pane, err)
	}
}

func TestExecuteHistoryContinuationRefusesDisabledTarget(t *testing.T) {
	service := &LaunchService{registry: agent.NewLaunchRegistry()}
	service.AttachAgentGate(func() settings.AgentsSettings {
		return settings.AgentsSettings{Enabled: map[string]bool{"codex": false}}
	})
	plan := agent.ContinuationPlan{
		Strategy: agent.StrategyContextTransfer,
		Session:  agent.ContinuationSource{Agent: history.AgentClaudeCode, ID: "s1", FilePath: "/x.jsonl"},
		Target:   history.AgentCodex,
	}
	_, err := service.ExecuteHistoryContinuation(context.Background(), "default", plan, "", HistoryContinuationDeps{})
	if err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Fatalf("err = %v, want the disabled-target refusal", err)
	}
}

func TestFreshInstallDetectsMissingStore(t *testing.T) {
	if fresh := NewSettingsService(settings.NewFileStore(t.TempDir())); !fresh.FreshInstall() {
		t.Fatal("a missing settings file must read as a fresh install")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, settings.FileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if existing := NewSettingsService(settings.NewFileStore(dir)); existing.FreshInstall() {
		t.Fatal("an existing settings file must not read as fresh")
	}
	if mem := NewSettingsService(settings.NewMemoryStore(settings.Default())); mem.FreshInstall() {
		t.Fatal("the memory store is never fresh")
	}
}

func TestValidateSettingsRejectsUnknownAgentKey(t *testing.T) {
	value := settings.Default()
	value.Agents.Enabled = map[string]bool{"not-an-agent": false}
	if err := ValidateSettings(&value); err == nil || !strings.Contains(err.Error(), "not-an-agent") {
		t.Fatalf("err = %v, want the unknown-agent-id refusal", err)
	}
	value.Agents.Enabled = map[string]bool{"codex": false}
	if err := ValidateSettings(&value); err != nil {
		t.Fatalf("a known agent id must validate: %v", err)
	}
}
