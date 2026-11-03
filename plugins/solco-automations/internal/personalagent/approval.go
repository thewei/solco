package personalagent

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Approval states. "expired" is derived: a pending card past its deadline.
const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalDenied   = "denied"
	ApprovalExpired  = "expired"
)

// Approval is one question to a person about one exact action on one subject
// at one revision. It cannot be answered for anything else.
type Approval struct {
	ID               string `json:"id"`
	AssistantID      string `json:"assistantId"`
	SubjectKind      string `json:"subjectKind"`
	SubjectID        string `json:"subjectId"`
	Action           string `json:"action"`
	ActionHash       string `json:"actionHash"`
	ExpectedRevision int    `json:"expectedRevision"`
	ExpiresAt        int64  `json:"expiresAt"`
	State            string `json:"state"`
	DecidedAt        int64  `json:"decidedAt"`
	CreatedAt        int64  `json:"createdAt"`
}

// RequestApprovalInput is the body of `assistant approval request`.
type RequestApprovalInput struct {
	AssistantID string        `json:"assistantId"`
	SubjectKind string        `json:"subjectKind"` // "task" | "goal"
	SubjectID   string        `json:"subjectId"`
	Action      string        `json:"action"`
	TTL         time.Duration `json:"ttl"`
}

const approvalCols = `id,assistant_id,subject_kind,subject_id,action,action_hash,expected_revision,expires_at,state,decided_at,created_at`

func (s *Store) scanApproval(r rowScanner) (Approval, error) {
	var a Approval
	err := r.Scan(&a.ID, &a.AssistantID, &a.SubjectKind, &a.SubjectID, &a.Action, &a.ActionHash, &a.ExpectedRevision, &a.ExpiresAt, &a.State, &a.DecidedAt, &a.CreatedAt)
	if err == nil && a.State == ApprovalPending && s.nowMS() > a.ExpiresAt {
		a.State = ApprovalExpired
	}
	return a, err
}

func (s *Store) getApprovalTx(q queryer, id string) (Approval, error) {
	a, err := s.scanApproval(q.QueryRow(`SELECT `+approvalCols+` FROM approvals WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, wrapBusy(err)
}

func actionHash(assistant, kind, subject, action string, revision int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", assistant, kind, subject, action, revision)))
	return hex.EncodeToString(sum[:])
}

// subjectRevision reads the current revision of the thing being approved and
// checks it belongs to the Assistant.
func subjectRevision(tx queryer, assistantID, kind, id string) (int, error) {
	switch kind {
	case "task":
		t, err := getTaskTx(tx, id)
		if err != nil {
			return 0, err
		}
		if t.AssistantID != assistantID {
			return 0, fmt.Errorf("%w: task belongs to another assistant", ErrInvalid)
		}
		return t.Revision, nil
	case "goal":
		g, err := getGoalTx(tx, id)
		if err != nil {
			return 0, err
		}
		if g.AssistantID != assistantID {
			return 0, fmt.Errorf("%w: goal belongs to another assistant", ErrInvalid)
		}
		return g.Revision, nil
	}
	return 0, fmt.Errorf("%w: subject kind %q", ErrInvalid, kind)
}

// RequestApproval asks a person to approve an exact action.
func (s *Store) RequestApproval(requestID string, in RequestApprovalInput) (Approval, bool, error) {
	in.Action = strings.TrimSpace(in.Action)
	if in.AssistantID == "" || in.SubjectID == "" || in.Action == "" {
		return Approval{}, false, ErrInvalid
	}
	if in.TTL <= 0 {
		in.TTL = 30 * time.Minute
	}
	return execute(s, requestID, "approval.request", in, func(tx *sql.Tx) (Approval, error) {
		if _, err := getAssistantTx(tx, in.AssistantID); err != nil {
			return Approval{}, err
		}
		rev, err := subjectRevision(tx, in.AssistantID, in.SubjectKind, in.SubjectID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return Approval{}, fmt.Errorf("%w: unknown subject", ErrInvalid)
			}
			return Approval{}, err
		}
		now := s.nowMS()
		id := newID("p")
		if _, err := tx.Exec(`INSERT INTO approvals(`+approvalCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, in.AssistantID, in.SubjectKind, in.SubjectID, in.Action,
			actionHash(in.AssistantID, in.SubjectKind, in.SubjectID, in.Action, rev), rev,
			now+in.TTL.Milliseconds(), ApprovalPending, 0, now); err != nil {
			return Approval{}, err
		}
		return s.getApprovalTx(tx, id)
	})
}

// DecideApproval records a person's answer. The hash proves the answer is for
// the exact card shown; a changed subject or an expired card is refused; the
// first valid answer wins.
func (s *Store) DecideApproval(requestID, id, hash string, approve bool) (Approval, bool, error) {
	payload := map[string]any{"id": id, "hash": hash, "approve": approve}
	return execute(s, requestID, "approval.decide", payload, func(tx *sql.Tx) (Approval, error) {
		a, err := s.getApprovalTx(tx, id)
		if err != nil {
			return a, err
		}
		if a.ActionHash != hash {
			return a, fmt.Errorf("%w: this answer is not for this card", ErrInvalid)
		}
		if a.State != ApprovalPending {
			return a, fmt.Errorf("%w: the approval is already %s", ErrBadState, a.State)
		}
		rev, err := subjectRevision(tx, a.AssistantID, a.SubjectKind, a.SubjectID)
		if err != nil {
			return a, err
		}
		if rev != a.ExpectedRevision {
			return a, ErrStaleRevision
		}
		state := ApprovalDenied
		if approve {
			state = ApprovalApproved
		}
		if _, err := tx.Exec(`UPDATE approvals SET state=?, decided_at=? WHERE id=?`, state, s.nowMS(), id); err != nil {
			return a, err
		}
		return s.getApprovalTx(tx, id)
	})
}

// GetApproval reads one approval.
func (s *Store) GetApproval(id string) (Approval, error) { return s.getApprovalTx(s.db, id) }

// ListApprovals returns an Assistant's approvals (every one's when empty),
// newest first.
func (s *Store) ListApprovals(assistantID string) ([]Approval, error) {
	q, args := `SELECT `+approvalCols+` FROM approvals`, []any{}
	if assistantID != "" {
		q += ` WHERE assistant_id=?`
		args = append(args, assistantID)
	}
	rows, err := s.db.Query(q+` ORDER BY created_at DESC, id`, args...)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		a, err := s.scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, wrapBusy(rows.Err())
}
