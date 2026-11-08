// [INPUT]: the words a person used for a schedule, a time zone, "now"
// [OUTPUT]: a cron expression pinned to a zone plus its next three runs — or a question
// [POS]: Gap R02 — the single words→rule parser; the scheduler still only ever sees cron
// [PROTOCOL]: Update header on changes; read project CLAUDE.md
package schedule

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// ErrOneTime is a one-time request. The plugin's scheduler only knows
// recurring cron rules; a "once" that silently repeats yearly would be a lie.
var ErrOneTime = errors.New("one-time schedules are not supported: give a recurring schedule, or start the work now as a task")

// NeedsClarification means the words did not pin down a schedule. The caller
// asks the person; nothing is guessed.
type NeedsClarification struct{ Question string }

func (n *NeedsClarification) Error() string { return "needs_clarification: " + n.Question }

// Parsed is a schedule in machine-checkable form.
type Parsed struct {
	Cron     string      // "CRON_TZ=<zone> m h dom mon dow"
	Timezone string      // IANA name
	Summary  string      // for the approval card
	Next     []time.Time // the next three runs
}

var ianaRe = regexp.MustCompile(`\b([A-Z][A-Za-z_]+/[A-Za-z_]+(?:/[A-Za-z_]+)?)\b`)

var weekdayWords = []struct {
	re  *regexp.Regexp
	dow string
}{
	{regexp.MustCompile(`\b(mon(day)?s?)\b|周一|星期一|礼拜一`), "1"},
	{regexp.MustCompile(`\b(tue(s|sday)?s?)\b|周二|星期二|礼拜二`), "2"},
	{regexp.MustCompile(`\b(wed(nesday)?s?)\b|周三|星期三|礼拜三`), "3"},
	{regexp.MustCompile(`\b(thu(r|rs|rsday)?s?)\b|周四|星期四|礼拜四`), "4"},
	{regexp.MustCompile(`\b(fri(day)?s?)\b|周五|星期五|礼拜五`), "5"},
	{regexp.MustCompile(`\b(sat(urday)?s?)\b|周六|星期六|礼拜六`), "6"},
	{regexp.MustCompile(`\b(sun(day)?s?)\b|周日|周天|星期日|星期天|礼拜天`), "0"},
}

var (
	oneTimeRe   = regexp.MustCompile(`\b(once|one[- ]time|one[- ]off|tomorrow|today|tonight|next (mon|tue|wed|thu|fri|sat|sun)\w*)\b|明天|后天|今天|今晚|一次性|仅一次`)
	weekdaysRe  = regexp.MustCompile(`weekdays?|work ?days?|business days?|mon(day)?\s*(-|to|through)\s*fri(day)?|工作日|周一到周五|周一至周五|星期一到星期五`)
	weekendsRe  = regexp.MustCompile(`weekends?|周末`)
	dailyRe     = regexp.MustCompile(`\b(daily|every ?day|each day|everyday)\b|每天|每日|天天`)
	hourlyRe    = regexp.MustCompile(`\bhourly\b|every hour\b|每小时|每个小时`)
	everyMinRe  = regexp.MustCompile(`every\s+(\d+)\s*(?:min(?:ute)?s?)\b|每\s*(\d+)\s*分钟`)
	everyHourRe = regexp.MustCompile(`every\s+(\d+)\s*(?:h(?:ours?|rs?)?)\b|每\s*(\d+)\s*(?:个)?小时`)
	clockRe     = regexp.MustCompile(`(\d{1,2})\s*(?:[:：]\s*(\d{1,2}))?\s*(am|pm|a\.m\.|p\.m\.)?`)
	zhClockRe   = regexp.MustCompile(`(\d{1,2})\s*[点時时](?:\s*(\d{1,2})\s*分?|\s*(半))?`)
	dayPartRe   = regexp.MustCompile(`早上|早晨|上午|中午|下午|傍晚|晚上|夜里|凌晨|\b(morning|noon|afternoon|evening|night)\b`)
)

// ParseText reads a recurring schedule written in English or Chinese. def is
// the zone used when the words name none; now anchors the next-run preview.
func ParseText(text string, def *time.Location, now time.Time) (Parsed, error) {
	raw := strings.TrimSpace(text)
	ask := func(q string) (Parsed, error) { return Parsed{}, &NeedsClarification{Question: q} }
	if raw == "" {
		return ask("When should this run? Say the days and the time, for example \"every weekday at 9:00\".")
	}
	loc := def
	zoneName := ""
	if m := ianaRe.FindStringSubmatch(raw); m != nil {
		l, err := time.LoadLocation(m[1])
		if err != nil {
			return ask(fmt.Sprintf("I do not know the time zone %q. Which IANA zone (for example Asia/Shanghai) do you mean?", m[1]))
		}
		loc, zoneName = l, m[1]
		raw = strings.Replace(raw, m[1], " ", 1)
	}
	if loc == nil {
		return ask("Which time zone should this schedule use (for example Asia/Shanghai)?")
	}
	if zoneName == "" {
		zoneName = loc.String()
		if zoneName == "Local" || zoneName == "" {
			return ask("Which time zone should this schedule use (for example Asia/Shanghai)?")
		}
	}
	s := strings.ToLower(raw)
	if oneTimeRe.MatchString(s) {
		return Parsed{}, ErrOneTime
	}

	var fields string // "m h dom mon dow"
	switch {
	case everyMinRe.MatchString(s):
		m := everyMinRe.FindStringSubmatch(s)
		n, _ := strconv.Atoi(first(m[1], m[2]))
		if n < 1 || n > 30 || 60%n != 0 {
			return ask("Cron can repeat every 1, 2, 3, 4, 5, 6, 10, 12, 15, 20 or 30 minutes evenly. Which interval do you want?")
		}
		fields = fmt.Sprintf("*/%d * * * *", n)
	case everyHourRe.MatchString(s):
		m := everyHourRe.FindStringSubmatch(s)
		n, _ := strconv.Atoi(first(m[1], m[2]))
		if n < 1 || n > 12 || 24%n != 0 {
			return ask("Cron can repeat every 1, 2, 3, 4, 6, 8 or 12 hours evenly. Which interval do you want?")
		}
		fields = fmt.Sprintf("0 */%d * * *", n)
	case hourlyRe.MatchString(s):
		fields = "0 * * * *"
	default:
		dow, ok := daysOf(s)
		if !ok {
			return ask("Which days should it run on (every day, every weekday, weekends, or named weekdays), and at what time?")
		}
		h, m, ok, err := clockOf(s)
		if err != nil {
			return ask(err.Error())
		}
		if !ok {
			return ask("At what time of day should it run? For example 9:00 or 6:30pm.")
		}
		fields = fmt.Sprintf("%d %d * * %s", m, h, dow)
	}
	expr := "CRON_TZ=" + zoneName + " " + fields
	sched, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(expr)
	if err != nil {
		return ask("I could not turn that into a schedule: " + err.Error())
	}
	t := now
	next := make([]time.Time, 0, 3)
	for range 3 {
		t = sched.Next(t)
		next = append(next, t)
	}
	return Parsed{Cron: expr, Timezone: zoneName, Summary: scheduleTextOf(expr), Next: next}, nil
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func daysOf(s string) (string, bool) {
	switch {
	case weekdaysRe.MatchString(s):
		return "1-5", true
	case weekendsRe.MatchString(s):
		return "0,6", true
	}
	var days []string
	for _, w := range weekdayWords {
		if w.re.MatchString(s) {
			days = append(days, w.dow)
		}
	}
	if len(days) > 0 {
		return strings.Join(sortedDays(days), ","), true
	}
	if dailyRe.MatchString(s) {
		return "*", true
	}
	return "", false
}

func sortedDays(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// clockOf finds one time of day. ok=false means none was said.
func clockOf(s string) (h, m int, ok bool, err error) {
	part := dayPartRe.FindString(s)
	var hs, ms, ap string
	if z := zhClockRe.FindStringSubmatch(s); z != nil {
		hs, ms = z[1], z[2]
		if z[3] != "" {
			ms = "30"
		}
	} else if c := clockRe.FindStringSubmatch(s); c != nil && (c[2] != "" || c[3] != "" || strings.Contains(s, "at ") || strings.Contains(s, "@")) {
		hs, ms, ap = c[1], c[2], strings.ReplaceAll(c[3], ".", "")
	} else {
		return 0, 0, false, nil
	}
	h, _ = strconv.Atoi(hs)
	if ms != "" {
		m, _ = strconv.Atoi(ms)
	}
	if h > 23 || m > 59 {
		return 0, 0, false, fmt.Errorf("%s:%02d is not a time of day. What time do you mean?", hs, m)
	}
	switch {
	case ap == "pm" && h < 12:
		h += 12
	case ap == "am" && h == 12:
		h = 0
	case ap == "" && h < 12 && (part == "下午" || part == "晚上" || part == "傍晚" || part == "夜里" || part == "afternoon" || part == "evening" || part == "night"):
		h += 12
	case ap == "" && h == 12 && (part == "凌晨"):
		h = 0
	}
	return h, m, true, nil
}

var tzPrefix = regexp.MustCompile(`^(?:CRON_)?TZ=(\S+)\s+(.*)$`)

// scheduleTextOf renders a cron expression (optionally "CRON_TZ=<zone> ")
// in words; see ScheduleText.
func scheduleTextOf(expr string) string {
	expr = strings.TrimSpace(expr)
	if m := tzPrefix.FindStringSubmatch(expr); m != nil {
		return scheduleTextPlain(m[2]) + " (" + m[1] + ")"
	}
	return scheduleTextPlain(expr)
}

// LocalZoneName names the Mac's own IANA zone, or "" when it cannot be told
// ("Local" is not a name a schedule can be pinned to).
func LocalZoneName() string {
	if tz := os.Getenv("TZ"); tz != "" && tz != "Local" {
		return strings.TrimPrefix(tz, ":")
	}
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(link, "zoneinfo/"); i >= 0 {
			return filepath.ToSlash(link[i+len("zoneinfo/"):])
		}
	}
	return ""
}
