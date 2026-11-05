package personalagent

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Checkpoint (Gap A03): the Assistant's own durable "where I am" record, written
// by the Coordinator at the end of every autonomous turn and read back at the
// start of the next, so a wake never depends on what a chat context still
// remembers. Progress, next action, blockers and stop condition are the
// Assistant's words; LastSeq is the core's own — the newest ledger fact that
// existed when the checkpoint was saved — so "what had I seen" is never a claim.
type Checkpoint struct {
	AssistantID string `json:"assistantId"`
	LastSeq     int64  `json:"lastSeq"`
	Progress    string `json:"progress"`
	NextAction  string `json:"nextAction"`
	Blockers    string `json:"blockers"`
	StopWhen    string `json:"stopWhen"`
	Revision    int    `json:"revision"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// CheckpointInput is the body of `assistant checkpoint set`.
type CheckpointInput struct {
	AssistantID string `json:"assistantId"`
	Progress    string `json:"progress"`
	NextAction  string `json:"nextAction"`
	Blockers    string `json:"blockers"`
	StopWhen    string `json:"stopWhen"`
}

const maxCheckpointField = 1200

func getCheckpointTx(q queryer, id string) (Checkpoint, error) {
	cp := Checkpoint{AssistantID: id}
	err := q.QueryRow(`SELECT last_seq,progress,next_action,blockers,stop_when,revision,updated_at FROM assistant_checkpoints WHERE assistant_id=?`, id).
		Scan(&cp.LastSeq, &cp.Progress, &cp.NextAction, &cp.Blockers, &cp.StopWhen, &cp.Revision, &cp.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Checkpoint{AssistantID: id}, nil
	}
	return cp, wrapBusy(err)
}

// GetCheckpoint reads an Assistant's checkpoint; one never saved is revision 0.
func (s *Store) GetCheckpoint(id string) (Checkpoint, error) {
	if _, err := getAssistantTx(s.db, id); err != nil {
		return Checkpoint{}, err
	}
	return getCheckpointTx(s.db, id)
}

// SetCheckpoint saves a new revision. A writer that read an older revision is
// refused (ErrRevision) rather than overwriting a newer one.
func (s *Store) SetCheckpoint(requestID string, in CheckpointInput, expectedRevision int) (Checkpoint, bool, error) {
	for _, f := range []string{in.Progress, in.NextAction, in.Blockers, in.StopWhen} {
		if len([]rune(f)) > maxCheckpointField {
			return Checkpoint{}, false, fmt.Errorf("%w: a checkpoint field may hold at most %d characters", ErrInvalid, maxCheckpointField)
		}
		if looksLikeSecret(f) {
			return Checkpoint{}, false, fmt.Errorf("%w: that looks like a credential; a checkpoint never keeps credentials", ErrInvalid)
		}
	}
	if strings.TrimSpace(in.Progress+in.NextAction+in.Blockers+in.StopWhen) == "" {
		return Checkpoint{}, false, fmt.Errorf("%w: a checkpoint needs at least one field", ErrInvalid)
	}
	return execute(s, requestID, "checkpoint.set", map[string]any{"in": in, "rev": expectedRevision}, func(tx *sql.Tx) (Checkpoint, error) {
		if _, err := getAssistantTx(tx, in.AssistantID); err != nil {
			return Checkpoint{}, err
		}
		cur, err := getCheckpointTx(tx, in.AssistantID)
		if err != nil {
			return Checkpoint{}, err
		}
		if cur.Revision != expectedRevision {
			return Checkpoint{}, fmt.Errorf("%w: the checkpoint is at revision %d, not %d", ErrStaleRevision, cur.Revision, expectedRevision)
		}
		var seq int64
		if err := tx.QueryRow(`SELECT COALESCE(MAX(rowid),0) FROM goal_facts WHERE assistant_id=?`, in.AssistantID).Scan(&seq); err != nil {
			return Checkpoint{}, wrapBusy(err)
		}
		now := s.nowMS()
		next := Checkpoint{AssistantID: in.AssistantID, LastSeq: seq, Progress: strings.TrimSpace(in.Progress), NextAction: strings.TrimSpace(in.NextAction),
			Blockers: strings.TrimSpace(in.Blockers), StopWhen: strings.TrimSpace(in.StopWhen), Revision: cur.Revision + 1, UpdatedAt: now}
		if _, err := tx.Exec(`INSERT INTO assistant_checkpoints(assistant_id,last_seq,progress,next_action,blockers,stop_when,revision,updated_at)
			VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(assistant_id) DO UPDATE SET last_seq=excluded.last_seq,progress=excluded.progress,
			next_action=excluded.next_action,blockers=excluded.blockers,stop_when=excluded.stop_when,revision=excluded.revision,updated_at=excluded.updated_at`,
			next.AssistantID, next.LastSeq, next.Progress, next.NextAction, next.Blockers, next.StopWhen, next.Revision, next.UpdatedAt); err != nil {
			return Checkpoint{}, wrapBusy(err)
		}
		return next, nil
	})
}
