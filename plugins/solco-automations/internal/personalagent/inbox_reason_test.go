package personalagent

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/herdr"
)

func byTask(in Inbox, id string) (InboxItem, bool) {
	for _, it := range in.Items {
		if it.TaskID == id && it.Kind != InboxApproval {
			return it, true
		}
	}
	return InboxItem{}, false
}

// Q05: every card names why it is there (a stable code the page and the phone
// can branch on) and carries what it can do and where its run is, so a click
// goes back to the original Task/Run instead of a sentence.
func TestInboxCardsNameTheirReasonAndTheirWayBack(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_800_000_000, 0)}
	s, _ := openTest(t, WithClock(clock.now))
	a := mustAssistant(t, s, "a1", "Ada")

	failed := mkTask(t, s, "t-fail", a.ID, "fails to start")
	r, _, _ := s.ClaimTask(failed.ID, "d")
	_ = s.MarkStarting(r.ID)
	_ = s.FinishRun(r.ID, RunFailed, "could not start")

	broke := mkTask(t, s, "t-broke", a.ID, "broke while working")
	r2, _, _ := s.ClaimTask(broke.ID, "d")
	_ = s.MarkStarting(r2.ID)
	_ = s.RecordLaunch(r2.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"})
	_ = s.FinishRun(r2.ID, RunFailed, "agent exited 1")

	blocked := mkTask(t, s, "t-blocked", a.ID, "waiting on a dialog")
	r4, _, _ := s.ClaimTask(blocked.ID, "d")
	_ = s.MarkStarting(r4.ID)
	_ = s.RecordLaunch(r4.ID, Session{PaneID: "w1:p2"})
	_ = s.BlockRun(r4.ID, "trust prompt")

	if _, _, err := s.RequestApproval("ap", RequestApprovalInput{AssistantID: a.ID, SubjectKind: "task", SubjectID: failed.ID, Action: "retry it", TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	clock.t = clock.t.Add(2 * time.Hour)
	in, err := s.Inbox(a.ID, InboxOptions{})
	if err != nil {
		t.Fatal(err)
	}

	want := []struct {
		task    string
		reason  string
		actions []string
		pane    string
	}{
		{failed.ID, "start_failed", []string{"retry"}, ""},
		{broke.ID, "task_failed", []string{"open_pane", "retry"}, "w1:p1"},
		{blocked.ID, "permission_prompt", []string{"open_pane"}, "w1:p2"},
	}
	for _, w := range want {
		it, ok := byTask(in, w.task)
		if !ok {
			t.Fatalf("no card for %s: %+v", w.task, in.Items)
		}
		if it.Reason != w.reason || !slices.Equal(it.Actions, w.actions) || it.PaneID != w.pane || (w.pane != "" && it.RunID == "") {
			t.Fatalf("%s = %+v, want reason %s actions %v pane %q", w.task, it, w.reason, w.actions, w.pane)
		}
	}
	for _, it := range in.Items {
		if it.Kind == InboxApproval && (it.Reason != "approval_task" || !slices.Equal(it.Actions, []string{"decide"})) {
			t.Fatalf("approval card = %+v", it)
		}
	}
}

// Q05: a review whose checks failed is not "ready for your review": it says so
// and outranks the ordinary ones.
func TestInboxSeparatesAFailedReviewFromAReadyOne(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	f.finish()
	in, _ := f.s.Inbox(f.asst.ID, InboxOptions{})
	if it, ok := byTask(in, f.task.ID); !ok || it.Kind != InboxReview || it.Reason != "review_ready" || !slices.Equal(it.Actions, []string{"review"}) {
		t.Fatalf("before the evidence = %+v", it)
	}

	f.approveChecks("test -f missing.txt")
	rv := f.start("s1")
	if rv.State != ReviewChecksFailed {
		t.Fatalf("review = %+v", rv)
	}
	in, _ = f.s.Inbox(f.asst.ID, InboxOptions{})
	it, ok := byTask(in, f.task.ID)
	if !ok || it.Kind != InboxReview || it.Reason != "review_checks_failed" || it.Priority != 3 || it.Detail == "Ready for your review" {
		t.Fatalf("failed checks = %+v", it)
	}
}

// Q06: one thing, one push. A card that changes kind while it stays in the
// inbox (stalled, then failed) is the same item for the notifier; it speaks
// again only after it has gone away and come back.
func TestInboxWatcherPushesOneItemOnceWhileItChangesKind(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_800_000_000, 0)}
	s, _ := openTest(t, WithClock(clock.now))
	a := mustAssistant(t, s, "a1", "Ada")
	task := mkTask(t, s, "t-1", a.ID, "never started")
	r, _, _ := s.ClaimTask(task.ID, "d")
	_ = s.MarkStarting(r.ID)
	clock.t = clock.t.Add(2 * time.Hour)

	w := &InboxWatcher{Store: s}
	if fresh, err := w.Tick(); err != nil || len(fresh) != 0 {
		t.Fatalf("first look must be silent: %+v %v", fresh, err)
	}
	if it, _ := byTask(mustInbox(t, s, a.ID), task.ID); it.Kind != InboxStalled {
		t.Fatalf("setup: %+v", it)
	}
	_ = s.FinishRun(r.ID, RunFailed, "could not start")
	if it, _ := byTask(mustInbox(t, s, a.ID), task.ID); it.Kind != InboxFailedStart {
		t.Fatalf("setup: %+v", it)
	}
	if fresh, _ := w.Tick(); len(fresh) != 0 {
		t.Fatalf("the same card must not push twice: %+v", fresh)
	}
}

func mustInbox(t *testing.T, s *Store, id string) Inbox {
	t.Helper()
	in, err := s.Inbox(id, InboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// Q05 (user decision 2026-11-05): the one failure the dispatcher can prove is
// "Herdr gave no structured answer" — the CLI failed without an error code
// (socket gone, binary missing, killed). That gets its own reason; whatever an
// Agent printed inside its pane (token, network) is never classified.
func TestADispatcherFailureHerdrNeverAnsweredIsHerdrUnreachable(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		code string
	}{
		{"no envelope", &herdr.APIError{Command: "agent start", Message: "failed to connect to socket"}, "herdr_unreachable"},
		{"refused with a code", &herdr.APIError{Command: "agent start", Code: "workspace_not_found", Message: "x"}, ""},
		{"plain error", errors.New("worktree failed"), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			clock := &tickClock{t: time.Unix(1_800_000_000, 0)}
			s, _ := openTest(t, WithClock(clock.now))
			_, task := newQueuedTask(t, s, KindResearch)
			d := &Dispatcher{Store: s, Launcher: &fakeLauncher{provisionErr: c.err}, Claimant: "daemon"}
			if _, err := d.RunTask(t.Context(), task.ID); err == nil {
				t.Fatal("expected the failure to be reported")
			}
			in := mustInbox(t, s, task.AssistantID)
			it, ok := byTask(in, task.ID)
			if !ok || it.Kind != InboxFailedStart {
				t.Fatalf("card = %+v", it)
			}
			want := c.code
			if want == "" {
				want = "start_failed"
			}
			if it.Reason != want {
				t.Fatalf("reason = %q, want %q", it.Reason, want)
			}
			if c.code != "" && (it.Detail == "Could not start — nothing was created" || !slices.Equal(it.Actions, []string{"retry"})) {
				t.Fatalf("the unreachable card must say what to do: %+v", it)
			}
		})
	}
}
