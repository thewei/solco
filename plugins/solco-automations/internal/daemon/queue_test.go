package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/history"
	"github.com/DnzzL/herdr-automations/internal/host"
	"github.com/DnzzL/herdr-automations/internal/runner"
)

// blockingHost provisions at once and holds every Do until released.
type blockingHost struct {
	started chan struct{}
	release chan struct{}
}

func (h *blockingHost) Provision(config.Automation) (host.Session, error) {
	return host.Session{WorkspaceID: "w1", PaneID: "w1:p1"}, nil
}

func (h *blockingHost) Do(host.Session, config.Automation, time.Duration) error {
	select {
	case <-h.started:
	default:
		close(h.started)
	}
	<-h.release
	return nil
}

// under overlap: queue, an occurrence that comes due while the previous run
// is still working is held as one pending promise, and fires once the runner
// is free — never twice.
func TestQueueHoldsOneOccurrenceUntilTheRunFinishes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	yaml := `automations:
  - name: q
    cron: "* * * * *"
    repo: /repo
    prompt: go
    on_overlap: queue
`
	if err := os.WriteFile(filepath.Join(dir, "automations.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	h := &blockingHost{started: make(chan struct{}), release: make(chan struct{})}
	runs := runner.New(h)
	state := &scheduleState{
		LastOccurrence: map[string]time.Time{"q": time.Now().Add(-time.Minute)},
		Invalid:        map[string]bool{}, Queued: map[string]bool{},
	}

	first := make(chan struct{})
	go func() {
		_ = runs.Run(config.Automation{
			Name: "q", Cron: "* * * * *", Repo: "/repo", Prompt: "go",
			Workspace: config.WorkspaceWorktree, OnOverlap: config.OnOverlapQueue,
			TimeoutMinutes: 1,
		}, history.TriggerCron)
		close(first)
	}()
	<-h.started // the in-flight run holds the slot

	evaluate(state, runs, nil)
	if !state.Queued["q"] {
		t.Fatal("the due occurrence was not queued while the run was in flight")
	}

	// A later tick while still busy queues nothing more.
	evaluate(state, runs, nil)
	recs, err := history.Runs("q", 0)
	if err != nil {
		t.Fatal(err)
	}
	queued := 0
	for _, r := range recs {
		if r.Status == history.StatusQueued {
			queued++
		}
	}
	if queued != 1 {
		t.Fatalf("queued records = %d, want exactly one: queue never stacks", queued)
	}

	close(h.release)
	<-first

	// The runner is free: the queued occurrence fires and the promise clears.
	evaluate(state, runs, nil)
	if _, still := state.Queued["q"]; still {
		t.Error("the pending flag survived its own fire")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		recs, _ := history.Runs("q", 0)
		done := 0
		for _, r := range recs {
			if r.Status == history.StatusDone {
				done++
			}
		}
		if done >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected the queued occurrence to run to done, history shows %+v", recs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
