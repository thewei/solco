package personalagent

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Goal (and Commitment) states. A Goal starts as a draft a person approves;
// "completed" is only ever reached by an explicit command that names its
// evidence — an Agent finishing a turn never completes anything.
const (
	GoalDraft     = "draft"
	GoalActive    = "active"
	GoalPaused    = "paused"
	GoalCompleted = "completed"
	GoalCancelled = "cancelled"
)

// Commitment is one thing a Goal promises to keep watching or doing.
type Commitment struct {
	ID              string `json:"id"`
	AssistantID     string `json:"assistantId"`
	GoalID          string `json:"goalId"`
	Kind            string `json:"kind"`
	SuccessCriteria string `json:"successCriteria"`
	ExitCondition   string `json:"exitCondition"`
	Spec            string `json:"spec"`
	State           string `json:"state"`
	NextCheckAt     int64  `json:"nextCheckAt"`
	LastCheckedAt   int64  `json:"lastCheckedAt"`
	LastError       string `json:"lastError"`
	Cursor          string `json:"cursor"`
	Revision        int    `json:"revision"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

// Goal is a long-lived objective owned by one Assistant.
type Goal struct {
	ID          string       `json:"id"`
	AssistantID string       `json:"assistantId"`
	Title       string       `json:"title"`
	Objective   string       `json:"objective"`
	State       string       `json:"state"`
	Evidence    string       `json:"evidence"`
	Commitments []Commitment `json:"commitments"`
	Revision    int          `json:"revision"`
	CreatedAt   int64        `json:"createdAt"`
	UpdatedAt   int64        `json:"updatedAt"`
}

// CommitmentInput is a commitment drafted together with its Goal.
type CommitmentInput struct {
	Kind            string `json:"kind"`
	SuccessCriteria string `json:"successCriteria,omitempty"`
	ExitCondition   string `json:"exitCondition,omitempty"`
	Spec            string `json:"spec,omitempty"`
}

// CreateGoalInput is the body of `assistant goal create`.
type CreateGoalInput struct {
	AssistantID string            `json:"assistantId"`
	Title       string            `json:"title"`
	Objective   string            `json:"objective,omitempty"`
	Commitments []CommitmentInput `json:"commitments,omitempty"`
}

const goalCols = `id,assistant_id,title,objective,state,evidence,revision,created_at,updated_at`
const commitmentCols = `id,assistant_id,goal_id,kind,success_criteria,exit_condition,spec,state,next_check_at,last_checked_at,last_error,cursor,revision,created_at,updated_at`

type queryer interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

func getGoalTx(q queryer, id string) (Goal, error) {
	var g Goal
	err := q.QueryRow(`SELECT `+goalCols+` FROM goals WHERE id=?`, id).
		Scan(&g.ID, &g.AssistantID, &g.Title, &g.Objective, &g.State, &g.Evidence, &g.Revision, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, wrapBusy(err)
	}
	g.Commitments, err = commitmentsOf(q, id)
	return g, err
}

func commitmentsOf(q queryer, goalID string) ([]Commitment, error) {
	rows, err := q.Query(`SELECT `+commitmentCols+` FROM commitments WHERE goal_id=? ORDER BY created_at, id`, goalID)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Commitment{}
	for rows.Next() {
		var c Commitment
		if err := rows.Scan(&c.ID, &c.AssistantID, &c.GoalID, &c.Kind, &c.SuccessCriteria, &c.ExitCondition, &c.Spec,
			&c.State, &c.NextCheckAt, &c.LastCheckedAt, &c.LastError, &c.Cursor, &c.Revision, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, wrapBusy(rows.Err())
}

// CreateGoal drafts a Goal (and its commitments). Nothing runs until a person
// approves it.
func (s *Store) CreateGoal(requestID string, in CreateGoalInput) (Goal, bool, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.AssistantID == "" || in.Title == "" {
		return Goal{}, false, ErrInvalid
	}
	for _, c := range in.Commitments {
		if strings.TrimSpace(c.Kind) == "" {
			return Goal{}, false, fmt.Errorf("%w: a commitment needs a kind", ErrInvalid)
		}
		if c.Kind == CommitmentPRWatch {
			if _, err := ParsePRWatchSpec(c.Spec); err != nil {
				return Goal{}, false, err
			}
		}
	}
	return execute(s, requestID, "goal.create", in, func(tx *sql.Tx) (Goal, error) {
		a, err := getAssistantTx(tx, in.AssistantID)
		if err != nil {
			return Goal{}, err
		}
		if a.Status != StatusActive {
			return Goal{}, ErrAssistantDisabled
		}
		now := s.nowMS()
		id := newID("g")
		if _, err := tx.Exec(`INSERT INTO goals(`+goalCols+`) VALUES(?,?,?,?,?,?,?,?,?)`,
			id, in.AssistantID, in.Title, in.Objective, GoalDraft, "", 1, now, now); err != nil {
			return Goal{}, err
		}
		for _, c := range in.Commitments {
			spec := c.Spec
			if spec == "" {
				spec = "{}"
			}
			if _, err := tx.Exec(`INSERT INTO commitments(`+commitmentCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				newID("c"), in.AssistantID, id, c.Kind, c.SuccessCriteria, c.ExitCondition, spec, GoalDraft, 0, 0, "", "", 1, now, now); err != nil {
				return Goal{}, err
			}
		}
		return getGoalTx(tx, id)
	})
}

// GetGoal reads one Goal with its commitments.
func (s *Store) GetGoal(id string) (Goal, error) { return getGoalTx(s.db, id) }

// ListGoals returns an Assistant's goals (every one's when id is empty),
// newest first.
func (s *Store) ListGoals(assistantID string) ([]Goal, error) {
	q, args := `SELECT id FROM goals`, []any{}
	if assistantID != "" {
		q += ` WHERE assistant_id=?`
		args = append(args, assistantID)
	}
	rows, err := s.db.Query(q+` ORDER BY created_at DESC, id`, args...)
	if err != nil {
		return nil, wrapBusy(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []Goal{}
	for _, id := range ids {
		g, err := getGoalTx(s.db, id)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// goal transitions: from → allowed targets.
var goalMoves = map[string][]string{
	GoalDraft:  {GoalActive, GoalCancelled},
	GoalActive: {GoalPaused, GoalCompleted, GoalCancelled},
	GoalPaused: {GoalActive, GoalCancelled},
}

func canMove(from, to string) bool {
	for _, t := range goalMoves[from] {
		if t == to {
			return true
		}
	}
	return false
}

func (s *Store) moveGoal(requestID, kind, id string, expected int, to, evidence string) (Goal, bool, error) {
	payload := map[string]any{"id": id, "to": to, "rev": expected, "evidence": evidence}
	return execute(s, requestID, kind, payload, func(tx *sql.Tx) (Goal, error) {
		g, err := getGoalTx(tx, id)
		if err != nil {
			return Goal{}, err
		}
		if g.Revision != expected {
			return Goal{}, ErrStaleRevision
		}
		if !canMove(g.State, to) {
			return Goal{}, fmt.Errorf("%w: a %s goal cannot become %s", ErrBadState, g.State, to)
		}
		if to == GoalActive {
			a, err := getAssistantTx(tx, g.AssistantID)
			if err != nil {
				return Goal{}, err
			}
			if a.Status != StatusActive {
				return Goal{}, ErrAssistantDisabled
			}
		}
		now := s.nowMS()
		if _, err := tx.Exec(`UPDATE goals SET state=?, evidence=CASE WHEN ?<>'' THEN ? ELSE evidence END, revision=revision+1, updated_at=? WHERE id=?`,
			to, evidence, evidence, now, id); err != nil {
			return Goal{}, err
		}
		if _, err := tx.Exec(`UPDATE commitments SET state=?, revision=revision+1, updated_at=? WHERE goal_id=? AND state NOT IN (?,?)`,
			to, now, id, GoalCompleted, GoalCancelled); err != nil {
			return Goal{}, err
		}
		return getGoalTx(tx, id)
	})
}

// ApproveGoal turns a draft into an active Goal (and its commitments).
func (s *Store) ApproveGoal(requestID, id string, expectedRevision int) (Goal, bool, error) {
	return s.moveGoal(requestID, "goal.approve", id, expectedRevision, GoalActive, "")
}

// SetGoalState pauses, resumes or cancels a Goal. Completion has its own
// command because it needs evidence.
func (s *Store) SetGoalState(requestID, id, state string, expectedRevision int) (Goal, bool, error) {
	if state != GoalActive && state != GoalPaused && state != GoalCancelled {
		return Goal{}, false, ErrInvalid
	}
	return s.moveGoal(requestID, "goal.state", id, expectedRevision, state, "")
}

// CompleteGoal is the only road to "completed": a person (or a verified
// check) states the evidence that the exit condition holds.
func (s *Store) CompleteGoal(requestID, id string, expectedRevision int, evidence string) (Goal, bool, error) {
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return Goal{}, false, fmt.Errorf("%w: completing a goal needs evidence", ErrInvalid)
	}
	return s.moveGoal(requestID, "goal.complete", id, expectedRevision, GoalCompleted, evidence)
}
