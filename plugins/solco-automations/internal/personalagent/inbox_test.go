package personalagent

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

type tickClock struct{ t time.Time }

func (c *tickClock) now() time.Time { return c.t }

func inboxKinds(in Inbox) map[string][]InboxItem {
	out := map[string][]InboxItem{}
	for _, it := range in.Items {
		out[it.Kind] = append(out[it.Kind], it)
	}
	return out
}

// E04: one DB snapshot answers "what needs me", with deterministic detection
// (no LLM, no polling Herdr per minute) of stalls and failed starts.
func TestInboxIsOneSnapshotOfWhatNeedsAPerson(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_800_000_000, 0)}
	s, _ := openTest(t, WithClock(clock.now))
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "a2", "Bob")

	// 1. A failed start: nothing was ever created in Herdr.
	failed := mkTask(t, s, "t-fail", a.ID, "fails to start")
	r, _, _ := s.ClaimTask(failed.ID, "d")
	_ = s.MarkStarting(r.ID)
	_ = s.FinishRun(r.ID, RunFailed, "could not start: herdr down")

	// 2. A run that failed after its pane existed is a plain "needs you", not a failed start.
	broke := mkTask(t, s, "t-broke", a.ID, "broke while working")
	r2, _, _ := s.ClaimTask(broke.ID, "d")
	_ = s.MarkStarting(r2.ID)
	_ = s.RecordLaunch(r2.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"})
	_ = s.FinishRun(r2.ID, RunFailed, "agent exited 1")

	// 3. Stalled: claimed long ago and never started; blocked on a dialog nobody answered.
	hung := mkTask(t, s, "t-hung", a.ID, "never started")
	_, _, _ = s.ClaimTask(hung.ID, "d")
	blocked := mkTask(t, s, "t-blocked", a.ID, "blocked on a dialog")
	r4, _, _ := s.ClaimTask(blocked.ID, "d")
	_ = s.MarkStarting(r4.ID)
	_ = s.RecordLaunch(r4.ID, Session{PaneID: "w1:p2"})
	_ = s.BlockRun(r4.ID, "trust prompt")
	fresh := mkTask(t, s, "t-fresh", a.ID, "running too long")
	r5, _, _ := s.ClaimTask(fresh.ID, "d")
	_ = s.MarkStarting(r5.ID)
	_ = s.RecordLaunch(r5.ID, Session{PaneID: "w1:p3"})

	// 4. A pending approval, and a finished task that wants acknowledging.
	if _, _, err := s.RequestApproval("ap", RequestApprovalInput{AssistantID: a.ID, SubjectKind: "task", SubjectID: failed.ID, Action: "retry it", TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	// 5. Another Assistant's trouble never shows in Ada's inbox.
	other := mkTask(t, s, "t-other", b.ID, "bob's task")
	r6, _, _ := s.ClaimTask(other.ID, "d")
	_ = s.MarkStarting(r6.ID)
	_ = s.FinishRun(r6.ID, RunFailed, "no worktree")

	clock.t = clock.t.Add(2 * time.Hour)
	in, err := s.Inbox(a.ID, InboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	by := inboxKinds(in)
	if len(by["failed_start"]) != 1 || by["failed_start"][0].TaskID != failed.ID {
		t.Fatalf("failed_start = %+v", by["failed_start"])
	}
	if len(by["needs_you"]) != 1 || by["needs_you"][0].TaskID != broke.ID {
		t.Fatalf("needs_you = %+v", by["needs_you"])
	}
	stalled := map[string]bool{}
	for _, it := range by["stalled"] {
		stalled[it.TaskID] = true
	}
	// Two hours on: the unstarted claim, the unanswered dialog and the Agent
	// that has been "running" past the limit are all stalled.
	if !stalled[hung.ID] || !stalled[blocked.ID] || !stalled[fresh.ID] || len(stalled) != 3 {
		t.Fatalf("stalled = %v", stalled)
	}
	if len(by["approval"]) != 1 {
		t.Fatalf("approval = %+v", by["approval"])
	}
	for _, it := range in.Items {
		if it.AssistantID != a.ID {
			t.Fatalf("another assistant's item leaked: %+v", it)
		}
	}
	// Highest priority first.
	for i := 1; i < len(in.Items); i++ {
		if in.Items[i-1].Priority < in.Items[i].Priority {
			t.Fatalf("not sorted by priority: %+v", in.Items)
		}
	}
	if in.NeedsYou != len(in.Items)-len(by["news"]) {
		t.Fatalf("needsYou = %d of %d items", in.NeedsYou, len(in.Items))
	}
	// The same snapshot twice is the same answer (no hidden state).
	again, _ := s.Inbox(a.ID, InboxOptions{})
	x, _ := json.Marshal(in)
	y, _ := json.Marshal(again)
	if !bytes.Equal(x, y) {
		t.Fatalf("snapshot drifted:\n%s\n%s", x, y)
	}
}

func TestInboxStallsNeedTime(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_800_000_000, 0)}
	s, _ := openTest(t, WithClock(clock.now))
	a := mustAssistant(t, s, "a1", "Ada")
	task := mkTask(t, s, "t", a.ID, "slow start")
	_, _, _ = s.ClaimTask(task.ID, "d")
	if in, _ := s.Inbox(a.ID, InboxOptions{}); len(in.Items) != 0 {
		t.Fatalf("a task claimed a moment ago is not stalled: %+v", in.Items)
	}
	clock.t = clock.t.Add(2 * time.Minute)
	if in, _ := s.Inbox(a.ID, InboxOptions{}); len(in.Items) != 0 {
		t.Fatalf("still within the start window: %+v", in.Items)
	}
	clock.t = clock.t.Add(10 * time.Minute)
	in, _ := s.Inbox(a.ID, InboxOptions{})
	if len(in.Items) != 1 || in.Items[0].Kind != "stalled" {
		t.Fatalf("a start that hangs is stalled: %+v", in.Items)
	}
	// Tunable, for tests and for a slower machine.
	if in, _ := s.Inbox(a.ID, InboxOptions{StartStall: time.Hour}); len(in.Items) != 0 {
		t.Fatalf("StartStall ignored: %+v", in.Items)
	}
}

func TestInboxReviewAndNews(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	task, _, _ := s.CreateTask("c", CreateTaskInput{AssistantID: a.ID, Title: "add feature", Brief: "b", Kind: KindCoding, RepoPath: t.TempDir()})
	r, _, _ := s.ClaimTask(task.ID, "d")
	_ = s.MarkStarting(r.ID)
	_ = s.RecordLaunch(r.ID, Session{PaneID: "w1:p1"})
	_ = s.FinishRun(r.ID, RunDone, "done")
	in, _ := s.Inbox(a.ID, InboxOptions{})
	by := inboxKinds(in)
	if len(by["review"]) != 1 || by["review"][0].TaskID != task.ID {
		t.Fatalf("a coding task waiting for review = %+v", in.Items)
	}
	// A finished research task is news, never needs-you.
	res := mkTask(t, s, "res", a.ID, "research done")
	r2, _, _ := s.ClaimTask(res.ID, "d")
	_ = s.MarkStarting(r2.ID)
	_ = s.RecordLaunch(r2.ID, Session{PaneID: "w1:p2"})
	_ = s.FinishRun(r2.ID, RunDone, "findings")
	in, _ = s.Inbox(a.ID, InboxOptions{})
	by = inboxKinds(in)
	if len(by["needs_you"]) != 0 || len(in.Items) != 1 {
		t.Fatalf("a finished research task asks nothing of the person: %+v", in.Items)
	}
	if in.Seq == 0 {
		t.Fatal("the snapshot must carry the event watermark")
	}
	news, _ := s.Inbox(a.ID, InboxOptions{Since: 0, News: true})
	if len(inboxKinds(news)["news"]) == 0 {
		t.Fatalf("news requested but none: %+v", news.Items)
	}
	none, _ := s.Inbox(a.ID, InboxOptions{Since: in.Seq, News: true})
	if len(inboxKinds(none)["news"]) != 0 {
		t.Fatalf("news after the watermark: %+v", none.Items)
	}
}

// SOLCO-E04: nothing an Assistant is told or logs carries a prompt, a brief, a
// secret or a private PR body.
func TestNoPromptOrSecretLeaksIntoInboxEventsOrLogs(t *testing.T) {
	const token = "sk-live-1234567890abcdef"
	const brief = "summarise the private PR body mentioning " + token
	for name, herdrSays := range map[string]string{
		"the whole brief echoed": "herdr said: " + brief,
		"a bare token":           "401 for key " + token,
		"a bearer header":        "Authorization: Bearer abcdefghijklmnop1234567890",
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			log.SetOutput(&logs)
			defer log.SetOutput(os.Stderr)

			s, _ := openTest(t)
			a := mustAssistant(t, s, "a1", "Ada")
			task, _, err := s.CreateTask("t", CreateTaskInput{AssistantID: a.ID, Title: "tidy up", Brief: brief, Kind: KindResearch})
			if err != nil {
				t.Fatal(err)
			}
			d := &Dispatcher{Store: s, Launcher: &fakeLauncher{provisionErr: errString(herdrSays)}, Claimant: "d"}
			d.Pump(t.Context())
			d.Wait()

			in, err := s.Inbox(a.ID, InboxOptions{News: true})
			if err != nil {
				t.Fatal(err)
			}
			events, _ := s.Events(a.ID, 0, 100)
			runs, _ := s.ListRuns(task.ID)
			wire, _ := json.Marshal(map[string]any{"inbox": in, "events": events, "runs": runs})
			for what, blob := range map[string]string{"snapshot": string(wire), "log": logs.String()} {
				for _, bad := range []string{token, brief, "abcdefghijklmnop1234567890"} {
					if strings.Contains(blob, bad) {
						t.Fatalf("%s leaks %q:\n%s", what, bad, blob)
					}
				}
			}
			if len(in.Items) == 0 {
				t.Fatal("the failed start must show up in the inbox")
			}
			if logs.Len() == 0 {
				t.Fatal("a failed run must still leave a diagnostic line")
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestInboxWatcherSaysOnlyWhatIsNewAndNothingOnFirstLook(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	old := mkTask(t, s, "old", a.ID, "old failure")
	r, _, _ := s.ClaimTask(old.ID, "d")
	_ = s.MarkStarting(r.ID)
	_ = s.FinishRun(r.ID, RunFailed, "x")
	w := &InboxWatcher{Store: s}
	if fresh, err := w.Tick(); err != nil || len(fresh) != 0 {
		t.Fatalf("first look must prime silently: %+v %v", fresh, err)
	}
	if fresh, _ := w.Tick(); len(fresh) != 0 {
		t.Fatalf("nothing changed: %+v", fresh)
	}
	next := mkTask(t, s, "next", a.ID, "next failure")
	r2, _, _ := s.ClaimTask(next.ID, "d")
	_ = s.MarkStarting(r2.ID)
	_ = s.FinishRun(r2.ID, RunFailed, "y")
	fresh, _ := w.Tick()
	if len(fresh) != 1 || fresh[0].TaskID != next.ID || fresh[0].Kind != InboxFailedStart {
		t.Fatalf("fresh = %+v", fresh)
	}
	if again, _ := w.Tick(); len(again) != 0 {
		t.Fatalf("an item is announced once: %+v", again)
	}
}

// F4: a Goal the Coordinator planned but nobody started is waiting for a
// person, so it is in the inbox until they start or cancel it; an unplanned
// draft is still being written and an active goal is not waiting.
func TestInboxCarriesAPlannedDraftGoalUntilItIsStarted(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	if _, _, err := s.CreateGoal("g-bare", CreateGoalInput{AssistantID: a.ID, Title: "Still being written"}); err != nil {
		t.Fatal(err)
	}
	planned, _, _ := s.CreateGoal("g-plan", CreateGoalInput{AssistantID: a.ID, Title: "Ship the report"})
	if _, _, err := s.SetGoalPlan("p1", planned.ID, twoStepPlan(), "first", 0); err != nil {
		t.Fatal(err)
	}
	in, err := s.Inbox(a.ID, InboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	items := inboxKinds(in)[InboxNeedsYou]
	if len(items) != 1 || items[0].GoalID != planned.ID || !strings.Contains(items[0].Detail, "start") || in.NeedsYou != 1 {
		t.Fatalf("a planned draft goal should be the one needs-you item: %+v", in)
	}
	if _, _, err := s.ApproveGoal("ap", planned.ID, 1); err != nil {
		t.Fatal(err)
	}
	if in, _ = s.Inbox(a.ID, InboxOptions{}); in.NeedsYou != 0 {
		t.Fatalf("a started goal is not waiting for anyone: %+v", in)
	}
}
