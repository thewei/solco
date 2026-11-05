package personalagent

import (
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"strings"
	"testing"
)

// Gap R02/AC-02: the person's own words become the schedule, in an explicit
// zone, with the next three runs on the approval card — and the card says so.
func TestGapR02RoutineDraftFromWordsPinsZoneAndPreviewsThreeRuns(t *testing.T) {
	e := newRoutineEnv(t)
	got, err := e.draft(t, "words-1", func(m map[string]any) {
		delete(m, "cron")
		m["schedule"] = "每个工作日早上9点"
		m["timezone"] = "Asia/Shanghai"
	})
	if err != nil {
		t.Fatal(err)
	}
	rt := got["routine"].(map[string]any)
	if rt["cron"] != "CRON_TZ=Asia/Shanghai 0 9 * * 1-5" {
		t.Fatalf("cron = %v", rt["cron"])
	}
	if got["timezone"] != "Asia/Shanghai" || len(got["next"].([]any)) != 3 {
		t.Fatalf("timezone/next = %v / %v", got["timezone"], got["next"])
	}
	ap := got["approval"].(map[string]any)
	if !strings.Contains(ap["action"].(string), "Asia/Shanghai") {
		t.Fatalf("the approval card must name the zone: %v", ap["action"])
	}
}

func TestGapR02AmbiguousWordsAskInsteadOfDrafting(t *testing.T) {
	e := newRoutineEnv(t)
	for _, words := range []string{"every weekday", "sometimes", "每天"} {
		_, err := e.draft(t, "ask-"+words, func(m map[string]any) {
			delete(m, "cron")
			m["schedule"] = words
			m["timezone"] = "Asia/Shanghai"
		})
		ce, ok := err.(*CodedError)
		if !ok || ce.Code != "needs_clarification" {
			t.Errorf("%q: err = %v, want needs_clarification", words, err)
		}
	}
	if rs, _ := e.s.ListRoutines(e.a.ID); len(rs) != 0 {
		t.Fatalf("an unclear request left routines behind: %+v", rs)
	}
	if _, err := e.draft(t, "once", func(m map[string]any) {
		delete(m, "cron")
		m["schedule"] = "tomorrow at 9am"
		m["timezone"] = "Asia/Shanghai"
	}); err == nil || err.(*CodedError).Code != "unsupported_schedule" {
		t.Fatalf("one-time request: %v", err)
	}
}

func TestGapR02CronAndWordsTogetherAreRefused(t *testing.T) {
	e := newRoutineEnv(t)
	if _, err := e.draft(t, "both", func(m map[string]any) { m["schedule"] = "daily at 9:00" }); err == nil {
		t.Fatal("two schedules for one routine must be refused")
	}
}

// W04/AC-02: a Routine that names a skill carries it to every run it causes,
// and the card the person answers says so.
func TestGapW04RoutineCarriesItsSkillsToEveryOccurrence(t *testing.T) {
	e := newRoutineEnv(t)
	got, err := e.draft(t, "skills-1", func(m map[string]any) { m["requiredSkills"] = []string{"ego-browser"} })
	if err != nil {
		t.Fatal(err)
	}
	ap := got["approval"].(map[string]any)
	if !strings.Contains(ap["action"].(string), "ego-browser") {
		t.Fatalf("the approval card must name the skill: %v", ap["action"])
	}
	e.approve(t, ap["id"].(string), ap["actionHash"].(string))
	next := at(9, 0).Add(24 * time.Hour)
	rs := &RuleScheduler{Store: e.s, Load: config.Load, Now: func() time.Time { return next.Add(5 * time.Second) }}
	if rep, err := rs.Tick(); err != nil || rep.Fired != 1 {
		t.Fatalf("tick = %+v %v", rep, err)
	}
	tasks, _ := e.s.ListTasks(e.a.ID)
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v", tasks)
	}
	if sk, _ := e.s.TaskSkills(tasks[0].ID); strings.Join(sk, ",") != "ego-browser" {
		t.Fatalf("the occurrence's task lost the routine's skills: %v", sk)
	}
}

// W04/AC-02 end of the chain: the occurrence's worker stops for an ungranted
// skill, and once the person grants it, runs with the skill named in its prompt.
func TestGapW04RoutineRunGatesAndInstructsTheSkill(t *testing.T) {
	e := newRoutineEnv(t)
	got, err := e.draft(t, "skills-run", func(m map[string]any) { m["requiredSkills"] = []string{"ego-browser"} })
	if err != nil {
		t.Fatal(err)
	}
	ap := got["approval"].(map[string]any)
	e.approve(t, ap["id"].(string), ap["actionHash"].(string))
	next := at(9, 0).Add(24 * time.Hour)
	rs := &RuleScheduler{Store: e.s, Load: config.Load, Now: func() time.Time { return next.Add(5 * time.Second) }}
	if rep, err := rs.Tick(); err != nil || rep.Fired != 1 {
		t.Fatalf("tick = %+v %v", rep, err)
	}
	tasks, _ := e.s.ListTasks(e.a.ID)
	l := &collectingLauncher{collect: []func() (WorkerResult, error){func() (WorkerResult, error) {
		r, _ := okResult("digest https://x.example/1")()
		r.Skills = []string{"ego-browser"}
		return r, nil
	}}}
	d := newResultDispatcher(e.s, l)
	d.Skills = func() map[string]SkillInfo { return map[string]SkillInfo{"ego-browser": {Name: "ego-browser"}} }
	d.Rules = func(name string) (config.Automation, bool) {
		cfg, _ := config.Load()
		if a := cfg.Find(name); a != nil {
			return *a, true
		}
		return config.Automation{}, false
	}
	run, _ := d.RunTask(t.Context(), tasks[0].ID)
	if run.Status != RunFailed || !strings.HasPrefix(run.Error, SkillNotGrantedPrefix) || l.provisions != 0 {
		t.Fatalf("an ungranted skill must stop the occurrence before launch: %+v", run)
	}
	cards, _ := e.s.ListApprovals(e.a.ID)
	var skillCard Approval
	for _, c := range cards {
		if c.SubjectKind == "skill" {
			skillCard = c
		}
	}
	if skillCard.ID == "" {
		t.Fatalf("no skill card: %+v", cards)
	}
	if _, _, err := e.s.DecideApproval("grant-1", skillCard.ID, skillCard.ActionHash, true); err != nil {
		t.Fatal(err)
	}
	run, err = d.RunTask(t.Context(), tasks[0].ID)
	if err != nil || run.Status != RunDone {
		t.Fatalf("after the grant: %+v %v", run, err)
	}
	if p := l.specs[len(l.specs)-1].Prompt; !strings.Contains(p, "Required skills: ego-browser") {
		t.Fatalf("the routine worker's prompt must name the skill:\n%s", p)
	}
}
