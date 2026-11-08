package nativeui

import (
	"testing"

	"github.com/thewei/solco/internal/agenthooks"
)

func TestHooksNeedingRepairOnlyNamesHooksSolcoInstalledEarlier(t *testing.T) {
	rows := []agenthooks.Diagnosis{
		{Provider: agenthooks.ProviderClaude, State: agenthooks.StateOK, Version: 3},
		{Provider: agenthooks.ProviderAntigravity, State: agenthooks.StateNotInstalled},              // never installed: quiet
		{Provider: agenthooks.ProviderAider, State: agenthooks.StateUnsupported},                     // no hook mechanism
		{Provider: agenthooks.ProviderClaude, State: agenthooks.StateOutdated, Version: 1},           // older script
		{Provider: agenthooks.ProviderAntigravity, State: agenthooks.StateNotInstalled, Version: 3},  // script there, wiring lost
		{Provider: agenthooks.ProviderClaude, State: agenthooks.StateError, Version: 3, Detail: "x"}, // refusals are not nagged
	}
	got := hooksNeedingRepair(rows)
	if len(got) != 2 || got[0] != "Claude Code" || got[1] != "Antigravity CLI" {
		t.Fatalf("hooksNeedingRepair = %q", got)
	}
	if hookRepairMessage(nil) != "" {
		t.Fatal("nothing to repair must not toast")
	}
	if m := hookRepairMessage(got); m != "Claude Code and Antigravity CLI live status needs an update" {
		t.Fatalf("message = %q", m)
	}
}
