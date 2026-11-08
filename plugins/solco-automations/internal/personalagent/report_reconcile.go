// [INPUT]: committed report marker and an OMP-owned provider transcript observation
// [OUTPUT]: verified reply IDs, or an explicit human-review state without auto-retrying
// [POS]: Outbox reconciliation; no model call, no synthetic Chat turn and no Agent lifecycle mutation
package personalagent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	reportReplyPollInterval = 10 * time.Second
	reportReplyReviewAfter  = 30 * time.Minute
)

func newReportMarker() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// reconcileReportReply never guesses a reply from Herdr idle or a stream
// delta. Only the exact OMP user message followed by a terminal, nonempty
// provider assistant row may advance ReplyVerified. The source file itself
// is never modified or copied to the Solco DB.
func (p *ReportPump) reconcileReportReply(r CoordinatorReport) (string, error) {
	s := p.Store
	now := s.nowMS()
	// A missing verification callback or an unusable persisted marker must
	// not strand an Assistant in "awaiting" forever. The owner can still
	// inspect the original Chat and explicitly skip/retry after the deadline.
	if p.Verify == nil || !validReportMarker(r.ReportMarker) {
		if r.LastSubmittedAt > 0 && now-r.LastSubmittedAt >= reportReplyReviewAfter.Milliseconds() {
			return p.reportReplyNeedsReview(r, ReplyAwaiting, "OMP reply verification is unavailable; check the original Chat")
		}
		return "", nil
	}
	if r.ReplyCheckedAt > 0 && now-r.ReplyCheckedAt < reportReplyPollInterval.Milliseconds() {
		return "", nil
	}
	res, err := s.db.Exec(`UPDATE coordinator_reports SET reply_checked_at=? WHERE assistant_id=? AND state=? AND reply_state=? AND report_marker=?`,
		now, r.AssistantID, ReportSubmitted, ReplyAwaiting, r.ReportMarker)
	if err != nil {
		return "", wrapBusy(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", nil
	}
	ev, scanErr := p.Verify(r.ReportMarker, r.LastSubmittedAt)
	if scanErr == nil && ev.State == ReplyVerified && validReplyIdentity(ev.SessionID) && validReplyIdentity(ev.MessageID) {
		// Persist completion and one privacy-safe Activity event atomically.
		// The event also wakes the phone's existing Activity notice pipeline;
		// its dedupe key is excluded from future automatic report source facts.
		tx, err := s.db.Begin()
		if err != nil {
			return "", wrapBusy(err)
		}
		defer tx.Rollback()
		res, err = tx.Exec(`UPDATE coordinator_reports SET reply_state=?,reply_session_id=?,reply_message_id=?,
            reply_verified_at=?,updated_at=? WHERE assistant_id=? AND state=? AND reply_state=? AND report_marker=?`,
			ReplyVerified, ev.SessionID, ev.MessageID, now, now, r.AssistantID, ReportSubmitted, ReplyAwaiting, r.ReportMarker)
		if err != nil {
			return "", wrapBusy(err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return "", nil
		}
		if _, err := addFactTx(tx, Fact{AssistantID: r.AssistantID, Kind: FactPulse,
			Text:     "Coordinator's progress report has a verified final OMP reply in the original conversation",
			Evidence: fmt.Sprintf("OMP session %s; message %s", ev.SessionID, ev.MessageID)},
			fmt.Sprintf("report:reply-verified:%s:%d", r.AssistantID, r.ThroughSeq), now); err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", wrapBusy(err)
		}
		return ReplyVerified, nil
	}
	if scanErr == nil && ev.State == ReplyInterrupted && validReplyIdentity(ev.SessionID) && validReplyIdentity(ev.MessageID) {
		return p.reportReplyNeedsReview(r, ReplyInterrupted, "OMP reported an aborted or failed response; review the original conversation")
	}
	// The source can be not-yet-flushed, unreadable, truncated or ambiguous.
	// Avoid alert storms, but do not postpone owner reconciliation forever.
	if r.LastSubmittedAt > 0 && now-r.LastSubmittedAt >= reportReplyReviewAfter.Milliseconds() {
		return p.reportReplyNeedsReview(r, ReplyAwaiting, "Could not verify an OMP final reply; review its original conversation before retrying")
	}
	return "", nil
}

func validReplyIdentity(v string) bool {
	if len(v) == 0 || len(v) > 160 {
		return false
	}
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == ':' {
			continue
		}
		return false
	}
	return true
}

func (p *ReportPump) reportReplyNeedsReview(r CoordinatorReport, replyState, reason string) (string, error) {
	s := p.Store
	tx, err := s.db.Begin()
	if err != nil {
		return "", wrapBusy(err)
	}
	defer tx.Rollback()
	now := s.nowMS()
	res, err := tx.Exec(`UPDATE coordinator_reports SET state=?,reply_state=?,last_error=?,updated_at=?
      WHERE assistant_id=? AND state=? AND reply_state=? AND report_marker=?`,
		ReportUncertain, replyState, reason, now, r.AssistantID, ReportSubmitted, ReplyAwaiting, r.ReportMarker)
	if err != nil {
		return "", wrapBusy(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", nil
	}
	// Use the existing warning prefix so a report error never triggers
	// another round of reporting about itself.
	if _, err := addFactTx(tx, Fact{AssistantID: r.AssistantID, Kind: FactBlocked,
		Text: "Coordinator report has no verified OMP reply; review the original OMP Chat before retrying"},
		fmt.Sprintf("report:uncertain:%s:%d", r.AssistantID, r.ThroughSeq), now); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", wrapBusy(err)
	}
	return ReportUncertain, nil
}
