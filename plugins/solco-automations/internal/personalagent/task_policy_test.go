package personalagent

import (
	"errors"
	"testing"
)

// requireChecksFixture is newReview with a task created `requireChecks`.
func requireChecksFixture(t *testing.T) *rv {
	t.Helper()
	w, s, repo := wtFixture(t)
	a := mustAssistant(t, s, "a-pol", "Ada")
	task, _, err := s.CreateTask("t-pol", CreateTaskInput{AssistantID: a.ID, Title: "Fix", Brief: "Fix it.", Kind: KindCoding, RepoPath: repo, RequireChecks: true})
	if err != nil {
		t.Fatal(err)
	}
	info, err := w.Prepare(task)
	if err != nil {
		t.Fatal(err)
	}
	f := &rv{t: t, s: s, w: w, repo: repo, task: task, info: info, asst: a, r: &Reviewer{Store: s, Worktrees: w}}
	f.agentWork(map[string]string{"x.txt": "x\n"}, "work")
	f.finish()
	return f
}

// Q03: a task may ask for MORE than its Assistant's policy, never less. A task
// that requires checks cannot reach "ready" on an Assistant with none approved.
func TestATaskThatRequiresChecksCannotBeReviewedWithoutApprovedChecks(t *testing.T) {
	f := requireChecksFixture(t)
	_, _, err := f.r.Start("s1", f.task.ID)
	if !errors.Is(err, ErrChecksRequired) {
		t.Fatalf("want ErrChecksRequired, got %v", err)
	}
	if rev, found, e := f.r.ForTask(f.task.ID); e == nil && found {
		t.Fatalf("a refused start must leave no review: %+v", rev)
	}
}

func TestATaskThatRequiresChecksIsReviewedOnceTheAssistantApprovedSome(t *testing.T) {
	f := requireChecksFixture(t)
	f.approveChecks("true")
	rev := f.start("s1")
	if rev.State != ReviewReady || len(rev.Checks) != 1 || !rev.Checks[0].OK {
		t.Fatalf("review = %+v", rev)
	}
}

// A task without the flag keeps the Assistant's policy exactly as before.
func TestATaskWithoutTheFlagInheritsTheAssistantPolicy(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "x\n"}, "work")
	f.finish()
	if rev := f.start("s1"); rev.State != ReviewReady {
		t.Fatalf("review = %+v", rev)
	}
}

func TestRequireChecksIsForCodingTasksOnly(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	_, _, err := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "Look", Brief: "Look.", Kind: KindResearch, RequireChecks: true})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

// The proof says where the policy comes from, so the person can tell what the
// task inherited from what it asked for.
func TestTaskProofNamesThePolicyAndItsSource(t *testing.T) {
	f := requireChecksFixture(t)
	f.approveChecks("true")
	p, err := f.s.TaskProof(f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	pol := p.Policy
	if !pol.RequireChecks || pol.ChecksFrom != "assistant" || len(pol.Checks) != 1 || pol.Checks[0] != "true" || !pol.MergeNeedsPerson {
		t.Fatalf("policy = %+v", pol)
	}
	plain := newReview(t)
	pp, err := plain.s.TaskProof(plain.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pp.Policy.RequireChecks || !pp.Policy.MergeNeedsPerson {
		t.Fatalf("plain policy = %+v", pp.Policy)
	}
}
