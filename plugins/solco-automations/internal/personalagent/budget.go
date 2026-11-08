package personalagent

import (
	"database/sql"
	"fmt"
	"time"
)

// The budget ledger (Gap A05). The counters that actually stop the system —
// the daily dispatch budget in `proactivity`, the wake budget, a plan's task
// budget — stay where they are. This is the one place that says what they did:
// each task the system started on its own (spent) and each time it held back
// (held, with the reason), so an exhausted budget is visible instead of a silent
// stop. It carries ids and reasons only: no brief, goal text or Agent output.
//
// Not measured, and said so: model tokens and money. Agents run in Herdr panes
// and the core never sees their usage; a figure here would be invented.

const (
	BudgetSupervisor = "supervisor"
	BudgetTrigger    = "trigger"
	BudgetWake       = "wake"

	BudgetSpent = "spent"
	BudgetHeld  = "held"

	BudgetReasonBudget     = "budget"      // today's dispatch budget is used up
	BudgetReasonLevel      = "level"       // the Assistant is set to Respond/Suggest
	BudgetReasonQuiet      = "quiet"       // Quiet Hours
	BudgetReasonPlanTasks  = "plan_tasks"  // the goal's own task budget
	BudgetReasonWakeBudget = "wake_budget" // the approved daily wake allowance

	BudgetRetentionDays = 14
)

// BudgetEvent is one ledger row.
type BudgetEvent struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
	At      int64  `json:"at"`
}

// BudgetHold counts today's holds of one kind for one reason.
type BudgetHold struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// BudgetSummary is today's picture for one Assistant.
type BudgetSummary struct {
	AssistantID string         `json:"assistantId"`
	Day         string         `json:"day"`
	Level       string         `json:"level"`
	QuietNow    bool           `json:"quietNow"`
	DailyBudget int            `json:"dailyBudget"`
	SpentToday  int            `json:"spentToday"`
	Remaining   int            `json:"remaining"`
	Spent       map[string]int `json:"spent"`
	Held        []BudgetHold   `json:"held"`
	Wake        BudgetWakeInfo `json:"wake"`
	Recent      []BudgetEvent  `json:"recent"`
	// Unmeasured names what this ledger cannot know.
	Unmeasured []string `json:"unmeasured"`
}

// BudgetWakeInfo is the heartbeat's own approved allowance.
type BudgetWakeInfo struct {
	Approved  bool `json:"approved"`
	Budget    int  `json:"budget"`
	UsedToday int  `json:"usedToday"`
}

// execer is what both *sql.DB and *sql.Tx offer: the ledger is written from
// code that holds a transaction and code that does not.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// recordBudget writes one ledger row; the same (kind, ref, outcome, reason, day)
// is one row, so a hold repeated on every tick is recorded once a day. Rows
// older than the retention window go with the next write.
func (s *Store) recordBudget(q execer, assistantID, day, kind, ref, outcome, reason string) error {
	now := s.nowMS()
	res, err := q.Exec(`INSERT OR IGNORE INTO budget_events(assistant_id,day,kind,ref,outcome,reason,at,dedupe) VALUES(?,?,?,?,?,?,?,?)`,
		assistantID, day, kind, ref, outcome, reason, now, fmt.Sprintf("%s|%s|%s|%s|%s|%s", assistantID, kind, ref, outcome, reason, day))
	if err != nil {
		return wrapBusy(err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		cutoff := now - int64(BudgetRetentionDays)*24*60*60*1000
		_, err = q.Exec(`DELETE FROM budget_events WHERE at<?`, cutoff)
	}
	return wrapBusy(err)
}

// BudgetSummary reads today's ledger with the live limits; limit bounds the
// recent rows (1..100, default 20).
func (s *Store) BudgetSummary(assistantID string, limit int) (BudgetSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if _, err := s.GetAssistant(assistantID); err != nil {
		return BudgetSummary{}, err
	}
	now := time.UnixMilli(s.nowMS())
	pol, err := getProactivityTx(s.db, assistantID, now)
	if err != nil {
		return BudgetSummary{}, err
	}
	sum := BudgetSummary{
		AssistantID: assistantID, Day: pol.day, Level: pol.Level, QuietNow: pol.QuietNow,
		DailyBudget: pol.DailyBudget, SpentToday: pol.DispatchToday, Remaining: max(0, pol.DailyBudget-pol.DispatchToday),
		Spent: map[string]int{}, Held: []BudgetHold{}, Recent: []BudgetEvent{}, Unmeasured: []string{"tokens", "cost"},
	}
	if h, err := s.GetHeartbeat(assistantID); err == nil {
		sum.Wake = BudgetWakeInfo{Approved: h.WakeApproved, Budget: h.WakeBudget}
		if h.WakesDay == now.UTC().Format("2006-01-02") {
			sum.Wake.UsedToday = h.WakesToday
		}
	}
	rows, err := s.db.Query(`SELECT kind,outcome,reason,COUNT(*) FROM budget_events WHERE assistant_id=? AND day=? GROUP BY kind,outcome,reason ORDER BY kind,reason`, assistantID, pol.day)
	if err != nil {
		return sum, wrapBusy(err)
	}
	for rows.Next() {
		var kind, outcome, reason string
		var n int
		if err := rows.Scan(&kind, &outcome, &reason, &n); err != nil {
			_ = rows.Close()
			return sum, err
		}
		if outcome == BudgetSpent {
			sum.Spent[kind] += n
		} else {
			sum.Held = append(sum.Held, BudgetHold{Kind: kind, Reason: reason, Count: n})
		}
	}
	_ = rows.Close()
	recent, err := s.db.Query(`SELECT kind,ref,outcome,reason,at FROM budget_events WHERE assistant_id=? ORDER BY id DESC LIMIT ?`, assistantID, limit)
	if err != nil {
		return sum, wrapBusy(err)
	}
	defer recent.Close()
	for recent.Next() {
		var e BudgetEvent
		if err := recent.Scan(&e.Kind, &e.Ref, &e.Outcome, &e.Reason, &e.At); err != nil {
			return sum, err
		}
		sum.Recent = append(sum.Recent, e)
	}
	return sum, recent.Err()
}
