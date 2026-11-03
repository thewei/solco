package personalagent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Assistant statuses.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// Assistant scopes.
const (
	ScopePersonal  = "personal"
	ScopeWorkspace = "workspace"
)

// Assistant is a persistent identity: its own Goals, Tasks, Memory and
// Coordinator session. Its ID is opaque and never derived from a Pane or a path.
type Assistant struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	RolePrompt         string   `json:"rolePrompt"`
	Scope              string   `json:"scope"`
	WorkspaceRefs      []string `json:"workspaceRefs"`
	CoordinatorSession string   `json:"coordinatorSession"`
	DefaultPolicy      string   `json:"defaultPolicy"`
	Status             string   `json:"status"`
	Revision           int      `json:"revision"`
	CreatedAt          int64    `json:"createdAt"`
	UpdatedAt          int64    `json:"updatedAt"`
}

// CreateAssistantInput is the body of `assistant create`.
type CreateAssistantInput struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	RolePrompt    string   `json:"rolePrompt,omitempty"`
	Scope         string   `json:"scope,omitempty"`
	WorkspaceRefs []string `json:"workspaceRefs,omitempty"`
}

const assistantCols = `id,name,description,role_prompt,scope,workspace_refs,coordinator_session,default_policy,status,revision,created_at,updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanAssistant(r rowScanner) (Assistant, error) {
	var a Assistant
	var refs string
	err := r.Scan(&a.ID, &a.Name, &a.Description, &a.RolePrompt, &a.Scope, &refs, &a.CoordinatorSession,
		&a.DefaultPolicy, &a.Status, &a.Revision, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return a, err
	}
	_ = json.Unmarshal([]byte(refs), &a.WorkspaceRefs)
	if a.WorkspaceRefs == nil {
		a.WorkspaceRefs = []string{}
	}
	return a, nil
}

func getAssistantTx(q interface {
	QueryRow(string, ...any) *sql.Row
}, id string) (Assistant, error) {
	a, err := scanAssistant(q.QueryRow(`SELECT `+assistantCols+` FROM assistants WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, wrapBusy(err)
}

// CreateAssistant records a new identity. The request id makes it idempotent.
func (s *Store) CreateAssistant(requestID string, in CreateAssistantInput) (Assistant, bool, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Assistant{}, false, ErrInvalid
	}
	if in.Scope == "" {
		in.Scope = ScopePersonal
	}
	if in.Scope != ScopePersonal && in.Scope != ScopeWorkspace {
		return Assistant{}, false, ErrInvalid
	}
	return execute(s, requestID, "assistant.create", in, func(tx *sql.Tx) (Assistant, error) {
		now := s.nowMS()
		id := newID("a")
		refs, _ := json.Marshal(append([]string{}, in.WorkspaceRefs...))
		_, err := tx.Exec(`INSERT INTO assistants(`+assistantCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, in.Name, in.Description, in.RolePrompt, in.Scope, string(refs), "", "review", StatusActive, 1, now, now)
		if err != nil {
			return Assistant{}, err
		}
		return getAssistantTx(tx, id)
	})
}

// RenameAssistant changes the display name; the ID and everything keyed by it
// are untouched. expectedRevision guards against a concurrent edit.
func (s *Store) RenameAssistant(requestID, id, name string, expectedRevision int) (Assistant, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Assistant{}, false, ErrInvalid
	}
	payload := map[string]any{"id": id, "name": name, "rev": expectedRevision}
	return execute(s, requestID, "assistant.rename", payload, func(tx *sql.Tx) (Assistant, error) {
		return updateAssistant(tx, s.nowMS(), id, expectedRevision, `name=?`, name)
	})
}

// SetAssistantStatus disables or re-enables an Assistant. Disabling stops new
// work; existing Panes and human changes are never cleaned up.
func (s *Store) SetAssistantStatus(requestID, id, status string, expectedRevision int) (Assistant, bool, error) {
	if status != StatusActive && status != StatusDisabled {
		return Assistant{}, false, ErrInvalid
	}
	payload := map[string]any{"id": id, "status": status, "rev": expectedRevision}
	return execute(s, requestID, "assistant.status", payload, func(tx *sql.Tx) (Assistant, error) {
		return updateAssistant(tx, s.nowMS(), id, expectedRevision, `status=?`, status)
	})
}

// SetCoordinatorSession remembers the real Herdr session reference of the
// Assistant's Coordinator (a ref string, never derived from a title).
func (s *Store) SetCoordinatorSession(requestID, id, ref string, expectedRevision int) (Assistant, bool, error) {
	payload := map[string]any{"id": id, "ref": ref, "rev": expectedRevision}
	return execute(s, requestID, "assistant.coordinator", payload, func(tx *sql.Tx) (Assistant, error) {
		return updateAssistant(tx, s.nowMS(), id, expectedRevision, `coordinator_session=?`, ref)
	})
}

func updateAssistant(tx *sql.Tx, now int64, id string, expected int, set string, arg any) (Assistant, error) {
	res, err := tx.Exec(`UPDATE assistants SET `+set+`, revision=revision+1, updated_at=? WHERE id=? AND revision=?`,
		arg, now, id, expected)
	if err != nil {
		return Assistant{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := getAssistantTx(tx, id); err != nil {
			return Assistant{}, err
		}
		return Assistant{}, ErrStaleRevision
	}
	return getAssistantTx(tx, id)
}

// GetAssistant reads one identity.
func (s *Store) GetAssistant(id string) (Assistant, error) {
	return getAssistantTx(s.db, id)
}

// ListAssistants returns every identity, oldest first.
func (s *Store) ListAssistants() ([]Assistant, error) {
	rows, err := s.db.Query(`SELECT ` + assistantCols + ` FROM assistants ORDER BY created_at, id`)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Assistant{}
	for rows.Next() {
		a, err := scanAssistant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, wrapBusy(rows.Err())
}
