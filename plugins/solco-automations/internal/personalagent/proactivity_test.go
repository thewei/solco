package personalagent

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func setPolicy(t *testing.T, s *Store, a Assistant, mut func(*ProactivityInput)) Proactivity {
	t.Helper()
	cur, err := s.GetProactivity(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := ProactivityInput{AssistantID: a.ID, Level: cur.Level, QuietStart: cur.QuietStart, QuietEnd: cur.QuietEnd, TZ: cur.TZ,
		ReportEveryMin: cur.ReportEveryMin, DailyBudget: cur.DailyBudget}
	mut(&in)
	p, _, err := s.SetProactivity("pol-"+time.Now().Format("150405.000000000"), in, cur.Revision)
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}
	return p
}

func indepPlan(n int) GoalPlanInput {
	in := GoalPlanInput{SuccessCriteria: "all done", NextAction: "go"}
	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		in.Milestones = append(in.Milestones, Milestone{ID: id, Title: "Part " + id,
			Task: &MilestoneTask{Kind: KindResearch, Title: "Task " + id, Brief: "Do " + id}})
	}
	return in
}

// U5-2: unset means today's behaviour; the rules validate and are versioned.
func TestProactivityDefaultsValidationAndRevision(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	p, err := s.GetProactivity(a.ID)
	if err != nil || p.Level != LevelFollowUp || p.Revision != 0 || p.DailyBudget != DefaultDailyBudget || p.QuietNow {
		t.Fatalf("defaults = %+v %v", p, err)
	}
	bad := []ProactivityInput{
		{AssistantID: a.ID, Level: "autopilot", DailyBudget: 5},
		{AssistantID: a.ID, Level: LevelSuggest, QuietStart: "22:00", DailyBudget: 5},
		{AssistantID: a.ID, Level: LevelSuggest, QuietStart: "25:00", QuietEnd: "08:00", DailyBudget: 5},
		{AssistantID: a.ID, Level: LevelSuggest, TZ: "Mars/Base", DailyBudget: 5},
		{AssistantID: a.ID, Level: LevelSuggest, DailyBudget: 0},
		{AssistantID: a.ID, Level: LevelSuggest, DailyBudget: MaxDailyBudget + 1},
		{AssistantID: a.ID, Level: LevelSuggest, DailyBudget: 5, ReportEveryMin: 2000},
	}
	for i, in := range bad {
		if _, _, err := s.SetProactivity("bad", in, 0); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: err = %v", i, err)
		}
	}
	p = setPolicy(t, s, a, func(in *ProactivityInput) { in.Level = LevelSuggest })
	if p.Revision != 1 || p.Level != LevelSuggest {
		t.Fatalf("set = %+v", p)
	}
	if _, _, err := s.SetProactivity("stale", ProactivityInput{AssistantID: a.ID, Level: LevelRespond, DailyBudget: 5}, 0); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale err = %v", err)
	}
}

func TestQuietWindowWrapsMidnightInItsZone(t *testing.T) {
	p := Proactivity{QuietStart: "22:00", QuietEnd: "08:00", TZ: "Asia/Shanghai"}
	at := func(h int) time.Time { return time.Date(2027, 1, 15, h, 0, 0, 0, time.UTC) }
	// Shanghai is UTC+8: 14:00 UTC is 22:00 there, 00:00 UTC is 08:00.
	for h, want := range map[int]bool{13: false, 14: true, 15: true, 23: true, 0: false, 1: false, 3: false} {
		if got := p.quietAt(at(h)); got != want {
			t.Fatalf("%02d:00 UTC quiet = %v, want %v", h, got, want)
		}
	}
	if (Proactivity{}).quietAt(at(3)) || (Proactivity{QuietStart: "09:00", QuietEnd: "09:00"}).quietAt(at(9)) {
		t.Fatal("no window, or an empty one, is never quiet")
	}
	day := Proactivity{QuietStart: "12:00", QuietEnd: "13:00", TZ: "UTC"}
	if !day.quietAt(at(12)) || day.quietAt(at(13)) {
		t.Fatal("a same-day window is [start,end)")
	}
}

// U5-2: Respond starts nothing; Suggest says what is next, once, and starts
// nothing; Follow up carries the Goal on. Nothing is lost by switching.
func TestLevelsGovernWhatStartsUnasked(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	g := activeGoalWithPlan(t, s, a, twoStepPlan())
	sv := &Supervisor{Store: s}
	setPolicy(t, s, a, func(in *ProactivityInput) { in.Level = LevelRespond })
	if r, _ := sv.Tick(); r.Dispatched != 0 || r.Facts != 0 {
		t.Fatalf("respond: %+v", r)
	}
	setPolicy(t, s, a, func(in *ProactivityInput) { in.Level = LevelSuggest })
	for i := 0; i < 3; i++ {
		_, _ = sv.Tick()
	}
	if tasks, _ := s.ListTasks(a.ID); len(tasks) != 0 {
		t.Fatalf("suggest started %d task(s)", len(tasks))
	}
	said := 0
	for _, tx := range factTexts(t, s, g.ID) {
		if strings.Contains(tx, "Collect the numbers") && strings.Contains(tx, "Suggest") {
			said++
		}
	}
	if said != 1 {
		t.Fatalf("suggest said it %d times: %v", said, factTexts(t, s, g.ID))
	}
	setPolicy(t, s, a, func(in *ProactivityInput) { in.Level = LevelFollowUp })
	if r, _ := sv.Tick(); r.Dispatched != 1 {
		t.Fatalf("follow up: %+v", r)
	}
}

// U5-2: quiet hours defer starting and reminding; both resume by themselves.
func TestQuietHoursDeferThenResume(t *testing.T) {
	clock := &tickClock{t: time.Date(2027, 1, 15, 15, 0, 0, 0, time.UTC)} // 23:00 in Shanghai
	s, _ := openTest(t, WithClock(clock.now))
	a := mustAssistant(t, s, "a1", "Ada")
	setPolicy(t, s, a, func(in *ProactivityInput) { in.QuietStart, in.QuietEnd, in.TZ = "22:00", "08:00", "Asia/Shanghai" })
	if p, _ := s.GetProactivity(a.ID); !p.QuietNow {
		t.Fatal("23:00 Shanghai should be quiet")
	}
	g := activeGoalWithPlan(t, s, a, twoStepPlan())
	sv := &Supervisor{Store: s}
	if r, _ := sv.Tick(); r.Dispatched != 0 || r.Facts != 0 {
		t.Fatalf("quiet tick: %+v", r)
	}
	clock.t = clock.t.Add(10 * time.Hour) // 09:00 in Shanghai
	if r, _ := sv.Tick(); r.Dispatched != 1 {
		t.Fatalf("after quiet hours: %+v", r)
	}
	// A reminder that came due in the night is said once the window ends, not lost.
	if err := s.setPlanLimits(g.ID, 0, 30*60*1000); err != nil {
		t.Fatal(err)
	}
	clock.t = time.Date(2027, 1, 15, 15, 0, 0, 0, time.UTC).Add(24 * time.Hour) // next 23:00 Shanghai
	_, _ = sv.Tick()
	stale := func() int {
		n := 0
		for _, k := range factKinds(t, s, g.ID) {
			if k == FactStale {
				n++
			}
		}
		return n
	}
	if stale() != 0 {
		t.Fatal("a reminder was said during quiet hours")
	}
	clock.t = clock.t.Add(10 * time.Hour)
	_, _ = sv.Tick()
	if stale() == 0 {
		t.Fatal("the reminder was lost")
	}
}

// U5-2: the daily budget caps what starts unasked, says so once a day, and a new day restarts it.
func TestDailyBudgetHoldsAndResets(t *testing.T) {
	clock := &tickClock{t: time.Date(2027, 1, 15, 3, 0, 0, 0, time.UTC)}
	s, _ := openTest(t, WithClock(clock.now))
	a := mustAssistant(t, s, "a1", "Ada")
	setPolicy(t, s, a, func(in *ProactivityInput) { in.DailyBudget, in.TZ = 2, "UTC" })
	g := activeGoalWithPlan(t, s, a, indepPlan(3))
	sv := &Supervisor{Store: s}
	for i := 0; i < 3; i++ {
		_, _ = sv.Tick()
	}
	if tasks, _ := s.ListTasks(a.ID); len(tasks) != 2 {
		t.Fatalf("tasks = %d, want the budget of 2", len(tasks))
	}
	held := 0
	for _, k := range factKinds(t, s, g.ID) {
		if k == FactHeld {
			held++
		}
	}
	if held != 1 {
		t.Fatalf("held facts = %d", held)
	}
	if p, _ := s.GetProactivity(a.ID); p.DispatchToday != 2 {
		t.Fatalf("tally = %d", p.DispatchToday)
	}
	clock.t = clock.t.Add(24 * time.Hour)
	if r, _ := sv.Tick(); r.Dispatched != 1 {
		t.Fatalf("next day: %+v", r)
	}
}

// U5-2: reminders respect the report cadence across Goals.
func TestReportCadenceSpacesReminders(t *testing.T) {
	clock := &tickClock{t: time.Unix(1_800_000_000, 0)}
	s, _ := openTest(t, WithClock(clock.now))
	a := mustAssistant(t, s, "a1", "Ada")
	setPolicy(t, s, a, func(in *ProactivityInput) { in.ReportEveryMin = 120 })
	g := activeGoalWithPlan(t, s, a, twoStepPlan())
	if err := s.setPlanLimits(g.ID, 0, 10*60*1000); err != nil {
		t.Fatal(err)
	}
	sv := &Supervisor{Store: s}
	_, _ = sv.Tick()
	count := func() (n int) {
		for _, k := range factKinds(t, s, g.ID) {
			if k == FactStale {
				n++
			}
		}
		return
	}
	clock.t = clock.t.Add(11 * time.Minute)
	_, _ = sv.Tick()
	if count() != 1 {
		t.Fatalf("first reminder: %d", count())
	}
	clock.t = clock.t.Add(30 * time.Minute) // due by backoff, but inside the cadence
	_, _ = sv.Tick()
	if count() != 1 {
		t.Fatalf("cadence broken: %d", count())
	}
	clock.t = clock.t.Add(100 * time.Minute)
	_, _ = sv.Tick()
	if count() != 2 {
		t.Fatalf("second reminder after the cadence: %d", count())
	}
}

// U5-2: the heartbeat's model wake is the system acting unasked, so the same
// rules hold it; the news waits and nothing is lost.
func TestHeartbeatWakeFollowsProactivity(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	now := time.Date(2027, 1, 15, 3, 0, 0, 0, time.UTC)
	h := setBeat(t, s, a, HeartbeatInput{Cron: "*/10 * * * *", Checklist: "- look", WakeBudget: 5})
	h = approveWake(t, s, h)
	b := beater(s, &now)
	_, _ = b.Tick()
	setPolicy(t, s, a, func(in *ProactivityInput) { in.Level = LevelSuggest })
	now = now.Add(11 * time.Minute)
	if r, _ := b.Tick(); r.Beats != 1 || r.Woke != 0 {
		t.Fatalf("suggest must not wake a model: %+v", r)
	}
	setPolicy(t, s, a, func(in *ProactivityInput) {
		in.Level = LevelFollowUp
		in.QuietStart, in.QuietEnd, in.TZ = "03:00", "08:00", "UTC"
	})
	now = now.Add(11 * time.Minute)
	if r, _ := b.Tick(); r.Woke != 0 {
		t.Fatalf("quiet hours must not wake a model: %+v", r)
	}
	now = now.Add(5 * time.Hour)
	if r, _ := b.Tick(); r.Woke != 1 {
		t.Fatalf("after quiet hours the wake resumes: %+v", r)
	}
}
