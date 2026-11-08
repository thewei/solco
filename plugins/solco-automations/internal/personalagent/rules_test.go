package personalagent

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// D: one dispatcher for every rule. The definitions stay in automations.yaml
// (so `put`, `approve`, the desktop page and the phone keep working); the
// ledger decides who dispatches. These tests pin SOLCO-D01 (the old behaviours
// have equal tests), SOLCO-D02 (the watermark: the same occurrence never runs
// under both schedulers or twice) and the rollback.

func rule(name, cron string, mut ...func(*config.Automation)) config.Automation {
	a := config.Automation{Name: name, Cron: cron, Repo: "/tmp/repo", Workspace: config.WorkspaceRoot, Agent: "claude",
		Prompt: "Do " + name, TimeoutMinutes: 30, CatchUpMinutes: 120, OnOverlap: config.OnOverlapSkip}
	for _, m := range mut {
		m(&a)
	}
	return a
}

type clockT struct{ t time.Time }

func (c *clockT) now() time.Time { return c.t }

func ruleSched(s *Store, cfg *config.Config, c *clockT) *RuleScheduler {
	return &RuleScheduler{Store: s, Load: func() (*config.Config, error) { return cfg, nil }, Now: c.now}
}

func ruleTasks(t *testing.T, s *Store, name string) []Task {
	t.Helper()
	asst, err := s.AutomationsAssistant()
	if err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListTasks(asst.ID)
	out := []Task{}
	for _, x := range all {
		if x.RuleID == LegacyRuleID(name) {
			out = append(out, x)
		}
	}
	return out
}

// adopt walks dry-run then adopt, as the CLI does.
func adopt(t *testing.T, s *Store, cfg *config.Config, name string, last map[string]time.Time, now time.Time) LegacyRule {
	t.Helper()
	if _, err := s.DryRunLegacy(cfg, last, now); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	r, _, err := s.AdoptLegacy("adopt-"+name, name, cfg, last, now)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	return r
}

func at(h, m int) time.Time { return time.Date(2027, 3, 1, h, m, 0, 0, time.Local) }

func TestLegacyDryRunReconcilesNextFire(t *testing.T) {
	s, _ := openTest(t)
	cfg := &config.Config{Automations: []config.Automation{
		rule("daily", "0 9 * * *"),
		rule("review", "*/30 * * * *", func(a *config.Automation) { a.Headless = true }),
		rule("viaHC", "0 8 * * *", func(a *config.Automation) { a.Template = "triage" }),
		rule("parked", "0 7 * * *", func(a *config.Automation) { a.Draft = true }),
	}}
	last := map[string]time.Time{"daily": at(9, 0).Add(-24 * time.Hour), "review": at(10, 0)}
	now := at(10, 20)
	got, err := s.DryRunLegacy(cfg, last, now)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]LegacyCandidate{}
	for _, c := range got {
		by[c.Name] = c
	}
	if len(by) != 4 {
		t.Fatalf("candidates = %+v", got)
	}
	for _, name := range []string{"daily", "review"} {
		c := by[name]
		if !c.Migratable || !c.Match || c.State != LegacyDryRun || c.PersonalNext.IsZero() || !c.PersonalNext.Equal(c.LegacyNext) {
			t.Fatalf("%s = %+v", name, c)
		}
	}
	if c := by["viaHC"]; c.Migratable || !strings.Contains(c.Reason, "template") {
		t.Fatalf("viaHC = %+v", c)
	}
	if c := by["parked"]; !c.Migratable { // a draft migrates as a draft: it still never fires
		t.Fatalf("parked = %+v", c)
	}
	// A dry run owns nothing: the old scheduler still dispatches everything.
	if owned, _ := s.OwnedLegacy(); len(owned) != 0 {
		t.Fatalf("a dry run took ownership: %v", owned)
	}
	// And it is idempotent.
	if again, _ := s.DryRunLegacy(cfg, last, now); len(again) != 4 {
		t.Fatalf("again = %+v", again)
	}
	led, _ := s.LegacyLedger()
	if len(led) != 3 { // daily, review, parked; viaHC has no row
		t.Fatalf("ledger = %+v", led)
	}
}

func TestAdoptMakesPersonalTheOnlyDispatcher(t *testing.T) {
	s, path := openTest(t)
	cfg := &config.Config{Automations: []config.Automation{rule("daily", "0 9 * * *"), rule("other", "0 9 * * *")}}
	clk := &clockT{at(8, 0)}
	last := map[string]time.Time{"daily": at(9, 0).Add(-24 * time.Hour), "other": at(9, 0).Add(-24 * time.Hour)}
	adopt(t, s, cfg, "daily", last, clk.t)

	owned, _ := s.OwnedLegacy()
	if !owned["daily"] || owned["other"] {
		t.Fatalf("owned = %v", owned)
	}
	// The old scheduler sees only what it still owns.
	rest := FilterOwned(cfg, owned)
	if len(rest.Automations) != 1 || rest.Automations[0].Name != "other" {
		t.Fatalf("legacy still sees %+v", rest.Automations)
	}
	if len(cfg.Automations) != 2 {
		t.Fatal("FilterOwned must not edit the definitions")
	}

	rs := ruleSched(s, cfg, clk)
	if r, err := rs.Tick(); err != nil || r.Fired != 0 {
		t.Fatalf("before the hour: %+v %v", r, err)
	}
	clk.t = at(9, 0).Add(10 * time.Second)
	if r, err := rs.Tick(); err != nil || r.Fired != 1 {
		t.Fatalf("at the hour: %+v %v", r, err)
	}
	if r, _ := rs.Tick(); r.Fired != 0 {
		t.Fatalf("the same occurrence fired twice: %+v", r)
	}
	// A daemon restart (new store handle, new scheduler) cannot replay it.
	_ = s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	rs2 := ruleSched(s2, cfg, clk)
	if r, _ := rs2.Tick(); r.Fired != 0 {
		t.Fatalf("replayed after restart: %+v", r)
	}
	if got := ruleTasks(t, s2, "daily"); len(got) != 1 {
		t.Fatalf("tasks = %d", len(got))
	} else if got[0].Kind != KindManual || got[0].RepoPath != "/tmp/repo" || got[0].Agent != "claude" || got[0].Brief != "Do daily" {
		t.Fatalf("task = %+v", got[0])
	}
	if got := ruleTasks(t, s2, "other"); len(got) != 0 {
		t.Fatalf("a rule the ledger does not own was dispatched: %+v", got)
	}
	// Next day, next occurrence (yesterday's run has finished, so no overlap).
	if _, err := s2.db.Exec(`UPDATE tasks SET state='done' WHERE rule_id=?`, LegacyRuleID("daily")); err != nil {
		t.Fatal(err)
	}
	clk.t = at(9, 0).Add(24*time.Hour + 5*time.Second)
	if r, _ := rs2.Tick(); r.Fired != 1 {
		t.Fatalf("next day: %+v", r)
	}
}

// D01: catch-up, missed and "never catch up" behave exactly as the old
// scheduler's (the decisions are schedule.Plan's own).
func TestCatchUpAndMissedMatchTheOldScheduler(t *testing.T) {
	cases := []struct {
		name      string
		catchUp   int
		now       time.Time
		fired     int
		missed    int
		wantNotes string
	}{
		{"on time", 120, at(9, 0).Add(20 * time.Second), 1, 0, ""},
		{"late but inside the window", 120, at(10, 30), 1, 0, ""},
		{"past the window", 120, at(13, 0), 0, 1, "catch-up window"},
		{"catch-up disabled", -1, at(9, 30), 0, 1, "catch-up disabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openTest(t)
			cfg := &config.Config{Automations: []config.Automation{rule("daily", "0 9 * * *", func(a *config.Automation) { a.CatchUpMinutes = tc.catchUp })}}
			clk := &clockT{at(8, 0)}
			adopt(t, s, cfg, "daily", map[string]time.Time{"daily": at(9, 0).Add(-24 * time.Hour)}, clk.t)
			clk.t = tc.now
			r, err := ruleSched(s, cfg, clk).Tick()
			if err != nil || r.Fired != tc.fired || r.Missed != tc.missed {
				t.Fatalf("%+v %v", r, err)
			}
			if tc.wantNotes != "" {
				found := false
				for _, n := range r.Notes {
					found = found || (n.Status == "missed" && strings.Contains(n.Detail, tc.wantNotes))
				}
				if !found {
					t.Fatalf("notes = %+v", r.Notes)
				}
			}
			if got := ruleTasks(t, s, "daily"); len(got) != tc.fired {
				t.Fatalf("tasks = %d", len(got))
			}
		})
	}
}

// D01: on_overlap skip drops the occurrence under a working run; queue keeps
// exactly one and runs it once the run is done — never more.
func TestOverlapSkipAndQueueMatchTheOldScheduler(t *testing.T) {
	for _, mode := range []string{config.OnOverlapSkip, config.OnOverlapQueue} {
		t.Run(mode, func(t *testing.T) {
			s, _ := openTest(t)
			cfg := &config.Config{Automations: []config.Automation{rule("hourly", "0 * * * *", func(a *config.Automation) { a.OnOverlap = mode })}}
			clk := &clockT{at(8, 30)}
			adopt(t, s, cfg, "hourly", map[string]time.Time{"hourly": at(8, 0)}, clk.t)
			rs := ruleSched(s, cfg, clk)

			clk.t = at(9, 0).Add(5 * time.Second)
			if r, _ := rs.Tick(); r.Fired != 1 {
				t.Fatalf("first: %+v", r)
			}
			first := ruleTasks(t, s, "hourly")[0]
			// The run is still going at 10:00 and at 11:00.
			_, run := claimAndRun(t, s, first)
			clk.t = at(10, 0).Add(5 * time.Second)
			r, _ := rs.Tick()
			if r.Fired != 0 {
				t.Fatalf("overlap fired: %+v", r)
			}
			if mode == config.OnOverlapSkip && (r.Skipped != 1 || r.Queued != 0) {
				t.Fatalf("skip: %+v", r)
			}
			if mode == config.OnOverlapQueue && (r.Queued != 1 || r.Skipped != 0) {
				t.Fatalf("queue: %+v", r)
			}
			clk.t = at(11, 0).Add(5 * time.Second)
			r, _ = rs.Tick()
			if r.Fired != 0 || r.Skipped != 1 { // queue-full is a skip; skip stays a skip
				t.Fatalf("second overlap: %+v", r)
			}
			// The run finishes: queue releases ONE occurrence, skip releases none.
			if err := s.FinishRun(run.ID, RunDone, "ok"); err != nil {
				t.Fatal(err)
			}
			clk.t = at(11, 1)
			r, _ = rs.Tick()
			want := 0
			if mode == config.OnOverlapQueue {
				want = 1
			}
			if r.Fired != want {
				t.Fatalf("release: %+v want %d", r, want)
			}
			clk.t = at(11, 2)
			if r, _ := rs.Tick(); r.Fired != 0 {
				t.Fatalf("queue released twice: %+v", r)
			}
			if got := len(ruleTasks(t, s, "hourly")); got != 1+want {
				t.Fatalf("tasks = %d", got)
			}
		})
	}
}

func TestDraftDisabledAndPausedNeverFire(t *testing.T) {
	s, _ := openTest(t)
	cfg := &config.Config{Automations: []config.Automation{
		rule("draft", "0 9 * * *", func(a *config.Automation) { a.Draft = true }),
		rule("off", "0 9 * * *", func(a *config.Automation) { a.Disabled = true }),
		rule("on", "0 9 * * *"),
	}}
	clk := &clockT{at(8, 0)}
	last := map[string]time.Time{}
	for _, n := range []string{"draft", "off", "on"} {
		last[n] = at(9, 0).Add(-24 * time.Hour)
		adopt(t, s, cfg, n, last, clk.t)
	}
	clk.t = at(9, 0).Add(5 * time.Second)
	cfg.Paused = true
	r, _ := ruleSched(s, cfg, clk).Tick()
	if r.Fired != 0 {
		t.Fatalf("paused fired: %+v", r)
	}
	sawPaused := false
	for _, n := range r.Notes {
		sawPaused = sawPaused || (n.Status == "skipped" && n.Name == "on" && strings.Contains(n.Detail, "paused"))
	}
	if !sawPaused {
		t.Fatalf("a paused occurrence is passed over visibly: %+v", r.Notes)
	}
	cfg.Paused = false
	clk.t = at(9, 0).Add(24*time.Hour + 5*time.Second)
	r, _ = ruleSched(s, cfg, clk).Tick()
	if r.Fired != 1 {
		t.Fatalf("only the live rule fires: %+v", r)
	}
	for _, n := range []string{"draft", "off"} {
		if got := ruleTasks(t, s, n); len(got) != 0 {
			t.Fatalf("%s fired: %+v", n, got)
		}
	}
}

func TestRollbackHandsStateBackOnce(t *testing.T) {
	s, _ := openTest(t)
	cfg := &config.Config{Automations: []config.Automation{rule("daily", "0 9 * * *")}}
	clk := &clockT{at(8, 0)}
	adopt(t, s, cfg, "daily", map[string]time.Time{"daily": at(9, 0).Add(-24 * time.Hour)}, clk.t)
	clk.t = at(9, 0).Add(5 * time.Second)
	rs := ruleSched(s, cfg, clk)
	if r, _ := rs.Tick(); r.Fired != 1 {
		t.Fatal("setup")
	}
	r, _, err := s.RollbackLegacy("rb", "daily")
	if err != nil || r.State != LegacyRolledBack {
		t.Fatalf("rollback: %+v %v", r, err)
	}
	if owned, _ := s.OwnedLegacy(); len(owned) != 0 {
		t.Fatalf("owned after rollback: %v", owned)
	}
	back, err := s.TakeHandbacks()
	if err != nil || len(back) != 1 || !back["daily"].Equal(at(9, 0)) {
		t.Fatalf("handback = %v %v (the old scheduler must resume at the last occurrence, not replay it)", back, err)
	}
	if again, _ := s.TakeHandbacks(); len(again) != 0 {
		t.Fatalf("handback repeated: %v", again)
	}
	clk.t = at(9, 0).Add(24*time.Hour + 5*time.Second)
	if r, _ := rs.Tick(); r.Fired != 0 {
		t.Fatalf("the personal scheduler kept dispatching a rolled-back rule: %+v", r)
	}
	// It can be adopted again later, from the old scheduler's new watermark.
	if _, _, err := s.AdoptLegacy("again", "daily", cfg, map[string]time.Time{"daily": at(9, 0)}, clk.t); err != nil {
		t.Fatalf("re-adopt: %v", err)
	}
}

func TestAdoptRefusesWhatItCannotRunAndReplaysSafely(t *testing.T) {
	s, _ := openTest(t)
	cfg := &config.Config{Automations: []config.Automation{
		rule("daily", "0 9 * * *"),
		rule("viaHC", "0 8 * * *", func(a *config.Automation) { a.Template = "triage" }),
	}}
	now := at(8, 0)
	if _, _, err := s.AdoptLegacy("a", "viaHC", cfg, nil, now); err == nil {
		t.Fatal("an hc-template rule cannot run on the Herdr launcher")
	}
	if _, _, err := s.AdoptLegacy("b", "ghost", cfg, nil, now); err == nil {
		t.Fatal("unknown rule")
	}
	first, replay, err := s.AdoptLegacy("c", "daily", cfg, nil, now)
	if err != nil || replay {
		t.Fatal(err)
	}
	again, replay, err := s.AdoptLegacy("c", "daily", cfg, nil, now)
	if err != nil || !replay || again.Revision != first.Revision {
		t.Fatalf("replay: %+v %v %v", again, replay, err)
	}
}

// D01: history retention is 30 days and 5000 records — for the rules'
// finished tasks too; running work is never pruned.
func TestRuleHistoryRetention(t *testing.T) {
	now := time.Unix(1_900_000_000, 0)
	clk := &clockT{now}
	s, _ := openTest(t, WithClock(clk.now))
	cfg := &config.Config{Automations: []config.Automation{rule("hourly", "0 * * * *")}}
	adopt(t, s, cfg, "hourly", map[string]time.Time{"hourly": now}, now)
	asst, _ := s.AutomationsAssistant()
	mk := func(i int, state string, age time.Duration) string {
		id := "t_old" + string(rune('a'+i))
		_, err := s.db.Exec(`INSERT INTO tasks(`+taskCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, asst.ID, "", LegacyRuleID("hourly"), "", KindManual, "hourly", "p", "/r", "claude", "", state, 0, "", 1, 1,
			now.Add(-age).UnixMilli(), now.Add(-age).UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	old := mk(0, TaskDone, 40*24*time.Hour)
	oldFail := mk(1, TaskFailed, 31*24*time.Hour)
	fresh := mk(2, TaskDone, 2*24*time.Hour)
	live := mk(3, TaskRunning, 90*24*time.Hour)
	n, err := s.PruneRuleHistory(30*24*time.Hour, 5000)
	if err != nil || n != 2 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	for id, want := range map[string]bool{old: false, oldFail: false, fresh: true, live: true} {
		_, err := s.GetTask(id)
		if (err == nil) != want {
			t.Fatalf("%s kept=%v want %v", id, err == nil, want)
		}
	}
	// The cap keeps the newest.
	newer := mk(4, TaskDone, 24*time.Hour)
	n, _ = s.PruneRuleHistory(30*24*time.Hour, 1)
	if n != 1 {
		t.Fatalf("cap pruned %d", n)
	}
	if _, err := s.GetTask(newer); err != nil {
		t.Fatal("the cap pruned the newest task")
	}
	if _, err := s.GetTask(live); err != nil {
		t.Fatal("a running task was pruned by the cap")
	}
}

// D01: a rule's task runs the rule's own prompt verbatim on the same engine
// the old runner used (headless, mcp, args, workspace mode), and every status
// change reaches the observer so the old history log stays whole.
func TestRuleTaskRunsTheRulesOwnDefinition(t *testing.T) {
	s, _ := openTest(t)
	def := rule("review", "*/30 * * * *", func(a *config.Automation) {
		a.Headless = true
		a.Prompt = "Review the open PRs"
		a.MCPConfig = "/tmp/mcp.json"
	})
	cfg := &config.Config{Automations: []config.Automation{def}}
	clk := &clockT{at(8, 0)}
	adopt(t, s, cfg, "review", map[string]time.Time{"review": at(8, 0)}, clk.t)
	clk.t = at(8, 30).Add(5 * time.Second)
	if r, _ := ruleSched(s, cfg, clk).Tick(); r.Fired != 1 {
		t.Fatal("setup")
	}
	// A memory exists; a rule's prompt must still go out verbatim.
	asst, _ := s.AutomationsAssistant()
	t.Setenv("SOLCO_ASSISTANTS_MEMORY", t.TempDir())
	writeBoth(t, s, asst.ID, "unrelated fact", "")

	l := &fakeLauncher{}
	var seen []string
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon",
		Rules: func(name string) (config.Automation, bool) { return def, name == "review" },
		OnSettled: func(task Task, run Run) {
			seen = append(seen, run.Status)
		}}
	if n := d.Pump(t.Context()); n != 1 {
		t.Fatalf("pump started %d", n)
	}
	d.Wait()
	if len(l.specs) != 1 {
		t.Fatalf("specs = %d", len(l.specs))
	}
	sp := l.specs[0]
	if sp.Prompt != "Review the open PRs" || sp.Auto == nil || !sp.Auto.Headless || sp.Auto.MCPConfig != "/tmp/mcp.json" {
		t.Fatalf("spec = %+v auto=%+v", sp, sp.Auto)
	}
	if len(seen) < 2 || seen[len(seen)-1] != RunDone {
		t.Fatalf("observer saw %v", seen)
	}
}

// A rule removed from automations.yaml stays dormant — its due occurrences fire
// nothing and no phantom task appears — and comes back when it is re-added.
// (Shepherdr core-automations: "removed spaces keep timed rules dormant until re-add".)
func TestARemovedRuleStaysDormantUntilItIsAddedBack(t *testing.T) {
	s, _ := openTest(t)
	cfg := &config.Config{Automations: []config.Automation{rule("daily", "*/10 * * * *")}}
	clock := &clockT{t: at(10, 0)}
	adopt(t, s, cfg, "daily", map[string]time.Time{"daily": at(10, 0)}, clock.t)
	gone := &config.Config{}
	sched := &RuleScheduler{Store: s, Load: func() (*config.Config, error) { return gone, nil }, Now: clock.now}
	clock.t = at(10, 30)
	if rep, err := sched.Tick(); err != nil || rep.Fired != 0 {
		t.Fatalf("a removed rule fired: %+v %v", rep, err)
	}
	if got := ruleTasks(t, s, "daily"); len(got) != 0 {
		t.Fatalf("phantom tasks for a removed rule: %d", len(got))
	}
	back := ruleSched(s, cfg, clock)
	clock.t = at(10, 40)
	rep, err := back.Tick()
	if err != nil || rep.Fired < 1 {
		t.Fatalf("a re-added rule did not come back: %+v %v", rep, err)
	}
}

// R04: two schedulers over the same database (two daemons, or a daemon and a
// keeper racing one another) turn one occurrence into exactly one task.
func TestTwoSchedulersRacingFireAnOccurrenceOnce(t *testing.T) {
	s1, path := openTest(t)
	cfg := &config.Config{Automations: []config.Automation{rule("daily", "0 9 * * *")}}
	clk := &clockT{at(8, 0)}
	last := map[string]time.Time{"daily": at(9, 0).Add(-24 * time.Hour)}
	adopt(t, s1, cfg, "daily", last, clk.t)
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	clk.t = at(9, 0).Add(10 * time.Second)
	var wg sync.WaitGroup
	var fired int32
	for _, s := range []*Store{s1, s2, s1, s2} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				if r, err := ruleSched(s, cfg, clk).Tick(); err == nil {
					atomic.AddInt32(&fired, int32(r.Fired))
				}
			}
		}(s)
	}
	wg.Wait()
	if fired != 1 || len(ruleTasks(t, s1, "daily")) != 1 {
		t.Fatalf("fired %d, tasks %d: want exactly one", fired, len(ruleTasks(t, s1, "daily")))
	}
}
