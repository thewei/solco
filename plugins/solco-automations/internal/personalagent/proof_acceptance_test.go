package personalagent

import "testing"

// Q07: a research (life) task is accepted on delivery, authenticity and
// freshness — never on a review or a merge. The proof says each, from the
// recorded run, and a research task never enters Git review.
func TestResearchTaskIsAcceptedOnDeliveryAuthenticityAndFreshnessNotReview(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	task := mkTask(t, s, "t1", a.ID, "Compare flight prices")
	r := finishWithResult(t, s, task.ID, WorkerResult{Summary: "Cheapest is Tuesday.", Evidence: EvidenceOMPTranscript, AgentSessionID: "s1", MessageID: "m1",
		SHA256: "ab", Sources: []SourceRef{{URL: "https://a.example/fares", Grounded: true}, {URL: "https://b.example/guess"}, {URL: "https://c.example/x", Grounded: true}}})

	got, _ := s.GetTask(task.ID)
	if got.State != TaskDone || got.ReviewState != "" {
		t.Fatalf("a research task must finish done with no review: %+v", got)
	}
	p, err := s.TaskProof(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	acc := p.Acceptance
	if acc == nil || !acc.Delivered || acc.Authenticity != "transcript" || acc.SourcesTotal != 3 || acc.SourcesGrounded != 2 {
		t.Fatalf("acceptance = %+v", acc)
	}
	fin, _ := s.GetRun(r.ID)
	if acc.FinishedAt == 0 || acc.FinishedAt != fin.FinishedAt {
		t.Fatalf("freshness must be the run's own finish time: %+v vs %d", acc, fin.FinishedAt)
	}
}

func TestAcceptanceSaysWhenAnswerIsOnlyTheAgentsNoteOrNothing(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	task := mkTask(t, s, "t1", a.ID, "Old style")
	r, _, _ := s.ClaimTask(task.ID, "d")
	_ = s.MarkStarting(r.ID)
	_ = s.RecordLaunch(r.ID, Session{PaneID: "w1:p1"})
	_ = s.FinishRun(r.ID, RunDone, "all good")
	p, _ := s.TaskProof(task.ID)
	if p.Acceptance == nil || !p.Acceptance.Delivered || p.Acceptance.Authenticity != "agent_note" {
		t.Fatalf("note acceptance = %+v", p.Acceptance)
	}
	open := mkTask(t, s, "t2", a.ID, "Queued")
	q, _ := s.TaskProof(open.ID)
	if q.Acceptance == nil || q.Acceptance.Delivered || q.Acceptance.Authenticity != "none" || q.Acceptance.FinishedAt != 0 {
		t.Fatalf("nothing delivered = %+v", q.Acceptance)
	}
}

// A coding task keeps the mature review flow; it has no research acceptance.
func TestCodingTaskHasNoResearchAcceptance(t *testing.T) {
	f := newReview(t)
	if p, _ := f.s.TaskProof(f.task.ID); p.Acceptance != nil {
		t.Fatalf("a coding task's acceptance is its review: %+v", p.Acceptance)
	}
}
