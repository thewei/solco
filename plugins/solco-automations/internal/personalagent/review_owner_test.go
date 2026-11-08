package personalagent

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// Q02: the Owner's three answers to a coding task's evidence are Accept (the
// existing approved merge), Reject and Resume. Neither of the new two merges
// anything or answers "done" on the Agent's say-so.

func (f *rv) approvalState(id string) string {
	f.t.Helper()
	ap, err := f.s.GetApproval(id)
	if err != nil {
		f.t.Fatal(err)
	}
	return ap.State
}

func TestResumeSendsTheTaskBackToItsOwnWorktreeWithTheOwnersWords(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"a.txt": "1\n"}, "first pass")
	f.finish()
	before := mainHead(t, f.repo)
	rev := f.start("s1")
	rev, ap, _, err := f.r.RequestMerge("rm", rev.ID)
	if err != nil {
		t.Fatal(err)
	}

	got, replayed, err := f.r.Resume("res1", rev.ID, "Add a test for a.txt and keep the file under 3 lines")
	if err != nil || replayed {
		t.Fatalf("resume: %v replayed=%v", err, replayed)
	}
	if got.State != ReviewChangesRequested || !strings.Contains(got.Error, "Add a test") {
		t.Fatalf("review = %+v", got)
	}
	task, _ := f.s.GetTask(f.task.ID)
	if task.State != TaskQueued || task.Attempt != f.task.Attempt+1 || task.NeedsYou || task.ReviewState != ReviewChangesRequested {
		t.Fatalf("task = %+v", task)
	}
	if !strings.Contains(task.Brief, f.task.Brief) || !strings.Contains(task.Brief, "Add a test for a.txt") {
		t.Fatalf("the brief must carry the original ask and the Owner's feedback:\n%s", task.Brief)
	}
	if f.approvalState(ap.ID) != ApprovalDenied {
		t.Fatalf("the pending merge card must not stay answerable: %s", f.approvalState(ap.ID))
	}
	if _, err := f.r.Merge(rev.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("merge after resume: %v", err)
	}
	if mainHead(t, f.repo) != before {
		t.Fatal("resume moved the main checkout")
	}
	// Same branch, same folder, work kept: the retry lands where the first pass was.
	again, err := f.w.Prepare(task)
	if err != nil || again.Path != f.info.Path || again.Branch != f.info.Branch {
		t.Fatalf("the retry must reuse the worktree: %+v %v", again, err)
	}
	if _, err := os.Stat(again.Path + "/a.txt"); err != nil {
		t.Fatalf("the first pass was lost: %v", err)
	}
	// Replaying the same request is the same answer, not a second attempt.
	if _, replayed, err := f.r.Resume("res1", rev.ID, "Add a test for a.txt and keep the file under 3 lines"); err != nil || !replayed {
		t.Fatalf("replay: %v %v", replayed, err)
	}
	if task2, _ := f.s.GetTask(f.task.ID); task2.Attempt != task.Attempt {
		t.Fatalf("attempt moved on replay: %d", task2.Attempt)
	}
}

func TestResumedWorkIsReviewedAgainFromItsNewHead(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"a.txt": "1\n"}, "first pass")
	f.finish()
	first := f.start("s1")
	if _, _, err := f.r.Resume("res", first.ID, "also add b.txt"); err != nil {
		t.Fatal(err)
	}
	f.agentWork(map[string]string{"b.txt": "2\n"}, "second pass")
	f.finish()
	second := f.start("s2")
	if second.ID == first.ID || second.Head == first.Head || second.Files != 2 {
		t.Fatalf("the second review must describe the new head: first=%+v second=%+v", first, second)
	}
	if old, _ := f.r.Get(first.ID); old.State != ReviewChangesRequested {
		t.Fatalf("the answered review keeps its answer: %s", old.State)
	}
}

func TestResumeNeedsWordsAndAReviewThatIsStillOpen(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"a.txt": "1\n"}, "work")
	f.finish()
	rev := f.start("s1")
	if _, _, err := f.r.Resume("r0", rev.ID, "   "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty feedback: %v", err)
	}
	if _, _, err := f.r.Resume("r1", rev.ID, strings.Repeat("x", 2001)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversize feedback: %v", err)
	}
	if _, _, err := f.r.Resume("r2", rev.ID, "fine"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.r.Resume("r3", rev.ID, "again"); !errors.Is(err, ErrBadState) {
		t.Fatalf("a review that was already answered: %v", err)
	}
	if _, err := f.r.Reject("rj", rev.ID, "no"); !errors.Is(err, ErrBadState) {
		t.Fatalf("reject after resume: %v", err)
	}
}

func TestResumeRefusesAMergedReview(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"a.txt": "1\n"}, "work")
	f.finish()
	rev := f.approveMerge(f.start("s1"))
	if _, err := f.r.Merge(rev.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.r.Resume("r", rev.ID, "more"); !errors.Is(err, ErrBadState) {
		t.Fatalf("resume of a merged review: %v", err)
	}
	if _, err := f.r.Reject("rj", rev.ID, "no"); !errors.Is(err, ErrBadState) {
		t.Fatalf("reject of a merged review: %v", err)
	}
}

func TestRejectClosesTheTaskWithoutMergingAndKeepsTheWork(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"a.txt": "1\n"}, "work")
	f.finish()
	before := mainHead(t, f.repo)
	rev := f.start("s1")
	rev, ap, _, err := f.r.RequestMerge("rm", rev.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.r.Reject("rj1", rev.ID, "wrong approach")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ReviewRejected || !strings.Contains(got.Error, "wrong approach") {
		t.Fatalf("review = %+v", got)
	}
	task, _ := f.s.GetTask(f.task.ID)
	if task.State != TaskCancelled || task.NeedsYou || task.ReviewState != ReviewRejected {
		t.Fatalf("task = %+v", task)
	}
	if f.approvalState(ap.ID) != ApprovalDenied {
		t.Fatalf("approval = %s", f.approvalState(ap.ID))
	}
	if _, err := f.r.Merge(rev.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("merge after reject: %v", err)
	}
	if mainHead(t, f.repo) != before {
		t.Fatal("reject moved the main checkout")
	}
	info, ok, err := f.w.Get(f.task.ID)
	if err != nil || !ok || info.State != "active" {
		t.Fatalf("the work must stay for inspection: %+v %v", info, err)
	}
	// A rejected task can still be given another go by the existing deliberate retry.
	if _, _, err := f.s.RetryTask("retry", f.task.ID, task.Revision); err != nil {
		t.Fatalf("retry of a rejected task: %v", err)
	}
}

// A research result and a coding result never share one "done": research is
// proved by its transcript, coding by a merge a person approved.
func TestACodingTaskIsNeverDoneBeforeItsMergeAndRejectNeverCountsAsDone(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"a.txt": "1\n"}, "work")
	f.finish()
	rev := f.start("s1")
	if task, _ := f.s.GetTask(f.task.ID); task.State == TaskDone {
		t.Fatal("evidence is not done")
	}
	if _, err := f.r.Reject("rj", rev.ID, "no"); err != nil {
		t.Fatal(err)
	}
	if task, _ := f.s.GetTask(f.task.ID); task.State == TaskDone {
		t.Fatal("a rejection is not done")
	}
}
