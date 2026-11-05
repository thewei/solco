package nativeui

import (
	"context"
	"time"

	"github.com/thewei/solco/internal/personalagent"
)

// Q02: the Owner's answers to a coding task's evidence. The plugin core owns
// the review state machine; this file only reads it for the drawer and sends
// the person's choice (Accept → merge card → Merge, Request changes, Reject).

// A review's approved checks can run for minutes and a merge touches the
// repository, so these calls get more room than an ordinary write.
const (
	assistantsReviewStartTimeout = 12 * time.Minute
	assistantsMergeTimeout       = 2 * time.Minute
)

// AssistantsReview is a coding task's review plus the state of its merge card,
// so the page can offer Merge once the person has said yes.
type AssistantsReview struct {
	personalagent.Review
	// ApprovalState is the merge card's state ("" = no card yet).
	ApprovalState string `json:"approvalState"`
}

// reviewOf reads one coding task's review for the drawer. Best effort: a task
// that is not a coding task, has no evidence yet, or an older plugin answers
// nothing, and the card simply offers no review.
func (b *AssistantsBridge) reviewOf(ctx context.Context, c *personalagent.Client, t personalagent.Task, approvals *[]personalagent.Approval) *AssistantsReview {
	if t.Kind != "coding" || (t.State != "review_required" && t.ReviewState == "") {
		return nil
	}
	rv, err := c.TaskReview(ctx, t.ID)
	if err != nil || rv == nil {
		return nil
	}
	out := &AssistantsReview{Review: *rv}
	if rv.ApprovalID != "" {
		if *approvals == nil {
			if list, err := c.Approvals(ctx, t.AssistantID); err == nil {
				*approvals = list
			} else {
				*approvals = []personalagent.Approval{}
			}
		}
		for _, ap := range *approvals {
			if ap.ID == rv.ApprovalID {
				out.ApprovalState = ap.State
			}
		}
	}
	return out
}

// StartReview collects a finished coding task's evidence and runs the
// Assistant's approved checks.
func (b *AssistantsBridge) StartReview(ctx context.Context, requestID, taskID string) error {
	return b.writeWithin(ctx, assistantsReviewStartTimeout, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.StartReview(ctx, requestID, taskID)
		return err
	})
}

// AcceptReview asks for the approval card of merging exactly this evidence;
// nothing merges until the person answers it.
func (b *AssistantsBridge) AcceptReview(ctx context.Context, requestID, reviewID string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, _, err := c.RequestMerge(ctx, requestID, reviewID)
		return err
	})
}

// MergeReview merges a review whose card was approved and still describes the branch.
func (b *AssistantsBridge) MergeReview(ctx context.Context, reviewID string) error {
	return b.writeWithin(ctx, assistantsMergeTimeout, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.MergeReview(ctx, reviewID)
		return err
	})
}

// ResumeReview sends the task back to its own worktree with the person's words.
func (b *AssistantsBridge) ResumeReview(ctx context.Context, requestID, reviewID, feedback string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.ResumeReview(ctx, requestID, reviewID, feedback)
		return err
	})
}

// RejectReview closes the task without merging; the work stays for inspection.
func (b *AssistantsBridge) RejectReview(ctx context.Context, requestID, reviewID, reason string) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.RejectReview(ctx, requestID, reviewID, reason)
		return err
	})
}
