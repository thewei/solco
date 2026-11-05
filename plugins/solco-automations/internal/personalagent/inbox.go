package personalagent

import (
	"fmt"
	"sort"
	"time"
)

// E04: the Inbox. One read of one database answers "what needs a person right
// now" — approvals, reviews, failed starts, stalls, finished work to
// acknowledge — for the desktop page, the phone and the notifier alike. Every
// rule here is a deterministic fact about a row; nothing asks a model to guess
// progress and nothing polls Herdr on a timer (the dispatcher already records
// what Herdr reports, as it reports it).

// Inbox item kinds.
const (
	InboxApproval    = "approval"
	InboxFailedStart = "failed_start"
	InboxStalled     = "stalled"
	InboxReview      = "review"
	InboxNeedsYou    = "needs_you"
	InboxNews        = "news"
)

var inboxPriority = map[string]int{
	InboxApproval: 4, InboxFailedStart: 3, InboxStalled: 3, InboxReview: 2, InboxNeedsYou: 2, InboxNews: 0,
}

// InboxOptions tunes one read. The zero value is the product default.
type InboxOptions struct {
	// StartStall is how long a claimed or starting task may take to reach a
	// running Agent (default 5 minutes).
	StartStall time.Duration
	// BlockStall is how long a task may sit blocked on a dialog nobody has
	// answered (default 10 minutes).
	BlockStall time.Duration
	// RunStall is how long a task may stay running before the inbox says so
	// (default 90 minutes: past the dispatcher's own 45-minute timeout).
	RunStall time.Duration
	// News adds finished-work events newer than Since to the snapshot.
	News  bool
	Since int64
	// Limit bounds the number of news items (default 20).
	Limit int
}

func (o InboxOptions) withDefaults() InboxOptions {
	if o.StartStall <= 0 {
		o.StartStall = 5 * time.Minute
	}
	if o.BlockStall <= 0 {
		o.BlockStall = 10 * time.Minute
	}
	if o.RunStall <= 0 {
		o.RunStall = 90 * time.Minute
	}
	if o.Limit <= 0 || o.Limit > 100 {
		o.Limit = 20
	}
	return o
}

// InboxItem is one thing in the inbox. Detail is a fixed phrase plus a title:
// never a prompt, a brief, an error text or a path.
type InboxItem struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	AssistantID string `json:"assistantId"`
	TaskID      string `json:"taskId,omitempty"`
	GoalID      string `json:"goalId,omitempty"`
	ApprovalID  string `json:"approvalId,omitempty"`
	Title       string `json:"title"`
	Detail      string `json:"detail"`
	Priority    int    `json:"priority"`
	At          int64  `json:"at"`
	// Reason is the stable code behind the card (start_failed, task_failed,
	// permission_prompt, review_checks_failed, approval_<subject>, …): clients
	// branch on it, never on Detail's words.
	Reason string `json:"reason,omitempty"`
	// RunID and PaneID name the task's newest run, so the card can go straight
	// back to where the work happened; Actions are what the card can do there
	// (open_pane, retry, ack, review, decide, open_goal). Opening the task
	// itself is always possible and is not listed.
	RunID   string   `json:"runId,omitempty"`
	PaneID  string   `json:"paneId,omitempty"`
	Actions []string `json:"actions,omitempty"`
}

// Inbox is one snapshot. NeedsYou counts everything except news; Seq is the
// event watermark the snapshot was read at (the next News read resumes there).
type Inbox struct {
	Items    []InboxItem `json:"items"`
	NeedsYou int         `json:"needsYou"`
	Seq      int64       `json:"seq"`
}

// Inbox reads one Assistant's inbox ("" = every Assistant's) in a single
// transaction, so the items and the watermark describe the same instant.
func (s *Store) Inbox(assistantID string, opt InboxOptions) (Inbox, error) {
	opt = opt.withDefaults()
	tx, err := s.db.Begin()
	if err != nil {
		return Inbox{}, wrapBusy(err)
	}
	defer func() { _ = tx.Rollback() }()
	now := s.nowMS()
	out := Inbox{Items: []InboxItem{}}
	add := func(it InboxItem) {
		if it.Priority == 0 {
			it.Priority = inboxPriority[it.Kind]
		}
		out.Items = append(out.Items, it)
	}

	// Approvals a person has not decided.
	aq := `SELECT id,assistant_id,subject_kind,subject_id,action,created_at FROM approvals WHERE state=?`
	args := []any{ApprovalPending}
	if assistantID != "" {
		aq += ` AND assistant_id=?`
		args = append(args, assistantID)
	}
	rows, err := tx.Query(aq+` ORDER BY created_at, id`, args...)
	if err != nil {
		return Inbox{}, wrapBusy(err)
	}
	pendingFor := map[string]bool{} // task id -> has a pending approval
	for rows.Next() {
		var id, asst, kind, subject, action string
		var at int64
		if err := rows.Scan(&id, &asst, &kind, &subject, &action, &at); err != nil {
			_ = rows.Close()
			return Inbox{}, err
		}
		if kind == "task" {
			pendingFor[subject] = true
		}
		add(InboxItem{ID: "approval:" + id, Kind: InboxApproval, AssistantID: asst, TaskID: taskIfTask(kind, subject), ApprovalID: id, Title: clip(action, 120), Detail: "Waiting for your decision", At: at,
			Reason: "approval_" + kind, Actions: []string{"decide"}})
	}
	_ = rows.Close()

	// Tasks, with the facts of their latest run.
	tq := `SELECT ` + prefixed("t", taskCols) + `,
		COALESCE((SELECT r.pane_id FROM runs r WHERE r.task_id=t.id ORDER BY r.created_at DESC, r.id DESC LIMIT 1),''),
		COALESCE((SELECT r.id FROM runs r WHERE r.task_id=t.id ORDER BY r.created_at DESC, r.id DESC LIMIT 1),''),
		COALESCE((SELECT r.fail_code FROM runs r WHERE r.task_id=t.id ORDER BY r.created_at DESC, r.id DESC LIMIT 1),'')
		FROM tasks t WHERE t.kind NOT IN (?,?)`
	targs := []any{KindCoordinator, KindGroupAgent}
	if assistantID != "" {
		tq += ` AND t.assistant_id=?`
		targs = append(targs, assistantID)
	}
	trows, err := tx.Query(tq+` ORDER BY t.updated_at DESC, t.id`, targs...)
	if err != nil {
		return Inbox{}, wrapBusy(err)
	}
	type scanned struct {
		t                   Task
		pane, run, failCode string
	}
	var tasks []scanned
	for trows.Next() {
		var sc scanned
		t, err := scanTask(withExtra{trows, []any{&sc.pane, &sc.run, &sc.failCode}})
		if err != nil {
			_ = trows.Close()
			return Inbox{}, err
		}
		sc.t = t
		tasks = append(tasks, sc)
	}
	_ = trows.Close()
	for _, sc := range tasks {
		t := sc.t
		age := time.Duration(now-t.UpdatedAt) * time.Millisecond
		item := InboxItem{ID: "task:" + t.ID, AssistantID: t.AssistantID, TaskID: t.ID, Title: clip(t.Title, 120), At: t.UpdatedAt, RunID: sc.run, PaneID: sc.pane}
		var actions []string
		switch {
		case t.State == TaskFailed && sc.pane == "":
			item.Kind, item.Reason, item.Detail = InboxFailedStart, "start_failed", "Could not start — nothing was created"
			actions = []string{"retry"}
			if sc.failCode == FailCodeHerdrUnreachable {
				item.Reason, item.Detail = FailCodeHerdrUnreachable, "Herdr could not be reached — start it, then retry"
			}
		case t.State == TaskUncertain:
			item.Kind, item.Reason, item.Detail = InboxFailedStart, "start_unknown", "Start outcome unknown — check before retrying"
			actions = []string{"open_pane"}
		case (t.State == TaskClaimed || t.State == TaskStarting) && age >= opt.StartStall:
			item.Kind, item.Reason, item.Detail = InboxStalled, "not_started", "Has not started"
			actions = []string{"open_pane"}
		case t.State == TaskBlocked && age >= opt.BlockStall:
			item.Kind, item.Reason, item.Detail = InboxStalled, "permission_prompt", "Waiting on a prompt nobody has answered"
			actions = []string{"open_pane"}
		case t.State == TaskRunning && age >= opt.RunStall:
			item.Kind, item.Reason, item.Detail = InboxStalled, "long_running", "Running for a long time"
			actions = []string{"open_pane"}
		case t.State == TaskReviewRequired:
			if pendingFor[t.ID] {
				continue // the approval card already carries it
			}
			item.Kind, item.Reason, item.Detail = InboxReview, "review_ready", "Ready for your review"
			switch t.ReviewState {
			case ReviewChecksFailed:
				item.Reason, item.Detail, item.Priority = "review_checks_failed", "Checks failed — read them before you decide", 3
			case ReviewStale:
				item.Reason, item.Detail = "review_stale", "Changed since it was reviewed — review again"
			case ReviewMergeFailed:
				item.Reason, item.Detail, item.Priority = "merge_failed", "The merge did not go through — the work is kept", 3
			case ReviewAwaiting:
				item.Reason, item.Detail = "merge_answered", "Merge answered — open it to finish"
			}
			actions = []string{"review"}
		case t.NeedsYou:
			if pendingFor[t.ID] {
				continue
			}
			item.Kind, item.Detail = InboxNeedsYou, needsYouPhrase(t)
			switch t.State {
			case TaskFailed:
				item.Reason, actions = "task_failed", []string{"open_pane", "retry"}
			case TaskBlocked:
				item.Reason, actions = "permission_prompt", []string{"open_pane"}
			case TaskWaiting:
				item.Reason = "dependency_unfinished"
			case TaskDone:
				item.Reason, actions = "task_done", []string{"ack"}
			default:
				item.Reason = "needs_you"
			}
		default:
			continue
		}
		for _, a := range actions {
			if a != "open_pane" || sc.pane != "" {
				item.Actions = append(item.Actions, a)
			}
		}
		add(item)
	}

	// A Goal whose plan is written but that nobody has started: the Coordinator
	// is done, the next move is the person's (start or cancel). An unplanned
	// draft is still being written, so it is not listed.
	gq := `SELECT g.id,g.assistant_id,g.title,g.updated_at FROM goals g
		JOIN goal_plans p ON p.goal_id=g.id WHERE g.state=?`
	gargs := []any{GoalDraft}
	if assistantID != "" {
		gq += ` AND g.assistant_id=?`
		gargs = append(gargs, assistantID)
	}
	grows, err := tx.Query(gq+` ORDER BY g.updated_at DESC, g.id`, gargs...)
	if err != nil {
		return Inbox{}, wrapBusy(err)
	}
	for grows.Next() {
		var id, asst, title string
		var at int64
		if err := grows.Scan(&id, &asst, &title, &at); err != nil {
			_ = grows.Close()
			return Inbox{}, err
		}
		add(InboxItem{ID: "goal:" + id, Kind: InboxNeedsYou, AssistantID: asst, GoalID: id, Title: clip(title, 120), Detail: "Plan ready — start it or cancel it", At: at,
			Reason: "plan_ready", Actions: []string{"open_goal"}})
	}
	_ = grows.Close()

	// The watermark, and optionally what happened since a reader last looked.
	eq := `SELECT COALESCE(MAX(rowid),0) FROM events`
	var eargs []any
	if assistantID != "" {
		eq += ` WHERE assistant_id=?`
		eargs = append(eargs, assistantID)
	}
	if err := tx.QueryRow(eq, eargs...).Scan(&out.Seq); err != nil {
		return Inbox{}, wrapBusy(err)
	}
	if opt.News {
		nq := `SELECT ` + eventCols + ` FROM events WHERE rowid>? AND kind IN ('task.completed','pr.merged','pr.changed','review.merged')`
		nargs := []any{opt.Since}
		if assistantID != "" {
			nq += ` AND assistant_id=?`
			nargs = append(nargs, assistantID)
		}
		nrows, err := tx.Query(nq+` ORDER BY rowid DESC LIMIT ?`, append(nargs, opt.Limit)...)
		if err != nil {
			return Inbox{}, wrapBusy(err)
		}
		for nrows.Next() {
			e, err := scanEvent(nrows)
			if err != nil {
				_ = nrows.Close()
				return Inbox{}, err
			}
			add(InboxItem{ID: "news:" + e.ID, Kind: InboxNews, AssistantID: e.AssistantID, TaskID: e.TaskID, Title: clip(e.Detail, 120), Detail: e.Kind, At: e.SeenAt})
		}
		_ = nrows.Close()
	}

	sort.SliceStable(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		if a.At != b.At {
			return a.At > b.At
		}
		return a.ID < b.ID
	})
	out.NeedsYou = 0
	for _, it := range out.Items {
		if it.Kind != InboxNews {
			out.NeedsYou++
		}
	}
	return out, nil
}

func taskIfTask(kind, id string) string {
	if kind == "task" {
		return id
	}
	return ""
}

func needsYouPhrase(t Task) string {
	switch t.State {
	case TaskFailed:
		return "Failed — retry or look at it"
	case TaskBlocked:
		return "Blocked on a prompt"
	case TaskWaiting:
		return "Waiting behind a task that did not finish"
	case TaskDone:
		return "Finished — acknowledge when you have read it"
	}
	return fmt.Sprintf("Needs you (%s)", t.State)
}

// withExtra scans a task row plus trailing columns of the same query.
type withExtra struct {
	r     rowScanner
	extra []any
}

func (w withExtra) Scan(dest ...any) error { return w.r.Scan(append(dest, w.extra...)...) }
