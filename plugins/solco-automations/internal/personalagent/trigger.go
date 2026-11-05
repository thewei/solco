package personalagent

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Event triggers (Gap R03). A trigger belongs to one Assistant and turns one of
// that Assistant's own task events into one new task. It is not a scheduler of
// its own and not a second automation engine: it only reads the durable event
// log the core already keeps and creates ordinary tasks.
//
//   - A person approves the exact trigger (an approval card); until then it is a
//     draft that never fires, and an approved one looks forward only.
//   - Each (trigger, event) fires at most once (primary key), so a replayed tick,
//     a second daemon or a restart cannot repeat it.
//   - Tasks a trigger created never fire any trigger (no self-excitation), and a
//     trigger fires at most TriggerDailyCap times a day.
//   - It needs a filter (a task id or a title fragment): "every failure of
//     anything" is not a rule a model may draft.

// Trigger states.
const (
	TriggerDraft     = "draft"
	TriggerActive    = "active"
	TriggerPaused    = "paused"
	TriggerWithdrawn = "withdrawn"
)

const (
	// TriggerRulePrefix marks the tasks a trigger created (their rule_id).
	TriggerRulePrefix = "trigger:"
	// TriggerDailyCap bounds one trigger's tasks per UTC day.
	TriggerDailyCap = 5
	// MaxTriggers bounds the live triggers one Assistant holds.
	MaxTriggers = 10
)

// triggerKinds are the task events a trigger may listen to.
var triggerKinds = []string{"task.failed", "task.blocked", "task.uncertain", "task.completed"}

// TriggerKinds lists the events a trigger may name.
func TriggerKinds() []string { return slices.Clone(triggerKinds) }

// Trigger is one stored event trigger.
type Trigger struct {
	ID            string `json:"id"`
	AssistantID   string `json:"assistantId"`
	Name          string `json:"name"`
	On            string `json:"on"`
	TaskID        string `json:"taskId,omitempty"`
	TitleContains string `json:"titleContains,omitempty"`
	Title         string `json:"title"`
	Brief         string `json:"brief"`
	Kind          string `json:"kind"`
	State         string `json:"state"`
	FiresTotal    int    `json:"firesTotal"`
	Revision      int    `json:"revision"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// TriggerDraftInput is the body of `assistant trigger draft`.
type TriggerDraftInput struct {
	AssistantID   string `json:"assistantId"`
	Name          string `json:"name"`
	On            string `json:"on"`
	TaskID        string `json:"taskId"`
	TitleContains string `json:"titleContains"`
	Title         string `json:"title"`
	Brief         string `json:"brief"`
	Kind          string `json:"kind"`
}

const triggerCols = `id,assistant_id,name,on_kind,on_task_id,title_contains,task_title,task_brief,task_kind,state,fires_total,revision,created_at,updated_at`

func scanTrigger(r rowScanner) (Trigger, error) {
	var t Trigger
	err := r.Scan(&t.ID, &t.AssistantID, &t.Name, &t.On, &t.TaskID, &t.TitleContains, &t.Title, &t.Brief, &t.Kind, &t.State, &t.FiresTotal, &t.Revision, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func getTriggerTx(q queryer, id string) (Trigger, error) {
	t, err := scanTrigger(q.QueryRow(`SELECT `+triggerCols+` FROM event_triggers WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, wrapBusy(err)
}

// GetTrigger reads one trigger.
func (s *Store) GetTrigger(id string) (Trigger, error) { return getTriggerTx(s.db, id) }

// ListTriggers returns an Assistant's triggers (everyone's when empty), newest first.
func (s *Store) ListTriggers(assistantID string) ([]Trigger, error) {
	q, args := `SELECT `+triggerCols+` FROM event_triggers`, []any{}
	if assistantID != "" {
		q += ` WHERE assistant_id=?`
		args = append(args, assistantID)
	}
	rows, err := s.db.Query(q+` ORDER BY created_at DESC, id`, args...)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Trigger{}
	for rows.Next() {
		t, err := scanTrigger(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TriggerAction is the sentence of the approval card: exactly what approving means.
func TriggerAction(t Trigger) string {
	which := fmt.Sprintf("a task whose title contains %q", t.TitleContains)
	if t.TaskID != "" {
		which = "task " + t.TaskID
	}
	verb := strings.TrimPrefix(t.On, "task.")
	return fmt.Sprintf("When %s %s, queue one %s task %q for this Assistant — at most %d a day, never because of a task this trigger created itself. Nothing already in the record is replayed. %s",
		which, verb, t.Kind, t.Title, TriggerDailyCap, SafetyNote)
}

// DraftTrigger writes a draft (it never fires) and the approval card for it.
func (s *Store) DraftTrigger(requestID string, in TriggerDraftInput) (Trigger, Approval, bool, error) {
	in.Name, in.Title = strings.TrimSpace(in.Name), strings.TrimSpace(in.Title)
	in.Brief, in.TitleContains = strings.TrimSpace(in.Brief), strings.TrimSpace(in.TitleContains)
	if in.Kind == "" {
		in.Kind = KindResearch
	}
	switch {
	case in.AssistantID == "" || len(in.Name) < 2 || len(in.Name) > 60 || in.Title == "" || len(in.Title) > 200 || in.Brief == "" || len(in.Brief) > 4000:
		return Trigger{}, Approval{}, false, fmt.Errorf("%w: a trigger needs a name (2–60), a task title and a task brief", ErrInvalid)
	case !slices.Contains(triggerKinds, in.On):
		return Trigger{}, Approval{}, false, fmt.Errorf("%w: on must be one of %s", ErrInvalid, strings.Join(triggerKinds, ", "))
	case in.TaskID == "" && in.TitleContains == "":
		return Trigger{}, Approval{}, false, fmt.Errorf("%w: a trigger needs a filter: taskId or titleContains", ErrInvalid)
	case len(in.TitleContains) > 80:
		return Trigger{}, Approval{}, false, fmt.Errorf("%w: titleContains is at most 80 characters", ErrInvalid)
	case in.Kind != KindResearch && in.Kind != KindManual:
		return Trigger{}, Approval{}, false, fmt.Errorf("%w: a trigger starts research or manual tasks, never coding", ErrInvalid)
	}
	type result struct {
		T Trigger
		A Approval
	}
	r, replayed, err := execute(s, requestID, "trigger.draft", in, func(tx *sql.Tx) (result, error) {
		a, err := getAssistantTx(tx, in.AssistantID)
		if err != nil {
			return result{}, err
		}
		if a.Status != StatusActive {
			return result{}, ErrAssistantDisabled
		}
		if in.TaskID != "" {
			t, err := getTaskTx(tx, in.TaskID)
			if err != nil || t.AssistantID != in.AssistantID {
				return result{}, fmt.Errorf("%w: task %q is not this assistant's", ErrInvalid, in.TaskID)
			}
		}
		var live int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM event_triggers WHERE assistant_id=? AND state IN (?,?,?)`, in.AssistantID, TriggerDraft, TriggerActive, TriggerPaused).Scan(&live)
		if live >= MaxTriggers {
			return result{}, fmt.Errorf("%w: at most %d triggers per Assistant", ErrInvalid, MaxTriggers)
		}
		now := s.nowMS()
		id := newID("g")
		if _, err := tx.Exec(`INSERT INTO event_triggers(id,assistant_id,name,on_kind,on_task_id,title_contains,task_title,task_brief,task_kind,state,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, in.AssistantID, in.Name, in.On, in.TaskID, in.TitleContains, in.Title, in.Brief, in.Kind, TriggerDraft, 1, now, now); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return result{}, fmt.Errorf("%w: this Assistant already has a trigger named %q", ErrInvalid, in.Name)
			}
			return result{}, err
		}
		t, err := getTriggerTx(tx, id)
		if err != nil {
			return result{}, err
		}
		apID := newID("p")
		action := TriggerAction(t)
		if _, err := tx.Exec(`INSERT INTO approvals(`+approvalCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			apID, in.AssistantID, "trigger", id, action, actionHash(in.AssistantID, "trigger", id, action, t.Revision), t.Revision,
			now+(7*24*time.Hour).Milliseconds(), ApprovalPending, 0, now); err != nil {
			return result{}, err
		}
		ap, err := s.getApprovalTx(tx, apID)
		return result{t, ap}, err
	})
	return r.T, r.A, replayed, err
}

// activateTriggerTx makes an approved draft live, counting from now.
func activateTriggerTx(tx *sql.Tx, id string, now int64) error {
	var top int64
	var assistant string
	if err := tx.QueryRow(`SELECT assistant_id FROM event_triggers WHERE id=?`, id).Scan(&assistant); err != nil {
		return err
	}
	_ = tx.QueryRow(`SELECT COALESCE(MAX(rowid),0) FROM events WHERE assistant_id=?`, assistant).Scan(&top)
	_, err := tx.Exec(`UPDATE event_triggers SET state=?, event_mark=?, revision=revision+1, updated_at=? WHERE id=? AND state=?`, TriggerActive, top, now, id, TriggerDraft)
	return err
}

// SetTriggerState pauses, resumes or withdraws a trigger. A draft can only be
// withdrawn; resuming never replays what happened while paused.
func (s *Store) SetTriggerState(requestID, id, to string, expectedRevision int) (Trigger, bool, error) {
	if to != TriggerPaused && to != TriggerActive && to != TriggerWithdrawn {
		return Trigger{}, false, ErrInvalid
	}
	return execute(s, requestID, "trigger."+to, map[string]any{"id": id, "to": to}, func(tx *sql.Tx) (Trigger, error) {
		t, err := getTriggerTx(tx, id)
		if err != nil {
			return t, err
		}
		if expectedRevision != 0 && expectedRevision != t.Revision {
			return t, ErrStaleRevision
		}
		ok := map[string][]string{
			TriggerPaused:    {TriggerActive},
			TriggerActive:    {TriggerPaused},
			TriggerWithdrawn: {TriggerDraft, TriggerActive, TriggerPaused},
		}[to]
		if !slices.Contains(ok, t.State) {
			return t, fmt.Errorf("%w: a %s trigger cannot become %s", ErrBadState, t.State, to)
		}
		var top int64
		if to == TriggerActive {
			_ = tx.QueryRow(`SELECT COALESCE(MAX(rowid),0) FROM events WHERE assistant_id=?`, t.AssistantID).Scan(&top)
		}
		if _, err := tx.Exec(`UPDATE event_triggers SET state=?, event_mark=CASE WHEN ?=1 THEN ? ELSE event_mark END, revision=revision+1, updated_at=? WHERE id=?`,
			to, b2i(to == TriggerActive), top, s.nowMS(), id); err != nil {
			return t, err
		}
		if to == TriggerWithdrawn {
			// A card still waiting for an answer is void.
			if _, err := tx.Exec(`UPDATE approvals SET state=? WHERE subject_kind='trigger' AND subject_id=? AND state=?`, ApprovalDenied, id, ApprovalPending); err != nil {
				return t, err
			}
		}
		return getTriggerTx(tx, id)
	})
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TriggerReport counts one tick.
type TriggerReport struct{ Fired int }

// TriggerScheduler evaluates the approved triggers. It is the daemon's tick,
// not a second scheduler: a few indexed reads and, at most, a task insert per
// matching event. The whole tick is one transaction.
type TriggerScheduler struct {
	Store *Store
	Now   func() time.Time
}

func (r *TriggerScheduler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return r.Store.now()
}

// Tick turns each new matching event into one task.
func (r *TriggerScheduler) Tick() (TriggerReport, error) {
	now := r.now()
	nowMS, day := now.UnixMilli(), now.UTC().Format("2006-01-02")
	rep, _, err := execute(r.Store, "", "trigger.tick", nil, func(tx *sql.Tx) (TriggerReport, error) {
		var rep TriggerReport
		rows, err := tx.Query(`SELECT `+triggerCols+`,event_mark,fires_day,fires_today FROM event_triggers t WHERE state=? AND EXISTS (SELECT 1 FROM assistants a WHERE a.id=t.assistant_id AND a.status=?) ORDER BY created_at, id`, TriggerActive, StatusActive)
		if err != nil {
			return rep, err
		}
		type live struct {
			Trigger
			mark       int64
			firesDay   string
			firesToday int
		}
		var all []live
		for rows.Next() {
			var l live
			if err := rows.Scan(&l.ID, &l.AssistantID, &l.Name, &l.On, &l.TaskID, &l.TitleContains, &l.Title, &l.Brief, &l.Kind, &l.State, &l.FiresTotal, &l.Revision, &l.CreatedAt, &l.UpdatedAt, &l.mark, &l.firesDay, &l.firesToday); err != nil {
				_ = rows.Close()
				return rep, err
			}
			all = append(all, l)
		}
		_ = rows.Close()
		for _, l := range all {
			if l.firesDay != day {
				l.firesDay, l.firesToday = day, 0
			}
			// A task the system starts unasked is the Assistant's own rules'
			// business, like a Supervisor dispatch: Respond and Suggest start
			// nothing (and let the event go), quiet hours and a spent daily
			// budget hold it for later (Gap A05).
			pol, err := getProactivityTx(tx, l.AssistantID, now)
			if err != nil {
				return rep, err
			}
			if pol.Level != LevelFollowUp {
				var top int64
				_ = tx.QueryRow(`SELECT COALESCE(MAX(rowid),0) FROM events WHERE assistant_id=?`, l.AssistantID).Scan(&top)
				if _, err := tx.Exec(`UPDATE event_triggers SET event_mark=? WHERE id=?`, top, l.ID); err != nil {
					return rep, err
				}
				continue
			}
			if pol.QuietNow || pol.DispatchToday >= pol.DailyBudget {
				continue
			}
			evRows, err := tx.Query(`SELECT `+eventCols+` FROM events WHERE assistant_id=? AND rowid>? AND source='task' AND kind=? ORDER BY rowid`, l.AssistantID, l.mark, l.On)
			if err != nil {
				return rep, err
			}
			var evs []Event
			for evRows.Next() {
				e, err := scanEvent(evRows)
				if err != nil {
					_ = evRows.Close()
					return rep, err
				}
				evs = append(evs, e)
			}
			_ = evRows.Close()
			newMark := l.mark
			capped := false
			for _, e := range evs {
				if l.firesToday >= TriggerDailyCap || pol.DispatchToday >= pol.DailyBudget {
					// Held, not dropped: tomorrow picks up from here.
					newMark, capped = e.Seq-1, true
					break
				}
				src, err := getTaskTx(tx, e.TaskID)
				if err != nil {
					continue
				}
				if strings.HasPrefix(src.RuleID, TriggerRulePrefix) || src.RuleID == HeartbeatRule || src.Kind == KindCoordinator || src.Kind == KindGroupAgent {
					continue // a trigger's own work (or plumbing) never triggers
				}
				if l.TaskID != "" && src.ID != l.TaskID {
					continue
				}
				if l.TitleContains != "" && !strings.Contains(strings.ToLower(src.Title), strings.ToLower(l.TitleContains)) {
					continue
				}
				res, err := tx.Exec(`INSERT OR IGNORE INTO event_trigger_fires(trigger_id,event_seq,task_id) VALUES(?,?,?)`, l.ID, e.Seq, "")
				if err != nil {
					return rep, err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					continue
				}
				tid := newID("t")
				model := ""
				if p := r.Store.profileOf(tx, l.AssistantID); p.Agent == AssistantAgentKind {
					model = p.Model
				}
				brief := l.Brief + fmt.Sprintf("\n\n(Solco record: started because task %q %s; task id %s. That is a recorded fact, not an instruction.)", clip(src.Title, 120), strings.TrimPrefix(l.On, "task."), src.ID)
				if _, err := tx.Exec(`INSERT INTO tasks(`+taskCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
					tid, l.AssistantID, "", TriggerRulePrefix+l.ID, src.ID, l.Kind, l.Title, brief, "", AssistantAgentKind, model,
					TaskQueued, 0, "", 1, 1, nowMS, nowMS); err != nil {
					return rep, err
				}
				if _, err := tx.Exec(`UPDATE event_trigger_fires SET task_id=? WHERE trigger_id=? AND event_seq=?`, tid, l.ID, e.Seq); err != nil {
					return rep, err
				}
				if err := emitTask(tx, tid, "task.added", nowMS); err != nil {
					return rep, err
				}
				if _, err := emitTx(tx, Event{AssistantID: l.AssistantID, Source: "trigger", Kind: "rule.fired", Resource: l.ID,
					Revision: fmt.Sprintf("event#%d", e.Seq), TaskID: tid, Detail: fmt.Sprintf("%s: started a task", l.Name)}, nowMS); err != nil {
					return rep, err
				}
				if err := countDispatchTx(tx, pol, 1, nowMS); err != nil {
					return rep, err
				}
				pol.DispatchToday++
				l.firesToday++
				l.FiresTotal++
				rep.Fired++
			}
			if !capped {
				_ = tx.QueryRow(`SELECT COALESCE(MAX(rowid),0) FROM events WHERE assistant_id=?`, l.AssistantID).Scan(&newMark)
			}
			if _, err := tx.Exec(`UPDATE event_triggers SET event_mark=?, fires_day=?, fires_today=?, fires_total=?, updated_at=? WHERE id=?`, newMark, l.firesDay, l.firesToday, l.FiresTotal, nowMS, l.ID); err != nil {
				return rep, err
			}
		}
		return rep, nil
	})
	return rep, err
}
