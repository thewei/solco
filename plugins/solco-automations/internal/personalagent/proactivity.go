package personalagent

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Proactivity levels: how much the system may start on its own for one
// Assistant. They govern only what Solco begins unasked — a Goal's next
// milestone, a stalled-goal reminder, a heartbeat's model wake. Replies to a
// message, work a person delegated, and schedules a person approved are never
// held back by them. "Approved autonomy" (acting on goals without an approved
// plan) is deliberately not a level yet.
const (
	LevelRespond  = "respond"   // nothing unasked
	LevelSuggest  = "suggest"   // says what it would do next; starts nothing
	LevelFollowUp = "follow_up" // carries approved Goals forward within budgets (the U3 behaviour)

	DefaultDailyBudget = 20
	MaxDailyBudget     = 100
)

// Proactivity is one Assistant's rules. Revision 0 means "never set": the
// defaults below apply and nothing is stored.
type Proactivity struct {
	AssistantID    string `json:"assistantId"`
	Level          string `json:"level"`
	QuietStart     string `json:"quietStart"`
	QuietEnd       string `json:"quietEnd"`
	TZ             string `json:"tz"`
	ReportEveryMin int    `json:"reportEveryMin"`
	DailyBudget    int    `json:"dailyBudget"`
	DispatchToday  int    `json:"dispatchToday"`
	QuietNow       bool   `json:"quietNow"`
	Revision       int    `json:"revision"`
	UpdatedAt      int64  `json:"updatedAt"`

	day string
}

// ProactivityInput is the body of `assistant proactivity set`.
type ProactivityInput struct {
	AssistantID    string `json:"assistantId"`
	Level          string `json:"level"`
	QuietStart     string `json:"quietStart"`
	QuietEnd       string `json:"quietEnd"`
	TZ             string `json:"tz"`
	ReportEveryMin int    `json:"reportEveryMin"`
	DailyBudget    int    `json:"dailyBudget"`
}

func (p Proactivity) location() *time.Location {
	if p.TZ != "" {
		if l, err := time.LoadLocation(p.TZ); err == nil {
			return l
		}
	}
	return time.Local
}

func parseClock(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// quietAt reports whether t falls inside the quiet window (which may wrap
// midnight). No window, or an unparseable one, is never quiet.
func (p Proactivity) quietAt(t time.Time) bool {
	a, ok1 := parseClock(p.QuietStart)
	b, ok2 := parseClock(p.QuietEnd)
	if !ok1 || !ok2 || a == b {
		return false
	}
	lt := t.In(p.location())
	m := lt.Hour()*60 + lt.Minute()
	if a < b {
		return m >= a && m < b
	}
	return m >= a || m < b
}

func defaultProactivity(id string) Proactivity {
	return Proactivity{AssistantID: id, Level: LevelFollowUp, DailyBudget: DefaultDailyBudget}
}

func getProactivityTx(q queryer, id string, now time.Time) (Proactivity, error) {
	p := defaultProactivity(id)
	err := q.QueryRow(`SELECT level,quiet_start,quiet_end,tz,report_every_min,daily_budget,dispatch_day,dispatch_today,revision,updated_at FROM proactivity WHERE assistant_id=?`, id).
		Scan(&p.Level, &p.QuietStart, &p.QuietEnd, &p.TZ, &p.ReportEveryMin, &p.DailyBudget, &p.day, &p.DispatchToday, &p.Revision, &p.UpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, wrapBusy(err)
	}
	today := now.In(p.location()).Format("2006-01-02")
	if p.day != today {
		p.DispatchToday = 0
	}
	p.day = today
	p.QuietNow = p.quietAt(now)
	return p, nil
}

// GetProactivity reads the rules (defaults when never set).
func (s *Store) GetProactivity(assistantID string) (Proactivity, error) {
	if _, err := s.GetAssistant(assistantID); err != nil {
		return Proactivity{}, err
	}
	return getProactivityTx(s.db, assistantID, time.UnixMilli(s.nowMS()))
}

func validateProactivity(in ProactivityInput) error {
	switch in.Level {
	case LevelRespond, LevelSuggest, LevelFollowUp:
	default:
		return fmt.Errorf("%w: level is respond, suggest or follow_up", ErrInvalid)
	}
	_, a := parseClock(in.QuietStart)
	_, b := parseClock(in.QuietEnd)
	if (in.QuietStart == "") != (in.QuietEnd == "") || (in.QuietStart != "" && !(a && b)) {
		return fmt.Errorf("%w: quiet hours are both times as HH:MM, or neither", ErrInvalid)
	}
	if in.TZ != "" {
		if _, err := time.LoadLocation(in.TZ); err != nil {
			return fmt.Errorf("%w: unknown time zone %q", ErrInvalid, in.TZ)
		}
	}
	if in.ReportEveryMin < 0 || in.ReportEveryMin > 1440 {
		return fmt.Errorf("%w: reportEveryMin is 0..1440", ErrInvalid)
	}
	if in.DailyBudget < 1 || in.DailyBudget > MaxDailyBudget {
		return fmt.Errorf("%w: dailyBudget is 1..%d tasks a day", ErrInvalid, MaxDailyBudget)
	}
	return nil
}

// SetProactivity writes the rules (expectedRevision 0 creates them).
func (s *Store) SetProactivity(requestID string, in ProactivityInput, expectedRevision int) (Proactivity, bool, error) {
	if err := validateProactivity(in); err != nil {
		return Proactivity{}, false, err
	}
	return execute(s, requestID, "proactivity.set", map[string]any{"in": in, "rev": expectedRevision}, func(tx *sql.Tx) (Proactivity, error) {
		if _, err := getAssistantTx(tx, in.AssistantID); err != nil {
			return Proactivity{}, err
		}
		now := time.UnixMilli(s.nowMS())
		cur, err := getProactivityTx(tx, in.AssistantID, now)
		if err != nil {
			return cur, err
		}
		if cur.Revision != expectedRevision {
			return cur, ErrStaleRevision
		}
		if cur.Revision == 0 {
			_, err = tx.Exec(`INSERT INTO proactivity(assistant_id,level,quiet_start,quiet_end,tz,report_every_min,daily_budget,revision,updated_at) VALUES(?,?,?,?,?,?,?,?,?)
				ON CONFLICT(assistant_id) DO UPDATE SET level=excluded.level,quiet_start=excluded.quiet_start,quiet_end=excluded.quiet_end,tz=excluded.tz,
				report_every_min=excluded.report_every_min,daily_budget=excluded.daily_budget,revision=1,updated_at=excluded.updated_at`,
				in.AssistantID, in.Level, in.QuietStart, in.QuietEnd, in.TZ, in.ReportEveryMin, in.DailyBudget, 1, now.UnixMilli())
		} else {
			_, err = tx.Exec(`UPDATE proactivity SET level=?,quiet_start=?,quiet_end=?,tz=?,report_every_min=?,daily_budget=?,revision=revision+1,updated_at=? WHERE assistant_id=?`,
				in.Level, in.QuietStart, in.QuietEnd, in.TZ, in.ReportEveryMin, in.DailyBudget, now.UnixMilli(), in.AssistantID)
		}
		if err != nil {
			return cur, err
		}
		return getProactivityTx(tx, in.AssistantID, now)
	})
}

// countDispatch adds n tasks the system started on its own to today's tally.
func (s *Store) countDispatch(assistantID string, n int) error {
	p, err := getProactivityTx(s.db, assistantID, time.UnixMilli(s.nowMS()))
	if err != nil {
		return err
	}
	if p.Revision == 0 {
		// Never configured: keep the default budget in a row of its own.
		_, err = s.db.Exec(`INSERT OR IGNORE INTO proactivity(assistant_id,level,daily_budget,revision,updated_at) VALUES(?,?,?,0,?)`,
			assistantID, LevelFollowUp, DefaultDailyBudget, s.nowMS())
		if err != nil {
			return wrapBusy(err)
		}
	}
	_, err = s.db.Exec(`UPDATE proactivity SET dispatch_day=?, dispatch_today=? WHERE assistant_id=?`, p.day, p.DispatchToday+n, assistantID)
	return wrapBusy(err)
}
