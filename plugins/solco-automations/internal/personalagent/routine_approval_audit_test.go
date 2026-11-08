package personalagent

import (
	"sync/atomic"
	"testing"
	"time"

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

// Two CLI requests with the same request ID can race across YAML and SQLite.
// A conflicting draft must not overwrite the winner's approved-to-review
// definition, even when it starts while the winner has written YAML but has
// not yet persisted its receipt.
func TestRoutineConcurrentConflictingRequestDoesNotOverwriteDraft(t *testing.T) {
	e := newRoutineEnv(t)
	firstPut := make(chan struct{})
	resume := make(chan struct{})
	var puts atomic.Int32
	e.api.ConfigPut = func(original string, x config.Automation) (config.PutResult, error) {
		out, err := config.Put(original, x, false)
		if puts.Add(1) == 1 {
			close(firstPut)
			<-resume
		}
		return out, err
	}
	type result struct {
		value map[string]any
		err   error
	}
	a := make(chan result, 1)
	b := make(chan result, 1)
	go func() {
		v, err := e.draft(t, "same-routine-request", nil)
		a <- result{v, err}
	}()
	select {
	case <-firstPut:
	case <-time.After(3 * time.Second):
		t.Fatal("first command never wrote the YAML draft")
	}
	go func() {
		v, err := e.draft(t, "same-routine-request", func(m map[string]any) {
			m["prompt"] = "Different prompt from rejected concurrent request"
		})
		b <- result{v, err}
	}()
	// Allow the second request to attempt a preflight while the first is
	// between its file write and durable receipt.
	time.Sleep(30 * time.Millisecond)
	close(resume)
	aResult, bResult := <-a, <-b
	if aResult.err != nil || aResult.value == nil {
		t.Fatalf("first draft: %v", aResult.err)
	}
	if codeOf(bResult.err) != "request_conflict" {
		t.Fatalf("different replay should conflict: %v", bResult.err)
	}
	if puts.Load() != 1 {
		t.Fatalf("rejected replay modified YAML, config writes=%d", puts.Load())
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if x := cfg.Find("morning-check"); x == nil || x.Prompt != "Check the repository for risky changes and summarise." || !x.Draft {
		t.Fatalf("winner lost to rejected replay: %+v", x)
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
