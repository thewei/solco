package personalagent

import (
	"testing"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// AUD-10: a rejected repeat of an already committed request must not first
// rewrite the persisted YAML. SQLite receipts are not enough if the file was
// changed before checking the original requestId.
func TestRoutineDraftConflictingReplayDoesNotTouchYAML(t *testing.T) {
	e := newRoutineEnv(t)
	if _, err := e.draft(t, "same-request", nil); err != nil {
		t.Fatal(err)
	}
	before, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	old := before.Find("morning-check").Prompt
	_, err = e.draft(t, "same-request", func(m map[string]any) { m["prompt"] = "Unapproved conflicting replay" })
	if codeOf(err) != "request_conflict" {
		t.Fatalf("expected request_conflict, got %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Find("morning-check").Prompt != old {
		t.Fatalf("request rejected but YAML was modified: %q", cfg.Find("morning-check").Prompt)
	}
}

// AUD-18: a concurrent writer may replace the approved draft precisely between
// the Keeper's initial read and the final approval. The guard must compare the
// SAME document it activates, rather than the Keeper's stale first snapshot.
func TestRoutineKeeperRefusesPromptSwappedDuringActivation(t *testing.T) {
	e := newRoutineEnv(t)
	first, err := e.draft(t, "owner-review", nil)
	if err != nil {
		t.Fatal(err)
	}
	ap := first["approval"].(map[string]any)
	if _, _, err := e.s.DecideApproval("decided", ap["id"].(string), ap["actionHash"].(string), true); err != nil {
		t.Fatal(err)
	}
	k := e.api.keeper()
	realApprove := k.ApproveChecked
	k.ApproveChecked = func(name, hash string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		altered := *cfg.Find(name)
		altered.Prompt = "The owner never reviewed this new prompt"
		changed, err := config.Put(name, altered, false)
		if err != nil {
			return err
		}
		if changed.Problem != "" {
			t.Fatalf("concurrent edit refused: %s", changed.Problem)
		}
		return realApprove(name, hash)
	}
	report, err := k.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if report.Enabled != 0 || report.Stale != 1 {
		t.Fatalf("unexpected activation: %+v", report)
	}
	state, err := e.s.GetRoutine("morning-check")
	if err != nil {
		t.Fatal(err)
	}
	if state.State != RoutineStale {
		t.Fatalf("new text active despite hash mismatch: %+v", state)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if a := cfg.Find("morning-check"); a == nil || !a.Draft {
		t.Fatalf("routine no longer a draft: %+v", a)
	}
}
