package personalagent

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultIdleSleep is how long a Coordinator's conversation must be quiet
// before it is put to sleep when the daemon's environment says nothing.
const DefaultIdleSleep = 30 * time.Minute

// IdleSleepAfter reads SOLCO_ASSISTANTS_IDLE_SLEEP_MIN (whole minutes): unset or
// unreadable = DefaultIdleSleep, 0 or negative = never sleep.
func IdleSleepAfter() time.Duration {
	raw := strings.TrimSpace(os.Getenv("SOLCO_ASSISTANTS_IDLE_SLEEP_MIN"))
	if raw == "" {
		return DefaultIdleSleep
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return DefaultIdleSleep
	}
	return time.Duration(max(n, 0)) * time.Minute
}

// CoordinatorLastActive is when the newest OMP transcript of an Assistant's
// Coordinator was last written (the zero time when there is none or it cannot
// be read). Every turn — the person's, the Agent's, a tool's — writes it.
func CoordinatorLastActive(assistantID string) time.Time {
	home, err := os.UserHomeDir()
	if err != nil {
		return time.Time{}
	}
	path := LastCoordinatorSession(home, assistantID)
	if path == "" {
		return time.Time{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// A Coordinator nobody has talked to for a while only holds an OMP process and
// a pane open. IdleSleeper stops such a Coordinator the way the person would
// (Stop Coordinator: state stopped, its pane closed); the next message starts it
// again with `--resume` on the same conversation, so nothing is lost but the
// boot time.
//
// It sleeps a Coordinator only when nothing could need that pane:
//   - the Agent sits at its prompt (Herdr: idle/done and interactive-ready) and
//     the conversation's transcript has not been written for After;
//   - no task of the Assistant is in flight and no approval waits for a person;
//   - no Goal is active (the Supervisor and its nudges expect a live Coordinator);
//   - nothing can be typed into the pane later: no approved wake, no report
//     cadence, no report queued or awaiting its reply.
//
// Any doubt (unreadable transcript, Herdr not answering, an unknown row) means
// the Coordinator stays up.

// IdleSleeper is stateless: every fact it needs is in the core, the
// transcript's own modification time and Herdr.
type IdleSleeper struct {
	Store *Store
	// After is how long the conversation must be quiet when the Assistant has not
	// chosen its own (Work style: assistant_runtime.idle_sleep_min); <= 0 turns
	// sleeping off for such an Assistant.
	After time.Duration
	// LastActive is when the Coordinator's conversation was last written; the
	// zero time means unknown and keeps the Coordinator awake.
	LastActive func(assistantID string) time.Time
	// AtPrompt says Herdr sees the Agent in the pane idle at its prompt.
	AtPrompt func(session, pane string) bool
	// Close closes the Coordinator's pane (HerdrPaneClose).
	Close func(session, workspace, pane string) error
}

// IdleResult counts one pass.
type IdleResult struct {
	Slept       int
	CloseFailed int
}

// Tick stops every Coordinator that has been idle for its Assistant's limit
// (After when it has none). Safe on a nil or unconfigured sleeper.
func (z *IdleSleeper) Tick() (IdleResult, error) {
	var res IdleResult
	if z == nil || z.Store == nil || z.LastActive == nil || z.AtPrompt == nil {
		return res, nil
	}
	s := z.Store
	now := s.nowMS()
	rows, err := s.db.Query(`SELECT c.assistant_id,c.state,c.task_id,c.herdr_session,c.workspace_id,c.pane_id,c.agent_session_id,c.revision,c.created_at,c.updated_at,c.stop_reason,r.idle_sleep_min
		FROM coordinators c LEFT JOIN assistant_runtime r ON r.assistant_id=c.assistant_id
		WHERE c.state=? AND c.pane_id<>''`, CoordinatorReady)
	if err != nil {
		return res, wrapBusy(err)
	}
	type candidate struct {
		c      Coordinator
		cutoff int64
	}
	var candidates []candidate
	for rows.Next() {
		var c Coordinator
		var own sql.NullInt64
		if err := rows.Scan(&c.AssistantID, &c.State, &c.TaskID, &c.HerdrSession, &c.WorkspaceID, &c.PaneID, &c.AgentSessionID, &c.Revision, &c.CreatedAt, &c.UpdatedAt, &c.StopReason, &own); err != nil {
			rows.Close()
			return res, err
		}
		after := z.After
		if own.Valid {
			after = time.Duration(own.Int64) * time.Minute
		}
		if after <= 0 {
			continue
		}
		if cutoff := now - after.Milliseconds(); c.UpdatedAt < cutoff {
			candidates = append(candidates, candidate{c, cutoff})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return res, err
	}
	rows.Close()
	for _, cand := range candidates {
		c := cand.c
		if !z.quiet(c, cand.cutoff) {
			continue
		}
		// One stop per Coordinator revision: a retried tick replays harmlessly.
		_, replayed, err := s.StopCoordinatorFor(fmt.Sprintf("idle-sleep:%s:%d", c.AssistantID, c.Revision), c.AssistantID, StopReasonIdle)
		if err != nil || replayed {
			continue
		}
		res.Slept++
		if z.Close != nil {
			if err := z.Close(c.HerdrSession, c.WorkspaceID, c.PaneID); err != nil {
				res.CloseFailed++
			}
		}
	}
	return res, nil
}

// quiet reports whether nothing could need this Coordinator right now.
func (z *IdleSleeper) quiet(c Coordinator, cutoff int64) bool {
	if last := z.LastActive(c.AssistantID); last.IsZero() || last.UnixMilli() >= cutoff {
		return false
	}
	busy, err := z.Store.needsLiveCoordinator(c.AssistantID)
	if err != nil || busy {
		return false
	}
	return z.AtPrompt(c.HerdrSession, c.PaneID)
}

// needsLiveCoordinator is true while something of this Assistant relies on its
// Coordinator pane being open (see the list above).
func (s *Store) needsLiveCoordinator(assistantID string) (bool, error) {
	count := func(query string, args ...any) (int, error) {
		var n int
		err := s.db.QueryRow(query, args...).Scan(&n)
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return n, err
	}
	checks := []struct {
		query string
		args  []any
	}{
		{`SELECT COUNT(*) FROM tasks WHERE assistant_id=? AND kind NOT IN (?,?) AND state IN (?,?,?,?,?,?)`,
			[]any{assistantID, KindCoordinator, KindGroupAgent, TaskQueued, TaskClaimed, TaskStarting, TaskRunning, TaskBlocked, TaskUncertain}},
		{`SELECT COUNT(*) FROM approvals WHERE assistant_id=? AND state=?`, []any{assistantID, ApprovalPending}},
		{`SELECT COUNT(*) FROM goals WHERE assistant_id=? AND state=?`, []any{assistantID, GoalActive}},
		{`SELECT COUNT(*) FROM heartbeats WHERE assistant_id=? AND wake_approved<>0`, []any{assistantID}},
		{`SELECT COUNT(*) FROM proactivity WHERE assistant_id=? AND report_every_min>0`, []any{assistantID}},
		{`SELECT COUNT(*) FROM coordinator_reports WHERE assistant_id=? AND (state IN (?,?) OR (state=? AND reply_state=?))`,
			[]any{assistantID, ReportQueued, ReportClaimed, ReportSubmitted, ReplyAwaiting}},
	}
	for _, check := range checks {
		n, err := count(check.query, check.args...)
		if err != nil || n > 0 {
			return true, err
		}
	}
	return false, nil
}
