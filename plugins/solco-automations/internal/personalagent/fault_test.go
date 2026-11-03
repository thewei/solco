package personalagent

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// C04 — fault injection. Every fault must end as a refusal or an explicit
// state, never as a lost fact, a duplicate effect or a silent retry of an
// external side effect. (A lost launch response, a daemon crash and a Herdr
// restart are pinned in dispatch_test.go and store_test.go:
// TestProvisionErrorAndUnknownOutcomeIsUncertain, TestDurableReceiptBeforeLaunch,
// TestResumeWithNoAgentInALiveWorkspaceIsUncertain, TestClaimTaskExactlyOnce.)

func lockDB(t *testing.T, path string) func() {
	t.Helper()
	other, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(50)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	other.SetMaxOpenConns(1)
	tx, err := other.Begin() // immediate: holds the write lock
	if err != nil {
		t.Fatal(err)
	}
	return func() { _ = tx.Rollback(); _ = other.Close() }
}

// A held lock is a bounded, explicit ErrBusy; nothing is written, and the same
// request succeeds exactly once after the lock is gone.
func TestDatabaseLockIsBoundedAndLeavesNothingBehind(t *testing.T) {
	s, path := openTest(t, WithBusyTimeout(50*time.Millisecond))
	a := mustAssistant(t, s, "a1", "Ada")
	setBeat(t, s, a, HeartbeatInput{Cron: "*/10 * * * *"})
	release := lockDB(t, path)
	start := time.Now()
	_, _, err := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "x", Brief: "y", Kind: KindResearch})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("the wait was not bounded: %v", time.Since(start))
	}
	now := time.Unix(1_800_000_000, 0)
	if _, err := (&Beater{Store: s, Now: func() time.Time { return now }}).Tick(); !errors.Is(err, ErrBusy) {
		t.Fatalf("heartbeat under a lock: %v", err)
	}
	release()
	if tasks, _ := s.ListTasks(a.ID); len(tasks) != 0 {
		t.Fatalf("a refused write left a task: %+v", tasks)
	}
	if _, replay, err := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "x", Brief: "y", Kind: KindResearch}); err != nil || replay {
		t.Fatalf("retry: replay=%v err=%v", replay, err)
	}
	if tasks, _ := s.ListTasks(a.ID); len(tasks) != 1 {
		t.Fatalf("tasks = %d", len(tasks))
	}
}

// An event that cannot be written takes its transition with it: a run is never
// finished without its event, and the retry produces exactly one.
func TestEventWriteFailureRollsTheTransitionBack(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	task, run := runningTask(t, s, a, "t1", "Check")
	before, _ := s.Events(a.ID, 0, 100)
	if _, err := s.db.Exec(`CREATE TRIGGER no_events BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(run.ID, RunDone, "ok"); err == nil {
		t.Fatal("finish succeeded although its event could not be written")
	}
	got, _ := s.GetRun(run.ID)
	tk, _ := s.GetTask(task.ID)
	if got.Status != RunRunning || tk.State != TaskRunning {
		t.Fatalf("half transition: run=%s task=%s", got.Status, tk.State)
	}
	if _, err := s.db.Exec(`DROP TRIGGER no_events`); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(run.ID, RunDone, "ok"); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Events(a.ID, 0, 100)
	if len(after) != len(before)+1 || after[len(after)-1].Kind != "task.completed" {
		t.Fatalf("events before=%d after=%d", len(before), len(after))
	}
}

// A heartbeat whose task cannot be written spends no budget and does not
// advance its clock: the next tick tries again.
func TestHeartbeatWakeIsAllOrNothing(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	now := time.Unix(1_800_000_000, 0)
	h := setBeat(t, s, a, HeartbeatInput{Cron: "*/10 * * * *", WakeBudget: 1})
	approveWake(t, s, h)
	b := beater(s, &now)
	_, _ = b.Tick()
	now = now.Add(11 * time.Minute)
	if _, err := s.db.Exec(`CREATE TRIGGER no_tasks BEFORE INSERT ON tasks BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Tick(); err == nil {
		t.Fatal("expected the failed wake to surface")
	}
	got, _ := s.GetHeartbeat(a.ID)
	if got.WakesToday != 0 || got.LastBeatAt == now.UnixMilli() {
		t.Fatalf("a failed wake spent its budget or moved its clock: %+v", got)
	}
	_, _ = s.db.Exec(`DROP TRIGGER no_tasks`)
	if r, err := b.Tick(); err != nil || r.Woke != 1 {
		t.Fatalf("retry: %+v %v", r, err)
	}
}

// Two phones answer the same card at once: one answer wins, the other is told
// the card is already decided, and the stored decision is the winner's.
func TestTwoPhonesDecidingTheSameApprovalOneWins(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	task, _, _ := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "x", Brief: "y", Kind: KindResearch})
	ap, _, err := s.RequestApproval("ap", RequestApprovalInput{AssistantID: a.ID, SubjectKind: "task", SubjectID: task.ID, Action: "run it"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]error, 2)
	states := make([]string, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _, err := s.DecideApproval("phone-"+string(rune('a'+i)), ap.ID, ap.ActionHash, i == 0)
			results[i], states[i] = err, got.State
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrBadState) {
			t.Fatalf("loser got %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("results = %v", results)
	}
	final, _ := s.GetApproval(ap.ID)
	want := ApprovalDenied
	if results[0] == nil {
		want = ApprovalApproved
	}
	if final.State != want {
		t.Fatalf("stored %s, winner wanted %s", final.State, want)
	}
}

// A phone that retries after a dropped connection (same requestId) gets its
// first answer back, not a second decision or an "already decided" refusal.
func TestApprovalRetryAfterReconnectIsIdempotent(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	task, _, _ := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "x", Brief: "y", Kind: KindResearch})
	ap, _, _ := s.RequestApproval("ap", RequestApprovalInput{AssistantID: a.ID, SubjectKind: "task", SubjectID: task.ID, Action: "run it"})
	first, replay, err := s.DecideApproval("retry-1", ap.ID, ap.ActionHash, true)
	if err != nil || replay {
		t.Fatal(err)
	}
	again, replay, err := s.DecideApproval("retry-1", ap.ID, ap.ActionHash, true)
	if err != nil || !replay || again.State != first.State || again.DecidedAt != first.DecidedAt {
		t.Fatalf("replay: %+v %v %v", again, replay, err)
	}
}

// A reader holding the database (an open read transaction in another
// connection) never blocks a write: WAL lets the write commit through it.
// (Shepherdr core-database-lock: "a write commits through a reader".)
func TestAWriteCommitsThroughAReaderStillHoldingTheDatabase(t *testing.T) {
	s, path := openTest(t, WithBusyTimeout(100*time.Millisecond))
	mustAssistant(t, s, "a1", "Ada")
	reader, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(50)")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.SetMaxOpenConns(1)
	tx, err := reader.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM assistants`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("reader sees %d, %v", n, err)
	}
	if _, _, err := s.CreateAssistant("a2", CreateAssistantInput{Name: "Bob"}); err != nil {
		t.Fatalf("a write must commit through a reader: %v", err)
	}
}

// Rules due while the database is locked: the whole tick waits once (bounded,
// not once per rule), fails with ErrBusy and writes nothing, so every rule is
// still due, none is left half-claimed, and the next tick after the lock fires
// each of them exactly once. (Shepherdr: "a due automation whose claim fails
// stays due"; "due automations under a lasting reader share one wait".)
func TestRulesDueUnderALockShareOneBoundedWaitAndStayDue(t *testing.T) {
	s, path := openTest(t, WithBusyTimeout(150*time.Millisecond))
	cfg := &config.Config{Automations: []config.Automation{
		rule("a", "*/10 * * * *"), rule("b", "*/10 * * * *"), rule("c", "*/10 * * * *"),
	}}
	clock := &clockT{t: at(10, 0)}
	for _, n := range []string{"a", "b", "c"} {
		adopt(t, s, cfg, n, map[string]time.Time{n: at(9, 50)}, clock.t)
	}
	clock.t = at(10, 10)
	sched := ruleSched(s, cfg, clock)
	release := lockDB(t, path)
	began := time.Now()
	_, err := sched.Tick()
	took := time.Since(began)
	release()
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("tick under a lock = %v, want ErrBusy", err)
	}
	if took > 3*150*time.Millisecond {
		t.Fatalf("three due rules waited %v: one wait per rule, not one in all", took)
	}
	for _, n := range []string{"a", "b", "c"} {
		if got := ruleTasks(t, s, n); len(got) != 0 {
			t.Fatalf("%s left %d task(s) from a refused tick", n, len(got))
		}
	}
	rep, err := sched.Tick()
	if err != nil || rep.Fired != 3 {
		t.Fatalf("after the lock: fired=%d err=%v", rep.Fired, err)
	}
	if rep, err := sched.Tick(); err != nil || rep.Fired != 0 {
		t.Fatalf("the same occurrence fired twice: %+v %v", rep, err)
	}
	for _, n := range []string{"a", "b", "c"} {
		if got := ruleTasks(t, s, n); len(got) != 1 {
			t.Fatalf("%s tasks = %d, want exactly 1", n, len(got))
		}
	}
}
