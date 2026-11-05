package personalagent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Task kinds.
const (
	KindResearch = "research" // read-only analysis, no repo writes
	KindCoding   = "coding"   // works in an isolated worktree, never merges
	KindManual   = "manual"   // a single Agent in a plain workspace
	// KindCoordinator is an Assistant's long-lived Coordinator Agent. It is made
	// only by StartCoordinator, never through CreateTask.
	KindCoordinator = "coordinator"
	// KindGroupAgent is the Agent an Assistant runs for one Group conversation
	// (its own session, never the private Coordinator's). Made only by the
	// Group pump when the person @-mentions the Assistant.
	KindGroupAgent = "groupagent"
)

// Task states (docs/personal-agent-go-mvp-plan.md §5.2).
const (
	TaskDraft          = "draft"
	TaskQueued         = "queued"
	TaskClaimed        = "claimed"
	TaskStarting       = "starting"
	TaskRunning        = "running"
	TaskBlocked        = "blocked"
	TaskReviewRequired = "review_required"
	TaskDone           = "done"
	TaskFailed         = "failed"
	TaskCancelled      = "cancelled"
	TaskUncertain      = "uncertain"
	// TaskWaiting is a published task whose dependencies are not all done.
	TaskWaiting = "waiting"
)

// Run statuses.
const (
	RunClaimed   = "claimed"
	RunStarting  = "starting"
	RunRunning   = "running"
	RunBlocked   = "blocked"
	RunDone      = "done"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
	RunUncertain = "uncertain"
)

// Task is one delegated unit of work owned by one Assistant.
type Task struct {
	ID           string `json:"id"`
	AssistantID  string `json:"assistantId"`
	GoalID       string `json:"goalId"`
	RuleID       string `json:"ruleId"`
	ParentTaskID string `json:"parentTaskId"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	Brief        string `json:"brief"`
	RepoPath     string `json:"repoPath"`
	Agent        string `json:"agent"`
	Model        string `json:"model"`
	State        string `json:"state"`
	NeedsYou     bool   `json:"needsYou"`
	ReviewState  string `json:"reviewState"`
	Attempt      int    `json:"attempt"`
	Revision     int    `json:"revision"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// Run is one attempt at one task: the claim, then the real Herdr identity.
type Run struct {
	ID             string `json:"id"`
	TaskID         string `json:"taskId"`
	RuleID         string `json:"ruleId"`
	EventID        string `json:"eventId"`
	IdempotencyKey string `json:"idempotencyKey"`
	Status         string `json:"status"`
	Claimant       string `json:"claimant"`
	HerdrSession   string `json:"herdrSession"`
	WorkspaceID    string `json:"workspaceId"`
	PaneID         string `json:"paneId"`
	AgentSessionID string `json:"agentSessionId"`
	Label          string `json:"label"`
	Result         string `json:"result"`
	Error          string `json:"error"`
	CreatedAt      int64  `json:"createdAt"`
	StartedAt      int64  `json:"startedAt"`
	FinishedAt     int64  `json:"finishedAt"`
	// Branch is the run worktree's branch when it has one (internal; the
	// legacy history record carries it so the app can find the checkout).
	Branch string `json:"-"`
}

// Session is the Herdr place a run happens, exactly as Herdr reported it.
// Fields Herdr did not report stay empty; nothing is guessed from a title.
type Session struct {
	HerdrSession   string `json:"herdrSession"`
	WorkspaceID    string `json:"workspaceId"`
	PaneID         string `json:"paneId"`
	AgentSessionID string `json:"agentSessionId,omitempty"`
	// Branch is the worktree branch Provision made, when it made one.
	Branch string `json:"branch,omitempty"`
}

// CreateTaskInput is the body of `assistant task create`.
type CreateTaskInput struct {
	AssistantID  string `json:"assistantId"`
	GoalID       string `json:"goalId,omitempty"`
	RuleID       string `json:"ruleId,omitempty"`
	ParentTaskID string `json:"parentTaskId,omitempty"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	Brief        string `json:"brief"`
	RepoPath     string `json:"repoPath,omitempty"`
	Agent        string `json:"agent,omitempty"`
	Model        string `json:"model,omitempty"`
	// Draft keeps the task out of the queue until a person approves it.
	Draft bool `json:"draft,omitempty"`
	// DependsOn lists tasks of the same Assistant that must be done first.
	DependsOn []string `json:"dependsOn,omitempty"`
	// GroupID files the task from a Group conversation: the Group gets a task
	// card that follows the task's real state. Only a member with a running
	// Group Agent may do it.
	GroupID string `json:"groupId,omitempty"`
	// RequiredSkills names skills the worker must load and use (Gap W04). They
	// must be installed for OMP and granted to the Assistant by a person.
	RequiredSkills []string `json:"requiredSkills,omitempty"`
	// TimeoutMin narrows how long this task's Agent may work (1–45 minutes;
	// 0 = the dispatcher's limit). It can only shorten the limit (Gap W09).
	TimeoutMin int `json:"timeoutMin,omitempty"`
	// RequireChecks (coding tasks only) refuses to review this task until its
	// Assistant has approved checks, and so never lets it reach "ready"
	// unchecked (Gap Q03). Like TimeoutMin it can only be stricter: there is no
	// per-task way to waive the Assistant's own checks or the person's merge.
	RequireChecks bool `json:"requireChecks,omitempty"`
}

// MaxTaskTimeoutMin is the longest an Agent may work, the dispatcher's default.
const MaxTaskTimeoutMin = 45

const taskCols = `id,assistant_id,goal_id,rule_id,parent_task_id,kind,title,brief,repo_path,agent,model,state,needs_you,review_state,attempt,revision,created_at,updated_at`

func scanTask(r rowScanner) (Task, error) {
	var t Task
	var needs int
	err := r.Scan(&t.ID, &t.AssistantID, &t.GoalID, &t.RuleID, &t.ParentTaskID, &t.Kind, &t.Title, &t.Brief, &t.RepoPath,
		&t.Agent, &t.Model, &t.State, &needs, &t.ReviewState, &t.Attempt, &t.Revision, &t.CreatedAt, &t.UpdatedAt)
	t.NeedsYou = needs != 0
	return t, err
}

type queryRower interface {
	QueryRow(string, ...any) *sql.Row
}

func getTaskTx(q queryRower, id string) (Task, error) {
	t, err := scanTask(q.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, wrapBusy(err)
}

const runCols = `id,task_id,rule_id,event_id,idempotency_key,status,claimant,herdr_session,workspace_id,pane_id,agent_session_id,label,result,error,created_at,started_at,finished_at,branch`

func scanRun(r rowScanner) (Run, error) {
	var x Run
	err := r.Scan(&x.ID, &x.TaskID, &x.RuleID, &x.EventID, &x.IdempotencyKey, &x.Status, &x.Claimant, &x.HerdrSession,
		&x.WorkspaceID, &x.PaneID, &x.AgentSessionID, &x.Label, &x.Result, &x.Error, &x.CreatedAt, &x.StartedAt, &x.FinishedAt, &x.Branch)
	return x, err
}

func getRunTx(q queryRower, id string) (Run, error) {
	r, err := scanRun(q.QueryRow(`SELECT `+runCols+` FROM runs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, wrapBusy(err)
}

// CreateTask records a delegated unit of work. It is queued at once unless the
// input asks for a draft; a disabled Assistant accepts nothing.
func (s *Store) CreateTask(requestID string, in CreateTaskInput) (Task, bool, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.AssistantID == "" || in.Title == "" || strings.TrimSpace(in.Brief) == "" {
		return Task{}, false, ErrInvalid
	}
	switch in.Kind {
	case KindResearch, KindCoding, KindManual:
	default:
		return Task{}, false, fmt.Errorf("%w: kind %q", ErrInvalid, in.Kind)
	}
	if in.Kind == KindCoding && strings.TrimSpace(in.RepoPath) == "" {
		return Task{}, false, fmt.Errorf("%w: a coding task needs a repoPath", ErrInvalid)
	}
	if in.TimeoutMin < 0 || in.TimeoutMin > MaxTaskTimeoutMin {
		return Task{}, false, fmt.Errorf("%w: timeoutMin is 1–%d minutes (a task can only shorten the limit)", ErrInvalid, MaxTaskTimeoutMin)
	}
	if in.RequireChecks && in.Kind != KindCoding {
		return Task{}, false, fmt.Errorf("%w: only a coding task has checks to require", ErrInvalid)
	}
	skills, err := normalizeSkills(in.RequiredSkills)
	if err != nil {
		return Task{}, false, err
	}
	in.RequiredSkills = skills
	return execute(s, requestID, "task.create", in, func(tx *sql.Tx) (Task, error) {
		a, err := getAssistantTx(tx, in.AssistantID)
		if err != nil {
			return Task{}, err
		}
		// During the OMP rollout, new Assistant work is dispatched through
		// OMP even when an old saved profile refers to a legacy provider.
		// Explicit legacy kinds are only used by pre-existing internal callers.
		in := in
		if in.Agent == "" {
			p := s.profileOf(tx, in.AssistantID)
			in.Agent = AssistantAgentKind
			if p.Agent == AssistantAgentKind && in.Model == "" {
				in.Model = p.Model
			}
		}
		if a.Status != StatusActive {
			return Task{}, ErrAssistantDisabled
		}
		if in.GoalID != "" {
			g, err := getGoalTx(tx, in.GoalID)
			if err != nil || g.AssistantID != in.AssistantID {
				return Task{}, fmt.Errorf("%w: goal %q is not this assistant's", ErrInvalid, in.GoalID)
			}
		}
		if in.ParentTaskID != "" {
			p, err := getTaskTx(tx, in.ParentTaskID)
			if err != nil || p.AssistantID != in.AssistantID {
				return Task{}, fmt.Errorf("%w: parent %q is not this assistant's task", ErrInvalid, in.ParentTaskID)
			}
		}
		deps, err := checkDeps(tx, in.AssistantID, "", in.DependsOn)
		if err != nil {
			return Task{}, err
		}
		state := TaskQueued
		if in.Draft {
			state = TaskDraft
		} else if open, err := anyOpenDep(tx, deps); err != nil {
			return Task{}, err
		} else if open {
			state = TaskWaiting
		}
		now := s.nowMS()
		id := newID("t")
		_, err = tx.Exec(`INSERT INTO tasks(`+taskCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, in.AssistantID, in.GoalID, in.RuleID, in.ParentTaskID, in.Kind, in.Title, in.Brief, in.RepoPath, in.Agent, in.Model,
			state, 0, "", 1, 1, now, now)
		if err != nil {
			return Task{}, err
		}
		for _, d := range deps {
			if _, err := tx.Exec(`INSERT INTO task_deps(task_id,depends_on) VALUES(?,?)`, id, d); err != nil {
				return Task{}, err
			}
		}
		for _, sk := range in.RequiredSkills {
			if _, err := tx.Exec(`INSERT INTO task_skills(task_id,skill) VALUES(?,?)`, id, sk); err != nil {
				return Task{}, err
			}
		}
		if in.TimeoutMin > 0 || in.RequireChecks {
			if _, err := tx.Exec(`INSERT INTO task_limits(task_id,timeout_min,require_checks) VALUES(?,?,?)`, id, in.TimeoutMin, in.RequireChecks); err != nil {
				return Task{}, err
			}
		}
		if err := emitTask(tx, id, "task.added", now); err != nil {
			return Task{}, err
		}
		created, err := getTaskTx(tx, id)
		if err != nil {
			return Task{}, err
		}
		if in.GroupID != "" {
			if err := linkGroupTask(tx, s, in.GroupID, created, now); err != nil {
				return Task{}, err
			}
		}
		return created, nil
	})
}

// GetTask reads one task.
func (s *Store) GetTask(id string) (Task, error) { return getTaskTx(s.db, id) }

// GetRun reads one run.
func (s *Store) GetRun(id string) (Run, error) { return getRunTx(s.db, id) }

// ListTasks returns an Assistant's tasks (every Assistant's when id is empty;
// never its Coordinator's own),
// newest first.
func (s *Store) ListTasks(assistantID string) ([]Task, error) {
	// An Assistant's Coordinator task is plumbing, not work the person queued:
	// it never shows in lists, counters or the task graph.
	q := `SELECT ` + taskCols + ` FROM tasks WHERE kind NOT IN (?,?)`
	args := []any{KindCoordinator, KindGroupAgent}
	if assistantID != "" {
		q += ` AND assistant_id=?`
		args = append(args, assistantID)
	}
	rows, err := s.db.Query(q+` ORDER BY created_at DESC, id`, args...)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, wrapBusy(rows.Err())
}

// ListRuns returns a task's runs (all runs when taskID is empty), newest first.
func (s *Store) ListRuns(taskID string) ([]Run, error) {
	q := `SELECT ` + runCols + ` FROM runs`
	args := []any{}
	if taskID != "" {
		q += ` WHERE task_id=?`
		args = append(args, taskID)
	}
	rows, err := s.db.Query(q+` ORDER BY created_at DESC, id`, args...)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, wrapBusy(rows.Err())
}

// ClaimTask is the durable step before any launch. Exactly one caller gets
// fresh=true and may launch; everyone else — including a caller after a crash —
// sees the same Run and must not launch again.
func (s *Store) ClaimTask(taskID, claimant string) (Run, bool, error) {
	type claim struct {
		Run   Run
		Fresh bool
	}
	c, _, err := execute(s, "", "task.claim", nil, func(tx *sql.Tx) (claim, error) {
		t, err := getTaskTx(tx, taskID)
		if err != nil {
			return claim{}, err
		}
		key := fmt.Sprintf("task:%s:%d", t.ID, t.Attempt)
		var existing string
		err = tx.QueryRow(`SELECT id FROM runs WHERE idempotency_key=?`, key).Scan(&existing)
		if err == nil {
			r, err := getRunTx(tx, existing)
			return claim{Run: r}, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return claim{}, err
		}
		if t.State != TaskQueued {
			return claim{}, fmt.Errorf("%w: %s", ErrNotClaimable, t.State)
		}
		a, err := getAssistantTx(tx, t.AssistantID)
		if err != nil {
			return claim{}, err
		}
		if a.Status != StatusActive {
			return claim{}, ErrAssistantDisabled
		}
		if t.GoalID != "" {
			if g, err := getGoalTx(tx, t.GoalID); err == nil && g.State != GoalActive {
				return claim{}, fmt.Errorf("%w: its goal is %s", ErrNotClaimable, g.State)
			}
		}
		now := s.nowMS()
		id := newID("r")
		if _, err := tx.Exec(`INSERT INTO runs(id,task_id,rule_id,idempotency_key,status,claimant,created_at) VALUES(?,?,?,?,?,?,?)`,
			id, t.ID, t.RuleID, key, RunClaimed, claimant, now); err != nil {
			return claim{}, err
		}
		if _, err := tx.Exec(`UPDATE tasks SET state=?, revision=revision+1, updated_at=? WHERE id=?`, TaskClaimed, now, t.ID); err != nil {
			return claim{}, err
		}
		r, err := getRunTx(tx, id)
		return claim{Run: r, Fresh: true}, err
	})
	return c.Run, c.Fresh, err
}

// setRun moves a run and its task together, in one transaction, refusing a
// transition the current status does not allow.
func (s *Store) setRun(runID string, from []string, runStatus, taskState string, patch func(*sql.Tx, int64) error) error {
	_, _, err := execute(s, "", "run.set", nil, func(tx *sql.Tx) (struct{}, error) {
		r, err := getRunTx(tx, runID)
		if err != nil {
			return struct{}{}, err
		}
		ok := false
		for _, f := range from {
			ok = ok || r.Status == f
		}
		if !ok {
			return struct{}{}, fmt.Errorf("%w: run is %s", ErrBadState, r.Status)
		}
		now := s.nowMS()
		finished := int64(0)
		switch runStatus {
		case RunDone, RunFailed, RunCancelled:
			finished = now
		}
		if _, err := tx.Exec(`UPDATE runs SET status=?, finished_at=CASE WHEN ?>0 THEN ? ELSE finished_at END WHERE id=?`,
			runStatus, finished, finished, runID); err != nil {
			return struct{}{}, err
		}
		if _, err := tx.Exec(`UPDATE tasks SET state=?, revision=revision+1, updated_at=? WHERE id=?`, taskState, now, r.TaskID); err != nil {
			return struct{}{}, err
		}
		if patch != nil {
			if err := patch(tx, now); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	return err
}

// MarkStarting records that the launch is about to be attempted. A crash from
// here until RecordLaunch leaves the run `uncertain`, never silently retried.
func (s *Store) MarkStarting(runID string) error {
	return s.setRun(runID, []string{RunClaimed}, RunStarting, TaskStarting, func(tx *sql.Tx, now int64) error {
		_, err := tx.Exec(`UPDATE runs SET started_at=? WHERE id=?`, now, runID)
		return err
	})
}

// SetLabel stores the Herdr label the launch will carry, before the launch, so
// an uncertain run can be found again by it.
func (s *Store) SetLabel(runID, label string) error {
	_, _, err := execute(s, "", "run.label", nil, func(tx *sql.Tx) (struct{}, error) {
		_, err := tx.Exec(`UPDATE runs SET label=? WHERE id=?`, label, runID)
		return struct{}{}, err
	})
	return err
}

// RecordLaunch stores the real Herdr identity Herdr reported and marks the run
// running. It also accepts an `uncertain` run: reconciliation found the pane.
func (s *Store) RecordLaunch(runID string, sess Session) error {
	return s.setRun(runID, []string{RunStarting, RunUncertain}, RunRunning, TaskRunning, func(tx *sql.Tx, _ int64) error {
		_, err := tx.Exec(`UPDATE runs SET herdr_session=?, workspace_id=?, pane_id=?, agent_session_id=?, branch=CASE WHEN ?='' THEN branch ELSE ? END, error='' WHERE id=?`,
			sess.HerdrSession, sess.WorkspaceID, sess.PaneID, sess.AgentSessionID, sess.Branch, sess.Branch, runID)
		return err
	})
}

// BlockRun marks a run that is waiting for a person (a dialog, a permission).
func (s *Store) BlockRun(runID, reason string) error {
	return s.setRun(runID, []string{RunRunning, RunStarting}, RunBlocked, TaskBlocked, func(tx *sql.Tx, now int64) error {
		if _, err := tx.Exec(`UPDATE tasks SET needs_you=1 WHERE id=(SELECT task_id FROM runs WHERE id=?)`, runID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE runs SET error=? WHERE id=?`, redactForRun(tx, runID, reason), runID); err != nil {
			return err
		}
		return emitRunTask(tx, runID, "task.blocked", now)
	})
}

// FinishRun ends a run. done → the task is done, except a coding task, which
// waits for a person's review; failed/cancelled map straight over.
func (s *Store) FinishRun(runID, status, note string) error {
	return s.finishRun(runID, status, note, nil)
}

// FailCodeHerdrUnreachable marks a run whose launch failed because Herdr never
// gave a structured answer (no error code at all: the socket is gone, the
// binary is missing or it was killed). It is the one start failure the
// dispatcher can prove; why an Agent failed inside its pane is not classified.
const FailCodeHerdrUnreachable = "herdr_unreachable"

// SetRunFailCode records a failed run's provable cause.
func (s *Store) SetRunFailCode(runID, code string) error {
	_, err := s.db.Exec(`UPDATE runs SET fail_code=? WHERE id=? AND status=?`, code, runID, RunFailed)
	return wrapBusy(err)
}

// FinishRunWithResult ends a run as done with a result Solco read back: the
// summary becomes the run's result and the whole record is stored with it.
func (s *Store) FinishRunWithResult(runID string, res WorkerResult) error {
	if strings.TrimSpace(res.Summary) == "" || res.Evidence == "" || res.SHA256 == "" {
		return fmt.Errorf("%w: a result needs a summary and its evidence", ErrInvalid)
	}
	return s.finishRun(runID, RunDone, res.Summary, &res)
}

// RunResult returns the stored result of a run, if it has one.
func (s *Store) RunResult(runID string) (WorkerResult, bool, error) {
	var body string
	err := s.db.QueryRow(`SELECT body FROM run_results WHERE run_id=?`, runID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkerResult{}, false, nil
	}
	if err != nil {
		return WorkerResult{}, false, wrapBusy(err)
	}
	var res WorkerResult
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		return WorkerResult{}, false, err
	}
	return res, true, nil
}

func (s *Store) finishRun(runID, status, note string, res *WorkerResult) error {
	var state string
	switch status {
	case RunDone:
		state = TaskDone
	case RunFailed:
		state = TaskFailed
	case RunCancelled:
		state = TaskCancelled
	default:
		return fmt.Errorf("%w: finish with %q", ErrInvalid, status)
	}
	return s.setRun(runID, []string{RunClaimed, RunStarting, RunRunning, RunBlocked, RunUncertain}, status, state, func(tx *sql.Tx, now int64) error {
		col := "result"
		if status != RunDone {
			col = "error"
		}
		if _, err := tx.Exec(`UPDATE runs SET `+col+`=? WHERE id=?`, redactForRun(tx, runID, note), runID); err != nil {
			return err
		}
		if res != nil {
			// The stored summary is redacted exactly like the run's own note;
			// SHA256 stays the digest of the message as it was read.
			stored := *res
			stored.Summary = redactForRun(tx, runID, stored.Summary)
			body, err := json.Marshal(stored)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO run_results(run_id,evidence,agent_session_id,message_id,sha256,body,collected_at) VALUES(?,?,?,?,?,?,?)
				ON CONFLICT(run_id) DO NOTHING`, runID, res.Evidence, res.AgentSessionID, res.MessageID, res.SHA256, string(body), now); err != nil {
				return err
			}
		}
		if status == RunDone {
			if _, err := tx.Exec(`UPDATE tasks SET state=CASE WHEN kind=? THEN ? ELSE state END,
				review_state=CASE WHEN kind=? THEN 'pending' ELSE review_state END,
				needs_you=CASE WHEN kind=? OR (rule_id<>'' AND rule_id<>?) THEN 1 ELSE 0 END WHERE id=(SELECT task_id FROM runs WHERE id=?)`,
				KindCoding, TaskReviewRequired, KindCoding, KindCoding, HeartbeatRule, runID); err != nil {
				return err
			}
			return emitRunTask(tx, runID, "task.completed", now)
		}
		if _, err := tx.Exec(`UPDATE tasks SET needs_you=? WHERE id=(SELECT task_id FROM runs WHERE id=?)`, boolInt(status == RunFailed), runID); err != nil {
			return err
		}
		if status == RunFailed {
			return emitRunTask(tx, runID, "task.failed", now)
		}
		return nil
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Recover is called once at daemon start. A run that was claimed or starting
// when the previous process died has an unknown outcome — Herdr may or may not
// have created its pane — so it becomes `uncertain`; it is never re-launched.
func (s *Store) Recover() (int, error) {
	n, _, err := execute(s, "", "recover", nil, func(tx *sql.Tx) (int, error) {
		rows, err := tx.Query(`SELECT id, task_id FROM runs WHERE status IN (?,?)`, RunClaimed, RunStarting)
		if err != nil {
			return 0, err
		}
		type pair struct{ run, task string }
		var found []pair
		for rows.Next() {
			var p pair
			if err := rows.Scan(&p.run, &p.task); err != nil {
				_ = rows.Close()
				return 0, err
			}
			found = append(found, p)
		}
		_ = rows.Close()
		now := s.nowMS()
		for _, p := range found {
			if _, err := tx.Exec(`UPDATE runs SET status=? WHERE id=?`, RunUncertain, p.run); err != nil {
				return 0, err
			}
			if _, err := tx.Exec(`UPDATE tasks SET state=?, needs_you=1, revision=revision+1, updated_at=? WHERE id=?`, TaskUncertain, now, p.task); err != nil {
				return 0, err
			}
			if err := emitTask(tx, p.task, "task.uncertain", now); err != nil {
				return 0, err
			}
		}
		return len(found), nil
	})
	return n, err
}

// parkUncertain moves a starting run whose outcome cannot be established to
// `uncertain` and asks a person (needs_you) — it is never retried by itself.
func (s *Store) parkUncertain(runID, reason string) (struct{}, error) {
	return struct{}{}, s.setRun(runID, []string{RunClaimed, RunStarting, RunRunning}, RunUncertain, TaskUncertain, func(tx *sql.Tx, now int64) error {
		if _, err := tx.Exec(`UPDATE tasks SET needs_you=1 WHERE id=(SELECT task_id FROM runs WHERE id=?)`, runID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE runs SET error=? WHERE id=?`, redactForRun(tx, runID, reason), runID); err != nil {
			return err
		}
		return emitRunTask(tx, runID, "task.uncertain", now)
	})
}

// QueuedTasks lists tasks ready to be claimed, oldest first, for Assistants
// that are active.
func (s *Store) QueuedTasks() ([]Task, error) {
	// Fair across Assistants (W08): each Assistant's oldest task is first in
	// line, then each one's second, and so on, so one with a long backlog
	// cannot hold the whole quota while another waits.
	rows, err := s.db.Query(`SELECT `+prefixed("q", taskCols)+` FROM (
		SELECT `+prefixed("t", taskCols)+`, ROW_NUMBER() OVER (PARTITION BY t.assistant_id ORDER BY t.created_at, t.id) AS rn
		FROM tasks t JOIN assistants a ON a.id=t.assistant_id
		WHERE t.state=? AND a.status=?
		AND (t.goal_id='' OR EXISTS(SELECT 1 FROM goals g WHERE g.id=t.goal_id AND g.state='active'))
	) q ORDER BY q.rn, q.created_at, q.id`, TaskQueued, StatusActive)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, wrapBusy(rows.Err())
}

func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + p
	}
	return strings.Join(parts, ",")
}

// RunsWithStatus lists runs in one status.
func (s *Store) RunsWithStatus(status string) ([]Run, error) {
	rows, err := s.db.Query(`SELECT `+runCols+` FROM runs WHERE status=? ORDER BY created_at, id`, status)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, wrapBusy(rows.Err())
}

// RetryTask makes a deliberate new attempt of a failed or cancelled task. An
// uncertain task is never retried this way: Reconcile settles it first.
func (s *Store) RetryTask(requestID, taskID string, expectedRevision int) (Task, bool, error) {
	payload := map[string]any{"id": taskID, "rev": expectedRevision}
	return execute(s, requestID, "task.retry", payload, func(tx *sql.Tx) (Task, error) {
		t, err := getTaskTx(tx, taskID)
		if err != nil {
			return Task{}, err
		}
		if t.Revision != expectedRevision {
			return Task{}, ErrStaleRevision
		}
		if t.State != TaskFailed && t.State != TaskCancelled {
			return Task{}, fmt.Errorf("%w: task is %s", ErrBadState, t.State)
		}
		a, err := getAssistantTx(tx, t.AssistantID)
		if err != nil {
			return Task{}, err
		}
		if a.Status != StatusActive {
			return Task{}, ErrAssistantDisabled
		}
		if _, err := tx.Exec(`UPDATE tasks SET state=?, attempt=attempt+1, needs_you=0, revision=revision+1, updated_at=? WHERE id=?`,
			TaskQueued, s.nowMS(), taskID); err != nil {
			return Task{}, err
		}
		return getTaskTx(tx, taskID)
	})
}

// AckTask clears Needs You after a person has looked at a finished task.
func (s *Store) AckTask(requestID, taskID string, expectedRevision int) (Task, bool, error) {
	payload := map[string]any{"id": taskID, "rev": expectedRevision}
	return execute(s, requestID, "task.ack", payload, func(tx *sql.Tx) (Task, error) {
		t, err := getTaskTx(tx, taskID)
		if err != nil {
			return t, err
		}
		if t.Revision != expectedRevision {
			return t, ErrStaleRevision
		}
		if _, err := tx.Exec(`UPDATE tasks SET needs_you=0, revision=revision+1, updated_at=? WHERE id=?`, s.nowMS(), taskID); err != nil {
			return t, err
		}
		return getTaskTx(tx, taskID)
	})
}

// redactForRun strips the run's own brief and credential-shaped text from a
// note before it is stored: an error an Agent or Herdr hands back may echo the
// prompt it was given.
func redactForRun(tx queryer, runID, text string) string {
	var brief string
	_ = tx.QueryRow(`SELECT t.brief FROM tasks t JOIN runs r ON r.task_id=t.id WHERE r.id=?`, runID).Scan(&brief)
	return Redact(text, brief)
}

// TaskTimeout is the task's own time limit, 0 when it has none.
func (s *Store) TaskTimeout(taskID string) time.Duration {
	var m int
	if err := s.db.QueryRow(`SELECT timeout_min FROM task_limits WHERE task_id=?`, taskID).Scan(&m); err != nil || m <= 0 {
		return 0
	}
	return time.Duration(m) * time.Minute
}

// TaskRequiresChecks says whether the task asked for approved checks.
func (s *Store) TaskRequiresChecks(taskID string) bool {
	var n int
	return s.db.QueryRow(`SELECT require_checks FROM task_limits WHERE task_id=?`, taskID).Scan(&n) == nil && n != 0
}
