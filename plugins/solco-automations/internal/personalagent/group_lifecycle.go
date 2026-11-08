package personalagent

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// A Group leaves the live surfaces in two steps, like an Assistant: Archive
// hides it (the log, members and Group Agent rows stay, nothing can be posted
// or delivered) and Restore brings it back; Delete Forever removes an archived
// Group's conversation for good. Both steps refuse while one of its Group
// Agents may still own a real Herdr pane: the pane is Herdr's, so the core
// asks Herdr (the same read-only PaneGone check the Assistant lifecycle uses)
// and never guesses.
//
// What a Group owns and Delete Forever therefore removes: the group row, its
// members, its message log, deliveries and Group Agent rows, the receipts that
// carry the group id, the surfaces' persona records and `personal/groups/<id>`.
// What it does not: the Assistants themselves, their tasks/runs (a Group Agent
// task is a hidden work record of its Assistant and leaves with that
// Assistant), and the Goals/tasks a member filed from the Group.

type groupPaneRef = herdrPaneRef

// groupHerdrPanes lists the Herdr identities the Group's Agents recorded.
func groupHerdrPanes(q queryer, groupID string) ([]groupPaneRef, error) {
	rows, err := q.Query(`SELECT herdr_session,workspace_id,pane_id FROM group_agents
		WHERE group_id=? AND pane_id<>'' ORDER BY herdr_session,workspace_id,pane_id`, groupID)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []groupPaneRef{}
	for rows.Next() {
		var p groupPaneRef
		if err := rows.Scan(&p.Session, &p.Workspace, &p.Pane); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, wrapBusy(rows.Err())
}

func verifyGroupHerdrPanes(panes []groupPaneRef, paneGone ...func(string, string, string) (bool, error)) error {
	for _, pane := range panes {
		if pane.Workspace == "" {
			return fmt.Errorf("%w: a group agent has an incomplete Herdr identity; cannot verify its pane is closed", ErrBadState)
		}
		if len(paneGone) == 0 || paneGone[0] == nil {
			return fmt.Errorf("%w: close the group agents' Herdr panes first; cannot verify they are closed", ErrBadState)
		}
		gone, err := paneGone[0](pane.Session, pane.Workspace, pane.Pane)
		if err != nil {
			return fmt.Errorf("%w: cannot verify the group agent pane is closed: %v", ErrBadState, err)
		}
		if !gone {
			return fmt.Errorf("%w: close the group agents' Herdr panes first", ErrBadState)
		}
	}
	return nil
}

// requireNoStartingGroupAgent refuses while a Group Agent is queued or being
// launched: its pane does not exist yet, so closure cannot be verified.
func requireNoStartingGroupAgent(q queryer, groupID string) error {
	var n int
	if err := q.QueryRow(`SELECT COUNT(*) FROM group_agents WHERE group_id=? AND pane_id='' AND state=?`,
		groupID, GroupAgentStarting).Scan(&n); err != nil {
		return wrapBusy(err)
	}
	if n > 0 {
		return fmt.Errorf("%w: a group agent is still starting; wait for it, then close its pane first", ErrBadState)
	}
	return nil
}

// ListArchivedGroups returns the archived Groups, most recently changed first.
func (s *Store) ListArchivedGroups() ([]Group, error) {
	rows, err := s.db.Query(`SELECT id FROM chat_groups WHERE status=? ORDER BY updated_at DESC, id`, StatusArchived)
	if err != nil {
		return nil, wrapBusy(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	out := []Group{}
	for _, id := range ids {
		g, err := getGroupTx(s.db, id)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// ArchiveGroup hides a Group without losing anything. Waiting deliveries are
// skipped (an archived Group calls nobody), and a Group Agent row whose pane
// Herdr confirms gone is marked stopped.
func (s *Store) ArchiveGroup(requestID, id string, rev int, paneGone ...func(string, string, string) (bool, error)) (Group, bool, error) {
	payload := map[string]any{"id": id, "rev": rev}
	if prior, replayed, err := peekReceipt[Group](s, requestID, "group.archive", payload); err != nil || replayed {
		return prior, replayed, err
	}
	// Herdr reads can block: never perform them under SQLite's write lock.
	verified, err := groupHerdrPanes(s.db, id)
	if err != nil {
		return Group{}, false, err
	}
	if err := verifyGroupHerdrPanes(verified, paneGone...); err != nil {
		return Group{}, false, err
	}
	return execute(s, requestID, "group.archive", payload, func(tx *sql.Tx) (Group, error) {
		g, err := getGroupTx(tx, id)
		if err != nil {
			return Group{}, err
		}
		if g.Status != "active" {
			return Group{}, ErrBadState
		}
		if g.Revision != rev {
			return Group{}, ErrStaleRevision
		}
		if err := requireNoStartingGroupAgent(tx, id); err != nil {
			return Group{}, err
		}
		current, err := groupHerdrPanes(tx, id)
		if err != nil {
			return Group{}, err
		}
		if !slices.Equal(verified, current) {
			return Group{}, fmt.Errorf("%w: the group agents' panes changed while verifying closure", ErrBadState)
		}
		now := s.nowMS()
		if _, err := tx.Exec(`UPDATE group_deliveries SET state=?, updated_at=? WHERE group_id=? AND state IN (?,?)`,
			DeliverySkipped, now, id, DeliveryQueued, DeliveryDelivering); err != nil {
			return Group{}, err
		}
		if _, err := tx.Exec(`UPDATE group_agents SET state=?, revision=revision+1, updated_at=?
			WHERE group_id=? AND state IN (?,?,?)`,
			GroupAgentStopped, now, id, GroupAgentStarting, GroupAgentReady, GroupAgentBlocked); err != nil {
			return Group{}, err
		}
		res, err := tx.Exec(`UPDATE chat_groups SET status=?, revision=revision+1, updated_at=? WHERE id=? AND revision=?`,
			StatusArchived, now, id, rev)
		if err != nil {
			return Group{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return Group{}, ErrStaleRevision
		}
		return getGroupTx(tx, id)
	})
}

// RestoreGroup brings an archived Group back to the live surfaces.
func (s *Store) RestoreGroup(requestID, id string, rev int) (Group, bool, error) {
	return execute(s, requestID, "group.restore", map[string]any{"id": id, "rev": rev}, func(tx *sql.Tx) (Group, error) {
		g, err := getGroupTx(tx, id)
		if err != nil {
			return Group{}, err
		}
		if g.Status != StatusArchived {
			return Group{}, ErrBadState
		}
		if g.Revision != rev {
			return Group{}, ErrStaleRevision
		}
		if _, err := tx.Exec(`UPDATE chat_groups SET status='active', revision=revision+1, updated_at=? WHERE id=?`,
			s.nowMS(), id); err != nil {
			return Group{}, err
		}
		return getGroupTx(tx, id)
	})
}

// DeleteArchivedGroup removes an archived Group's conversation for good. No
// receipt of the deletion is kept, and the receipts of the Group's own earlier
// requests (their results carry its messages) are removed with it.
func (s *Store) DeleteArchivedGroup(id string, rev int, paneGone ...func(string, string, string) (bool, error)) error {
	if !memorySafeID.MatchString(id) {
		return ErrInvalid
	}
	verified, err := groupHerdrPanes(s.db, id)
	if err != nil {
		return err
	}
	if err := verifyGroupHerdrPanes(verified, paneGone...); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return wrapBusy(err)
	}
	defer tx.Rollback()
	g, err := getGroupTx(tx, id)
	if err != nil {
		return err
	}
	if g.Status != StatusArchived {
		return fmt.Errorf("%w: archive the group before deleting it", ErrBadState)
	}
	if g.Revision != rev {
		return ErrStaleRevision
	}
	if err := requireNoStartingGroupAgent(tx, id); err != nil {
		return err
	}
	current, err := groupHerdrPanes(tx, id)
	if err != nil {
		return err
	}
	if !slices.Equal(verified, current) {
		return fmt.Errorf("%w: the group agents' panes changed while verifying closure", ErrBadState)
	}
	// Results carry the group id (Group, GroupAgent and Message all name it);
	// instr is a literal test, the generated ids hold underscores.
	for _, q := range []string{
		`DELETE FROM receipts WHERE instr(result, ?) > 0`,
		`DELETE FROM persona_applied WHERE surface='group:'||?`,
		`DELETE FROM group_deliveries WHERE group_id=?`,
		`DELETE FROM group_messages WHERE group_id=?`,
		`DELETE FROM group_agents WHERE group_id=?`,
		`DELETE FROM group_members WHERE group_id=?`,
		`DELETE FROM chat_groups WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return wrapBusy(fmt.Errorf("delete group: %w", err))
		}
	}
	if err := tx.Commit(); err != nil {
		return wrapBusy(err)
	}
	if err := os.RemoveAll(filepath.Join(WorkDirRoot(), "personal", "groups", id)); err != nil {
		return fmt.Errorf("records deleted but group folder cleanup failed: %w", err)
	}
	return nil
}
