package schedule

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Gap R02: one parser turns the words a person says into a machine-checkable
// cron in an explicit time zone. It never guesses.
func TestParseTextTurnsWordsIntoCronInAZone(t *testing.T) {
	sh := mustLoc(t, "Asia/Shanghai")
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, sh) // a Friday
	cases := []struct{ in, want string }{
		{"every weekday at 9:00", "CRON_TZ=Asia/Shanghai 0 9 * * 1-5"},
		{"Every weekday at 9am", "CRON_TZ=Asia/Shanghai 0 9 * * 1-5"},
		{"每个工作日早上9点", "CRON_TZ=Asia/Shanghai 0 9 * * 1-5"},
		{"每个工作日早上 9:00", "CRON_TZ=Asia/Shanghai 0 9 * * 1-5"},
		{"daily at 6:30pm", "CRON_TZ=Asia/Shanghai 30 18 * * *"},
		{"每天晚上8点半", "CRON_TZ=Asia/Shanghai 30 20 * * *"},
		{"every day at 12am", "CRON_TZ=Asia/Shanghai 0 0 * * *"},
		{"every day at 12pm", "CRON_TZ=Asia/Shanghai 0 12 * * *"},
		{"every 30 minutes", "CRON_TZ=Asia/Shanghai */30 * * * *"},
		{"每30分钟", "CRON_TZ=Asia/Shanghai */30 * * * *"},
		{"hourly", "CRON_TZ=Asia/Shanghai 0 * * * *"},
		{"每小时", "CRON_TZ=Asia/Shanghai 0 * * * *"},
		{"every 2 hours", "CRON_TZ=Asia/Shanghai 0 */2 * * *"},
		{"every monday and thursday at 08:15", "CRON_TZ=Asia/Shanghai 15 8 * * 1,4"},
		{"每周一 10:00", "CRON_TZ=Asia/Shanghai 0 10 * * 1"},
		{"weekends at 10:00", "CRON_TZ=Asia/Shanghai 0 10 * * 0,6"},
		{"每周末上午10点", "CRON_TZ=Asia/Shanghai 0 10 * * 0,6"},
	}
	for _, c := range cases {
		got, err := ParseText(c.in, sh, now)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got.Cron != c.want {
			t.Errorf("%q: cron = %q, want %q", c.in, got.Cron, c.want)
		}
		if got.Timezone != "Asia/Shanghai" || len(got.Next) != 3 {
			t.Errorf("%q: zone %q next %v", c.in, got.Timezone, got.Next)
		}
	}
}

func TestParseTextAsksInsteadOfGuessing(t *testing.T) {
	sh := mustLoc(t, "Asia/Shanghai")
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, sh)
	for _, in := range []string{"every weekday", "每天", "sometimes", "", "every day at 25:00", "every 0 minutes", "every 90 minutes", "every day at 9:75"} {
		_, err := ParseText(in, sh, now)
		var nc *NeedsClarification
		if !errors.As(err, &nc) {
			t.Errorf("%q: err = %v, want a NeedsClarification", in, err)
		}
	}
	for _, in := range []string{"tomorrow at 9am", "once at 9am", "明天早上9点", "just once next friday"} {
		_, err := ParseText(in, sh, now)
		if !errors.Is(err, ErrOneTime) {
			t.Errorf("%q: err = %v, want ErrOneTime", in, err)
		}
	}
}

func TestParseTextNextThreeRunsAreInThePersonsZone(t *testing.T) {
	sh := mustLoc(t, "Asia/Shanghai")
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, sh) // Friday after 09:00
	got, err := ParseText("every weekday at 9:00", sh, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{
		time.Date(2026, 11, 9, 9, 0, 0, 0, sh), time.Date(2026, 11, 10, 9, 0, 0, 0, sh), time.Date(2026, 11, 11, 9, 0, 0, 0, sh),
	}
	for i := range want {
		if !got.Next[i].Equal(want[i]) {
			t.Errorf("next[%d] = %s, want %s", i, got.Next[i], want[i])
		}
	}
	if !strings.Contains(got.Summary, "09:00") || !strings.Contains(got.Summary, "Asia/Shanghai") {
		t.Errorf("summary = %q", got.Summary)
	}
}

// DST: the schedule stays on the wall clock across the spring-forward gap.
func TestParseTextKeepsTheWallClockAcrossDST(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	now := time.Date(2026, 3, 7, 8, 0, 0, 0, ny) // first run is before the change, the rest after
	got, err := ParseText("daily at 9:00", ny, now)
	if err != nil {
		t.Fatal(err)
	}
	for i, n := range got.Next {
		if h, m, _ := n.In(ny).Clock(); h != 9 || m != 0 {
			t.Errorf("next[%d] = %s, want 09:00 local", i, n.In(ny))
		}
	}
	if _, off1 := got.Next[0].In(ny).Zone(); func() bool { _, off2 := got.Next[2].In(ny).Zone(); return off1 == off2 }() {
		t.Errorf("the test window must cross the DST change")
	}
}

func TestParseTextHonoursAnExplicitZoneInTheWords(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	got, err := ParseText("every day at 9:00 Europe/London", mustLoc(t, "Asia/Shanghai"), now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Timezone != "Europe/London" || got.Cron != "CRON_TZ=Europe/London 0 9 * * *" {
		t.Fatalf("got %+v", got)
	}
}

func TestScheduleTextSeesThroughATimezonePrefix(t *testing.T) {
	got := scheduleTextOf("CRON_TZ=Asia/Shanghai 0 9 * * 1-5")
	if got != "Weekdays 09:00 (Asia/Shanghai)" {
		t.Fatalf("got %q", got)
	}
}
