package personalagent

import (
	"strings"
	"testing"
)

// SOLCO-W07: with no hard sandbox, nothing Solco says or does promises that an
// Agent cannot write, delete or merge. The limits are instructions; the only
// enforcement is that Solco itself has no merge/push/delete path, and a person
// approves anything that changes the world.
func TestNoSandboxMeansNoSafetyPromise(t *testing.T) {
	if SandboxEnforced {
		t.Fatal("this build claims an enforced sandbox it does not have")
	}
	if !strings.Contains(SafetyNote, "not an enforced sandbox") {
		t.Fatalf("SafetyNote = %q", SafetyNote)
	}
	a := Assistant{Name: "Ada"}
	for _, kind := range []string{KindResearch, KindCoding, KindManual} {
		p := strings.ToLower(BuildPrompt(a, Task{Kind: kind, Title: "x", Brief: "do it"}))
		for _, promise := range []string{"guarantee", "sandbox", "cannot", "safe to", "is safe", "impossible"} {
			if strings.Contains(p, promise) {
				t.Fatalf("%s prompt makes a promise (%q):\n%s", kind, promise, p)
			}
		}
		if !strings.Contains(p, "do not") {
			t.Fatalf("%s prompt states no limit:\n%s", kind, p)
		}
	}

	s, _ := openTest(t)
	api := &API{Store: s}
	// An Assistant's policy cannot be loosened through the API: the body field
	// is ignored and review stays.
	made, err := call(t, api, `{"requestId":"r1","name":"Ada","defaultPolicy":"auto","autoMerge":true}`, "assistant", "create")
	if err != nil {
		t.Fatal(err)
	}
	id := made["assistant"].(map[string]any)
	if id["defaultPolicy"] != "review" {
		t.Fatalf("policy = %v", id["defaultPolicy"])
	}
	// There is no merge, push or delete command to be tricked into running.
	for _, cmd := range []string{"merge", "push", "delete", "remove"} {
		if _, err := call(t, api, `{}`, "assistant", "task", cmd, "t_x"); codeOf(err) != "invalid" {
			t.Fatalf("task %s is a command: %v", cmd, err)
		}
	}
	// The approval card for a model wake says what the limits really are.
	setBeat(t, s, mustAssistantByID(t, s, id["id"].(string)), HeartbeatInput{Cron: "*/10 * * * *", WakeBudget: 1})
	card, _, err := s.RequestHeartbeatApproval("w", id["id"].(string))
	if err != nil || !strings.Contains(card.Action, "not an enforced sandbox") {
		t.Fatalf("card = %+v %v", card, err)
	}
}

func mustAssistantByID(t *testing.T, s *Store, id string) Assistant {
	t.Helper()
	a, err := s.GetAssistant(id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
