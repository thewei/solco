package nativeui

import (
	"context"
	"strings"
	"testing"
)

func TestAssistantsAutonomyBridgeForwardsThroughTheCore(t *testing.T) {
	calls := stubAssistants(t, false)
	b := NewAssistantsBridge(openAssistantsShell(t))
	ctx := context.Background()

	view, err := b.Autonomy(ctx, "a_1")
	if err != nil || len(view.Keys) != 1 || len(view.Events) != 1 || len(view.Triggers) != 1 {
		t.Fatalf("autonomy = %+v err=%v", view, err)
	}
	if view.HookPath != "/api/v3/hooks/assistants/a_1/wake" || view.Endpoint != "" {
		t.Fatalf("path %q endpoint %q (Remote Access is off in this shell)", view.HookPath, view.Endpoint)
	}

	made, err := b.CreateHookKey(ctx, "r1", "a_1", []string{"ci"})
	if err != nil || made.Secret != "s3cr3t" || made.Key.ID != "hk_2222222222222222" {
		t.Fatalf("create = %+v err=%v", made, err)
	}
	last := lastCallOf(calls, "hook key create")
	if !strings.Contains(last[1], `"assistantId":"a_1"`) || !strings.Contains(last[1], `"requestId":"r1"`) {
		t.Fatalf("create call = %v", last)
	}

	if err := b.RevokeHookKey(ctx, "r2", "a_1", "hk_1111111111111111"); err != nil {
		t.Fatal(err)
	}
	n := len(*calls)
	if err := b.RevokeHookKey(ctx, "r3", "a_1", "hk_9999999999999999"); err == nil {
		t.Fatal("a key the Assistant does not hold was revoked")
	}
	if lastCallOf(calls, "hook key revoke hk_9999999999999999")[0] != "" {
		t.Fatalf("a foreign key reached the core: %v", (*calls)[n:])
	}

	for _, action := range []string{"pause", "resume", "cancel"} {
		if err := b.TriggerAction(ctx, "t-"+action, action, "a_1", "g_9", 3); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		last := lastCallOf(calls, "trigger "+action)
		if last[0] != "trigger "+action+" g_9" || !strings.Contains(last[1], `"expectedRevision":3`) {
			t.Fatalf("%s call = %v", action, last)
		}
	}
	if err := b.TriggerAction(ctx, "t-x", "approve", "a_1", "g_9", 3); err == nil {
		t.Fatal("approving a trigger is the approval card's answer, not a bridge action")
	}
	if err := b.TriggerAction(ctx, "t-y", "pause", "a_1", "g_404", 3); err == nil {
		t.Fatal("a trigger the Assistant does not hold was changed")
	}
}
