package personalagent

import (
	"database/sql"
	"fmt"
	"strings"
)

// The durable event log (docs/personal-agent-go-mvp-plan.md C01). Every event
// is a verified fact — a state the store itself committed or a PR state the
// watcher read — written in the same transaction as the thing that caused it,
// so no transition exists without its event and none is lost to a crash. The
// UNIQUE(source, resource, revision) key dedupes: the same fact reported three
// times is one event. `seq` (the row id) is the watermark a reader resumes
// from; `priority` orders what a reader looks at first.

// Event kinds and their default priority (higher = look first).
var eventPriority = map[string]int{
	"task.added":               0,
	"task.completed":           1,
	"task.blocked":             2,
	"task.uncertain":           2,
	"task.failed":              3,
	"pr.changed":               0,
	"pr.merged":                2,
	"goal.progress":            0,
	"git.main_red":             3,
	"review.ready":             2,
	"review.failed":            3,
	"review.merged":            2,
	"review.changes_requested": 2,
	"review.rejected":          2,
	"rule.fired":               1,
	"rule.missed":              2,
	"rule.skipped":             0,
	"rule.queued":              0,
	"hook.check":               2,
}

// EventKinds lists the kinds a heartbeat rule may name.
func EventKinds() []string {
	out := make([]string, 0, len(eventPriority))
	for k := range eventPriority {
		out = append(out, k)
	}
	return out
}

// Event is one durable fact. Detail is a short sentence of ids and titles;
// it never carries a prompt, a brief, a result or an error text.
type Event struct {
	Seq          int64  `json:"seq"`
	ID           string `json:"id"`
	AssistantID  string `json:"assistantId"`
	CommitmentID string `json:"commitmentId,omitempty"`
	Source       string `json:"source"`
	Kind         string `json:"kind"`
	Resource     string `json:"resource"`
	Revision     string `json:"revision"`
	TaskID       string `json:"taskId,omitempty"`
	Priority     int    `json:"priority"`
	Detail       string `json:"detail"`
	SeenAt       int64  `json:"seenAt"`
}

const eventCols = `rowid,id,assistant_id,commitment_id,source,kind,resource,revision,task_id,priority,detail,seen_at`

func scanEvent(r rowScanner) (Event, error) {
	var e Event
	err := r.Scan(&e.Seq, &e.ID, &e.AssistantID, &e.CommitmentID, &e.Source, &e.Kind, &e.Resource, &e.Revision, &e.TaskID, &e.Priority, &e.Detail, &e.SeenAt)
	return e, err
}

// emitTx writes one event inside the caller's transaction. It reports whether
// the fact was new.
func emitTx(tx *sql.Tx, e Event, now int64) (bool, error) {
	if _, ok := eventPriority[e.Kind]; !ok {
		return false, fmt.Errorf("%w: event kind %q", ErrInvalid, e.Kind)
	}
	if e.Source == "" || e.Resource == "" || e.Revision == "" || e.AssistantID == "" {
		return false, fmt.Errorf("%w: an event needs an assistant, source, resource and revision", ErrInvalid)
	}
	if e.Priority == 0 {
		e.Priority = eventPriority[e.Kind]
	}
	if e.ID == "" {
		e.ID = newID("e")
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO events(id,assistant_id,commitment_id,source,kind,resource,revision,task_id,priority,detail,seen_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.AssistantID, e.CommitmentID, e.Source, e.Kind, e.Resource, e.Revision, e.TaskID, e.Priority, clip(e.Detail, 240), now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// emitTask records a task's transition. Heartbeat tasks are the heartbeat's
// own; their events exist but the heartbeat ignores them (no wake loops).
func emitTask(tx *sql.Tx, taskID, kind string, now int64) error {
	var assistant, title, state, taskKind string
	var attempt int
	if err := tx.QueryRow(`SELECT assistant_id,title,state,attempt,kind FROM tasks WHERE id=?`, taskID).Scan(&assistant, &title, &state, &attempt, &taskKind); err != nil {
		return err
	}
	// A Coordinator coming up or settling is not news; only its failure is.
	if (taskKind == KindCoordinator || taskKind == KindGroupAgent) && (kind == "task.added" || kind == "task.completed") {
		return nil
	}
	verb := strings.TrimPrefix(kind, "task.")
	_, err := emitTx(tx, Event{
		AssistantID: assistant, Source: "task", Kind: kind, Resource: taskID,
		Revision: fmt.Sprintf("%s#%d", verb, attempt), TaskID: taskID,
		Detail: fmt.Sprintf("%s: %s", title, verb),
	}, now)
	return err
}

// RecordEvent writes one verified external fact (a failing main branch, …).
// The same (source, resource, revision) is recorded once.
func (s *Store) RecordEvent(e Event) (Event, bool, error) {
	var out Event
	created := false
	_, _, err := execute(s, "", "event.record", nil, func(tx *sql.Tx) (struct{}, error) {
		if _, err := getAssistantTx(tx, e.AssistantID); err != nil {
			return struct{}{}, err
		}
		made, err := emitTx(tx, e, s.nowMS())
		if err != nil {
			return struct{}{}, err
		}
		created = made
		got, err := scanEvent(tx.QueryRow(`SELECT `+eventCols+` FROM events WHERE source=? AND resource=? AND revision=?`, e.Source, e.Resource, e.Revision))
		out = got
		return struct{}{}, err
	})
	return out, created, err
}

// Events returns an Assistant's events after a watermark, oldest first.
func (s *Store) Events(assistantID string, afterSeq int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT `+eventCols+` FROM events WHERE assistant_id=? AND rowid>? ORDER BY rowid LIMIT ?`, assistantID, afterSeq, limit)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, wrapBusy(rows.Err())
}

// AddProgress records a progress note on a Goal as an event: evidence of what
// was checked, not a state change (completion stays CompleteGoal's, with its
// own evidence).
func (s *Store) AddProgress(requestID, goalID, note string) (Event, bool, error) {
	note = strings.TrimSpace(note)
	if goalID == "" || note == "" {
		return Event{}, false, fmt.Errorf("%w: progress needs a goal and a note", ErrInvalid)
	}
	return execute(s, requestID, "goal.progress", map[string]string{"goal": goalID, "note": note}, func(tx *sql.Tx) (Event, error) {
		g, err := getGoalTx(tx, goalID)
		if err != nil {
			return Event{}, err
		}
		if g.State == GoalCancelled || g.State == GoalCompleted {
			return Event{}, fmt.Errorf("%w: the goal is %s", ErrBadState, g.State)
		}
		id := newID("e")
		now := s.nowMS()
		if _, err := emitTx(tx, Event{ID: id, AssistantID: g.AssistantID, Source: "goal", Kind: "goal.progress", Resource: g.ID, Revision: id, Detail: note}, now); err != nil {
			return Event{}, err
		}
		return scanEvent(tx.QueryRow(`SELECT `+eventCols+` FROM events WHERE id=?`, id))
	})
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// emitRunTask is emitTask for the task a run belongs to.
func emitRunTask(tx *sql.Tx, runID, kind string, now int64) error {
	var taskID string
	if err := tx.QueryRow(`SELECT task_id FROM runs WHERE id=?`, runID).Scan(&taskID); err != nil {
		return err
	}
	return emitTask(tx, taskID, kind, now)
}
