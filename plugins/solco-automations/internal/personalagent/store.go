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
	ErrChecksRequired    = errors.New("the task requires approved checks")
	ErrHookRejected      = errors.New("webhook not authenticated")
	ErrRateLimited       = errors.New("too many requests")
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
	// verifier checks owner seals on grants; nil = no owner key = no grant
	// ever authorises anything.
	verifier GrantVerifier
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
	// v5: the legacy-rule ledger. Definitions stay in automations.yaml; this
	// table only says who dispatches a rule and where its watermark is.
	`CREATE TABLE legacy_rules (
		name TEXT PRIMARY KEY,
		state TEXT NOT NULL,
		last_occurrence INTEGER NOT NULL DEFAULT 0,
		queued INTEGER NOT NULL DEFAULT 0,
		handback INTEGER NOT NULL DEFAULT 0,
		revision INTEGER NOT NULL DEFAULT 1,
		adopted_at INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL DEFAULT 0
	);`,
	// v6: the worktree branch a run provisioned (history and the folder-trust
	// check both name it); never on the wire.
	`ALTER TABLE runs ADD COLUMN branch TEXT NOT NULL DEFAULT '';`,
	// v7: each coding task's isolated checkout (E02).
	`CREATE TABLE worktrees (
		task_id TEXT PRIMARY KEY,
		repo TEXT NOT NULL,
		path TEXT NOT NULL,
		branch TEXT NOT NULL,
		target_branch TEXT NOT NULL,
		base_head TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT 'active',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);`,
	// v8: review evidence and the approved check list (E03).
	`CREATE TABLE reviews (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		assistant_id TEXT NOT NULL,
		state TEXT NOT NULL,
		branch TEXT NOT NULL,
		head TEXT NOT NULL,
		base_head TEXT NOT NULL,
		target_branch TEXT NOT NULL,
		target_head TEXT NOT NULL,
		files INTEGER NOT NULL DEFAULT 0,
		insertions INTEGER NOT NULL DEFAULT 0,
		deletions INTEGER NOT NULL DEFAULT 0,
		paths TEXT NOT NULL DEFAULT '[]',
		diff_hash TEXT NOT NULL DEFAULT '',
		checks TEXT NOT NULL DEFAULT '[]',
		agent_review_task_id TEXT NOT NULL DEFAULT '',
		approval_id TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		revision INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX reviews_by_task ON reviews(task_id, created_at);
	CREATE TABLE review_checks (
		assistant_id TEXT PRIMARY KEY,
		active TEXT NOT NULL DEFAULT '[]',
		pending TEXT NOT NULL DEFAULT '[]',
		revision INTEGER NOT NULL DEFAULT 0
	);`,
	// v9: the task dependency graph and each Assistant's Coordinator (E01).
	`CREATE TABLE task_deps (
		task_id TEXT NOT NULL,
		depends_on TEXT NOT NULL,
		PRIMARY KEY (task_id, depends_on)
	);
	CREATE INDEX task_deps_by_dep ON task_deps(depends_on);
	CREATE TABLE coordinators (
		assistant_id TEXT PRIMARY KEY,
		state TEXT NOT NULL,
		task_id TEXT NOT NULL DEFAULT '',
		herdr_session TEXT NOT NULL DEFAULT '',
		workspace_id TEXT NOT NULL DEFAULT '',
		pane_id TEXT NOT NULL DEFAULT '',
		agent_session_id TEXT NOT NULL DEFAULT '',
		revision INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);`,
	// v10: Groups (Assistants 3.1 U1): one ordered message log, members,
	// per-mention deliveries, and the Agent an Assistant runs for a Group.
	`CREATE TABLE chat_groups (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		revision INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE group_members (
		group_id TEXT NOT NULL REFERENCES chat_groups(id),
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		added_at INTEGER NOT NULL,
		PRIMARY KEY (group_id, assistant_id)
	);
	CREATE TABLE group_messages (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		id TEXT NOT NULL UNIQUE,
		group_id TEXT NOT NULL REFERENCES chat_groups(id),
		author_kind TEXT NOT NULL,
		author_id TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT 'text',
		body TEXT NOT NULL DEFAULT '',
		mentions TEXT NOT NULL DEFAULT '[]',
		reply_to TEXT NOT NULL DEFAULT '',
		task_id TEXT NOT NULL DEFAULT '',
		fact TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	CREATE INDEX group_messages_by_group ON group_messages(group_id, seq);
	CREATE TABLE group_deliveries (
		id TEXT PRIMARY KEY,
		group_id TEXT NOT NULL REFERENCES chat_groups(id),
		message_id TEXT NOT NULL REFERENCES group_messages(id),
		assistant_id TEXT NOT NULL,
		state TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		next_at INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		UNIQUE (message_id, assistant_id)
	);
	CREATE INDEX group_deliveries_open ON group_deliveries(state, created_at);
	CREATE TABLE group_agents (
		group_id TEXT NOT NULL REFERENCES chat_groups(id),
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		state TEXT NOT NULL,
		task_id TEXT NOT NULL DEFAULT '',
		herdr_session TEXT NOT NULL DEFAULT '',
		workspace_id TEXT NOT NULL DEFAULT '',
		pane_id TEXT NOT NULL DEFAULT '',
		agent_session_id TEXT NOT NULL DEFAULT '',
		last_seq INTEGER NOT NULL DEFAULT 0,
		revision INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (group_id, assistant_id)
	);
	CREATE INDEX group_agents_by_task ON group_agents(task_id);`,
	// v11: the newest message a launching Group Agent's brief carried. It only
	// becomes last_seq once the Agent really finished that turn.
	`ALTER TABLE group_agents ADD COLUMN launch_seq INTEGER NOT NULL DEFAULT 0;`,
	// v12: Assistants 3.1 U2 — a versioned "how you work" text per Assistant (the
	// active text stays assistants.role_prompt; this table is the history), the
	// revision each running surface actually launched with, the Assistant's own
	// Agent/Model/Effort, per-Assistant MCP grants and what each launch bound.
	`CREATE TABLE assistant_instructions (
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		revision INTEGER NOT NULL,
		body TEXT NOT NULL,
		source TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (assistant_id, revision)
	);
	CREATE TABLE persona_applied (
		assistant_id TEXT NOT NULL,
		surface TEXT NOT NULL,
		revision INTEGER NOT NULL,
		applied_at INTEGER NOT NULL,
		PRIMARY KEY (assistant_id, surface)
	);
	CREATE TABLE assistant_runtime (
		assistant_id TEXT PRIMARY KEY REFERENCES assistants(id),
		agent_kind TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		effort TEXT NOT NULL DEFAULT '',
		revision INTEGER NOT NULL DEFAULT 1,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE mcp_grants (
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		server_id TEXT NOT NULL,
		revision INTEGER NOT NULL DEFAULT 1,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (assistant_id, server_id)
	);
	CREATE TABLE mcp_bindings (
		assistant_id TEXT NOT NULL,
		surface TEXT NOT NULL,
		agent_kind TEXT NOT NULL,
		servers TEXT NOT NULL DEFAULT '[]',
		strict INTEGER NOT NULL DEFAULT 0,
		unsupported TEXT NOT NULL DEFAULT '',
		bound_at INTEGER NOT NULL,
		verified_state TEXT NOT NULL DEFAULT '',
		verified_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (assistant_id, surface)
	);`,
	// v13: Assistants 3.1 U3 — the plan behind a Goal (success criteria, verifiable
	// milestones, next action), its revision trail, and the ledger of system
	// facts the Goal supervisor derives from committed state. Facts are the
	// store's own words, never an Agent's.
	`CREATE TABLE goal_plans (
		goal_id TEXT PRIMARY KEY REFERENCES goals(id),
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		success_criteria TEXT NOT NULL DEFAULT '',
		milestones TEXT NOT NULL DEFAULT '[]',
		next_action TEXT NOT NULL DEFAULT '',
		plan_revision INTEGER NOT NULL DEFAULT 1,
		replans INTEGER NOT NULL DEFAULT 0,
		max_tasks INTEGER NOT NULL DEFAULT 12,
		stale_after_ms INTEGER NOT NULL DEFAULT 2700000,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE goal_plan_revisions (
		goal_id TEXT NOT NULL,
		revision INTEGER NOT NULL,
		success_criteria TEXT NOT NULL DEFAULT '',
		milestones TEXT NOT NULL DEFAULT '[]',
		reason TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		PRIMARY KEY (goal_id, revision)
	);
	CREATE TABLE goal_facts (
		id TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		goal_id TEXT NOT NULL DEFAULT '',
		task_id TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL,
		text TEXT NOT NULL,
		evidence TEXT NOT NULL DEFAULT '',
		dedupe_key TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL
	);
	CREATE INDEX goal_facts_by_assistant ON goal_facts(assistant_id);
	CREATE INDEX goal_facts_by_goal ON goal_facts(goal_id);`,
	// v14: routines — the owner binding of a scheduled automation. The definition
	// stays in automations.yaml (one scheduler, one file); this row says whose it
	// is, who asked, which exact definition a person approved, and whether it was enabled.
	`CREATE TABLE routines (
		name TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		requested_by TEXT NOT NULL DEFAULT 'assistant',
		source TEXT NOT NULL DEFAULT 'chat',
		correlation_id TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL DEFAULT 'draft',
		revision INTEGER NOT NULL DEFAULT 1,
		def_hash TEXT NOT NULL DEFAULT '',
		approved INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX routines_by_assistant ON routines(assistant_id);`,
	// v15: Memory workspace and proactivity (U5). memory_versions keeps every
	// revision (a forgotten item's versions keep no content); memory_files is
	// what the folder projection last wrote, so a person's edit is the file
	// that no longer matches it; proactivity is one Assistant's rules for what
	// the system may start on its own.
	`CREATE TABLE memory_versions (
		memory_id TEXT NOT NULL,
		revision INTEGER NOT NULL,
		author TEXT NOT NULL,
		state TEXT NOT NULL,
		content TEXT NOT NULL,
		at INTEGER NOT NULL,
		PRIMARY KEY (memory_id, revision)
	);
	INSERT INTO memory_versions(memory_id,revision,author,state,content,at) SELECT id,revision,'system',state,content,updated_at FROM memory_items;
	CREATE TABLE memory_files (
		assistant_id TEXT NOT NULL,
		rel TEXT NOT NULL,
		memory_id TEXT NOT NULL,
		revision INTEGER NOT NULL,
		hash TEXT NOT NULL,
		PRIMARY KEY (assistant_id, rel)
	);
	CREATE TABLE proactivity (
		assistant_id TEXT PRIMARY KEY REFERENCES assistants(id),
		level TEXT NOT NULL,
		quiet_start TEXT NOT NULL DEFAULT '',
		quiet_end TEXT NOT NULL DEFAULT '',
		tz TEXT NOT NULL DEFAULT '',
		report_every_min INTEGER NOT NULL DEFAULT 0,
		daily_budget INTEGER NOT NULL DEFAULT 20,
		dispatch_day TEXT NOT NULL DEFAULT '',
		dispatch_today INTEGER NOT NULL DEFAULT 0,
		revision INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);`,
	// v16: SQLite intent survives YAML -> SQLite crash windows of routine drafts.
	// It only authorizes finishing an approval *request*, never executing one.
	`CREATE TABLE routine_intents (
		name TEXT PRIMARY KEY,
		payload TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);`,
	// v17: the existing Fact ledger becomes a stable, non-reusable sequence
	// for cursor-based Activity replay. An ordinary SQLite rowid can be reused
	// after the newest row is purged; AUTOINCREMENT cannot. Keep all old seq
	// values so existing desktop/mobile receipts and cursors stay valid.
	// The same SQLite remains the only owner of the messages.
	`CREATE TABLE goal_facts_next (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		id TEXT NOT NULL UNIQUE,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		goal_id TEXT NOT NULL DEFAULT '',
		task_id TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL,
		text TEXT NOT NULL,
		evidence TEXT NOT NULL DEFAULT '',
		dedupe_key TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL
	);
	INSERT INTO goal_facts_next(seq,id,assistant_id,goal_id,task_id,kind,text,evidence,dedupe_key,created_at)
	SELECT rowid,id,assistant_id,goal_id,task_id,kind,text,evidence,dedupe_key,created_at FROM goal_facts ORDER BY rowid;
	DROP TABLE goal_facts;
	ALTER TABLE goal_facts_next RENAME TO goal_facts;
	CREATE INDEX goal_facts_by_assistant ON goal_facts(assistant_id);
	CREATE INDEX goal_facts_by_goal ON goal_facts(goal_id);
	CREATE INDEX goal_facts_activity ON goal_facts(assistant_id,seq);
	CREATE TABLE activity_cursors (
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		consumer TEXT NOT NULL,
		ack_seq INTEGER NOT NULL DEFAULT 0 CHECK(ack_seq>=0),
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (assistant_id,consumer)
	);`,
	// v18: one durable, non-replaying progress-prompt outbox per Assistant.
	// A crash after claiming but before confirming the Herdr submit is never
	// automatically replayed. The submitted watermark is only advanced by a
	// positively confirmed Herdr AgentSubmit, not by a model completion guess.
	`CREATE TABLE coordinator_reports (
		assistant_id TEXT PRIMARY KEY REFERENCES assistants(id),
		state TEXT NOT NULL DEFAULT 'idle',
		from_seq INTEGER NOT NULL DEFAULT 0,
		through_seq INTEGER NOT NULL DEFAULT 0,
		submitted_seq INTEGER NOT NULL DEFAULT 0,
		attempts INTEGER NOT NULL DEFAULT 0,
		next_at INTEGER NOT NULL DEFAULT 0,
		last_submitted_at INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		herdr_session TEXT NOT NULL DEFAULT '',
		workspace_id TEXT NOT NULL DEFAULT '',
		pane_id TEXT NOT NULL DEFAULT '',
		coordinator_revision INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL DEFAULT 0
	);`,
	// v19: A Herdr-accepted prompt is not an OMP response. An opaque nonce
	// binds the sent batch to a real OMP user message and a subsequent final
	// assistant record in the provider's read-only JSONL. Pre-v19 receipts are
	// explicitly untracked rather than falsely marked as verified.
	`ALTER TABLE coordinator_reports ADD COLUMN report_marker TEXT NOT NULL DEFAULT '';
	ALTER TABLE coordinator_reports ADD COLUMN reply_state TEXT NOT NULL DEFAULT 'untracked';
	ALTER TABLE coordinator_reports ADD COLUMN reply_session_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE coordinator_reports ADD COLUMN reply_message_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE coordinator_reports ADD COLUMN reply_verified_at INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE coordinator_reports ADD COLUMN reply_checked_at INTEGER NOT NULL DEFAULT 0;
	-- Pre-v19 submitted receipts only prove Herdr accepted the prompt.
	-- They have no correlation marker; do not send a new report as though
	-- the last OMP answer had been verified. Existing owners can explicitly
	-- skip/retry the untracked batch through the normal review UI.
	UPDATE coordinator_reports SET state='uncertain',
	last_error='Legacy Herdr-accepted report has no verified OMP reply; check the original Chat'
	WHERE state='submitted' AND reply_state='untracked';`,
	// v20: a Worker's result is a readable, attributable record (Gap W02):
	// what the Agent finally said, where Solco read it from, and which tools,
	// skills and grounded URLs the transcript shows. An Agent that merely went
	// idle leaves no row here and its run is not done.
	`CREATE TABLE run_results (
		run_id TEXT PRIMARY KEY REFERENCES runs(id),
		evidence TEXT NOT NULL,
		agent_session_id TEXT NOT NULL DEFAULT '',
		message_id TEXT NOT NULL DEFAULT '',
		sha256 TEXT NOT NULL,
		body TEXT NOT NULL,
		collected_at INTEGER NOT NULL
	);`,
	// v21: skills a task must use, and the skills a person let an Assistant use
	// (Gap W04/C10). A grant is only ever written by a person's approval.
	`CREATE TABLE task_skills (
		task_id TEXT NOT NULL REFERENCES tasks(id),
		skill TEXT NOT NULL,
		PRIMARY KEY (task_id, skill)
	);
	CREATE TABLE skill_grants (
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		skill TEXT NOT NULL,
		granted_at INTEGER NOT NULL,
		PRIMARY KEY (assistant_id, skill)
	);`,
	// v22: skills a routine's runs must use; copied onto every task it fires.
	`CREATE TABLE routine_skills (
		routine_name TEXT NOT NULL,
		skill TEXT NOT NULL,
		PRIMARY KEY (routine_name, skill)
	);`,
	// v23: the Assistant's Autopilot checkpoint (Gap A03) and the wake marker on
	// the coordinator outbox (Gap A01): kind 'report' (opt-in cadence) or 'wake'
	// (an autonomous inspect→act turn), with the reason a wake was raised.
	`CREATE TABLE assistant_checkpoints (
		assistant_id TEXT PRIMARY KEY,
		last_seq INTEGER NOT NULL DEFAULT 0,
		progress TEXT NOT NULL DEFAULT '',
		next_action TEXT NOT NULL DEFAULT '',
		blockers TEXT NOT NULL DEFAULT '',
		stop_when TEXT NOT NULL DEFAULT '',
		revision INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL DEFAULT 0
	);
	ALTER TABLE coordinator_reports ADD COLUMN kind TEXT NOT NULL DEFAULT 'report';
	ALTER TABLE coordinator_reports ADD COLUMN wake_reason TEXT NOT NULL DEFAULT '';
	ALTER TABLE coordinator_reports ADD COLUMN wake_queued_at INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE heartbeats ADD COLUMN wake_pending INTEGER NOT NULL DEFAULT 0;`,
	// v24: a task's own, narrower time limit in minutes (Gap W09). Absent = the
	// dispatcher's limit; it can never be longer than that.
	`CREATE TABLE task_limits (
		task_id TEXT PRIMARY KEY REFERENCES tasks(id),
		timeout_min INTEGER NOT NULL
	);`,
	// v25: event triggers (Gap R03) — an approved rule that turns one of an
	// Assistant's own task events into one new task, deduped per event.
	`CREATE TABLE event_triggers (
		id TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		name TEXT NOT NULL,
		on_kind TEXT NOT NULL,
		on_task_id TEXT NOT NULL DEFAULT '',
		title_contains TEXT NOT NULL DEFAULT '',
		task_title TEXT NOT NULL,
		task_brief TEXT NOT NULL,
		task_kind TEXT NOT NULL,
		state TEXT NOT NULL,
		event_mark INTEGER NOT NULL DEFAULT 0,
		fires_day TEXT NOT NULL DEFAULT '',
		fires_today INTEGER NOT NULL DEFAULT 0,
		fires_total INTEGER NOT NULL DEFAULT 0,
		revision INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		UNIQUE(assistant_id, name)
	);
	CREATE TABLE event_trigger_fires (
		trigger_id TEXT NOT NULL,
		event_seq INTEGER NOT NULL,
		task_id TEXT NOT NULL,
		PRIMARY KEY (trigger_id, event_seq)
	);`,
	// v26: webhook ingress (Gaps A06/A09) — per-Assistant revocable keys and the
	// durable receipt of every accepted delivery.
	`CREATE TABLE hook_keys (
		id TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		secret TEXT NOT NULL,
		sources TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		revoked_at INTEGER NOT NULL DEFAULT 0,
		last_used_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE hook_events (
		id TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL,
		key_id TEXT NOT NULL,
		source TEXT NOT NULL,
		delivery_id TEXT NOT NULL,
		payload_hash TEXT NOT NULL,
		resource_kind TEXT NOT NULL DEFAULT '',
		resource_id TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		routing TEXT NOT NULL,
		received_at INTEGER NOT NULL,
		UNIQUE(key_id, delivery_id)
	);`,
	// v27: capability grants and the receipts of their decisions (Gap A04).
	// A grant is immutable and sealed by the owner's key; the core verifies the
	// seal at use time, so a row inserted by hand authorises nothing.
	`CREATE TABLE capability_grants (
		id TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		capability TEXT NOT NULL,
		effect TEXT NOT NULL,
		scope TEXT NOT NULL DEFAULT '{}',
		max_per_day INTEGER NOT NULL DEFAULT 0,
		issued_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		revoked_at INTEGER NOT NULL DEFAULT 0,
		issued_by TEXT NOT NULL DEFAULT 'owner',
		seal TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	CREATE INDEX capability_grants_assistant ON capability_grants(assistant_id, capability);
	CREATE TABLE authorization_receipts (
		id TEXT PRIMARY KEY,
		assistant_id TEXT NOT NULL,
		grant_id TEXT NOT NULL DEFAULT '',
		capability TEXT NOT NULL,
		action_hash TEXT NOT NULL DEFAULT '',
		request_id TEXT NOT NULL UNIQUE,
		decision TEXT NOT NULL,
		reason TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);
	CREATE INDEX authorization_receipts_grant ON authorization_receipts(grant_id, created_at);`,
	// v28: the budget ledger (Gap A05) — one row for each task the system
	// started on its own and each time it held back, with the reason. A side
	// record: the counters in proactivity stay authoritative.
	`CREATE TABLE budget_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		assistant_id TEXT NOT NULL REFERENCES assistants(id),
		day TEXT NOT NULL,
		kind TEXT NOT NULL,
		ref TEXT NOT NULL,
		outcome TEXT NOT NULL,
		reason TEXT NOT NULL,
		at INTEGER NOT NULL,
		dedupe TEXT NOT NULL UNIQUE
	);
	CREATE INDEX budget_events_assistant ON budget_events(assistant_id, at);`,
	// v29: a failed run's cause when the dispatcher itself can prove it (Gap
	// Q05): Herdr gave no structured answer. Never text, never what an Agent
	// printed in its pane.
	`ALTER TABLE runs ADD COLUMN fail_code TEXT NOT NULL DEFAULT '';`,
	// v30: a task may require checks (Gap Q03) — only ever stricter than its
	// Assistant's policy, so it lives beside the task's other narrowing limit.
	`ALTER TABLE task_limits ADD COLUMN require_checks INTEGER NOT NULL DEFAULT 0;`,
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

// peekReceipt returns the stored result of an earlier request without running
// anything, for commands whose work happens outside the transaction.
func peekReceipt[T any](s *Store, requestID, kind string, payload any) (T, bool, error) {
	var zero T
	if requestID == "" {
		return zero, false, nil
	}
	var storedKind, storedHash, stored string
	err := s.db.QueryRow(`SELECT kind, payload_hash, result FROM receipts WHERE request_id=?`, requestID).Scan(&storedKind, &storedHash, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, false, nil
	}
	if err != nil {
		return zero, false, wrapBusy(err)
	}
	if storedKind != kind || storedHash != hashPayload(kind, payload) {
		return zero, false, ErrRequestConflict
	}
	var out T
	if err := json.Unmarshal([]byte(stored), &out); err != nil {
		return zero, false, fmt.Errorf("stored receipt unreadable: %w", err)
	}
	return out, true, nil
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
