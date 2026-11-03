package remotehost

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

/**
 * [INPUT]: server_test.go 的 harness、automations.go 的 handler 与 Options.Automations 缝
 * [OUTPUT]: 对外提供 /api/v3/automations 的行为回归：无缝或门关 404、需配对、列表永不为 null、能力位只在门开时出现
 * [POS]: automations.go 的守卫测试；缝由测试注入，绝不读真实插件
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func automationsHarness(t *testing.T, list Automations, open bool) *harness {
	t.Helper()
	h := newHarness(t)
	h.srv.opts.Automations = func(context.Context) (Automations, bool) { return list, open }
	return h
}

func TestAutomationsAbsentWithoutSourceOrWithClosedGate(t *testing.T) {
	h := newHarness(t)
	if resp := h.get("/api/v3/automations", h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "automations_unavailable" {
		t.Fatalf("no seam: status %d", resp.StatusCode)
	}
	h = automationsHarness(t, Automations{}, false)
	if resp := h.get("/api/v3/automations", h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "automations_unavailable" {
		t.Fatalf("closed gate: status %d", resp.StatusCode)
	}
}

func TestAutomationsNeedsAuth(t *testing.T) {
	h := automationsHarness(t, Automations{}, true)
	if resp := h.get("/api/v3/automations", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
}

func TestAutomationsNeverNullAndCapabilityFollowsTheGate(t *testing.T) {
	for _, open := range []bool{true, false} {
		h := automationsHarness(t, Automations{}, open)
		var boot struct {
			Capabilities map[string]bool `json:"capabilities"`
		}
		if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&boot); err != nil {
			t.Fatal(err)
		}
		if boot.Capabilities["automations"] != open {
			t.Errorf("gate open=%v: capabilities %v", open, boot.Capabilities)
		}
		if !open {
			continue
		}
		var out struct {
			Automations []AutomationItem `json:"automations"`
		}
		if err := json.NewDecoder(h.get("/api/v3/automations", h.token, nil).Body).Decode(&out); err != nil || out.Automations == nil {
			t.Fatalf("automations decode %v, body %#v", err, out.Automations)
		}
	}
}

// sampleAutomations is the golden file's desktop: an unread failure, a running
// automation, a draft awaiting approval and a paused one.
func sampleAutomations() Automations {
	return Automations{Scheduler: true, DraftPrompt: "Draft an automation: state its goal, trigger, guardrails and cost in the rationale, and always create it as a draft.", Agents: []AutomationAgent{{ID: "claude", Label: "Claude Code"}, {ID: "codex", Label: "Codex"}}, Automations: []AutomationItem{
		{
			Name: "nightly-ci-triage", State: "active", Schedule: "Daily 03:00", Agent: "claude", Headless: true,
			NextRunMs: 1786129200000, Unread: true,
			LastRun: &AutomationRun{ID: "run-7", Status: "failed", AtMs: 1786042800000, DurationSec: 31},
		},
		{
			Name: "weekly-recap", State: "active", Schedule: "Mondays 09:00", Agent: "codex", NextRunMs: 1786410000000,
			LastRun: &AutomationRun{ID: "run-3", Status: "running", AtMs: 1786042900000},
		},
		{Name: "dependency-sweep", State: "draft", Schedule: "Daily 09:00", Agent: "claude"},
		{Name: "old-report", State: "paused", Schedule: "Weekdays 18:00", Agent: "pi"},
	}}
}
