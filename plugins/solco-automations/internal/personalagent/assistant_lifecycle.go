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
	"strings"
)

// StatusArchived removes an identity from active surfaces without losing data.
const StatusArchived = "archived"

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

// ArchiveAssistant preserves Goals, tasks, chats, memory and sessions. It is
// refused while an Agent or an enabled routine might still be doing work.
func (s *Store) ArchiveAssistant(requestID, id string, rev int) (Assistant, bool, error) {
	return execute(s, requestID, "assistant.archive", map[string]any{"id": id, "rev": rev}, func(tx *sql.Tx) (Assistant, error) {
		a, err := getAssistantTx(tx, id)
		if err != nil {
			return Assistant{}, err
		}
		if a.Status == StatusArchived {
			return Assistant{}, ErrBadState
		}
		if a.Revision != rev {
			return Assistant{}, ErrStaleRevision
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
		return updateAssistant(tx, s.nowMS(), id, rev, "status=?", StatusArchived)
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
	// Perform external checks outside SQLite's write transaction: Herdr
	// reads can take seconds and must not hold the single-writer lock.
	panes, err := s.db.Query("SELECT DISTINCT r.herdr_session,r.workspace_id,r.pane_id FROM runs r JOIN tasks t ON t.id=r.task_id WHERE t.assistant_id=? AND r.pane_id<>''", id)
	if err != nil {
		return wrapBusy(err)
	}
	for panes.Next() {
		var sess, workspace, pane string
		if err := panes.Scan(&sess, &workspace, &pane); err != nil {
			panes.Close()
			return err
		}
		if len(paneGone) == 0 || paneGone[0] == nil {
			panes.Close()
			return fmt.Errorf("%w: close the Assistant's Herdr panes before deletion; cannot verify their state", ErrBadState)
		}
		gone, err := paneGone[0](sess, workspace, pane)
		if err != nil {
			panes.Close()
			return fmt.Errorf("%w: cannot verify Herdr pane closure: %v", ErrBadState, err)
		}
		if !gone {
			panes.Close()
			return fmt.Errorf("%w: close the Assistant's Herdr pane before deletion", ErrBadState)
		}
	}
	if err := panes.Err(); err != nil {
		panes.Close()
		return wrapBusy(err)
	}
	panes.Close()
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
	queries := []string{
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
		"DELETE FROM goal_facts WHERE assistant_id=?",
		"DELETE FROM goal_plan_revisions WHERE goal_id IN (SELECT id FROM goals WHERE assistant_id=?)",
		"DELETE FROM goal_plans WHERE assistant_id=?",
		"DELETE FROM approvals WHERE assistant_id=?",
		"DELETE FROM commitments WHERE assistant_id=?",
		"DELETE FROM heartbeats WHERE assistant_id=?",
		"DELETE FROM events WHERE assistant_id=?",
		"DELETE FROM coordinators WHERE assistant_id=?",
		"DELETE FROM task_deps WHERE task_id IN (SELECT id FROM tasks WHERE assistant_id=?) OR depends_on IN (SELECT id FROM tasks WHERE assistant_id=?)",
		"DELETE FROM runs WHERE task_id IN (SELECT id FROM tasks WHERE assistant_id=?)",
		"DELETE FROM tasks WHERE assistant_id=?",
		"DELETE FROM goals WHERE assistant_id=?",
		"DELETE FROM assistants WHERE id=?",
	}
	for _, q := range queries {
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
