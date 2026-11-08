package personalagent

import (
	"strings"
	"testing"
)

// runningTask walks a queued task to a live run.
func runningTask(t *testing.T, s *Store, a Assistant, req, title string) (Task, Run) {
	t.Helper()
	task, _, err := s.CreateTask(req, CreateTaskInput{AssistantID: a.ID, Title: title, Brief: "SECRET-BRIEF-BODY do the thing", Kind: KindResearch})
	if err != nil {
		t.Fatal(err)
	}
	run, fresh, err := s.ClaimTask(task.ID, "test")
	if err != nil || !fresh {
		t.Fatalf("claim: %v fresh=%v", err, fresh)
	}
	if err := s.MarkStarting(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLaunch(run.ID, Session{HerdrSession: "d", WorkspaceID: "w1", PaneID: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	return task, run
}

func kinds(evs []Event) []string {
	out := []string{}
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

// C01: a task's added / blocked / completed / failed moments are events, each
// written in the same transaction as the transition, naming the task and its
// title but never its brief.
func TestTaskLifecycleEmitsEvents(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	_, run := runningTask(t, s, a, "t1", "Check the PR")
	if err := s.BlockRun(run.ID, "trust dialog"); err != nil {
		t.Fatal(err)
	}
	_, run2 := runningTask(t, s, a, "t2", "Second")
	if err := s.FinishRun(run2.ID, RunFailed, "boom"); err != nil {
		t.Fatal(err)
	}
	_, run3 := runningTask(t, s, a, "t3", "Third")
	if err := s.FinishRun(run3.ID, RunDone, "all good"); err != nil {
		t.Fatal(err)
	}
	evs, err := s.Events(a.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"task.added", "task.blocked", "task.added", "task.failed", "task.added", "task.completed"}
	if got := kinds(evs); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for _, e := range evs {
		if e.Seq == 0 || e.Source != "task" || e.AssistantID != a.ID || e.TaskID == "" {
			t.Fatalf("event = %+v", e)
		}
		if strings.Contains(e.Detail, "SECRET-BRIEF-BODY") || strings.Contains(e.Detail, "boom") || strings.Contains(e.Detail, "all good") {
			t.Fatalf("event leaks task text: %q", e.Detail)
		}
	}
	if evs[1].Priority <= evs[0].Priority || evs[3].Priority <= evs[5].Priority || evs[5].Priority <= evs[0].Priority {
		t.Fatalf("priorities: added=%d blocked=%d failed=%d completed=%d", evs[0].Priority, evs[1].Priority, evs[3].Priority, evs[5].Priority)
	}
	// Strictly increasing watermark.
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq <= evs[i-1].Seq {
			t.Fatalf("seq not increasing: %+v", evs)
		}
	}
	// A reader resumes after its watermark and sees only what is new.
	after, _ := s.Events(a.ID, evs[3].Seq, 100)
	if len(after) != 2 {
		t.Fatalf("after watermark = %v", kinds(after))
	}
}

// C01: the same fact reported three times is one event.
func TestRecordEventDedupes(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	in := Event{AssistantID: a.ID, Source: "git", Kind: "git.main_red", Resource: "o/r@main", Revision: "sha1", Priority: 2, Detail: "main is red"}
	made := 0
	for i := 0; i < 3; i++ {
		_, created, err := s.RecordEvent(in)
		if err != nil {
			t.Fatal(err)
		}
		if created {
			made++
		}
	}
	if made != 1 {
		t.Fatalf("created %d times", made)
	}
	in.Revision = "sha2" // a new fact
	if _, created, _ := s.RecordEvent(in); !created {
		t.Fatal("a new revision is a new event")
	}
	if _, _, err := s.RecordEvent(Event{AssistantID: a.ID}); err == nil {
		t.Fatal("an event needs a source, kind, resource and revision")
	}
}
