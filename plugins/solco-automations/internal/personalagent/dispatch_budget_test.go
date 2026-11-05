package personalagent

import (
	"fmt"
	"testing"
	"time"
)

// W08: a busy Assistant must not starve the others, and a daemon restart must
// not hand out a second full quota on top of the runs it just re-attached to.
func TestQueuedTasksInterleaveAssistantsInsteadOfFirstComeFirstServed(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_800_000_000, 0)}
	s, _ := openTest(t, WithClock(clock.now))
	ada := mustAssistant(t, s, "a1", "Ada")
	bob := mustAssistant(t, s, "a2", "Bob")
	for i := 1; i <= 3; i++ {
		clock.t = clock.t.Add(time.Second)
		mkTask(t, s, fmt.Sprintf("ada-%d", i), ada.ID, fmt.Sprintf("Ada job %d", i))
	}
	clock.t = clock.t.Add(time.Second)
	mkTask(t, s, "bob-1", bob.ID, "Bob job 1")
	queued, err := s.QueuedTasks()
	if err != nil || len(queued) != 4 {
		t.Fatalf("queued = %d %v", len(queued), err)
	}
	if queued[0].AssistantID == queued[1].AssistantID {
		t.Fatalf("the first two to start must belong to different Assistants: %s, %s", queued[0].Title, queued[1].Title)
	}
	if queued[0].Title != "Ada job 1" || queued[1].Title != "Bob job 1" {
		t.Fatalf("each Assistant's oldest task goes first, oldest Assistant first: %q then %q", queued[0].Title, queued[1].Title)
	}
}

func TestReattachedRunsCountAgainstTheParallelLimitAfterARestart(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	for i := 1; i <= 2; i++ {
		task := mkTask(t, s, fmt.Sprintf("running-%d", i), a.ID, fmt.Sprintf("Was running %d", i))
		run, _, err := s.ClaimTask(task.ID, "daemon")
		if err != nil {
			t.Fatal(err)
		}
		_ = s.MarkStarting(run.ID)
		if err := s.RecordLaunch(run.ID, Session{HerdrSession: "default", WorkspaceID: fmt.Sprintf("w%d", i), PaneID: fmt.Sprintf("w%d:p1", i)}); err != nil {
			t.Fatal(err)
		}
	}
	mkTask(t, s, "waiting-1", a.ID, "Still queued")
	gate := make(chan struct{})
	l := &fakeLauncher{workGate: gate}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon", MaxParallel: 2}
	if n := d.Resume(t.Context()); n != 2 {
		t.Fatalf("resumed %d, want 2", n)
	}
	if started := d.Pump(t.Context()); started != 0 {
		t.Fatalf("two runs are already working: a restart must not start %d more over the limit", started)
	}
	close(gate)
	d.Wait()
	if started := d.Pump(t.Context()); started != 1 {
		t.Fatalf("once they finish the queued task may start, started %d", started)
	}
	d.Wait()
}
