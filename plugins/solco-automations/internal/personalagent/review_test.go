package personalagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// rv is one coding task that has finished work in its worktree.
type rv struct {
	t    *testing.T
	s    *Store
	w    *Worktrees
	r    *Reviewer
	repo string
	asst Assistant
	task Task
	info WorktreeInfo
}

func newReview(t *testing.T) *rv {
	t.Helper()
	w, s, repo := wtFixture(t)
	task := codingTask(t, s, repo)
	info, err := w.Prepare(task)
	if err != nil {
		t.Fatal(err)
	}
	f := &rv{t: t, s: s, w: w, repo: repo, task: task, info: info}
	f.asst = mustAssistantByID(t, s, task.AssistantID)
	f.r = &Reviewer{Store: s, Worktrees: w}
	return f
}

// agentWork is what a coding Agent does in its worktree: files, then a commit.
func (f *rv) agentWork(files map[string]string, msg string) {
	f.t.Helper()
	for name, body := range files {
		p := filepath.Join(f.info.Path, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	gitT(f.t, f.info.Path, "add", "-A")
	gitT(f.t, f.info.Path, "commit", "-m", msg)
}

// finish moves the task to review_required the way a finished coding run does.
func (f *rv) finish() {
	f.t.Helper()
	if _, err := f.s.db.Exec(`UPDATE tasks SET state=?, review_state='pending', needs_you=1 WHERE id=?`, TaskReviewRequired, f.task.ID); err != nil {
		f.t.Fatal(err)
	}
}

func (f *rv) approveChecks(cmds ...string) {
	f.t.Helper()
	ap, _, err := f.s.RequestChecks("chk-"+strings.Join(cmds, ","), f.asst.ID, cmds)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, _, err := f.s.DecideApproval("chk-dec-"+ap.ID, ap.ID, ap.ActionHash, true); err != nil {
		f.t.Fatal(err)
	}
}

func (f *rv) start(req string) Review {
	f.t.Helper()
	rev, _, err := f.r.Start(req, f.task.ID)
	if err != nil {
		f.t.Fatalf("start: %v", err)
	}
	return rev
}

func (f *rv) approveMerge(rev Review) Review {
	f.t.Helper()
	rev, ap, _, err := f.r.RequestMerge("rm-"+rev.ID, rev.ID)
	if err != nil {
		f.t.Fatalf("request merge: %v", err)
	}
	if _, _, err := f.s.DecideApproval("dm-"+ap.ID, ap.ID, ap.ActionHash, true); err != nil {
		f.t.Fatalf("approve: %v", err)
	}
	return rev
}

func mainHead(t *testing.T, repo string) string { return gitT(t, repo, "rev-parse", "HEAD") }

// E03: the evidence is the real diff, with names kept literal.
func TestReviewStartCollectsLiteralEvidence(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"a\tb.txt": "x\n", "[literal].txt": "y\n", "dir/new file.go": "package x\n"}, "work")
	gitT(t, f.info.Path, "mv", "README.md", "RENAMED.md")
	gitT(t, f.info.Path, "commit", "-m", "rename")
	f.finish()

	rev := f.start("s1")
	if rev.State != ReviewReady || rev.Head != gitT(t, f.info.Path, "rev-parse", "HEAD") || rev.BaseHead != f.info.BaseHead {
		t.Fatalf("review = %+v", rev)
	}
	if rev.TargetHead != mainHead(t, f.repo) || rev.TargetBranch != "main" {
		t.Fatalf("target = %+v", rev)
	}
	want := map[string]bool{"a\tb.txt": true, "[literal].txt": true, "dir/new file.go": true, "RENAMED.md": true}
	if rev.Files != 4 || rev.Insertions != 3 || len(rev.Paths) != 4 {
		t.Fatalf("stats = files %d +%d paths %q", rev.Files, rev.Insertions, rev.Paths)
	}
	for _, p := range rev.Paths {
		if !want[p] {
			t.Fatalf("unexpected path %q in %q", p, rev.Paths)
		}
	}
	if len(rev.DiffHash) != 64 {
		t.Fatalf("diff hash = %q", rev.DiffHash)
	}
	// Starting again for the same state is the same review (idempotent).
	if again := f.start("s1"); again.ID != rev.ID {
		t.Fatalf("replay made a second review: %s vs %s", again.ID, rev.ID)
	}
	if got, _ := f.s.GetTask(f.task.ID); got.ReviewState != ReviewReady {
		t.Fatalf("task review state = %q", got.ReviewState)
	}
}

func TestReviewStartRefusesWhatIsNotReviewable(t *testing.T) {
	f := newReview(t)
	// Not finished yet.
	if _, _, err := f.r.Start("a", f.task.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("running task: %v", err)
	}
	f.finish()
	// Nothing committed.
	if _, _, err := f.r.Start("b", f.task.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("no commits: %v", err)
	}
	// Committed, but with uncommitted leftovers: the evidence would not be the work.
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	if err := os.WriteFile(filepath.Join(f.info.Path, "scratch.txt"), []byte("?"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.r.Start("c", f.task.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("dirty worktree: %v", err)
	}
	// A research task is never merged.
	a := f.asst
	rt, _, _ := f.s.CreateTask("rt", CreateTaskInput{AssistantID: a.ID, Title: "R", Brief: "r", Kind: KindResearch})
	if _, _, err := f.r.Start("d", rt.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("research task: %v", err)
	}
}

// Checks are code execution; only a list a person approved ever runs.
func TestChecksRunOnlyAfterAPersonApprovedTheList(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()
	marker := filepath.Join(t.TempDir(), "ran")

	// Requested but not approved: nothing runs, nothing is recorded as passed.
	if _, _, err := f.s.RequestChecks("c1", f.asst.ID, []string{"touch " + marker}); err != nil {
		t.Fatal(err)
	}
	rev := f.start("s1")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an unapproved check ran")
	}
	if len(rev.Checks) != 0 || rev.State != ReviewReady {
		t.Fatalf("review = %+v", rev)
	}

	// Approved: it runs in the worktree, and a re-run of the review shows it.
	f.approveChecks("touch " + marker + " && test -f x.txt")
	f.finish()
	rev2, _, err := f.r.Start("s2", f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rev2.Checks) != 1 || !rev2.Checks[0].OK || rev2.State != ReviewReady {
		t.Fatalf("checks = %+v state=%s", rev2.Checks, rev2.State)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the approved check did not run")
	}

	// Changing the list withdraws the approval: the new list does not run.
	other := filepath.Join(t.TempDir(), "other")
	if _, _, err := f.s.RequestChecks("c2", f.asst.ID, []string{"touch " + other}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Checks(f.asst.ID); len(got) != 1 || !strings.Contains(got[0], marker) {
		t.Fatalf("active list changed before approval: %v", got)
	}
	// Bounds.
	if _, _, err := f.s.RequestChecks("c3", f.asst.ID, make([]string, MaxChecks+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too many checks: %v", err)
	}
}

func TestFailedChecksBlockTheMergeRequest(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()
	f.approveChecks("echo boom 1>&2; exit 3")
	rev := f.start("s1")
	if rev.State != ReviewChecksFailed || len(rev.Checks) != 1 || rev.Checks[0].OK || rev.Checks[0].ExitCode != 3 {
		t.Fatalf("review = %+v", rev)
	}
	if !strings.Contains(rev.Checks[0].Output, "boom") {
		t.Fatalf("output tail = %q", rev.Checks[0].Output)
	}
	if _, _, _, err := f.r.RequestMerge("rm", rev.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("request merge after failed checks: %v", err)
	}
}

// The core promise: nothing merges until a person approved exactly this head,
// and the merge re-checks everything at the instant it acts.
func TestHumanApprovedMergeHappyPath(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"feature.txt": "feature\n"}, "add feature")
	f.finish()
	before := mainHead(t, f.repo)
	rev := f.start("s1")

	// Review is not a merge: the main checkout has not moved.
	if mainHead(t, f.repo) != before {
		t.Fatal("starting a review moved the main checkout")
	}
	if _, err := f.r.Merge(rev.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("merge without a request: %v", err)
	}
	rev, ap, _, err := f.r.RequestMerge("rm", rev.ID)
	if err != nil || rev.State != ReviewAwaiting || ap.SubjectKind != "task" || ap.SubjectID != f.task.ID {
		t.Fatalf("request = %+v %+v %v", rev, ap, err)
	}
	if !strings.Contains(ap.Action, rev.Head[:12]) || !strings.Contains(ap.Action, f.info.Branch) || !strings.Contains(ap.Action, "main") {
		t.Fatalf("the card must name the exact head, branch and target: %q", ap.Action)
	}
	// Pending approval: still no merge.
	if _, err := f.r.Merge(rev.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("merge before the answer: %v", err)
	}
	if _, _, err := f.s.DecideApproval("d1", ap.ID, ap.ActionHash, true); err != nil {
		t.Fatal(err)
	}
	if mainHead(t, f.repo) != before {
		t.Fatal("approving alone moved the main checkout")
	}

	got, err := f.r.Merge(rev.ID)
	if err != nil || got.State != ReviewMerged {
		t.Fatalf("merge = %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(f.repo, "feature.txt")); err != nil {
		t.Fatal("the work is not in the main checkout")
	}
	if out := gitT(t, f.repo, "status", "--porcelain"); out != "" {
		t.Fatalf("main checkout left dirty: %q", out)
	}
	if gitT(t, f.repo, "rev-parse", "--abbrev-ref", "HEAD") != "main" {
		t.Fatal("the main checkout changed branch")
	}
	gitT(t, f.repo, "merge-base", "--is-ancestor", rev.Head, "HEAD")
	task, _ := f.s.GetTask(f.task.ID)
	if task.State != TaskDone || task.ReviewState != ReviewMerged || task.NeedsYou {
		t.Fatalf("task = %+v", task)
	}
	if _, err := os.Stat(f.info.Path); err == nil {
		t.Fatal("the merged worktree was kept")
	}
	if out := gitT(t, f.repo, "branch", "--list", f.info.Branch); out != "" {
		t.Fatalf("the merged branch was kept: %q", out)
	}
	evs, _ := f.s.Events(f.asst.ID, 0, 100)
	merged := 0
	for _, e := range evs {
		if e.Kind == "review.merged" {
			merged++
			if strings.Contains(e.Detail, "feature") {
				t.Fatalf("event detail carries content: %q", e.Detail)
			}
		}
	}
	if merged != 1 {
		t.Fatalf("review.merged events = %d", merged)
	}

	// Asking again is a no-op: one merge commit, no error.
	commits := gitT(t, f.repo, "rev-list", "--count", "HEAD")
	again, err := f.r.Merge(rev.ID)
	if err != nil || again.State != ReviewMerged || gitT(t, f.repo, "rev-list", "--count", "HEAD") != commits {
		t.Fatalf("second merge = %+v %v", again, err)
	}
}

func TestDenyOrNoApprovalNeverMerges(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()
	before := mainHead(t, f.repo)
	rev := f.start("s1")
	rev, ap, _, _ := f.r.RequestMerge("rm", rev.ID)
	if _, _, err := f.s.DecideApproval("d", ap.ID, ap.ActionHash, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.Merge(rev.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("merge after deny: %v", err)
	}
	if mainHead(t, f.repo) != before {
		t.Fatal("a denied merge moved the main checkout")
	}
}

// The approval is for one head. If the branch moves after the answer, the
// merge refuses and the main checkout is untouched; a new review makes the old
// card unanswerable.
func TestAtomicRecheckOfTheBranchHead(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()
	before := mainHead(t, f.repo)
	rev := f.approveMerge(f.start("s1"))

	f.agentWork(map[string]string{"sneaky.txt": "added after approval\n"}, "more")
	got, err := f.r.Merge(rev.ID)
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("merge after the head moved: %v", err)
	}
	if mainHead(t, f.repo) != before {
		t.Fatal("a stale merge moved the main checkout")
	}
	if _, serr := os.Stat(filepath.Join(f.repo, "sneaky.txt")); serr == nil {
		t.Fatal("unreviewed work reached the main checkout")
	}
	if cur, _ := f.r.Get(rev.ID); cur.State != ReviewStale {
		t.Fatalf("state = %s (%+v)", cur.State, got)
	}

	// A fresh review of the new head; its card is a different card.
	rev2 := f.start("s2")
	if rev2.ID == rev.ID || rev2.Head == rev.Head {
		t.Fatalf("expected a new review for the new head: %+v", rev2)
	}
	_, ap2, _, _ := f.r.RequestMerge("rm2", rev2.ID)
	// A third review while the card is pending withdraws it.
	f.agentWork(map[string]string{"third.txt": "3\n"}, "third")
	f.start("s3")
	if _, _, err := f.s.DecideApproval("old", ap2.ID, ap2.ActionHash, true); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("answering a superseded card: %v", err)
	}
}

func TestMergeRefusesWhenTheTargetMovedOrTheMainCheckoutIsNotClean(t *testing.T) {
	// Target branch moved after approval.
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()
	rev := f.approveMerge(f.start("s1"))
	if err := os.WriteFile(filepath.Join(f.repo, "other.txt"), []byte("o\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, f.repo, "add", ".")
	gitT(t, f.repo, "commit", "-m", "someone else")
	moved := mainHead(t, f.repo)
	if _, err := f.r.Merge(rev.ID); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("target moved: %v", err)
	}
	if mainHead(t, f.repo) != moved {
		t.Fatal("main moved")
	}

	// Dirty main checkout (the person's own work in progress).
	g := newReview(t)
	g.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	g.finish()
	rev = g.approveMerge(g.start("s1"))
	if err := os.WriteFile(filepath.Join(g.repo, "README.md"), []byte("my edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := mainHead(t, g.repo)
	if _, err := g.r.Merge(rev.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("dirty main: %v", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(g.repo, "README.md")); string(raw) != "my edit\n" || mainHead(t, g.repo) != before {
		t.Fatal("the person's edit or HEAD changed")
	}

	// Another branch checked out.
	h := newReview(t)
	h.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	h.finish()
	rev = h.approveMerge(h.start("s1"))
	gitT(t, h.repo, "checkout", "-b", "elsewhere")
	if _, err := h.r.Merge(rev.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("wrong branch: %v", err)
	}
	if gitT(t, h.repo, "rev-parse", "--abbrev-ref", "HEAD") != "elsewhere" {
		t.Fatal("the checkout was switched")
	}
}

// A conflict is a failure that returns to the task: main is left exactly as it
// was, the worktree and branch stay, and the person is told.
func TestMergeConflictAbortsAndKeepsTheWork(t *testing.T) {
	f := newReview(t)
	// main edits README after the worktree was made, before the review starts.
	if err := os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("main version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, f.repo, "commit", "-am", "main edit")
	f.agentWork(map[string]string{"README.md": "branch version\n"}, "branch edit")
	f.finish()
	rev := f.approveMerge(f.start("s1"))
	tip := mainHead(t, f.repo)

	got, err := f.r.Merge(rev.ID)
	if err == nil {
		t.Fatal("a conflicting merge succeeded")
	}
	if got.State != ReviewMergeFailed || !strings.Contains(strings.ToLower(got.Error), "conflict") {
		t.Fatalf("review = %+v err=%v", got, err)
	}
	if mainHead(t, f.repo) != tip || gitT(t, f.repo, "status", "--porcelain") != "" {
		t.Fatal("the aborted merge left the main checkout changed")
	}
	if _, serr := os.Stat(filepath.Join(f.repo, ".git", "MERGE_HEAD")); serr == nil {
		t.Fatal("MERGE_HEAD left behind")
	}
	if _, serr := os.Stat(f.info.Path); serr != nil {
		t.Fatal("the worktree was removed after a failed merge")
	}
	task, _ := f.s.GetTask(f.task.ID)
	if task.State != TaskReviewRequired || !task.NeedsYou || task.ReviewState != ReviewMergeFailed {
		t.Fatalf("task = %+v", task)
	}
}

func TestOnlyOneConcurrentMergeHappens(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()
	rev := f.approveMerge(f.start("s1"))
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = f.r.Merge(rev.ID) }()
	}
	wg.Wait()
	if n := gitT(t, f.repo, "rev-list", "--merges", "--count", "HEAD"); n != "1" {
		t.Fatalf("merge commits = %s", n)
	}
	if cur, _ := f.r.Get(rev.ID); cur.State != ReviewMerged {
		t.Fatalf("state = %s", cur.State)
	}
}

// A crash between claiming the merge and recording it: the next start settles
// it from what git actually holds.
func TestRecoverSettlesAnInterruptedMerge(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()
	rev := f.approveMerge(f.start("s1"))
	// Simulate: claimed, git merged, process died before recording.
	if _, err := f.s.db.Exec(`UPDATE reviews SET state=? WHERE id=?`, ReviewMerging, rev.ID); err != nil {
		t.Fatal(err)
	}
	gitT(t, f.repo, "merge", "--no-ff", "-m", "m", f.info.Branch)
	if n, err := f.r.Recover(); err != nil || n != 1 {
		t.Fatalf("recover = %d %v", n, err)
	}
	if cur, _ := f.r.Get(rev.ID); cur.State != ReviewMerged {
		t.Fatalf("state = %s", cur.State)
	}
	if task, _ := f.s.GetTask(f.task.ID); task.State != TaskDone {
		t.Fatalf("task = %+v", task)
	}

	// The other half: died before git did anything → failed, nothing merged.
	g := newReview(t)
	g.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	g.finish()
	rev = g.approveMerge(g.start("s1"))
	before := mainHead(t, g.repo)
	if _, err := g.s.db.Exec(`UPDATE reviews SET state=? WHERE id=?`, ReviewMerging, rev.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := g.r.Recover(); err != nil || n != 1 {
		t.Fatalf("recover = %d %v", n, err)
	}
	if cur, _ := g.r.Get(rev.ID); cur.State != ReviewMergeFailed || mainHead(t, g.repo) != before {
		t.Fatalf("state = %s", cur.State)
	}
}

// Optional second Agent: a read-only research child of the task with the
// evidence in its brief; it gates nothing by itself.
func TestOptionalAgentReviewIsAReadOnlyChildTask(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()
	rev := f.start("s1")
	rev, child, _, err := f.r.RequestAgentReview("ar", rev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if child.Kind != KindResearch || child.ParentTaskID != f.task.ID || child.RepoPath != f.info.Path || rev.AgentReviewTaskID != child.ID {
		t.Fatalf("child = %+v review = %+v", child, rev)
	}
	if !strings.Contains(child.Brief, rev.Head[:12]) {
		t.Fatalf("the brief must name the head: %q", child.Brief)
	}
}

// A reviewer Agent that fails to start fails only its own child task: the
// reviewed task, the review and its evidence stay exactly as they were.
// (Shepherdr core-failed-starts: "a reviewer that fails to start … leaves the
// task alone".)
func TestAFailedReviewerLeavesTheReviewedTaskAlone(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()
	rev := f.start("s1")
	rev, child, _, err := f.r.RequestAgentReview("ar", rev.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := f.s.GetTask(f.task.ID)
	run, _, err := f.s.ClaimTask(child.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.FinishRun(run.ID, RunFailed, "reviewer could not start"); err != nil {
		t.Fatal(err)
	}
	gotChild, _ := f.s.GetTask(child.ID)
	if gotChild.State != TaskFailed || !gotChild.NeedsYou {
		t.Fatalf("the reviewer's own task = %+v", gotChild)
	}
	parent, _ := f.s.GetTask(f.task.ID)
	if parent.State != before.State || parent.ReviewState != before.ReviewState || parent.NeedsYou != before.NeedsYou || parent.Revision != before.Revision {
		t.Fatalf("the reviewed task moved: %+v", parent)
	}
	if again, err := f.r.Get(rev.ID); err != nil || again.State != ReviewReady || again.Head != rev.Head {
		t.Fatalf("the review changed: %+v %v", again, err)
	}
}

// The person's own files in the main checkout survive a merge that would add
// the same path — whether the file is untracked or even ignored (git itself
// overwrites an ignored file during a merge). The merge is refused and the
// bytes are untouched. (Shepherdr core-tasks: stray / ignored file protection.)
func TestAMergeNeverOverwritesTheMainCheckoutsUntrackedOrIgnoredFiles(t *testing.T) {
	for _, ignored := range []bool{false, true} {
		name := "untracked"
		if ignored {
			name = "ignored"
		}
		t.Run(name, func(t *testing.T) {
			f := newReview(t)
			f.agentWork(map[string]string{"notes.txt": "the agent's version\n"}, "adds notes")
			f.finish()
			rev := f.approveMerge(f.start("s1"))
			if ignored {
				ex := filepath.Join(f.repo, ".git", "info", "exclude")
				if err := os.WriteFile(ex, []byte("notes.txt\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			mine := filepath.Join(f.repo, "notes.txt")
			if err := os.WriteFile(mine, []byte("MY OWN WORK\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := mainHead(t, f.repo)
			got, err := f.r.Merge(rev.ID)
			if err == nil || got.State == ReviewMerged {
				t.Fatalf("a merge that would overwrite the person's file went through: %+v %v", got, err)
			}
			if raw, _ := os.ReadFile(mine); string(raw) != "MY OWN WORK\n" {
				t.Fatalf("the person's file was overwritten: %q", raw)
			}
			if mainHead(t, f.repo) != before {
				t.Fatal("main moved")
			}
		})
	}
}
