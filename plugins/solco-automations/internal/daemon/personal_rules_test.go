package daemon

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/history"
	"github.com/thewei/solco/plugins/solco-automations/internal/host"
	"github.com/thewei/solco/plugins/solco-automations/internal/personalagent"
	"github.com/thewei/solco/plugins/solco-automations/internal/runner"
)

// countHost provisions at once and counts the runs each automation got.
type countHost struct {
	mu sync.Mutex
	n  map[string]int
}

func (h *countHost) Provision(a config.Automation) (host.Session, error) {
	h.mu.Lock()
	h.n[a.Name]++
	h.mu.Unlock()
	return host.Session{WorkspaceID: "w1", PaneID: "w1:p1"}, nil
}
func (h *countHost) Do(host.Session, config.Automation, time.Duration) error { return nil }
func (h *countHost) count(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n[name]
}

func personalFixture(t *testing.T) (*personal, *countHost, *runner.Runner, *scheduleState, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	yaml := `automations:
  - name: owned
    cron: "* * * * *"
    repo: /repo
    prompt: go
  - name: plain
    cron: "* * * * *"
    repo: /repo
    prompt: go
`
	if err := os.WriteFile(filepath.Join(dir, "automations.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	store, err := personalagent.Open(filepath.Join(dir, "personal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	p := &personal{store: store, seen: map[string]string{}}
	h := &countHost{n: map[string]int{}}
	due := time.Now().Add(-2 * time.Minute)
	state := &scheduleState{
		LastOccurrence: map[string]time.Time{"owned": due, "plain": due},
		Invalid:        map[string]bool{}, Queued: map[string]bool{},
	}
	return p, h, runner.New(h), state, cfg
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// D02/R05: a rule the ledger owns is invisible to the legacy scheduler, so an
// occurrence is never dispatched twice; the rest of automations.yaml keeps
// running through the old path untouched.
func TestLegacySchedulerSkipsOwnedRules(t *testing.T) {
	p, h, runs, state, cfg := personalFixture(t)
	last := map[string]time.Time{"owned": state.LastOccurrence["owned"]}
	if _, _, err := p.store.AdoptLegacy("", "owned", cfg, last, time.Now()); err != nil {
		t.Fatal(err)
	}
	evaluateWith(state, runs, nil, p)
	waitFor(t, "the unowned rule to run", func() bool { return h.count("plain") >= 1 })
	time.Sleep(100 * time.Millisecond)
	if h.count("owned") != 0 {
		t.Fatalf("the legacy scheduler dispatched an owned rule %d time(s)", h.count("owned"))
	}
	if h.count("plain") != 1 {
		t.Fatalf("plain ran %d times", h.count("plain"))
	}
}

// Rollback gives the rule back exactly once, resuming from the ledger's
// watermark (not an older one the old scheduler would replay).
func TestRollbackReturnsTheRuleToTheLegacyScheduler(t *testing.T) {
	p, h, runs, state, cfg := personalFixture(t)
	watermark := time.Now().Add(-90 * time.Second).Truncate(time.Millisecond)
	if _, _, err := p.store.AdoptLegacy("", "owned", cfg, map[string]time.Time{"owned": watermark}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.store.RollbackLegacy("", "owned"); err != nil {
		t.Fatal(err)
	}
	delete(state.LastOccurrence, "owned") // the old state forgot it while owned
	evaluateWith(state, runs, nil, p)
	waitFor(t, "the rolled-back rule to run again", func() bool { return h.count("owned") >= 1 })
	if again := p.handbacks(); len(again) != 0 {
		t.Fatalf("the handback was not consumed: %v", again)
	}
}

// A rule's run reaches history.jsonl in the old record shape, once per status.
func TestRuleRunsWriteTheOldHistory(t *testing.T) {
	p, _, _, _, _ := personalFixture(t)
	task := personalagent.Task{RuleID: personalagent.LegacyRuleID("owned")}
	run := personalagent.Run{ID: "r_1", Status: personalagent.RunRunning, WorkspaceID: "w1", PaneID: "w1:p1", Branch: "auto/owned-x"}
	p.settled(task, run)
	p.settled(task, run) // duplicate: written once
	run.Status = personalagent.RunDone
	run.CreatedAt, run.StartedAt, run.FinishedAt = 1000, 2000, 5000
	p.settled(task, run)
	p.settled(personalagent.Task{RuleID: ""}, run) // not a rule: ignored

	recs, err := history.Runs("owned", 0)
	if err != nil || len(recs) != 1 {
		t.Fatalf("history = %+v %v", recs, err)
	}
	if recs[0].Status != history.StatusDone || recs[0].Worktree != "auto/owned-x" || recs[0].PaneID != "w1:p1" {
		t.Fatalf("record = %+v", recs[0])
	}
}
