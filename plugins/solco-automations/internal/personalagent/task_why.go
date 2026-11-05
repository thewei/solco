package personalagent

import (
	"fmt"
	"strings"
)

// C08: "why is this stuck?" answered from the records, in fixed phrases. A
// Coordinator (or a person) gets the state, the one thing it waits for and,
// for a failed run, the stable reason code — never the run's free-form error.

// TaskWhy is the answer.
type TaskWhy struct {
	TaskID    string   `json:"taskId"`
	State     string   `json:"state"`
	Reason    string   `json:"reason"`
	Code      string   `json:"code,omitempty"`
	WaitingOn []string `json:"waitingOn,omitempty"`
	Approval  string   `json:"approvalId,omitempty"`
	NeedsYou  bool     `json:"needsYou"`
}

// TaskWhy explains one task's current state.
func (s *Store) TaskWhy(taskID string) (TaskWhy, error) {
	t, err := s.GetTask(taskID)
	if err != nil {
		return TaskWhy{}, err
	}
	w := TaskWhy{TaskID: t.ID, State: t.State, NeedsYou: t.NeedsYou}
	if aps, err := s.ListApprovals(t.AssistantID); err == nil {
		for _, ap := range aps {
			if ap.State == ApprovalPending && ap.SubjectKind == "task" && ap.SubjectID == t.ID {
				w.Approval = ap.ID
			}
		}
	}
	var lastErr string
	_ = s.db.QueryRow(`SELECT error FROM runs WHERE task_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`, t.ID).Scan(&lastErr)
	if code, _, found := strings.Cut(lastErr, ": "); found && isStableCode(code) {
		w.Code = code
	}
	switch t.State {
	case TaskDraft:
		w.Reason, w.NeedsYou = "A draft: it does not run until a person publishes it.", true
	case TaskWaiting:
		deps, _ := s.Dependencies(t.ID)
		var names []string
		for _, id := range deps {
			if d, err := s.GetTask(id); err == nil && d.State != TaskDone {
				w.WaitingOn = append(w.WaitingOn, d.ID)
				names = append(names, fmt.Sprintf("%s (%s, %s)", clip(d.Title, 60), d.ID, d.State))
			}
		}
		w.Reason = "Waiting for " + strings.Join(names, "; ") + " to finish."
	case TaskQueued:
		w.Reason = "Queued: the dispatcher starts it as soon as it has room."
		if a, err := s.GetAssistant(t.AssistantID); err == nil && a.Status != StatusActive {
			w.Reason = "Queued, but this Assistant is paused, so nothing is dispatched."
		}
	case TaskClaimed, TaskStarting:
		w.Reason = "Starting: Solco is launching the worker in Herdr."
	case TaskRunning:
		w.Reason = "Running in its Herdr pane."
	case TaskBlocked:
		w.Reason, w.NeedsYou = "Blocked on a prompt in its pane that only a person can answer.", true
	case TaskReviewRequired:
		w.Reason, w.NeedsYou = "Finished; waiting for a person's review.", true
	case TaskFailed:
		w.Reason = "The last run failed."
		if w.Code != "" {
			w.Reason = "The last run failed (" + w.Code + ")."
		}
	case TaskUncertain:
		w.Reason, w.NeedsYou = "Solco could not confirm the start; check Herdr before retrying.", true
	case TaskDone:
		w.Reason = "Done."
	case TaskCancelled:
		w.Reason = "Cancelled."
	}
	if w.Approval != "" {
		w.Reason, w.NeedsYou = "Waiting for the person's approval ("+w.Approval+").", true
	}
	return w, nil
}

func isStableCode(code string) bool {
	if code == "" {
		return false
	}
	for _, c := range code {
		if (c < 'a' || c > 'z') && c != '_' {
			return false
		}
	}
	return true
}
