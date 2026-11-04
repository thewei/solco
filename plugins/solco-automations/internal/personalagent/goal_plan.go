package personalagent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// GoalPlan (Assistants 3.1 U3, docs/assistants-messenger-agent-v3-1-plan.md §5):
// the plan behind a Goal — success criteria, verifiable milestones, the next
// action — persisted beside it with its revision trail. A plan is a proposal
// from the Coordinator (or a person) that Go enforces: bounded size, bounded
// replans, milestones that only ever become "verified" from committed task
// state. The Supervisor (supervisor.go) acts on it; nothing here runs a model.

const (
	MaxMilestones       = 12
	MaxReplans          = 5
	MaxStaleReminders   = 3
	DefaultMaxTasks     = 12
	DefaultStaleAfterMS = 45 * 60 * 1000
	DefaultFanOut       = 3
	maxMilestoneBrief   = 8 << 10
)

// ErrReplanLimit says a plan was already revised as often as allowed; only a
// person may raise it (by starting a new Goal or editing the plan on the Mac).
var ErrReplanLimit = errors.New("the plan was revised as often as allowed; ask the person")

// Milestone states, all derived from the task a milestone is bound to.
const (
	MilestonePending   = "pending"   // not started: waiting on earlier milestones or on the supervisor
	MilestoneWorking   = "working"   // its task is queued or running
	MilestoneVerifying = "verifying" // its task finished and awaits review
	MilestoneVerified  = "verified"  // its task is done (a coding task: merged)
	MilestoneBlocked   = "blocked"   // its task failed, blocked, was cancelled or is uncertain
	MilestoneNeedsYou  = "needs_you" // no task, or a coding task awaiting its publisher: only a person moves it
)

// MilestoneTask is the work a milestone delegates. The Assistant's own
// Agent/Model profile applies when Agent/Model are empty.
type MilestoneTask struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Brief    string `json:"brief"`
	RepoPath string `json:"repoPath,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Model    string `json:"model,omitempty"`
}

// Milestone is one verifiable step. A milestone with a Task is delegated and
// verified by Go from that task's own state; one without is confirmed by a
// person. TaskID/Attempt are execution bookkeeping the supervisor fills.
type Milestone struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	DependsOn []string       `json:"dependsOn,omitempty"`
	Task      *MilestoneTask `json:"task,omitempty"`
	TaskID    string         `json:"taskId,omitempty"`
	Attempt   int            `json:"attempt,omitempty"`
}

// MilestoneView is a milestone with its derived state and evidence line.
type MilestoneView struct {
	Milestone
	State    string `json:"state"`
	Evidence string `json:"evidence,omitempty"`
}

// PlanRevision is one entry of the plan's trail.
type PlanRevision struct {
	Revision   int    `json:"revision"`
	Reason     string `json:"reason"`
	Milestones int    `json:"milestones"`
	CreatedAt  int64  `json:"createdAt"`
}

// GoalPlan is the plan with everything derived from committed state.
type GoalPlan struct {
	GoalID          string          `json:"goalId"`
	AssistantID     string          `json:"assistantId"`
	SuccessCriteria string          `json:"successCriteria"`
	Milestones      []MilestoneView `json:"milestones"`
	NextAction      string          `json:"nextAction"`
	PlanRevision    int             `json:"planRevision"`
	Replans         int             `json:"replans"`
	MaxTasks        int             `json:"maxTasks"`
	StaleAfterMS    int64           `json:"staleAfterMs"`
	Verified        int             `json:"verified"`
	Total           int             `json:"total"`
	// State is the Goal's current state as a person should read it: draft,
	// paused, completed, cancelled, or for an active Goal working / verifying /
	// blocked / waiting_user / active.
	State     string         `json:"state"`
	Revisions []PlanRevision `json:"revisions"`
	UpdatedAt int64          `json:"updatedAt"`
}

// GoalPlanInput is what a Coordinator or person submits.
type GoalPlanInput struct {
	SuccessCriteria string      `json:"successCriteria,omitempty"`
	Milestones      []Milestone `json:"milestones"`
	NextAction      string      `json:"nextAction,omitempty"`
}

var milestoneIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func validatePlan(in *GoalPlanInput) error {
	if len(in.Milestones) == 0 {
		return fmt.Errorf("%w: a plan needs at least one milestone", ErrInvalid)
	}
	if len(in.Milestones) > MaxMilestones {
		return fmt.Errorf("%w: a plan has at most %d milestones", ErrInvalid, MaxMilestones)
	}
	in.SuccessCriteria = strings.TrimSpace(in.SuccessCriteria)
	in.NextAction = strings.TrimSpace(in.NextAction)
	if len(in.SuccessCriteria) > 2000 || len(in.NextAction) > 500 {
		return fmt.Errorf("%w: success criteria or next action is too long", ErrInvalid)
	}
	seen := map[string]bool{}
	for i := range in.Milestones {
		m := &in.Milestones[i]
		m.Title = strings.TrimSpace(m.Title)
		m.TaskID, m.Attempt = "", 0 // bookkeeping is the supervisor's, never the submitter's
		if m.ID == "" {
			m.ID = fmt.Sprintf("m%d", i+1)
		}
		if !milestoneIDRe.MatchString(m.ID) || seen[m.ID] {
			return fmt.Errorf("%w: milestone id %q is empty, malformed or repeated", ErrInvalid, m.ID)
		}
		if m.Title == "" || len(m.Title) > 160 {
			return fmt.Errorf("%w: milestone %s needs a title of up to 160 characters", ErrInvalid, m.ID)
		}
		for _, d := range m.DependsOn {
			if !seen[d] { // only earlier milestones: the plan is acyclic by construction
				return fmt.Errorf("%w: milestone %s depends on %q, which is not an earlier milestone", ErrInvalid, m.ID, d)
			}
		}
		if t := m.Task; t != nil {
			t.Title, t.Brief = strings.TrimSpace(t.Title), strings.TrimSpace(t.Brief)
			switch t.Kind {
			case KindResearch, KindCoding, KindManual:
			default:
				return fmt.Errorf("%w: milestone %s task kind %q", ErrInvalid, m.ID, t.Kind)
			}
			if t.Title == "" || t.Brief == "" || len(t.Brief) > maxMilestoneBrief {
				return fmt.Errorf("%w: milestone %s task needs a title and a brief of up to 8 KiB", ErrInvalid, m.ID)
			}
			if t.Kind == KindCoding && strings.TrimSpace(t.RepoPath) == "" {
				return fmt.Errorf("%w: milestone %s is a coding task and needs a repoPath", ErrInvalid, m.ID)
			}
		}
		seen[m.ID] = true
	}
	return nil
}

type planRow struct {
	goalID, assistantID, criteria, next string
	milestones                          []Milestone
	rev, replans, maxTasks              int
	staleAfter, updatedAt               int64
}

func getPlanRowTx(q queryer, goalID string) (planRow, error) {
	var p planRow
	var raw string
	err := q.QueryRow(`SELECT goal_id,assistant_id,success_criteria,milestones,next_action,plan_revision,replans,max_tasks,stale_after_ms,updated_at FROM goal_plans WHERE goal_id=?`, goalID).
		Scan(&p.goalID, &p.assistantID, &p.criteria, &raw, &p.next, &p.rev, &p.replans, &p.maxTasks, &p.staleAfter, &p.updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, wrapBusy(err)
	}
	if err := json.Unmarshal([]byte(raw), &p.milestones); err != nil {
		return p, fmt.Errorf("stored plan unreadable: %w", err)
	}
	return p, nil
}

func taskOpen(state string) bool {
	switch state {
	case TaskDone, TaskFailed, TaskCancelled:
		return false
	}
	return true
}

// SetGoalPlan stores a plan. expectedRevision 0 means "there is no plan yet";
// anything else must be the current planRevision and counts as a replan.
// Replanning keeps a milestone's task when its definition is unchanged,
// resends a milestone whose finished task is replaced by a changed definition,
// and refuses to change or drop a milestone whose task is still open.
func (s *Store) SetGoalPlan(requestID, goalID string, in GoalPlanInput, reason string, expectedRevision int) (GoalPlan, bool, error) {
	if err := validatePlan(&in); err != nil {
		return GoalPlan{}, false, err
	}
	reason = clip(strings.TrimSpace(reason), 300)
	payload := map[string]any{"goal": goalID, "plan": in, "reason": reason, "rev": expectedRevision}
	return execute(s, requestID, "goal.plan", payload, func(tx *sql.Tx) (GoalPlan, error) {
		g, err := getGoalTx(tx, goalID)
		if err != nil {
			return GoalPlan{}, err
		}
		if g.State == GoalCancelled || g.State == GoalCompleted {
			return GoalPlan{}, fmt.Errorf("%w: the goal is %s", ErrBadState, g.State)
		}
		now := s.nowMS()
		old, err := getPlanRowTx(tx, goalID)
		first := errors.Is(err, ErrNotFound)
		if err != nil && !first {
			return GoalPlan{}, err
		}
		if first != (expectedRevision == 0) || (!first && old.rev != expectedRevision) {
			return GoalPlan{}, ErrStaleRevision
		}
		raw := func(ms []Milestone) string { b, _ := json.Marshal(ms); return string(b) }
		if first {
			if _, err := tx.Exec(`INSERT INTO goal_plans(goal_id,assistant_id,success_criteria,milestones,next_action,plan_revision,replans,max_tasks,stale_after_ms,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
				goalID, g.AssistantID, in.SuccessCriteria, raw(in.Milestones), in.NextAction, 1, 0, DefaultMaxTasks, DefaultStaleAfterMS, now); err != nil {
				return GoalPlan{}, err
			}
			if _, err := tx.Exec(`INSERT INTO goal_plan_revisions(goal_id,revision,success_criteria,milestones,reason,created_at) VALUES(?,?,?,?,?,?)`,
				goalID, 1, in.SuccessCriteria, raw(in.Milestones), reason, now); err != nil {
				return GoalPlan{}, err
			}
			if _, err := addFactTx(tx, Fact{AssistantID: g.AssistantID, GoalID: goalID, Kind: FactPlanned,
				Text: fmt.Sprintf("Plan: %d milestone(s)%s", len(in.Milestones), reasonSuffix(reason))}, "planned:"+goalID+":1", now); err != nil {
				return GoalPlan{}, err
			}
			return planViewTx(tx, goalID)
		}
		if old.replans >= MaxReplans {
			return GoalPlan{}, ErrReplanLimit
		}
		merged, err := carryOver(tx, old.milestones, in.Milestones)
		if err != nil {
			return GoalPlan{}, err
		}
		rev := old.rev + 1
		if _, err := tx.Exec(`UPDATE goal_plans SET success_criteria=?, milestones=?, next_action=?, plan_revision=?, replans=replans+1, updated_at=? WHERE goal_id=?`,
			in.SuccessCriteria, raw(merged), in.NextAction, rev, now, goalID); err != nil {
			return GoalPlan{}, err
		}
		if _, err := tx.Exec(`INSERT INTO goal_plan_revisions(goal_id,revision,success_criteria,milestones,reason,created_at) VALUES(?,?,?,?,?,?)`,
			goalID, rev, in.SuccessCriteria, raw(merged), reason, now); err != nil {
			return GoalPlan{}, err
		}
		if _, err := addFactTx(tx, Fact{AssistantID: g.AssistantID, GoalID: goalID, Kind: FactReplanned,
			Text: fmt.Sprintf("Plan revised (rev %d, %d of %d revisions used)%s", rev, old.replans+1, MaxReplans, reasonSuffix(reason))},
			fmt.Sprintf("planned:%s:%d", goalID, rev), now); err != nil {
			return GoalPlan{}, err
		}
		return planViewTx(tx, goalID)
	})
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return " — " + reason
}

// carryOver binds the new milestones to the tasks the old plan already made.
func carryOver(tx queryer, old, next []Milestone) ([]Milestone, error) {
	byID := map[string]Milestone{}
	for _, m := range old {
		byID[m.ID] = m
	}
	kept := map[string]bool{}
	out := make([]Milestone, len(next))
	for i, m := range next {
		out[i] = m
		prev, existed := byID[m.ID]
		if !existed {
			continue
		}
		kept[m.ID] = true
		if prev.TaskID == "" {
			out[i].Attempt = prev.Attempt
			continue
		}
		same := prev.Task != nil && m.Task != nil && *prev.Task == *m.Task
		if same {
			out[i].TaskID, out[i].Attempt = prev.TaskID, prev.Attempt
			continue
		}
		t, err := getTaskTx(tx, prev.TaskID)
		if err != nil {
			return nil, err
		}
		if taskOpen(t.State) {
			return nil, fmt.Errorf("%w: milestone %s has a task that is still %s; wait for it or cancel it first", ErrBadState, m.ID, t.State)
		}
		out[i].Attempt = prev.Attempt + 1 // a new definition: a new attempt, a new task
	}
	for id, prev := range byID {
		if kept[id] || prev.TaskID == "" {
			continue
		}
		t, err := getTaskTx(tx, prev.TaskID)
		if err == nil && taskOpen(t.State) {
			return nil, fmt.Errorf("%w: milestone %s was removed but its task is still %s", ErrBadState, id, t.State)
		}
	}
	return out, nil
}

// GetGoalPlan reads a Goal's plan with every state derived from the tasks.
func (s *Store) GetGoalPlan(goalID string) (GoalPlan, error) { return planViewTx(s.db, goalID) }

func milestoneState(q queryer, m Milestone, byID map[string]string) (state, evidence string, err error) {
	if m.Task == nil {
		for _, d := range m.DependsOn {
			if byID[d] != MilestoneVerified {
				return MilestonePending, "", nil
			}
		}
		return MilestoneNeedsYou, "", nil
	}
	if m.TaskID == "" {
		return MilestonePending, "", nil
	}
	t, err := getTaskTx(q, m.TaskID)
	if err != nil {
		return "", "", err
	}
	switch t.State {
	case TaskDone:
		return MilestoneVerified, fmt.Sprintf("task %q done (%s)", t.Title, t.ID), nil
	case TaskDraft:
		return MilestoneNeedsYou, fmt.Sprintf("task %q waits for your approval (%s)", t.Title, t.ID), nil
	case TaskReviewRequired:
		return MilestoneVerifying, fmt.Sprintf("task %q awaits review (%s)", t.Title, t.ID), nil
	case TaskFailed, TaskCancelled, TaskBlocked, TaskUncertain:
		return MilestoneBlocked, fmt.Sprintf("task %q is %s (%s)", t.Title, t.State, t.ID), nil
	}
	return MilestoneWorking, fmt.Sprintf("task %q is %s (%s)", t.Title, t.State, t.ID), nil
}

func planViewTx(q queryer, goalID string) (GoalPlan, error) {
	g, err := getGoalTx(q, goalID)
	if err != nil {
		return GoalPlan{}, err
	}
	p, err := getPlanRowTx(q, goalID)
	if err != nil {
		return GoalPlan{}, err
	}
	view := GoalPlan{GoalID: goalID, AssistantID: p.assistantID, SuccessCriteria: p.criteria, NextAction: p.next,
		PlanRevision: p.rev, Replans: p.replans, MaxTasks: p.maxTasks, StaleAfterMS: p.staleAfter,
		Total: len(p.milestones), UpdatedAt: p.updatedAt, Milestones: []MilestoneView{}, Revisions: []PlanRevision{}}
	states := map[string]string{}
	counts := map[string]int{}
	for _, m := range p.milestones {
		st, ev, err := milestoneState(q, m, states)
		if err != nil {
			return GoalPlan{}, err
		}
		states[m.ID] = st
		counts[st]++
		view.Milestones = append(view.Milestones, MilestoneView{Milestone: m, State: st, Evidence: ev})
	}
	view.Verified = counts[MilestoneVerified]
	switch {
	case g.State != GoalActive:
		view.State = g.State
	case counts[MilestoneBlocked] > 0:
		view.State = "blocked"
	case counts[MilestoneWorking] > 0:
		view.State = "working"
	case counts[MilestoneVerifying] > 0:
		view.State = "verifying"
	case counts[MilestoneNeedsYou] > 0:
		view.State = "waiting_user"
	default:
		view.State = "active"
	}
	rows, err := q.Query(`SELECT revision,reason,milestones,created_at FROM goal_plan_revisions WHERE goal_id=? ORDER BY revision`, goalID)
	if err != nil {
		return GoalPlan{}, wrapBusy(err)
	}
	defer rows.Close()
	for rows.Next() {
		var r PlanRevision
		var raw string
		if err := rows.Scan(&r.Revision, &r.Reason, &raw, &r.CreatedAt); err != nil {
			return GoalPlan{}, err
		}
		var ms []Milestone
		_ = json.Unmarshal([]byte(raw), &ms)
		r.Milestones = len(ms)
		view.Revisions = append(view.Revisions, r)
	}
	return view, wrapBusy(rows.Err())
}
