package personalagent

import (
	"database/sql"
	"fmt"
)

// The Primary Assistant (2026-11-07). One Assistant is the person's front door:
// it is created first, is always listed first, can be neither archived nor
// deleted, and — alone — may manage the others through its solco_assistant
// broker (`manage.*`, api_manage.go). The marker is one `meta` row, so there is
// no second table to drift; every reader goes through these helpers.

const metaPrimary = "primary_assistant"

// primaryIDOf reads the marker and checks it still names a live, ordinary
// Assistant. A stale or missing marker answers "".
func primaryIDOf(q interface {
	QueryRow(string, ...any) *sql.Row
}) string {
	var id string
	err := q.QueryRow(`SELECT m.value FROM meta m JOIN assistants a ON a.id=m.value
		WHERE m.key=? AND a.status<>'archived'
		AND m.value NOT IN (SELECT value FROM meta WHERE key='automations_assistant')`, metaPrimary).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}

// PrimaryAssistantID is the Primary's id, or "" while no ordinary Assistant exists.
func (s *Store) PrimaryAssistantID() string { return primaryIDOf(s.db) }

// IsPrimary reports whether id is the Primary Assistant.
func (s *Store) IsPrimary(id string) bool { return id != "" && primaryIDOf(s.db) == id }

// EnsurePrimary names the oldest ordinary, non-archived Assistant the Primary
// when none is marked (a database from before the marker, or a Primary lost to
// manual surgery). It never moves a marker that still points at a live one.
func (s *Store) EnsurePrimary() (string, error) {
	if id := primaryIDOf(s.db); id != "" {
		return id, nil
	}
	if _, err := s.db.Exec(`INSERT INTO meta(key,value) SELECT ?, id FROM assistants
		WHERE status<>'archived' AND id NOT IN (SELECT value FROM meta WHERE key='automations_assistant')
		ORDER BY created_at,rowid LIMIT 1
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, metaPrimary); err != nil {
		return "", wrapBusy(err)
	}
	return primaryIDOf(s.db), nil
}

func errPrimaryProtected(what string) error {
	return fmt.Errorf("%w: the Primary Assistant cannot be %s", ErrBadState, what)
}
