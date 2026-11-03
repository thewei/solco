// Package personalagent is the Go-native business core behind Solco's
// Assistants: the one durable writer of Assistant identity, Goals, Tasks,
// Runs, Approvals and Memory (docs/personal-agent-go-mvp-plan.md §5).
//
// Everything lives in one SQLite database that only the daemon (and the CLI it
// serves) writes. Every mutating command carries a request id; the command and
// its receipt commit in one transaction, so a replayed request returns the
// original answer and never a second effect. The package never claims
// cross-process exactly-once: a claim is committed *before* anything is
// launched, and a launch whose outcome is unknown becomes `uncertain` instead
// of being retried.
package personalagent

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Errors a caller can branch on. Each maps to one stable wire code upstream.
var (
	ErrRequestConflict   = errors.New("request id reused with a different body")
	ErrStaleRevision     = errors.New("revision is stale")
	ErrNotFound          = errors.New("not found")
	ErrAssistantDisabled = errors.New("assistant is disabled")
	ErrBusy              = errors.New("database is locked")
	ErrNotClaimable      = errors.New("task is not claimable")
	ErrBadState          = errors.New("state does not allow this")
	ErrInvalid           = errors.New("invalid input")
)

// DefaultBusyTimeout is how long one statement waits out another process's
// lock before failing with ErrBusy.
const DefaultBusyTimeout = 5 * time.Second

// Option tunes Open.
type Option func(*Store)

// WithBusyTimeout bounds how long a statement waits for a lock held elsewhere.
func WithBusyTimeout(d time.Duration) Option { return func(s *Store) { s.busy = d } }

// WithClock replaces the wall clock (tests).
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// Store is the single transactional database.
type Store struct {
	db   *sql.DB
	path string
	busy time.Duration
	now  func() time.Time
}

// Open creates (0600) or opens the database and migrates it forward.
func Open(path string, opts ...Option) (*Store, error) {
	s := &Store{path: path, busy: DefaultBusyTimeout, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		_ = f.Close()
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout("+strconv.FormatInt(s.busy.Milliseconds(), 10)+")")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s.db = db
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Path is where the database lives.
func (s *Store) Path() string { return s.path }

func (s *Store) nowMS() int64 { return s.now().UnixMilli() }

// migrations run in order; the index+1 is the schema version.
var migrations = []string{
	// v1: identity, tasks, runs, receipts.
	`CREATE TABLE assistants (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		role_prompt TEXT NOT NULL DEFAULT '',
		scope TEXT NOT NULL DEFAULT 'personal',
		workspace_refs TEXT NOT NULL DEFAULT '[]',
		coordinator_session TEXT NOT NULL DEFAULT '',
		default_policy TEXT NOT NULL DEFAULT 'review',
		status TEXT NOT NULL DEFAULT 'active',
		revision INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE tasks (
		id TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		goal_id TEXT NOT NULL DEFAULT '',
		rule_id TEXT NOT NULL DEFAULT '',
		parent_task_id TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL,
		title TEXT NOT NULL,
		brief TEXT NOT NULL,
		repo_path TEXT NOT NULL DEFAULT '',
		agent TEXT NOT NULL DEFAULT 'claude',
		model TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL,
		needs_you INTEGER NOT NULL DEFAULT 0,
		review_state TEXT NOT NULL DEFAULT '',
		attempt INTEGER NOT NULL DEFAULT 1,
		revision INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX tasks_by_assistant ON tasks(assistant_id, state);
	CREATE TABLE runs (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL REFERENCES tasks(id),
		rule_id TEXT NOT NULL DEFAULT '',
		event_id TEXT NOT NULL DEFAULT '',
		idempotency_key TEXT NOT NULL UNIQUE,
		status TEXT NOT NULL,
		claimant TEXT NOT NULL DEFAULT '',
		herdr_session TEXT NOT NULL DEFAULT '',
		workspace_id TEXT NOT NULL DEFAULT '',
		pane_id TEXT NOT NULL DEFAULT '',
		agent_session_id TEXT NOT NULL DEFAULT '',
		label TEXT NOT NULL DEFAULT '',
		result TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		started_at INTEGER NOT NULL DEFAULT 0,
		finished_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX runs_by_task ON runs(task_id);
	CREATE TABLE receipts (
		request_id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		payload_hash TEXT NOT NULL,
		result TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);`,
	// v2: goals, commitments, memory, approvals.
	`CREATE TABLE goals (
		id TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		title TEXT NOT NULL,
		objective TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL,
		evidence TEXT NOT NULL DEFAULT '',
		revision INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX goals_by_assistant ON goals(assistant_id, state);
	CREATE TABLE commitments (
		id TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		goal_id TEXT NOT NULL REFERENCES goals(id),
		kind TEXT NOT NULL,
		success_criteria TEXT NOT NULL DEFAULT '',
		exit_condition TEXT NOT NULL DEFAULT '',
		spec TEXT NOT NULL DEFAULT '{}',
		state TEXT NOT NULL,
		next_check_at INTEGER NOT NULL DEFAULT 0,
		revision INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX commitments_by_goal ON commitments(goal_id);
	CREATE TABLE memory_items (
		id TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		scope TEXT NOT NULL DEFAULT 'assistant',
		kind TEXT NOT NULL DEFAULT 'fact',
		content TEXT NOT NULL,
		source_ref TEXT NOT NULL,
		state TEXT NOT NULL,
		shared_with TEXT NOT NULL DEFAULT '[]',
		revision INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX memory_by_assistant ON memory_items(assistant_id, state);
	CREATE TABLE approvals (
		id TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		subject_kind TEXT NOT NULL,
		subject_id TEXT NOT NULL,
		action TEXT NOT NULL,
		action_hash TEXT NOT NULL,
		expected_revision INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		state TEXT NOT NULL,
		decided_at INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	);
	CREATE INDEX approvals_by_assistant ON approvals(assistant_id, state);`,
	// v3: deterministic watching — a cursor per commitment, a deduped event log.
	`ALTER TABLE commitments ADD COLUMN last_checked_at INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE commitments ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
	ALTER TABLE commitments ADD COLUMN cursor TEXT NOT NULL DEFAULT '';
	CREATE TABLE events (
		id TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		commitment_id TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL,
		resource TEXT NOT NULL,
		revision TEXT NOT NULL,
		task_id TEXT NOT NULL DEFAULT '',
		seen_at INTEGER NOT NULL,
		UNIQUE(source, resource, revision)
	);`,
	// v4: the event log grows kind/priority/detail (rowid is the watermark) and
	// each Assistant may own one heartbeat.
	`ALTER TABLE events ADD COLUMN kind TEXT NOT NULL DEFAULT 'pr.changed';
	ALTER TABLE events ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE events ADD COLUMN detail TEXT NOT NULL DEFAULT '';
	CREATE TABLE heartbeats (
		assistant_id TEXT PRIMARY KEY REFERENCES assistants(id),
		checklist TEXT NOT NULL DEFAULT '',
		cron TEXT NOT NULL DEFAULT '',
		event_rules TEXT NOT NULL DEFAULT '{}',
		wake_budget INTEGER NOT NULL DEFAULT 0,
		wake_approved INTEGER NOT NULL DEFAULT 0,
		last_beat_at INTEGER NOT NULL DEFAULT 0,
		event_mark INTEGER NOT NULL DEFAULT 0,
		wakes_day TEXT NOT NULL DEFAULT '',
		wakes_today INTEGER NOT NULL DEFAULT 0,
		held TEXT NOT NULL DEFAULT '[]',
		revision INTEGER NOT NULL DEFAULT 1,
		updated_at INTEGER NOT NULL DEFAULT 0
	);`,
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return wrapBusy(err)
	}
	have, err := s.SchemaVersion()
	if err != nil {
		return err
	}
	for v := have; v < len(migrations); v++ {
		tx, err := s.db.Begin()
		if err != nil {
			return wrapBusy(err)
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate to v%d: %w", v+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES('schema_version',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(v+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return wrapBusy(err)
		}
	}
	return nil
}

// SchemaVersion is the highest applied migration (0 on a fresh file).
func (s *Store) SchemaVersion() (int, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, wrapBusy(err)
	}
	n, _ := strconv.Atoi(v)
	return n, nil
}

// wrapBusy turns SQLite's lock errors into ErrBusy so callers need no driver
// knowledge.
func wrapBusy(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "database is locked") || strings.Contains(msg, "SQLITE_BUSY") {
		return fmt.Errorf("%w: %v", ErrBusy, err)
	}
	return err
}

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

func hashPayload(kind string, payload any) string {
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(append([]byte(kind+"\x00"), raw...))
	return hex.EncodeToString(sum[:])
}

// execute runs fn and, when requestID is set, its receipt in one transaction.
// A replay (same id, same body) returns the stored result without running fn;
// the same id with another body is ErrRequestConflict. The bool reports a
// replay.
func execute[T any](s *Store, requestID, kind string, payload any, fn func(tx *sql.Tx) (T, error)) (T, bool, error) {
	var zero T
	tx, err := s.db.Begin()
	if err != nil {
		return zero, false, wrapBusy(err)
	}
	defer func() { _ = tx.Rollback() }()

	hash := ""
	if requestID != "" {
		hash = hashPayload(kind, payload)
		var storedKind, storedHash, stored string
		err := tx.QueryRow(`SELECT kind, payload_hash, result FROM receipts WHERE request_id=?`, requestID).
			Scan(&storedKind, &storedHash, &stored)
		switch {
		case err == nil:
			if storedKind != kind || storedHash != hash {
				return zero, false, ErrRequestConflict
			}
			var out T
			if err := json.Unmarshal([]byte(stored), &out); err != nil {
				return zero, false, fmt.Errorf("stored receipt unreadable: %w", err)
			}
			return out, true, nil
		case !errors.Is(err, sql.ErrNoRows):
			return zero, false, wrapBusy(err)
		}
	}
	out, err := fn(tx)
	if err != nil {
		return zero, false, wrapBusy(err)
	}
	if requestID != "" {
		raw, err := json.Marshal(out)
		if err != nil {
			return zero, false, err
		}
		if _, err := tx.Exec(`INSERT INTO receipts(request_id,kind,payload_hash,result,created_at) VALUES(?,?,?,?,?)`,
			requestID, kind, hash, string(raw), s.nowMS()); err != nil {
			return zero, false, wrapBusy(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return zero, false, wrapBusy(err)
	}
	return out, false, nil
}
