package nativeui

import (
	"context"
	"strings"
	"testing"
)

func TestAssistantsPersonaBridgeForwardsThroughTheCore(t *testing.T) {
	calls := stubAssistants(t, false)
	b := NewAssistantsBridge(openAssistantsShell(t))
	ctx := context.Background()

	view, err := b.Persona(ctx, "a_1")
	if err != nil || view.Runtime.Running == nil {
		t.Fatalf("persona = %+v err=%v", view, err)
	}
	for _, want := range []string{"persona instructions a_1", "persona runtime a_1", "persona mcp a_1"} {
		if c := lastCallOf(calls, want); c[0] == "" {
			t.Fatalf("no %q call in %v", want, *calls)
		}
	}

	if err := b.SaveInstructions(ctx, "r1", "a_1", "Be brief.", 2); err != nil {
		t.Fatal(err)
	}
	last := lastCallOf(calls, "persona set-instructions")
	if last[0] != "persona set-instructions a_1" || !strings.Contains(last[1], `"expectedRevision":2`) || !strings.Contains(last[1], `"requestId":"r1"`) {
		t.Fatalf("save call = %v", last)
	}
	if err := b.RestoreInstructions(ctx, "r2", "a_1", 1, 3); err != nil {
		t.Fatal(err)
	}
	if err := b.GrantMCP(ctx, "r3", "a_1", "alpha", true); err != nil {
		t.Fatal(err)
	}
	last = lastCallOf(calls, "persona mcp-grant")
	if last[0] != "persona mcp-grant a_1" || !strings.Contains(last[1], `"serverId":"alpha"`) || !strings.Contains(last[1], `"granted":true`) {
		t.Fatalf("grant call = %v", last)
	}
	if err := b.VerifyMCP(ctx, "a_1"); err != nil {
		t.Fatal(err)
	}
	if last = lastCallOf(calls, "persona mcp-verify"); last[0] != "persona mcp-verify a_1" {
		t.Fatalf("verify call = %v", last)
	}
	if err := b.VerifyMCP(ctx, ""); err == nil {
		t.Fatal("an empty id must be refused before the core")
	}

	// An Agent the settings do not offer never becomes a profile default.
	n := len(*calls)
	if err := b.SaveRuntime(ctx, "r4", "a_1", "not-offered", "", "", 0, 30); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("an unavailable agent was accepted: %v", err)
	}
	if len(*calls) != n {
		t.Fatalf("a refused runtime reached the core: %v", (*calls)[n:])
	}
	if err := b.SaveRuntime(ctx, "r5", "a_1", "", "", "", 0, 60); err != nil {
		t.Fatalf("clearing the profile: %v", err)
	}
	if last = lastCallOf(calls, "persona set-runtime"); last[0] != "persona set-runtime a_1" || !strings.Contains(last[1], `"idleSleepMin":60`) {
		t.Fatalf("the page's idle sleep must reach the core: %v", last)
	}
	if err := b.SaveRuntime(ctx, "r6", "a_1", "", "", "", 1, 0); err != nil {
		t.Fatalf("never sleeping: %v", err)
	}
	if last = lastCallOf(calls, "persona set-runtime"); !strings.Contains(last[1], `"idleSleepMin":0`) {
		t.Fatalf("0 (never) must be sent, not dropped as an empty value: %v", last)
	}
	// A negative number is "leave the stored choice": saving the Agent alone
	// must not pin the default.
	if err := b.SaveRuntime(ctx, "r7", "a_1", "", "", "", 2, -1); err != nil {
		t.Fatalf("keeping the idle sleep: %v", err)
	}
	if last = lastCallOf(calls, "persona set-runtime"); strings.Contains(last[1], "idleSleepMin") {
		t.Fatalf("an unchanged idle sleep must not be sent: %v", last)
	}
}

// lastCallOf is the newest recorded core call whose command starts with prefix.
func lastCallOf(calls *[][2]string, prefix string) [2]string {
	for i := len(*calls) - 1; i >= 0; i-- {
		if strings.HasPrefix((*calls)[i][0], prefix) {
			return (*calls)[i]
		}
	}
	return [2]string{}
}
