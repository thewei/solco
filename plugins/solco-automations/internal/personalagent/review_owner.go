package personalagent

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Q02: the Owner's answers to a coding task's evidence.
//
//	Accept  — RequestMerge → person approves the exact head/diff/tip → Merge
//	Resume  — send the task back to its own worktree and branch with the Owner's words
//	Reject  — close the task; nothing merges, the work stays for inspection
//
// Resume and Reject are the person's alone (a Coordinator's scope does not list
// them). Both withdraw the merge card that was waiting on this review, so an
// answer can never be given to evidence the Owner has already turned down.

// MaxOwnerNote bounds the Owner's feedback or reason.
const MaxOwnerNote = 2000

func ownerNote(s string, required bool) (string, error) {
	s = strings.TrimSpace(s)
	if required && s == "" {
		return "", fmt.Errorf("%w: say what should change", ErrInvalid)
	}
	if utf8.RuneCountInString(s) > MaxOwnerNote {
		return "", fmt.Errorf("%w: keep it under %d characters", ErrInvalid, MaxOwnerNote)
	}
	return s, nil
}

// answerReviewTx closes one open review with the Owner's answer and withdraws
// its merge card. It returns the review and the task it belongs to.
func (r *Reviewer) answerReviewTx(tx *sql.Tx, reviewID, state, text string) (Review, Task, error) {
	rv, err := getReviewTx(tx, reviewID)
	if err != nil {
		return rv, Task{}, err
	}
	switch rv.State {
	case ReviewReady, ReviewChecksFailed, ReviewAwaiting, ReviewStale, ReviewMergeFailed:
	default:
		return rv, Task{}, fmt.Errorf("%w: the review is %s", ErrBadState, rv.State)
	}
	task, err := getTaskTx(tx, rv.TaskID)
	if err != nil {
		return rv, task, err
	}
	if task.State != TaskReviewRequired {
		return rv, task, fmt.Errorf("%w: the task is %s, not waiting for review", ErrBadState, task.State)
	}
	now := r.Store.nowMS()
	if rv.ApprovalID != "" {
		if _, err := tx.Exec(`UPDATE approvals SET state=?, decided_at=? WHERE id=? AND state=?`,
			ApprovalDenied, now, rv.ApprovalID, ApprovalPending); err != nil {
			return rv, task, err
		}
	}
	if _, err := tx.Exec(`UPDATE reviews SET state=?, error=?, revision=revision+1, updated_at=? WHERE id=?`, state, text, now, rv.ID); err != nil {
		return rv, task, err
	}
	return rv, task, nil
}

// Resume sends a task whose evidence is not good enough back to its own
// worktree and branch (the work so far is kept) as a new attempt, with the
// Owner's words appended to its brief. The next finished attempt is reviewed
// afresh from its new head.
func (r *Reviewer) Resume(requestID, reviewID, feedback string) (Review, bool, error) {
	fb, err := ownerNote(feedback, true)
	if err != nil {
		return Review{}, false, err
	}
	payload := map[string]string{"review": reviewID, "feedback": fb}
	return execute(r.Store, requestID, "review.resume", payload, func(tx *sql.Tx) (Review, error) {
		rv, task, err := r.answerReviewTx(tx, reviewID, ReviewChangesRequested, "Changes requested: "+fb)
		if err != nil {
			return rv, err
		}
		a, err := getAssistantTx(tx, task.AssistantID)
		if err != nil {
			return rv, err
		}
		if a.Status != StatusActive {
			return rv, ErrAssistantDisabled
		}
		now := r.Store.nowMS()
		brief := task.Brief + fmt.Sprintf("\n\nThe Owner reviewed your previous attempt (branch %s, commit %s) and asks for changes:\n%s\nContinue on the same branch in the same worktree.", rv.Branch, shortOf(rv.Head), fb)
		if _, err := tx.Exec(`UPDATE tasks SET state=?, brief=?, attempt=attempt+1, needs_you=0, review_state=?, revision=revision+1, updated_at=? WHERE id=?`,
			TaskQueued, brief, ReviewChangesRequested, now, task.ID); err != nil {
			return rv, err
		}
		if err := emitReview(tx, task, rv.ID, ReviewChangesRequested, "review.changes_requested", now); err != nil {
			return rv, err
		}
		return getReviewTx(tx, rv.ID)
	})
}

// Reject closes a coding task whose evidence the Owner turned down. The task
// is cancelled (a person may still retry it on purpose), nothing merges, and
// the worktree stays so the work can be looked at.
func (r *Reviewer) Reject(requestID, reviewID, reason string) (Review, error) {
	why, err := ownerNote(reason, false)
	if err != nil {
		return Review{}, err
	}
	text := "Rejected"
	if why != "" {
		text += ": " + why
	}
	payload := map[string]string{"review": reviewID, "reason": why}
	rv, _, err := execute(r.Store, requestID, "review.reject", payload, func(tx *sql.Tx) (Review, error) {
		rv, task, err := r.answerReviewTx(tx, reviewID, ReviewRejected, text)
		if err != nil {
			return rv, err
		}
		now := r.Store.nowMS()
		if _, err := tx.Exec(`UPDATE tasks SET state=?, needs_you=0, review_state=?, revision=revision+1, updated_at=? WHERE id=?`,
			TaskCancelled, ReviewRejected, now, task.ID); err != nil {
			return rv, err
		}
		if err := emitReview(tx, task, rv.ID, ReviewRejected, "review.rejected", now); err != nil {
			return rv, err
		}
		return getReviewTx(tx, rv.ID)
	})
	return rv, err
}
