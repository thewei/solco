/**
 * [INPUT]: Store 的 Assistant/Run/Task/Memory/Group/Routine SQLite 状态及独立的记忆目录
 * [OUTPUT]: ArchiveAssistant、RestoreAssistant、ListArchivedAssistants 与受外部资源校验约束的 DeleteArchivedAssistant
 * [POS]: 归档是可逆状态；永久删除在无法确认 Herdr/Group/Worktree 已清理时拒绝，不伪造清理成功
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package personalagent

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// StatusArchived removes an identity from active surfaces without losing data.
const StatusArchived = "archived"

// herdrPaneRef captures the external identity the Assistant's Runs own.
// Sort consistently so the preflight read and the write transaction can be
// compared without holding the SQLite writer lock while reading Herdr.
type herdrPaneRef struct {
	Session, Workspace, Pane string
}

func assistantHerdrPanes(q queryer, id string) ([]herdrPaneRef, error) {
	rows, err := q.Query("SELECT DISTINCT r.herdr_session,r.workspace_id,r.pane_id FROM runs r JOIN tasks t ON t.id=r.task_id "+
		"WHERE t.assistant_id=? AND r.pane_id<>'' ORDER BY r.herdr_session,r.workspace_id,r.pane_id", id)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []herdrPaneRef{}
	for rows.Next() {
		var p herdrPaneRef
		if err := rows.Scan(&p.Session, &p.Workspace, &p.Pane); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, wrapBusy(rows.Err())
}

func verifyAssistantHerdrPanes(panes []herdrPaneRef, paneGone ...func(string, string, string) (bool, error)) error {
	for _, pane := range panes {
		// An empty session is a run recorded before sessions were named; the
		// verifier then asks every running session (see HerdrPaneGone).
		if pane.Workspace == "" {
			return fmt.Errorf("%w: Assistant Run has incomplete Herdr identity; cannot verify pane closure", ErrBadState)
		}
		if len(paneGone) == 0 || paneGone[0] == nil {
			return fmt.Errorf("%w: close the Assistant's Herdr panes before archiving or deleting; cannot verify closure", ErrBadState)
		}
		gone, err := paneGone[0](pane.Session, pane.Workspace, pane.Pane)
		if err != nil {
			return fmt.Errorf("%w: cannot verify Herdr pane closure: %v", ErrBadState, err)
		}
		if !gone {
			return fmt.Errorf("%w: close the Assistant's Herdr pane before archiving or deleting", ErrBadState)
		}
	}
	return nil
}

func (s *Store) ListArchivedAssistants() ([]Assistant, error) {
	rows, err := s.db.Query("SELECT "+assistantCols+" FROM assistants WHERE status=? ORDER BY updated_at DESC,id", StatusArchived)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Assistant{}
	for rows.Next() {
		a, e := scanAssistant(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, wrapBusy(rows.Err())
}

func requireNoActiveRuns(tx *sql.Tx, id string) error {
	var n int
	err := tx.QueryRow("SELECT COUNT(*) FROM runs r JOIN tasks t ON t.id=r.task_id WHERE t.assistant_id=? AND r.status IN ('claimed','starting','running','blocked','uncertain')", id).Scan(&n)
	if err != nil {
		return wrapBusy(err)
	}
	if n > 0 {
		return fmt.Errorf("%w: settle running or uncertain Agent sessions before archiving", ErrBadState)
	}
	// Active tasks without a live Run can still be claimed by the scheduler.
	// Do not archive until those jobs finish or are explicitly cancelled.
	if err := tx.QueryRow("SELECT COUNT(*) FROM tasks WHERE assistant_id=? AND state IN ('queued','waiting','claimed','starting','running','blocked')", id).Scan(&n); err != nil {
		return wrapBusy(err)
	}
	if n > 0 {
		return fmt.Errorf("%w: settle queued or unfinished Assistant tasks before archiving", ErrBadState)
	}
	return nil
}

// settleClosedPaneWork ends the records of work whose Herdr pane the caller has
// just verified gone. A Group Agent or Coordinator whose terminal was closed
// by hand (or whose Group was archived) never reports back, so its Run stays
// `blocked`/`running` and its Task stays open forever — and would then block
// the Assistant's archive although nothing is running. Only a Run that owns a
// recorded pane is settled (cancelled); a Run still launching (no pane yet) and
// tasks that never started keep refusing, because they may still be claimed.
func settleClosedPaneWork(tx *sql.Tx, id string, now int64) error {
	if _, err := tx.Exec(`UPDATE runs SET status=?, finished_at=?, error=CASE WHEN error='' THEN 'Herdr pane closed' ELSE error END
		WHERE pane_id<>'' AND status IN (?,?,?,?,?) AND task_id IN (SELECT id FROM tasks WHERE assistant_id=?)`,
		RunCancelled, now, RunClaimed, RunStarting, RunRunning, RunBlocked, RunUncertain, id); err != nil {
		return wrapBusy(err)
	}
	if _, err := tx.Exec(`UPDATE tasks SET state=?, needs_you=0 WHERE assistant_id=? AND state IN (?,?,?,?)
		AND EXISTS (SELECT 1 FROM runs r WHERE r.task_id=tasks.id)
		AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.task_id=tasks.id AND r.status IN (?,?,?,?,?))`,
		TaskCancelled, id, TaskClaimed, TaskStarting, TaskRunning, TaskBlocked,
		RunClaimed, RunStarting, RunRunning, RunBlocked, RunUncertain); err != nil {
		return wrapBusy(err)
	}
	if _, err := tx.Exec(`UPDATE group_agents SET state=?, updated_at=? WHERE assistant_id=? AND state NOT IN (?,?)`,
		GroupAgentStopped, now, id, GroupAgentNone, GroupAgentStopped); err != nil {
		return wrapBusy(err)
	}
	return nil
}

// ArchiveAssistant preserves Goals, tasks, chats and memory. It refuses
// unfinished work, active routines, or any recorded Herdr pane whose closure
// cannot be positively verified. An archived Coordinator with settled work
// is marked stopped so Restore cannot resume a stale live identity.
func (s *Store) ArchiveAssistant(requestID, id string, rev int, paneGone ...func(string, string, string) (bool, error)) (Assistant, bool, error) {
	payload := map[string]any{"id": id, "rev": rev}
	if s.IsPrimary(id) {
		return Assistant{}, false, errPrimaryProtected("archived")
	}
	// Replays cannot require an old Herdr pane that may have been removed
	// since the first archive; the receipt already records the safe action.
	if prior, replayed, err := peekReceipt[Assistant](s, requestID, "assistant.archive", payload); err != nil || replayed {
		return prior, replayed, err
	}
	// Herdr reads can block: never perform them under SQLite's write lock.
	verifiedPanes, err := assistantHerdrPanes(s.db, id)
	if err != nil {
		return Assistant{}, false, err
	}
	if err := verifyAssistantHerdrPanes(verifiedPanes, paneGone...); err != nil {
		return Assistant{}, false, err
	}
	return execute(s, requestID, "assistant.archive", payload, func(tx *sql.Tx) (Assistant, error) {
		a, err := getAssistantTx(tx, id)
		if err != nil {
			return Assistant{}, err
		}
		if a.Primary {
			return Assistant{}, errPrimaryProtected("archived")
		}
		if a.Status == StatusArchived {
			return Assistant{}, ErrBadState
		}
		if a.Revision != rev {
			return Assistant{}, ErrStaleRevision
		}
		// A Run cannot gain a new pane after our external read without
		// invalidating the authorization to hide this Assistant.
		currentPanes, err := assistantHerdrPanes(tx, id)
		if err != nil {
			return Assistant{}, err
		}
		if !slices.Equal(verifiedPanes, currentPanes) {
			return Assistant{}, fmt.Errorf("%w: Assistant panes changed while verifying closure", ErrBadState)
		}
		if err := settleClosedPaneWork(tx, id, s.nowMS()); err != nil {
			return Assistant{}, err
		}
		if err := requireNoActiveRuns(tx, id); err != nil {
			return Assistant{}, err
		}
		var n int
		if err := tx.QueryRow("SELECT COUNT(*) FROM routines WHERE assistant_id=? AND state='active'", id).Scan(&n); err != nil {
			return Assistant{}, err
		}
		if n > 0 {
			return Assistant{}, fmt.Errorf("%w: pause or cancel active routines before archiving", ErrBadState)
		}
		now := s.nowMS()
		// Herdr confirms all recorded panes are gone. Clear any stale
		// ready/starting state so restoring can launch a fresh Coordinator.
		if _, err := tx.Exec("UPDATE coordinators SET state=?, revision=revision+1, updated_at=? WHERE assistant_id=? AND state NOT IN (?,?)",
			CoordinatorStopped, now, id, CoordinatorNone, CoordinatorStopped); err != nil {
			return Assistant{}, err
		}
		return updateAssistant(tx, now, id, rev, "status=?", StatusArchived)
	})
}

func (s *Store) RestoreAssistant(requestID, id string, rev int) (Assistant, bool, error) {
	return execute(s, requestID, "assistant.restore", map[string]any{"id": id, "rev": rev}, func(tx *sql.Tx) (Assistant, error) {
		a, err := getAssistantTx(tx, id)
		if err != nil {
			return Assistant{}, err
		}
		if a.Status != StatusArchived {
			return Assistant{}, ErrBadState
		}
		return updateAssistant(tx, s.nowMS(), id, rev, "status=?", StatusActive)
	})
}

// assistantPurgeStatements removes everything an Assistant owns, children before parents (foreign keys are on).
// Each statement takes the Assistant id (the two OR-forms take it twice). TestPurgeCoversEveryAssistantTable
// fails when a table that points at an Assistant, its tasks or its runs is missing here — a missed table
// either blocks the delete with a constraint error or leaves personal rows behind.
var assistantPurgeStatements = []string{
	"DELETE FROM group_deliveries WHERE assistant_id=? OR message_id IN (SELECT id FROM group_messages WHERE author_kind='assistant' AND author_id=?)",
	"DELETE FROM group_agents WHERE assistant_id=?",
	"DELETE FROM group_messages WHERE author_kind='assistant' AND author_id=?",
	"DELETE FROM persona_applied WHERE assistant_id=?",
	"DELETE FROM mcp_bindings WHERE assistant_id=?",
	"DELETE FROM mcp_grants WHERE assistant_id=?",
	"DELETE FROM assistant_runtime WHERE assistant_id=?",
	"DELETE FROM assistant_instructions WHERE assistant_id=?",
	"DELETE FROM proactivity WHERE assistant_id=?",
	"DELETE FROM memory_files WHERE assistant_id=?",
	"DELETE FROM memory_versions WHERE memory_id IN (SELECT id FROM memory_items WHERE assistant_id=?)",
	"DELETE FROM memory_items WHERE assistant_id=?",
	"DELETE FROM review_checks WHERE assistant_id=?",
	"DELETE FROM reviews WHERE assistant_id=?",
	"DELETE FROM activity_cursors WHERE assistant_id=?",
	"DELETE FROM coordinator_reports WHERE assistant_id=?",
	"DELETE FROM goal_facts WHERE assistant_id=?",
	"DELETE FROM goal_plan_revisions WHERE goal_id IN (SELECT id FROM goals WHERE assistant_id=?)",
	"DELETE FROM goal_plans WHERE assistant_id=?",
	"DELETE FROM approvals WHERE assistant_id=?",
	"DELETE FROM commitments WHERE assistant_id=?",
	"DELETE FROM heartbeats WHERE assistant_id=?",
	"DELETE FROM events WHERE assistant_id=?",
	"DELETE FROM coordinators WHERE assistant_id=?",
	"DELETE FROM task_deps WHERE task_id IN (SELECT id FROM tasks WHERE assistant_id=?) OR depends_on IN (SELECT id FROM tasks WHERE assistant_id=?)",
	"DELETE FROM run_results WHERE run_id IN (SELECT id FROM runs WHERE task_id IN (SELECT id FROM tasks WHERE assistant_id=?))",
	"DELETE FROM task_skills WHERE task_id IN (SELECT id FROM tasks WHERE assistant_id=?)",
	"DELETE FROM task_limits WHERE task_id IN (SELECT id FROM tasks WHERE assistant_id=?)",
	"DELETE FROM runs WHERE task_id IN (SELECT id FROM tasks WHERE assistant_id=?)",
	"DELETE FROM tasks WHERE assistant_id=?",
	"DELETE FROM goals WHERE assistant_id=?",
	"DELETE FROM skill_grants WHERE assistant_id=?",
	"DELETE FROM event_triggers WHERE assistant_id=?",
	"DELETE FROM hook_events WHERE assistant_id=?",
	"DELETE FROM hook_keys WHERE assistant_id=?",
	"DELETE FROM capability_grants WHERE assistant_id=?",
	"DELETE FROM authorization_receipts WHERE assistant_id=?",
	"DELETE FROM budget_events WHERE assistant_id=?",
	"DELETE FROM assistant_checkpoints WHERE assistant_id=?",
	"DELETE FROM assistants WHERE id=?",
}

// DeleteArchivedAssistant is intentionally conservative: deletion is refused
// if external Herdr panes, Git worktrees, routines, or shared Groups remain.
// Those resources cannot be deleted safely just by deleting SQLite rows.
// No receipt is retained after the deletion: it would contain personal data.
// A paneGone verifier is optional for tests without Herdr. Without positive
// proof of closure, previously allocated panes block deletion.
func (s *Store) DeleteArchivedAssistant(id string, rev int, paneGone ...func(string, string, string) (bool, error)) error {
	if !memorySafeID.MatchString(id) {
		return ErrInvalid
	}
	if s.IsPrimary(id) {
		return errPrimaryProtected("deleted")
	}
	// Perform external checks outside SQLite's write transaction: Herdr
	// reads can take seconds and must not hold the single-writer lock.
	verifiedPanes, err := assistantHerdrPanes(s.db, id)
	if err != nil {
		return err
	}
	if err := verifyAssistantHerdrPanes(verifiedPanes, paneGone...); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return wrapBusy(err)
	}
	defer tx.Rollback()
	a, err := getAssistantTx(tx, id)
	if err != nil {
		return err
	}
	if a.Status != StatusArchived {
		return fmt.Errorf("%w: archive before permanently deleting", ErrBadState)
	}
	if a.Revision != rev {
		return ErrStaleRevision
	}
	if err := requireNoActiveRuns(tx, id); err != nil {
		return err
	}
	currentPanes, err := assistantHerdrPanes(tx, id)
	if err != nil {
		return err
	}
	if !slices.Equal(verifiedPanes, currentPanes) {
		return fmt.Errorf("%w: Assistant panes changed while verifying closure", ErrBadState)
	}
	for _, check := range []struct{ query, reason string }{
		{"SELECT COUNT(*) FROM routines WHERE assistant_id=?", "cancel all routines and their YAML definitions first"},
		{"SELECT COUNT(*) FROM worktrees WHERE task_id IN (SELECT id FROM tasks WHERE assistant_id=?)", "clean up external Git worktrees first"},
		{"SELECT COUNT(*) FROM group_members WHERE assistant_id=?", "remove the Assistant from shared Groups first"},
	} {
		var n int
		if err := tx.QueryRow(check.query, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: %s", ErrBadState, check.reason)
		}
	}
	// Receipts contain serialized result bodies, sometimes only a task/goal
	// id rather than the owning Assistant id. Collect dependent opaque IDs
	// while rows still exist, or deleting those rows would leave personal
	// data in the idempotency ledger.
	identifiers := []string{id}
	for _, q := range []string{
		"SELECT id FROM tasks WHERE assistant_id=?",
		"SELECT id FROM goals WHERE assistant_id=?",
		"SELECT id FROM memory_items WHERE assistant_id=?",
		"SELECT id FROM approvals WHERE assistant_id=?",
		"SELECT id FROM goal_facts WHERE assistant_id=?",
	} {
		rows, err := tx.Query(q, id)
		if err != nil {
			return wrapBusy(err)
		}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return err
			}
			identifiers = append(identifiers, value)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	for _, identifier := range identifiers {
		// instr is a literal substring test: generated IDs contain
		// underscores, which would be wildcard characters in LIKE.
		if _, err := tx.Exec("DELETE FROM receipts WHERE instr(result, ?) > 0", identifier); err != nil {
			return wrapBusy(err)
		}
	}
	for _, q := range assistantPurgeStatements {
		args := []any{id}
		if strings.Contains(q, " OR message_id IN ") || strings.Contains(q, " OR depends_on IN ") {
			args = append(args, id)
		}
		if _, err := tx.Exec(q, args...); err != nil {
			return wrapBusy(fmt.Errorf("delete Assistant: %w", err))
		}
	}
	if err := tx.Commit(); err != nil {
		return wrapBusy(err)
	}
	for _, p := range []string{
		filepath.Join(MemoryRoot(), id),
		filepath.Join(WorkDirRoot(), "personal", "coordinators", id),
		filepath.Join(WorkDirRoot(), "personal", "tasks", id),
		filepath.Join(filepath.Dir(s.path), "assistants-mcp", id+".json"),
	} {
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("records deleted but file cleanup failed: %w", err)
		}
	}
	groupRoot := filepath.Join(WorkDirRoot(), "personal", "groups")
	if children, err := os.ReadDir(groupRoot); err == nil {
		for _, entry := range children {
			if !entry.IsDir() || !memorySafeID.MatchString(entry.Name()) {
				continue
			}
			groupDir := filepath.Join(groupRoot, entry.Name())
			if err := os.RemoveAll(filepath.Join(groupDir, id)); err != nil {
				return fmt.Errorf("records deleted but Group Agent folder cleanup failed: %w", err)
			}
			_ = os.Remove(groupDir) // only if empty; never touch another member
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("records deleted but Group Agent folder inspection failed: %w", err)
	}
	return nil
}
