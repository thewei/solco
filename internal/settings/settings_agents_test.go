package settings

import "testing"

func TestAgentsEnabledAbsentKeyIsOn(t *testing.T) {
	var none AgentsSettings
	if !none.IsEnabled("claude-code") {
		t.Fatal("an absent key must stay enabled: the toggle only records opt-outs")
	}
	off := AgentsSettings{Enabled: map[string]bool{"claude-code": false}}
	if off.IsEnabled("claude-code") {
		t.Fatal("an explicit false must hide the agent")
	}
	if !off.IsEnabled("codex") {
		t.Fatal("other agents stay enabled beside an explicit false")
	}
	if !off.IsEnabled("codex") || !none.IsEnabled("codex") {
		t.Fatal("nil and empty maps behave the same")
	}
}
