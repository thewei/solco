package personalagent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// idleRig is one Assistant whose Coordinator has been ready in pane w1:p1 for
// two hours, with a transcript last written an hour ago, behind a sleeper that
// waits thirty minutes.
type idleRig struct {
	t       *testing.T
	s       *Store
	a       Assistant
	z       *IdleSleeper
	closed  []string
	closeIn error
	prompt  bool
	active  time.Time
}

func newIdleRig(t *testing.T) *idleRig {
	t.Helper()
	base := time.Unix(1_800_000_000, 0)
	s, _ := openTest(t, WithClock(func() time.Time { return base }))
	r := &idleRig{t: t, s: s, a: mustAssistant(t, s, "idle-a", "Ada"), prompt: true, active: base.Add(-time.Hour)}
	c, _, err := s.StartCoordinatorAs("idle-start", r.a.ID, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE coordinators SET state=?, herdr_session='s', workspace_id='w1', pane_id='w1:p1', updated_at=? WHERE assistant_id=?`,
		CoordinatorReady, base.Add(-2*time.Hour).UnixMilli(), c.AssistantID); err != nil {
		t.Fatal(err)
	}
	r.z = &IdleSleeper{
		Store:      s,
		After:      30 * time.Minute,
		LastActive: func(string) time.Time { return r.active },
		AtPrompt:   func(string, string) bool { return r.prompt },
		Close: func(session, workspace, pane string) error {
			r.closed = append(r.closed, session+"/"+workspace+"/"+pane)
			return r.closeIn
		},
	}
	return r
}

func (r *idleRig) state() string {
	r.t.Helper()
	c, err := r.s.GetCoordinator(r.a.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	return c.State
}

func (r *idleRig) tick() IdleResult {
	r.t.Helper()
	res, err := r.z.Tick()
	if err != nil {
		r.t.Fatal(err)
	}
	return res
}

func (r *idleRig) exec(query string, args ...any) {
	r.t.Helper()
	if _, err := r.s.db.Exec(query, args...); err != nil {
		r.t.Fatal(err)
	}
}

// A Coordinator that has been quiet and at its prompt for longer than the limit
// is stopped and its pane closed, once.
func TestIdleCoordinatorGoesToSleep(t *testing.T) {
	r := newIdleRig(t)
	if res := r.tick(); res.Slept != 1 || res.CloseFailed != 0 {
		t.Fatalf("result = %+v", res)
	}
	if r.state() != CoordinatorStopped {
		t.Fatalf("state = %s", r.state())
	}
	if len(r.closed) != 1 || r.closed[0] != "s/w1/w1:p1" {
		t.Fatalf("closed = %v", r.closed)
	}
	if res := r.tick(); res.Slept != 0 || len(r.closed) != 1 {
		t.Fatalf("a stopped Coordinator is left alone: %+v closed=%v", res, r.closed)
	}
}

// A pane Herdr would not close is counted, never retried blindly: the
// Coordinator is already stopped and the next start asks Herdr for proof.
func TestIdleSleepCountsAPaneThatWontClose(t *testing.T) {
	r := newIdleRig(t)
	r.closeIn = errors.New("herdr says no")
	if res := r.tick(); res.Slept != 1 || res.CloseFailed != 1 {
		t.Fatalf("result = %+v", res)
	}
	if r.state() != CoordinatorStopped {
		t.Fatalf("state = %s", r.state())
	}
}

func TestIdleSleepWaitsForQuiet(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*idleRig)
	}{
		{"conversation written recently", func(r *idleRig) { r.active = time.Unix(1_800_000_000, 0).Add(-time.Minute) }},
		{"conversation time unknown", func(r *idleRig) { r.active = time.Time{} }},
		{"agent not at its prompt", func(r *idleRig) { r.prompt = false }},
		{"coordinator became ready a moment ago", func(r *idleRig) {
			r.exec(`UPDATE coordinators SET updated_at=?`, time.Unix(1_800_000_000, 0).Add(-time.Minute).UnixMilli())
		}},
		{"a task is running", func(r *idleRig) {
			task, _, err := r.s.CreateTask("idle-task", CreateTaskInput{AssistantID: r.a.ID, Kind: KindResearch, Title: "t", Brief: "b"})
			if err != nil {
				r.t.Fatal(err)
			}
			r.exec(`UPDATE tasks SET state=? WHERE id=?`, TaskRunning, task.ID)
		}},
		{"an approval waits for the person", func(r *idleRig) {
			r.exec(`INSERT INTO approvals(id,assistant_id,subject_kind,subject_id,action,action_hash,expected_revision,expires_at,state,created_at) VALUES('ap1',?,?,?,?,?,?,?,?,?)`,
				r.a.ID, "task", "t1", "do it", "hash", 1, int64(1_900_000_000_000), ApprovalPending, int64(1_700_000_000_000))
		}},
		{"a goal is active", func(r *idleRig) {
			g, _, err := r.s.CreateGoal("idle-goal", CreateGoalInput{AssistantID: r.a.ID, Title: "g"})
			if err != nil {
				r.t.Fatal(err)
			}
			r.exec(`UPDATE goals SET state=? WHERE id=?`, GoalActive, g.ID)
		}},
		{"an approved wake exists", func(r *idleRig) {
			r.exec(`INSERT INTO heartbeats(assistant_id,wake_approved) VALUES(?,1)`, r.a.ID)
		}},
		{"periodic reports are on", func(r *idleRig) {
			if _, _, err := r.s.SetProactivity("idle-pro", ProactivityInput{AssistantID: r.a.ID, Level: "follow_up", ReportEveryMin: 60, DailyBudget: 10}, 0); err != nil {
				r.t.Fatal(err)
			}
		}},
		{"a report is queued", func(r *idleRig) {
			r.exec(`INSERT INTO coordinator_reports(assistant_id,state) VALUES(?,?)`, r.a.ID, ReportQueued)
		}},
		{"a report awaits its reply", func(r *idleRig) {
			r.exec(`INSERT INTO coordinator_reports(assistant_id,state,reply_state) VALUES(?,?,?)`, r.a.ID, ReportSubmitted, ReplyAwaiting)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newIdleRig(t)
			c.setup(r)
			if res := r.tick(); res.Slept != 0 {
				t.Fatalf("slept anyway: %+v", res)
			}
			if r.state() != CoordinatorReady || len(r.closed) != 0 {
				t.Fatalf("state=%s closed=%v", r.state(), r.closed)
			}
		})
	}
}

func TestIdleSleepIsOffWithoutALimit(t *testing.T) {
	r := newIdleRig(t)
	r.z.After = 0
	if res := r.tick(); res.Slept != 0 || r.state() != CoordinatorReady {
		t.Fatalf("result = %+v state=%s", res, r.state())
	}
}

// Finished work does not keep a Coordinator awake.
func TestIdleSleepIgnoresFinishedWork(t *testing.T) {
	r := newIdleRig(t)
	task, _, err := r.s.CreateTask("idle-done", CreateTaskInput{AssistantID: r.a.ID, Kind: KindResearch, Title: "t", Brief: "b"})
	if err != nil {
		t.Fatal(err)
	}
	r.exec(`UPDATE tasks SET state=? WHERE id=?`, TaskDone, task.ID)
	if res := r.tick(); res.Slept != 1 {
		t.Fatalf("result = %+v", res)
	}
}

func TestIdleSleepAfterReadsTheEnvironment(t *testing.T) {
	for raw, want := range map[string]time.Duration{"": DefaultIdleSleep, "45": 45 * time.Minute, "0": 0, "-5": 0, "soon": DefaultIdleSleep} {
		t.Setenv("SOLCO_ASSISTANTS_IDLE_SLEEP_MIN", raw)
		if got := IdleSleepAfter(); got != want {
			t.Errorf("%q: %v, want %v", raw, got, want)
		}
	}
}

// The conversation's own file says when it was last written; no file, no answer.
func TestCoordinatorLastActiveIsTheTranscriptsWriteTime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const id = "a_0123456789abcdef"
	if got := CoordinatorLastActive(id); !got.IsZero() {
		t.Fatalf("no transcript yet, got %v", got)
	}
	dir := filepath.Join(home, ".omp", "agent", "sessions", "--tmp-personal-coordinators-"+id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(file, at, at); err != nil {
		t.Fatal(err)
	}
	if got := CoordinatorLastActive(id); !got.Equal(at) {
		t.Fatalf("got %v, want %v", got, at)
	}
}

// Sleep leaves its reason on the Coordinator (so a chat that opens warms it up),
// a person's own Stop does not, and a new start clears it.
func TestIdleSleepRecordsItsReason(t *testing.T) {
	r := newIdleRig(t)
	r.tick()
	c, err := r.s.GetCoordinator(r.a.ID)
	if err != nil || c.State != CoordinatorStopped || c.StopReason != StopReasonIdle {
		t.Fatalf("slept = %+v %v", c, err)
	}

	again := newIdleRig(t)
	if c, _, err := again.s.StopCoordinator("person-stop", again.a.ID); err != nil || c.State != CoordinatorStopped || c.StopReason != "" {
		t.Fatalf("a person's stop = %+v %v", c, err)
	}

	restarted, _, err := r.s.StartCoordinatorAs("wake", r.a.ID, "", "", func(string, string, string) (bool, error) { return true, nil })
	if err != nil || restarted.State != CoordinatorStarting || restarted.StopReason != "" {
		t.Fatalf("restart = %+v %v", restarted, err)
	}
}

// An Assistant's own idle-sleep minutes beat the daemon's default, and 0 means never.
func TestIdleSleepFollowsTheAssistantsOwnChoice(t *testing.T) {
	r := newIdleRig(t) // transcript quiet for an hour, default 30 minutes
	if _, _, err := r.s.SetRuntime("idle-never", r.a.ID, "", "", "", 0, 0); err != nil {
		t.Fatal(err)
	}
	if res := r.tick(); res.Slept != 0 || r.state() != CoordinatorReady {
		t.Fatalf("never: %+v state=%s", res, r.state())
	}
	if _, _, err := r.s.SetRuntime("idle-90", r.a.ID, "", "", "", 1, 60); err != nil {
		t.Fatal(err)
	}
	// Quiet for 59 minutes is not older than sixty: stays up.
	r.active = time.Unix(1_800_000_000, 0).Add(-59 * time.Minute)
	if res := r.tick(); res.Slept != 0 {
		t.Fatalf("60 min, quiet 59: %+v", res)
	}
	r.active = time.Unix(1_800_000_000, 0).Add(-61 * time.Minute)
	if res := r.tick(); res.Slept != 1 || r.state() != CoordinatorStopped {
		t.Fatalf("60 min, quiet 61: %+v state=%s", res, r.state())
	}
}

// With no daemon default an Assistant that chose a limit still sleeps by it.
func TestIdleSleepOwnChoiceNeedsNoDefault(t *testing.T) {
	r := newIdleRig(t)
	r.z.After = 0
	if res := r.tick(); res.Slept != 0 {
		t.Fatalf("no default, no choice: %+v", res)
	}
	if _, _, err := r.s.SetRuntime("idle-15", r.a.ID, "", "", "", 0, 15); err != nil {
		t.Fatal(err)
	}
	if res := r.tick(); res.Slept != 1 {
		t.Fatalf("own 15 min: %+v", res)
	}
}

func TestRuntimeIdleSleepChoices(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "rt-a", "Rae")
	rt, err := s.GetRuntime(a.ID)
	if err != nil || rt.Profile.IdleSleepMin != int(DefaultIdleSleep/time.Minute) {
		t.Fatalf("the default is shown: %+v %v", rt.Profile, err)
	}
	if _, _, err := s.SetRuntime("rt-bad", a.ID, "", "", "", 0, 7); !errors.Is(err, ErrInvalid) {
		t.Fatalf("7 minutes is not offered: %v", err)
	}
	rt, _, err = s.SetRuntime("rt-60", a.ID, "claude", "", "", 0, 60)
	if err != nil || rt.Profile.IdleSleepMin != 60 {
		t.Fatalf("60: %+v %v", rt.Profile, err)
	}
	// A save that says nothing about it keeps the earlier choice.
	rt, _, err = s.SetRuntime("rt-keep", a.ID, "codex", "", "", 1)
	if err != nil || rt.Profile.IdleSleepMin != 60 || rt.Profile.Agent != "codex" {
		t.Fatalf("kept: %+v %v", rt.Profile, err)
	}
	rt, _, err = s.SetRuntime("rt-never", a.ID, "codex", "", "", 2, 0)
	if err != nil || rt.Profile.IdleSleepMin != 0 {
		t.Fatalf("never: %+v %v", rt.Profile, err)
	}
}
