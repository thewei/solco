package personalagent

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func beater(s *Store, clock *time.Time) *Beater {
	return &Beater{Store: s, Now: func() time.Time { return *clock }}
}

func setBeat(t *testing.T, s *Store, a Assistant, in HeartbeatInput) Heartbeat {
	t.Helper()
	in.AssistantID = a.ID
	cur, _ := s.GetHeartbeat(a.ID)
	h, _, err := s.SetHeartbeat("hb-"+time.Now().Format("150405.000000000"), in, cur.Revision)
	if err != nil {
		t.Fatalf("set heartbeat: %v", err)
	}
	return h
}

// approveWake walks the approval card for a heartbeat's model wakes.
func approveWake(t *testing.T, s *Store, h Heartbeat) Heartbeat {
	t.Helper()
	ap, _, err := s.RequestApproval("ap-"+time.Now().Format("150405.000000000"), RequestApprovalInput{
		AssistantID: h.AssistantID, SubjectKind: "heartbeat", SubjectID: h.AssistantID, Action: "wake the model on the heartbeat"})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, _, err := s.DecideApproval("d-"+ap.ID, ap.ID, ap.ActionHash, true); err != nil {
		t.Fatalf("decide: %v", err)
	}
	h2, _ := s.GetHeartbeat(h.AssistantID)
	return h2
}

func taskTitles(t *testing.T, s *Store, a Assistant, title string) []Task {
	t.Helper()
	all, _ := s.ListTasks(a.ID)
	out := []Task{}
	for _, x := range all {
		if x.Title == title {
			out = append(out, x)
		}
	}
	return out
}

// C02: the default heartbeat is deterministic. It runs on its time rule,
// remembers when, and wakes no model: no task, no Agent.
func TestHeartbeatDefaultsToNoModelWake(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	now := time.Unix(1_800_000_000, 0)
	h := setBeat(t, s, a, HeartbeatInput{Cron: "*/10 * * * *", Checklist: "- check the board"})
	if h.WakeBudget != 0 || h.WakeApproved {
		t.Fatalf("defaults: %+v", h)
	}
	b := beater(s, &now)
	if r, err := b.Tick(); err != nil || r.Beats != 0 { // first sight is only the baseline
		t.Fatalf("baseline: %+v %v", r, err)
	}
	now = now.Add(11 * time.Minute)
	r, err := b.Tick()
	if err != nil || r.Beats != 1 || r.Woke != 0 {
		t.Fatalf("beat: %+v %v", r, err)
	}
	got, _ := s.GetHeartbeat(a.ID)
	if got.LastBeatAt != now.UnixMilli() {
		t.Fatalf("lastBeatAt = %d", got.LastBeatAt)
	}
	if tasks, _ := s.ListTasks(a.ID); len(tasks) != 0 {
		t.Fatalf("a deterministic beat made tasks: %+v", tasks)
	}
}

// W06 (heartbeat half): a long sleep is one beat, not a flood; a clock moved
// backwards does not freeze the rule either.
func TestHeartbeatAfterSleepAndClockRollback(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	now := time.Unix(1_800_000_000, 0)
	setBeat(t, s, a, HeartbeatInput{Cron: "*/5 * * * *"})
	b := beater(s, &now)
	_, _ = b.Tick()
	now = now.Add(10 * time.Hour)
	if r, _ := b.Tick(); r.Beats != 1 {
		t.Fatalf("after sleep: %+v", r)
	}
	if r, _ := b.Tick(); r.Beats != 0 {
		t.Fatalf("a second tick in the same instant beat again: %+v", r)
	}
	now = now.Add(-3 * time.Hour) // the clock was set back
	if r, _ := b.Tick(); r.Beats != 1 {
		t.Fatalf("after a clock rollback the rule must resume at once: %+v", r)
	}
	got, _ := s.GetHeartbeat(a.ID)
	if got.LastBeatAt != now.UnixMilli() {
		t.Fatalf("lastBeatAt should follow the clock: %d vs %d", got.LastBeatAt, now.UnixMilli())
	}
}

func TestHeartbeatInputIsValidated(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	bad := []HeartbeatInput{
		{AssistantID: a.ID, Cron: "every minute"},
		{AssistantID: a.ID, Cron: "* * * * *"}, // faster than every 5 minutes
		{AssistantID: a.ID, WakeBudget: -1},
		{AssistantID: a.ID, WakeBudget: 25},
		{AssistantID: a.ID, Events: map[string]string{"task.failed": "shout"}},
		{AssistantID: a.ID, Events: map[string]string{"nonsense": "wake"}},
		{AssistantID: a.ID, Checklist: strings.Repeat("x", 4097)},
		{AssistantID: "a_missing"},
	}
	for i, in := range bad {
		if _, _, err := s.SetHeartbeat("bad"+string(rune('a'+i)), in, 0); err == nil {
			t.Fatalf("case %d accepted: %+v", i, in)
		}
	}
	h := setBeat(t, s, a, HeartbeatInput{Cron: "*/5 * * * *"})
	if _, _, err := s.SetHeartbeat("stale", HeartbeatInput{AssistantID: a.ID, Cron: "*/10 * * * *"}, h.Revision+4); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale: %v", err)
	}
}

// C02: a model wake needs a budget AND a person's approval of those exact
// settings; changing them takes the approval away.
func TestModelWakeNeedsBudgetAndApproval(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	now := time.Unix(1_800_000_000, 0)
	h := setBeat(t, s, a, HeartbeatInput{Cron: "*/10 * * * *", Checklist: "- look around", WakeBudget: 2})
	b := beater(s, &now)
	_, _ = b.Tick()
	now = now.Add(11 * time.Minute)
	if r, _ := b.Tick(); r.Beats != 1 || r.Woke != 0 {
		t.Fatalf("an unapproved wake must not start: %+v", r)
	}
	h = approveWake(t, s, h)
	if !h.WakeApproved {
		t.Fatalf("approval did not stick: %+v", h)
	}
	for i := 0; i < 3; i++ {
		now = now.Add(11 * time.Minute)
		r, _ := b.Tick()
		if i < 2 && r.Woke != 1 {
			t.Fatalf("wake %d: %+v", i, r)
		}
		if i == 2 && r.Woke != 0 {
			t.Fatalf("budget of 2 exceeded: %+v", r)
		}
		// The woken task is done so only the budget stops the third.
		for _, tk := range taskTitles(t, s, a, "Heartbeat") {
			if tk.State == TaskQueued {
				_, run := claimAndRun(t, s, tk)
				_ = s.FinishRun(run.ID, RunDone, "nothing to do")
			}
		}
	}
	tasks := taskTitles(t, s, a, "Heartbeat")
	if len(tasks) != 2 {
		t.Fatalf("heartbeat tasks = %d", len(tasks))
	}
	if tasks[0].Kind != KindResearch || !strings.Contains(tasks[0].Brief, "- look around") || !strings.Contains(tasks[0].Brief, "read-only") {
		t.Fatalf("brief = %q", tasks[0].Brief)
	}
	// A new day, a new budget.
	now = now.Add(24 * time.Hour)
	if r, _ := b.Tick(); r.Woke != 1 {
		t.Fatalf("next day: %+v", r)
	}
	// Raising the budget withdraws the approval.
	cur, _ := s.GetHeartbeat(a.ID)
	h2, _, err := s.SetHeartbeat("raise", HeartbeatInput{AssistantID: a.ID, Cron: cur.Cron, Checklist: cur.Checklist, WakeBudget: 9}, cur.Revision)
	if err != nil || h2.WakeApproved {
		t.Fatalf("raise: %+v %v", h2, err)
	}
}

func claimAndRun(t *testing.T, s *Store, tk Task) (Task, Run) {
	t.Helper()
	run, _, err := s.ClaimTask(tk.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.MarkStarting(run.ID)
	_ = s.RecordLaunch(run.ID, Session{HerdrSession: "d", WorkspaceID: "w", PaneID: "w:p"})
	return tk, run
}

// C02: one heartbeat task at a time — a wake is not stacked on a running one.
func TestOneHeartbeatTaskAtATime(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	now := time.Unix(1_800_000_000, 0)
	h := setBeat(t, s, a, HeartbeatInput{Cron: "*/10 * * * *", WakeBudget: 5})
	approveWake(t, s, h)
	b := beater(s, &now)
	_, _ = b.Tick()
	now = now.Add(11 * time.Minute)
	if r, _ := b.Tick(); r.Woke != 1 {
		t.Fatalf("first wake: %+v", r)
	}
	now = now.Add(11 * time.Minute)
	if r, _ := b.Tick(); r.Woke != 0 {
		t.Fatalf("stacked a wake on an unfinished one: %+v", r)
	}
}

// C01+C02: event rules decide what an event does — wake, wait for the next
// beat, or nothing — and every event is judged once (watermark).
func TestEventRulesWakeHoldOrIgnore(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	now := time.Unix(1_800_000_000, 0)
	h := setBeat(t, s, a, HeartbeatInput{Cron: "0 9 * * *", WakeBudget: 3, Events: map[string]string{"task.failed": "wake", "task.blocked": "hold"}})
	approveWake(t, s, h)
	b := beater(s, &now)
	_, _ = b.Tick() // baseline mark: events before the heartbeat existed are not news

	_, blocked := runningTask(t, s, a, "t1", "Blocked one")
	_ = s.BlockRun(blocked.ID, "dialog")
	_, failed := runningTask(t, s, a, "t2", "Failing one")
	_ = s.FinishRun(failed.ID, RunFailed, "boom")

	r, err := b.Tick()
	if err != nil || r.Woke != 1 {
		t.Fatalf("tick: %+v %v", r, err)
	}
	got, _ := s.GetHeartbeat(a.ID)
	if len(got.Held) != 0 { // the wake consumed the held news
		t.Fatalf("held after wake = %v", got.Held)
	}
	tasks := taskTitles(t, s, a, "Heartbeat")
	if len(tasks) != 1 || !strings.Contains(tasks[0].Brief, "Blocked one") || !strings.Contains(tasks[0].Brief, "Failing one") {
		t.Fatalf("the wake should carry the held and the waking news: %+v", tasks)
	}
	// A second tick finds nothing new: each event is judged once.
	if r, _ := b.Tick(); r.Woke != 0 {
		t.Fatalf("an event was judged twice: %+v", r)
	}
	// `task.added` has no rule, so it is neither held nor woken.
	if g, _ := s.GetHeartbeat(a.ID); len(g.Held) != 0 {
		t.Fatalf("held = %v", g.Held)
	}
}

// C02: without an approved wake a waking event falls back to being held, so
// the news is not lost and still costs no model call.
func TestWakeEventWithoutApprovalIsHeld(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	now := time.Unix(1_800_000_000, 0)
	setBeat(t, s, a, HeartbeatInput{Cron: "0 9 * * *", WakeBudget: 3, Events: map[string]string{"task.failed": "wake"}})
	b := beater(s, &now)
	_, _ = b.Tick()
	_, failed := runningTask(t, s, a, "t2", "Failing one")
	_ = s.FinishRun(failed.ID, RunFailed, "boom")
	if r, _ := b.Tick(); r.Woke != 0 {
		t.Fatalf("woke without approval: %+v", r)
	}
	g, _ := s.GetHeartbeat(a.ID)
	if len(g.Held) != 1 || !strings.Contains(g.Held[0], "Failing one") {
		t.Fatalf("held = %v", g.Held)
	}
}

// A paused Assistant is not woken and its heartbeat does not run.
func TestPausedAssistantHasNoHeartbeat(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	now := time.Unix(1_800_000_000, 0)
	h := setBeat(t, s, a, HeartbeatInput{Cron: "*/10 * * * *", WakeBudget: 1})
	approveWake(t, s, h)
	b := beater(s, &now)
	_, _ = b.Tick()
	cur, _ := s.GetAssistant(a.ID)
	if _, _, err := s.SetAssistantStatus("pause", a.ID, StatusDisabled, cur.Revision); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if r, _ := b.Tick(); r.Beats != 0 || r.Woke != 0 {
		t.Fatalf("paused assistant beat: %+v", r)
	}
}

// Progress and completion carry evidence: progress notes are events on the
// goal, completion needs the evidence text (already enforced by the Goal).
func TestGoalProgressIsRecordedOnce(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g, _, err := s.CreateGoal("g1", CreateGoalInput{AssistantID: a.ID, Title: "Keep CI green"})
	if err != nil {
		t.Fatal(err)
	}
	if g, _, err = s.ApproveGoal("ap", g.ID, g.Revision); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddProgress("p1", g.ID, "Checked the PR: CI is green"); err != nil {
		t.Fatal(err)
	}
	if _, replay, err := s.AddProgress("p1", g.ID, "Checked the PR: CI is green"); err != nil || !replay {
		t.Fatalf("replay: %v %v", replay, err)
	}
	if _, _, err := s.AddProgress("p2", g.ID, "  "); err == nil {
		t.Fatal("an empty note is not progress")
	}
	evs, _ := s.Events(a.ID, 0, 50)
	n := 0
	for _, e := range evs {
		if e.Kind == "goal.progress" {
			n++
			if e.Detail != "Checked the PR: CI is green" || e.Resource != g.ID {
				t.Fatalf("event = %+v", e)
			}
		}
	}
	if n != 1 {
		t.Fatalf("progress events = %d", n)
	}
}

// The heartbeat's own task finishing or failing is an event, but never news to
// the heartbeat: otherwise each wake would cause the next (a wake loop).
// (Shepherdr core-automations: "suppress automation-added recursion".)
func TestAHeartbeatsOwnTaskNeverWakesItAgain(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	now := time.Unix(1_800_000_000, 0)
	h := setBeat(t, s, a, HeartbeatInput{Cron: "0 9 * * *", WakeBudget: 5, Events: map[string]string{"task.failed": "wake", "task.completed": "wake"}})
	approveWake(t, s, h)
	b := beater(s, &now)
	_, _ = b.Tick() // baseline
	_, failing := runningTask(t, s, a, "t1", "Failing one")
	_ = s.FinishRun(failing.ID, RunFailed, "boom")
	if r, err := b.Tick(); err != nil || r.Woke != 1 {
		t.Fatalf("first wake: %+v %v", r, err)
	}
	beat := taskTitles(t, s, a, "Heartbeat")
	if len(beat) != 1 {
		t.Fatalf("heartbeat tasks = %d", len(beat))
	}
	run, _, err := s.ClaimTask(beat[0].ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(run.ID, RunFailed, "the wake itself failed"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		now = now.Add(time.Minute)
		if r, err := b.Tick(); err != nil || r.Woke != 0 {
			t.Fatalf("the heartbeat's own failure woke it again: %+v %v", r, err)
		}
	}
	if got := taskTitles(t, s, a, "Heartbeat"); len(got) != 1 {
		t.Fatalf("heartbeat tasks = %d, want 1", len(got))
	}
}
