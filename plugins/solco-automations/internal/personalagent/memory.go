package personalagent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Memory states and scopes. A proposal is a candidate; only a person's
// approval makes it a long-term fact, and forgetting removes its content.
const (
	MemoryCandidate = "candidate"
	MemoryApproved  = "approved"
	MemoryForgotten = "forgotten"

	MemoryScopeAssistant = "assistant"

	// Who made a version. Informational: the CLI cannot prove who is typing, so
	// the only enforcement is the approval rules, not this label.
	MemByUser     = "user"
	MemByAgent    = "agent"
	MemByFiles    = "files"
	MemBySystem   = "system"
	MaxMemoryText = 8 << 10 // bytes in one memory item
	MaxMemoryKept = 500     // items an Assistant may keep (forgotten ones do not count)
)

// MemoryItem is one remembered thing, owned by exactly one Assistant.
type MemoryItem struct {
	ID          string   `json:"id"`
	AssistantID string   `json:"assistantId"`
	Scope       string   `json:"scope"`
	Kind        string   `json:"kind"`
	Content     string   `json:"content"`
	SourceRef   string   `json:"sourceRef"`
	State       string   `json:"state"`
	SharedWith  []string `json:"sharedWith"`
	Revision    int      `json:"revision"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
}

// ProposeMemoryInput is the body of `assistant memory propose`.
type ProposeMemoryInput struct {
	AssistantID string `json:"assistantId"`
	Content     string `json:"content"`
	SourceRef   string `json:"sourceRef"`
	Kind        string `json:"kind,omitempty"`
}

const memoryCols = `id,assistant_id,scope,kind,content,source_ref,state,shared_with,revision,created_at,updated_at`

func scanMemory(r rowScanner) (MemoryItem, error) {
	var m MemoryItem
	var shared string
	err := r.Scan(&m.ID, &m.AssistantID, &m.Scope, &m.Kind, &m.Content, &m.SourceRef, &m.State, &shared, &m.Revision, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return m, err
	}
	_ = json.Unmarshal([]byte(shared), &m.SharedWith)
	if m.SharedWith == nil {
		m.SharedWith = []string{}
	}
	return m, nil
}

func getMemoryTx(q queryer, id string) (MemoryItem, error) {
	m, err := scanMemory(q.QueryRow(`SELECT `+memoryCols+` FROM memory_items WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, wrapBusy(err)
}

// ProposeMemory records a candidate. It must say where it came from.
func (s *Store) ProposeMemory(requestID string, in ProposeMemoryInput) (MemoryItem, bool, error) {
	return s.proposeAs(MemByAgent, requestID, in)
}

func (s *Store) proposeAs(author, requestID string, in ProposeMemoryInput) (MemoryItem, bool, error) {
	in.Content = strings.TrimSpace(in.Content)
	if in.AssistantID == "" || in.Content == "" || strings.TrimSpace(in.SourceRef) == "" {
		return MemoryItem{}, false, fmt.Errorf("%w: memory needs content and a sourceRef", ErrInvalid)
	}
	if looksLikeSecret(in.Content) {
		return MemoryItem{}, false, fmt.Errorf("%w: that looks like a credential; memory never keeps credentials", ErrInvalid)
	}
	return execute(s, requestID, "memory.propose", in, func(tx *sql.Tx) (MemoryItem, error) {
		return s.insertMemoryTx(tx, in, MemoryCandidate, author)
	})
}

// insertMemoryTx is the one place a memory row is born: it checks the bounds,
// writes the row and its first version.
func (s *Store) insertMemoryTx(tx *sql.Tx, in ProposeMemoryInput, state, author string) (MemoryItem, error) {
	if len(in.Content) > MaxMemoryText {
		return MemoryItem{}, fmt.Errorf("%w: a memory item is at most %d bytes; split it", ErrInvalid, MaxMemoryText)
	}
	if in.Kind == "" {
		in.Kind = "fact"
	}
	if _, err := getAssistantTx(tx, in.AssistantID); err != nil {
		return MemoryItem{}, err
	}
	var kept int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM memory_items WHERE assistant_id=? AND state<>?`, in.AssistantID, MemoryForgotten).Scan(&kept); err != nil {
		return MemoryItem{}, err
	}
	if kept >= MaxMemoryKept {
		return MemoryItem{}, fmt.Errorf("%w: this Assistant already keeps %d memory items; forget some first", ErrInvalid, MaxMemoryKept)
	}
	now := s.nowMS()
	id := newID("m")
	if _, err := tx.Exec(`INSERT INTO memory_items(`+memoryCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		id, in.AssistantID, MemoryScopeAssistant, in.Kind, in.Content, in.SourceRef, state, "[]", 1, now, now); err != nil {
		return MemoryItem{}, err
	}
	m, err := getMemoryTx(tx, id)
	if err != nil {
		return m, err
	}
	return m, recordVersionTx(tx, m, author, now)
}

// AddMemory is a person writing a fact down themselves: it is approved at
// once, because the approval is the act of writing it.
func (s *Store) AddMemory(requestID, assistantID, content, kind string) (MemoryItem, bool, error) {
	content = strings.TrimSpace(content)
	if assistantID == "" || content == "" {
		return MemoryItem{}, false, fmt.Errorf("%w: memory needs content", ErrInvalid)
	}
	if looksLikeSecret(content) {
		return MemoryItem{}, false, fmt.Errorf("%w: that looks like a credential; memory never keeps credentials", ErrInvalid)
	}
	in := ProposeMemoryInput{AssistantID: assistantID, Content: content, SourceRef: "user", Kind: kind}
	return execute(s, requestID, "memory.add", in, func(tx *sql.Tx) (MemoryItem, error) {
		return s.insertMemoryTx(tx, in, MemoryApproved, MemByUser)
	})
}

// GetMemory reads one item.
func (s *Store) GetMemory(id string) (MemoryItem, error) { return getMemoryTx(s.db, id) }

func (s *Store) editMemory(requestID, kind, author, id string, expected int, payload any, fn func(tx *sql.Tx, m MemoryItem, now int64) error) (MemoryItem, bool, error) {
	return execute(s, requestID, kind, payload, func(tx *sql.Tx) (MemoryItem, error) {
		m, err := getMemoryTx(tx, id)
		if err != nil {
			return m, err
		}
		if m.Revision != expected {
			return m, ErrStaleRevision
		}
		if m.State == MemoryForgotten {
			return m, fmt.Errorf("%w: memory was forgotten", ErrBadState)
		}
		now := s.nowMS()
		if err := fn(tx, m, now); err != nil {
			return m, err
		}
		next, err := getMemoryTx(tx, id)
		if err != nil {
			return next, err
		}
		if next.Revision != m.Revision {
			if err := recordVersionTx(tx, next, author, now); err != nil {
				return next, err
			}
		}
		return next, nil
	})
}

func bump(tx *sql.Tx, now int64, id, set string, args ...any) error {
	_, err := tx.Exec(`UPDATE memory_items SET `+set+`, revision=revision+1, updated_at=? WHERE id=?`, append(append([]any{}, args...), now, id)...)
	return err
}

// ApproveMemory promotes a candidate to a long-term fact.
func (s *Store) ApproveMemory(requestID, id string, expectedRevision int) (MemoryItem, bool, error) {
	return s.approveAs(MemByUser, requestID, id, expectedRevision)
}

func (s *Store) approveAs(author, requestID, id string, expectedRevision int) (MemoryItem, bool, error) {
	return s.editMemory(requestID, "memory.approve", author, id, expectedRevision, map[string]any{"id": id, "rev": expectedRevision},
		func(tx *sql.Tx, m MemoryItem, now int64) error {
			if m.State != MemoryCandidate {
				return fmt.Errorf("%w: memory is %s", ErrBadState, m.State)
			}
			return bump(tx, now, id, `state=?`, MemoryApproved)
		})
}

// CorrectMemory rewrites an item; what is visible changes immediately.
func (s *Store) CorrectMemory(requestID, id, content string, expectedRevision int) (MemoryItem, bool, error) {
	return s.correctAs(MemByUser, requestID, id, content, expectedRevision)
}

func (s *Store) correctAs(author, requestID, id, content string, expectedRevision int) (MemoryItem, bool, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return MemoryItem{}, false, ErrInvalid
	}
	if looksLikeSecret(content) {
		return MemoryItem{}, false, fmt.Errorf("%w: that looks like a credential; memory never keeps credentials", ErrInvalid)
	}
	if len(content) > MaxMemoryText {
		return MemoryItem{}, false, fmt.Errorf("%w: a memory item is at most %d bytes; split it", ErrInvalid, MaxMemoryText)
	}
	return s.editMemory(requestID, "memory.correct", author, id, expectedRevision, map[string]any{"id": id, "c": content, "rev": expectedRevision},
		func(tx *sql.Tx, m MemoryItem, now int64) error { return bump(tx, now, id, `content=?`, content) })
}

// ForgetMemory deletes the content and every share; the row stays as a
// tombstone so ids and receipts remain valid.
func (s *Store) ForgetMemory(requestID, id string, expectedRevision int) (MemoryItem, bool, error) {
	return s.forgetAs(MemByUser, requestID, id, expectedRevision)
}

func (s *Store) forgetAs(author, requestID, id string, expectedRevision int) (MemoryItem, bool, error) {
	return s.editMemory(requestID, "memory.forget", author, id, expectedRevision, map[string]any{"id": id, "rev": expectedRevision},
		func(tx *sql.Tx, m MemoryItem, now int64) error {
			if _, err := tx.Exec(`UPDATE memory_versions SET content='' WHERE memory_id=?`, id); err != nil {
				return err
			}
			return bump(tx, now, id, `state=?, content='', shared_with='[]'`, MemoryForgotten)
		})
}

// ShareMemory grants another Assistant read access to an approved item.
func (s *Store) ShareMemory(requestID, id, withAssistant string, expectedRevision int) (MemoryItem, bool, error) {
	return s.editMemory(requestID, "memory.share", MemByUser, id, expectedRevision, map[string]any{"id": id, "with": withAssistant, "rev": expectedRevision},
		func(tx *sql.Tx, m MemoryItem, now int64) error {
			if m.State != MemoryApproved {
				return fmt.Errorf("%w: only approved memory can be shared", ErrBadState)
			}
			if withAssistant == m.AssistantID {
				return fmt.Errorf("%w: an assistant already owns its memory", ErrInvalid)
			}
			if _, err := getAssistantTx(tx, withAssistant); err != nil {
				return err
			}
			if slices.Contains(m.SharedWith, withAssistant) {
				return nil
			}
			raw, _ := json.Marshal(append(m.SharedWith, withAssistant))
			return bump(tx, now, id, `shared_with=?`, string(raw))
		})
}

// UnshareMemory revokes a grant.
func (s *Store) UnshareMemory(requestID, id, withAssistant string, expectedRevision int) (MemoryItem, bool, error) {
	return s.editMemory(requestID, "memory.unshare", MemByUser, id, expectedRevision, map[string]any{"id": id, "with": withAssistant, "rev": expectedRevision},
		func(tx *sql.Tx, m MemoryItem, now int64) error {
			next := slices.DeleteFunc(slices.Clone(m.SharedWith), func(x string) bool { return x == withAssistant })
			raw, _ := json.Marshal(next)
			return bump(tx, now, id, `shared_with=?`, string(raw))
		})
}

// ListMemory returns every item an Assistant owns, in every state.
func (s *Store) ListMemory(assistantID string) ([]MemoryItem, error) {
	return s.queryMemory(`SELECT `+memoryCols+` FROM memory_items WHERE assistant_id=? ORDER BY created_at, id`, assistantID)
}

// VisibleMemory is what a context may use: the Assistant's own approved items
// plus approved items another Assistant explicitly shared with it.
func (s *Store) VisibleMemory(assistantID string) ([]MemoryItem, error) {
	all, err := s.queryMemory(`SELECT `+memoryCols+` FROM memory_items WHERE state=? ORDER BY created_at, id`, MemoryApproved)
	if err != nil {
		return nil, err
	}
	out := []MemoryItem{}
	for _, m := range all {
		if m.AssistantID == assistantID || slices.Contains(m.SharedWith, assistantID) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *Store) queryMemory(q string, args ...any) ([]MemoryItem, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []MemoryItem{}
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, wrapBusy(rows.Err())
}
