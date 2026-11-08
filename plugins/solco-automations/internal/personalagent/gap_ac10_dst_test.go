package personalagent

import (
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// AC-10: a Routine pinned to a zone fires at the zone's wall-clock time on the
// days it is asked for, once per occurrence, across both US daylight-saving
// changes — the instant moves by an hour in UTC, never the local time, and no
// occurrence is doubled or lost on the changeover day itself.
func TestGapAC10DailyRoutineKeepsItsLocalTimeAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database")
	}
	cases := []struct {
		name       string
		start, end time.Time // UTC; a Sunday changeover inside
		wantDays   int
	}{
		{"spring forward 2027-03-14", time.Date(2027, 3, 11, 12, 0, 0, 0, time.UTC), time.Date(2027, 3, 17, 12, 0, 0, 0, time.UTC), 6},
		{"fall back 2027-11-07", time.Date(2027, 11, 4, 12, 0, 0, 0, time.UTC), time.Date(2027, 11, 10, 12, 0, 0, 0, time.UTC), 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newRoutineEnv(t)
			e.clk.t = c.start
			got, err := e.draft(t, "ac10-"+c.name, func(m map[string]any) {
				delete(m, "cron")
				m["schedule"] = "every day at 9:00"
				m["timezone"] = "America/New_York"
			})
			if err != nil {
				t.Fatal(err)
			}
			ap := got["approval"].(map[string]any)
			e.approve(t, ap["id"].(string), ap["actionHash"].(string))

			var now time.Time
			rs := &RuleScheduler{Store: e.s, Load: config.Load, Now: func() time.Time { return now }}
			var fired []time.Time
			// Tick every five minutes of real time through the changeover.
			for now = c.start; now.Before(c.end); now = now.Add(5 * time.Minute) {
				rep, err := rs.Tick()
				if err != nil {
					t.Fatal(err)
				}
				if rep.Fired > 1 {
					t.Fatalf("%d occurrences in one tick at %s", rep.Fired, now)
				}
				if rep.Fired == 1 {
					fired = append(fired, now.In(ny))
					// The day's run finishes, so the next day's occurrence is not skipped as an overlap.
					if _, err := e.s.db.Exec(`UPDATE tasks SET state='done' WHERE rule_id=?`, LegacyRuleID("morning-check")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if len(fired) != c.wantDays {
				t.Fatalf("fired %d times, want one per day (%d): %v", len(fired), c.wantDays, fired)
			}
			days := map[string]bool{}
			for _, f := range fired {
				if f.Hour() != 9 || f.Minute() > 5 {
					t.Errorf("fired at local %s, want 09:00 New York time", f.Format("2006-01-02 15:04 MST"))
				}
				if days[f.Format("2006-01-02")] {
					t.Errorf("two occurrences on %s", f.Format("2006-01-02"))
				}
				days[f.Format("2006-01-02")] = true
			}
			if ts, _ := e.s.ListTasks(e.a.ID); len(ts) != c.wantDays {
				t.Fatalf("%d tasks, want %d: one occurrence, one task", len(ts), c.wantDays)
			}
		})
	}
}

// A wall-clock time that does not exist on the spring-forward day (02:30) never
// runs twice. The scheduler's cron library does not run it at all that day (its
// documented behaviour: "jobs scheduled during daylight-savings leap-ahead
// transitions will not be run"); every other day keeps 02:30. This pins what is
// true today so a change is a decision, not an accident — see AC-10 notes.
func TestGapAC10NonexistentLocalTimeNeverRunsTwiceAndIsSkippedThatDay(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tz database")
	}
	start, end := time.Date(2027, 3, 12, 12, 0, 0, 0, time.UTC), time.Date(2027, 3, 16, 12, 0, 0, 0, time.UTC)
	e := newRoutineEnv(t)
	e.clk.t = start
	got, err := e.draft(t, "ac10-gap", func(m map[string]any) {
		delete(m, "cron")
		m["schedule"] = "every day at 2:30"
		m["timezone"] = "America/New_York"
	})
	if err != nil {
		t.Fatal(err)
	}
	ap := got["approval"].(map[string]any)
	e.approve(t, ap["id"].(string), ap["actionHash"].(string))
	var now time.Time
	rs := &RuleScheduler{Store: e.s, Load: config.Load, Now: func() time.Time { return now }}
	perDay := map[string]int{}
	for now = start; now.Before(end); now = now.Add(5 * time.Minute) {
		rep, err := rs.Tick()
		if err != nil {
			t.Fatal(err)
		}
		if rep.Fired > 0 {
			perDay[now.In(ny).Format("2006-01-02")] += rep.Fired
			if _, err := e.s.db.Exec(`UPDATE tasks SET state='done' WHERE rule_id=?`, LegacyRuleID("morning-check")); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, day := range []string{"2027-03-13", "2027-03-15", "2027-03-16"} {
		if perDay[day] != 1 {
			t.Errorf("%s fired %d times, want exactly 1 (%v)", day, perDay[day], perDay)
		}
	}
	if perDay["2027-03-14"] != 0 {
		t.Errorf("the changeover day's nonexistent 02:30 ran %d times; today it is skipped (%v)", perDay["2027-03-14"], perDay)
	}
}
