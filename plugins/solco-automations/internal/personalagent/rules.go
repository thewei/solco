package personalagent

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/schedule"
)

// Stage D: one dispatcher for every rule (docs/personal-agent-go-mvp-plan.md).
//
// The definitions of the old scheduled automations stay in automations.yaml —
// so `put`, `approve`, the desktop page and the phone keep working untouched.
// What moves is *dispatch ownership*: a ledger row per rule says whether the
// old runner or this scheduler fires it, and carries the rule's watermark (the
// last occurrence it was evaluated for). Occurrences become Tasks under a
// system Assistant, deduped by (rule, occurrence) in the event log, executed
// by the Dispatcher on the same Herdr engine the old runner used. The policy
// is schedule.Plan itself, so cron, catch-up, missed, pause and draft behave
// as they always did.

// Ledger states.
const (
	LegacyDryRun     = "dry_run"     // reconciled and disabled: owns nothing
	LegacyActive     = "active"      // this scheduler dispatches it
	LegacyRolledBack = "rolled_back" // the old scheduler dispatches it again
)

const legacyPrefix = "legacy:"

// LegacyRuleID is the rule id of a legacy automation's tasks.
func LegacyRuleID(name string) string { return legacyPrefix + name }

// LegacyRuleName reverses LegacyRuleID.
func LegacyRuleName(ruleID string) (string, bool) {
	if strings.HasPrefix(ruleID, legacyPrefix) {
		return strings.TrimPrefix(ruleID, legacyPrefix), true
	}
	return "", false
}

// LegacyRule is one ledger row.
type LegacyRule struct {
	Name           string `json:"name"`
	State          string `json:"state"`
	LastOccurrence int64  `json:"lastOccurrence"`
	Queued         bool   `json:"queued"`
	Handback       bool   `json:"handback"`
	Revision       int    `json:"revision"`
	AdoptedAt      int64  `json:"adoptedAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

// LegacyCandidate is the dry-run's answer for one automation.
type LegacyCandidate struct {
	Name         string    `json:"name"`
	Migratable   bool      `json:"migratable"`
	Reason       string    `json:"reason,omitempty"`
	State        string    `json:"state,omitempty"`
	LegacyNext   time.Time `json:"legacyNext"`
	PersonalNext time.Time `json:"personalNext"`
	Match        bool      `json:"match"`
}

const legacyCols = `name,state,last_occurrence,queued,handback,revision,adopted_at,updated_at`

func scanLegacy(r rowScanner) (LegacyRule, error) {
	var l LegacyRule
	var q, h int
	err := r.Scan(&l.Name, &l.State, &l.LastOccurrence, &q, &h, &l.Revision, &l.AdoptedAt, &l.UpdatedAt)
	l.Queued, l.Handback = q == 1, h == 1
	return l, err
}

func getLegacyTx(q queryer, name string) (LegacyRule, bool, error) {
	l, err := scanLegacy(q.QueryRow(`SELECT `+legacyCols+` FROM legacy_rules WHERE name=?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return l, false, nil
	}
	return l, err == nil, wrapBusy(err)
}

// migratable says whether the Herdr launcher can run this definition the way
// the old runner did. What needs the hc backend or an hc template cannot.
func migratable(a config.Automation, backend config.Backend) (bool, string) {
	switch {
	case backend == config.BackendHC:
		return false, "the config selects the hc backend; Assistants run only in the Herdr session"
	case a.Template != "":
		return false, "it uses an hc template (hc backend only)"
	}
	return true, ""
}

// watermark is the occurrence the rule was last evaluated for; an automation
// the old scheduler never saw starts counting from now, as Register does.
func watermark(last map[string]time.Time, name string, now time.Time) time.Time {
	if t, ok := last[name]; ok && !t.IsZero() {
		return t
	}
	return now
}

// DryRunLegacy reconciles every automation without taking ownership: a
// migratable one gets a disabled ledger row carrying the old scheduler's
// watermark, and the next fire time the new scheduler would compute is set
// beside the old one's. Running it again changes nothing.
func (s *Store) DryRunLegacy(cfg *config.Config, legacyLast map[string]time.Time, now time.Time) ([]LegacyCandidate, error) {
	out := []LegacyCandidate{}
	_, _, err := execute(s, "", "legacy.dryrun", nil, func(tx *sql.Tx) (struct{}, error) {
		out = out[:0]
		for _, a := range cfg.Automations {
			c := LegacyCandidate{Name: a.Name}
			c.Migratable, c.Reason = migratable(a, cfg.Backend)
			from := watermark(legacyLast, a.Name, now)
			if next, ok := schedule.NextRun(a, from); ok {
				c.LegacyNext = next
			}
			if !c.Migratable {
				out = append(out, c)
				continue
			}
			row, exists, err := getLegacyTx(tx, a.Name)
			if err != nil {
				return struct{}{}, err
			}
			switch {
			case !exists:
				_, err = tx.Exec(`INSERT INTO legacy_rules(`+legacyCols+`) VALUES(?,?,?,?,?,?,?,?)`,
					a.Name, LegacyDryRun, from.UnixMilli(), 0, 0, 1, 0, s.nowMS())
				row = LegacyRule{Name: a.Name, State: LegacyDryRun, LastOccurrence: from.UnixMilli()}
			case row.State == LegacyRolledBack:
				_, err = tx.Exec(`UPDATE legacy_rules SET state=?, last_occurrence=?, revision=revision+1, updated_at=? WHERE name=?`,
					LegacyDryRun, from.UnixMilli(), s.nowMS(), a.Name)
				row.State, row.LastOccurrence = LegacyDryRun, from.UnixMilli()
			}
			if err != nil {
				return struct{}{}, err
			}
			c.State = row.State
			if next, ok := schedule.NextRun(a, time.UnixMilli(row.LastOccurrence)); ok {
				c.PersonalNext = next
			}
			c.Match = !c.PersonalNext.IsZero() && c.PersonalNext.Equal(c.LegacyNext)
			out = append(out, c)
		}
		return struct{}{}, nil
	})
	return out, err
}

// AdoptLegacy makes this scheduler the rule's only dispatcher, from the old
// scheduler's watermark, so no occurrence is lost and none runs twice.
func (s *Store) AdoptLegacy(requestID, name string, cfg *config.Config, legacyLast map[string]time.Time, now time.Time) (LegacyRule, bool, error) {
	return execute(s, requestID, "legacy.adopt", map[string]string{"name": name}, func(tx *sql.Tx) (LegacyRule, error) {
		a := cfg.Find(name)
		if a == nil {
			return LegacyRule{}, fmt.Errorf("%w: no automation named %q", ErrNotFound, name)
		}
		if ok, why := migratable(*a, cfg.Backend); !ok {
			return LegacyRule{}, fmt.Errorf("%w: %s", ErrInvalid, why)
		}
		row, exists, err := getLegacyTx(tx, name)
		if err != nil {
			return row, err
		}
		from := watermark(legacyLast, name, now).UnixMilli()
		if exists && row.State == LegacyDryRun && legacyLast[name].IsZero() {
			from = row.LastOccurrence // keep the dry run's watermark when no newer one is known
		}
		if !exists {
			_, err = tx.Exec(`INSERT INTO legacy_rules(`+legacyCols+`) VALUES(?,?,?,?,?,?,?,?)`,
				name, LegacyActive, from, 0, 0, 1, now.UnixMilli(), s.nowMS())
		} else {
			_, err = tx.Exec(`UPDATE legacy_rules SET state=?, last_occurrence=?, queued=0, handback=0, revision=revision+1, adopted_at=?, updated_at=? WHERE name=?`,
				LegacyActive, from, now.UnixMilli(), s.nowMS(), name)
		}
		if err != nil {
			return LegacyRule{}, err
		}
		row, _, err = getLegacyTx(tx, name)
		return row, err
	})
}

// RollbackLegacy gives a rule back to the old scheduler. Its watermark is
// handed over once (TakeHandbacks) so the old scheduler resumes there, not at
// an older occurrence it would replay.
func (s *Store) RollbackLegacy(requestID, name string) (LegacyRule, bool, error) {
	return execute(s, requestID, "legacy.rollback", map[string]string{"name": name}, func(tx *sql.Tx) (LegacyRule, error) {
		row, exists, err := getLegacyTx(tx, name)
		if err != nil {
			return row, err
		}
		if !exists {
			return row, fmt.Errorf("%w: %q is not in the ledger", ErrNotFound, name)
		}
		if row.State != LegacyActive {
			return row, fmt.Errorf("%w: %q is %s", ErrBadState, name, row.State)
		}
		if _, err := tx.Exec(`UPDATE legacy_rules SET state=?, queued=0, handback=1, revision=revision+1, updated_at=? WHERE name=?`,
			LegacyRolledBack, s.nowMS(), name); err != nil {
			return row, err
		}
		row, _, err = getLegacyTx(tx, name)
		return row, err
	})
}

// OwnedLegacy lists the rules this scheduler dispatches.
func (s *Store) OwnedLegacy() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT name FROM legacy_rules WHERE state=?`, LegacyActive)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = true
	}
	return out, wrapBusy(rows.Err())
}

// LegacyLedger returns every row.
func (s *Store) LegacyLedger() ([]LegacyRule, error) {
	rows, err := s.db.Query(`SELECT ` + legacyCols + ` FROM legacy_rules ORDER BY name`)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []LegacyRule{}
	for rows.Next() {
		l, err := scanLegacy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, wrapBusy(rows.Err())
}

// TakeHandbacks returns the watermark of each rolled-back rule once.
func (s *Store) TakeHandbacks() (map[string]time.Time, error) {
	out := map[string]time.Time{}
	_, _, err := execute(s, "", "legacy.handback", nil, func(tx *sql.Tx) (struct{}, error) {
		rows, err := tx.Query(`SELECT name,last_occurrence FROM legacy_rules WHERE handback=1`)
		if err != nil {
			return struct{}{}, err
		}
		for rows.Next() {
			var n string
			var ms int64
			if err := rows.Scan(&n, &ms); err != nil {
				_ = rows.Close()
				return struct{}{}, err
			}
			out[n] = time.UnixMilli(ms)
		}
		_ = rows.Close()
		_, err = tx.Exec(`UPDATE legacy_rules SET handback=0 WHERE handback=1`)
		return struct{}{}, err
	})
	return out, err
}

// FilterOwned returns cfg without the rules this scheduler owns — what the old
// scheduler is allowed to see. cfg itself is not touched.
func FilterOwned(cfg *config.Config, owned map[string]bool) *config.Config {
	out := *cfg
	out.Automations = nil
	for _, a := range cfg.Automations {
		if !owned[a.Name] {
			out.Automations = append(out.Automations, a)
		}
	}
	return &out
}

// AutomationsAssistant is the system Assistant the rules' tasks belong to. It
// is created on first use and found again by the id kept in the meta table.
func (s *Store) AutomationsAssistant() (Assistant, error) {
	var out Assistant
	_, _, err := execute(s, "", "legacy.assistant", nil, func(tx *sql.Tx) (struct{}, error) {
		a, err := automationsAssistantTx(tx, s.nowMS())
		out = a
		return struct{}{}, err
	})
	return out, err
}

func automationsAssistantTx(tx *sql.Tx, now int64) (Assistant, error) {
	var id string
	err := tx.QueryRow(`SELECT value FROM meta WHERE key='automations_assistant'`).Scan(&id)
	if err == nil {
		return getAssistantTx(tx, id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Assistant{}, err
	}
	id = newID("a")
	if _, err := tx.Exec(`INSERT INTO assistants(`+assistantCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, "Automations", "Runs the scheduled automations from automations.yaml.", "", ScopePersonal, "[]", "", "review", StatusActive, 1, now, now); err != nil {
		return Assistant{}, err
	}
	if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES('automations_assistant',?)`, id); err != nil {
		return Assistant{}, err
	}
	return getAssistantTx(tx, id)
}

// RuleNote is one thing the old history log should hear about: an occurrence
// that did not become a run (missed, skipped, queued).
type RuleNote struct {
	Name    string
	Status  string // "missed", "skipped", "queued"
	Trigger string
	Detail  string
	Count   int
}

// RuleReport counts one scheduler tick.
type RuleReport struct {
	Fired, Missed, Skipped, Queued int
	Notes                          []RuleNote
}

// RuleScheduler fires the rules this store owns.
type RuleScheduler struct {
	Store *Store
	// Load reads the current definitions (config.Load in the daemon).
	Load func() (*config.Config, error)
	Now  func() time.Time
}

func (r *RuleScheduler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

const terminalTaskStates = `'done','failed','cancelled','review_required','draft'`

func ruleInFlight(tx *sql.Tx, name string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE rule_id=? AND state NOT IN (`+terminalTaskStates+`)`, LegacyRuleID(name)).Scan(&n)
	return n > 0, err
}

// Tick evaluates the owned rules once, in one transaction: the watermarks,
// the occurrence events and the tasks they cause commit together.
func (r *RuleScheduler) Tick() (RuleReport, error) {
	owned, err := r.Store.OwnedLegacy()
	if err != nil || len(owned) == 0 {
		return RuleReport{}, err
	}
	cfg, err := r.Load()
	if err != nil {
		return RuleReport{}, err
	}
	now := r.now()
	mine := *cfg
	mine.Automations = nil
	for _, a := range cfg.Automations {
		if owned[a.Name] {
			mine.Automations = append(mine.Automations, a)
		}
	}
	rep, _, err := execute(r.Store, "", "legacy.tick", nil, func(tx *sql.Tx) (RuleReport, error) {
		var rep RuleReport
		ledger := map[string]LegacyRule{}
		state := schedule.State{LastOccurrence: map[string]time.Time{}}
		for name := range owned {
			row, _, err := getLegacyTx(tx, name)
			if err != nil {
				return rep, err
			}
			ledger[name] = row
			if row.LastOccurrence > 0 {
				state.LastOccurrence[name] = time.UnixMilli(row.LastOccurrence)
			}
		}
		sys, err := automationsAssistantTx(tx, now.UnixMilli())
		if err != nil {
			return rep, err
		}
		asst := sys
		nowMS := now.UnixMilli()

		fire := func(a config.Automation, revision string, trigger string) error {
			// An enabled Routine's runs belong to the Assistant that owns it.
			owner := sys
			if id, ok := routineOwnerTx(tx, a.Name); ok {
				owner.ID = id
			}
			asst := owner
			created, err := emitTx(tx, Event{AssistantID: asst.ID, Source: "rule", Kind: "rule.fired", Resource: a.Name, Revision: revision,
				Detail: fmt.Sprintf("%s: fired (%s)", a.Name, trigger)}, nowMS)
			if err != nil || !created {
				return err // not created: this occurrence already ran
			}
			tid := newID("t")
			brief := a.Prompt
			if brief == "" {
				brief = "workflow " + a.Workflow
			}
			if _, err := tx.Exec(`INSERT INTO tasks(`+taskCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				tid, asst.ID, "", LegacyRuleID(a.Name), "", KindManual, a.Name, brief, a.Repo, a.Agent, a.Model,
				TaskQueued, 0, "", 1, 1, nowMS, nowMS); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE events SET task_id=? WHERE source='rule' AND resource=? AND revision=? AND kind='rule.fired'`, tid, a.Name, revision); err != nil {
				return err
			}
			if err := emitTask(tx, tid, "task.added", nowMS); err != nil {
				return err
			}
			rep.Fired++
			return nil
		}
		note := func(a config.Automation, kind, status, trigger, detail, revision string, count int) error {
			if _, err := emitTx(tx, Event{AssistantID: asst.ID, Source: "rule", Kind: kind, Resource: a.Name, Revision: revision,
				Detail: fmt.Sprintf("%s: %s", a.Name, status)}, nowMS); err != nil {
				return err
			}
			rep.Notes = append(rep.Notes, RuleNote{Name: a.Name, Status: status, Trigger: trigger, Detail: detail, Count: count})
			return nil
		}

		// A queued occurrence waits for its rule to be free, and dies with a
		// rule that was removed, paused or unapproved while it waited.
		for name, row := range ledger {
			if !row.Queued {
				continue
			}
			a := mine.Find(name)
			clear := a == nil || a.Disabled || a.Draft
			if !clear {
				busy, err := ruleInFlight(tx, name)
				if err != nil {
					return rep, err
				}
				if busy {
					continue
				}
				if err := fire(*a, fmt.Sprintf("q%d", nowMS), "queued"); err != nil {
					return rep, err
				}
			}
			if _, err := tx.Exec(`UPDATE legacy_rules SET queued=0 WHERE name=?`, name); err != nil {
				return rep, err
			}
			row.Queued = false
			ledger[name] = row
		}

		res := schedule.Plan(&mine, state, now)
		for _, d := range res.Decisions {
			a := d.Automation
			switch d.Kind {
			case schedule.Missed:
				rep.Missed++
				if err := note(a, "rule.missed", "missed", "cron", d.Reason, fmt.Sprintf("%d#%d", d.At.UnixMilli(), nowMS), d.Count); err != nil {
					return rep, err
				}
			case schedule.Paused:
				rep.Skipped++
				if err := note(a, "rule.skipped", "skipped", "cron", fmt.Sprintf("paused (%d)", d.Count), fmt.Sprintf("paused:%d", d.At.UnixMilli()), d.Count); err != nil {
					return rep, err
				}
			case schedule.Fire:
				busy, err := ruleInFlight(tx, a.Name)
				if err != nil {
					return rep, err
				}
				rev := fmt.Sprint(d.At.UnixMilli())
				switch {
				case busy && a.OverlapQueues() && !ledger[a.Name].Queued:
					rep.Queued++
					if _, err := tx.Exec(`UPDATE legacy_rules SET queued=1 WHERE name=?`, a.Name); err != nil {
						return rep, err
					}
					row := ledger[a.Name]
					row.Queued = true
					ledger[a.Name] = row
					if err := note(a, "rule.queued", "queued", string(d.Trigger), "previous run still in flight", rev, 1); err != nil {
						return rep, err
					}
				case busy:
					rep.Skipped++
					why := "previous run still in flight"
					if a.OverlapQueues() {
						why = "an occurrence is already queued"
					}
					if err := note(a, "rule.skipped", "skipped", string(d.Trigger), why, rev, 1); err != nil {
						return rep, err
					}
				default:
					if err := fire(a, rev, string(d.Trigger)); err != nil {
						return rep, err
					}
				}
			}
		}
		for name, t := range res.State.LastOccurrence {
			if !owned[name] {
				continue
			}
			if t.UnixMilli() != ledger[name].LastOccurrence {
				if _, err := tx.Exec(`UPDATE legacy_rules SET last_occurrence=?, updated_at=? WHERE name=?`, t.UnixMilli(), nowMS, name); err != nil {
					return rep, err
				}
			}
		}
		return rep, nil
	})
	return rep, err
}

// PruneRuleHistory keeps the rules' finished tasks (and their runs and events)
// for `keep`, at most `max` of them, newest first — the old history log's 30
// days / 5000 records. A task still in flight is never pruned.
func (s *Store) PruneRuleHistory(keep time.Duration, max int) (int, error) {
	cutoff := s.now().Add(-keep).UnixMilli()
	n, _, err := execute(s, "", "legacy.prune", nil, func(tx *sql.Tx) (int, error) {
		doomed := map[string]bool{}
		rows, err := tx.Query(`SELECT id FROM tasks WHERE rule_id LIKE 'legacy:%' AND state IN ('done','failed','cancelled') AND updated_at<?`, cutoff)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			doomed[id] = true
		}
		_ = rows.Close()
		if max > 0 {
			rows, err := tx.Query(`SELECT id FROM tasks WHERE rule_id LIKE 'legacy:%' AND state IN ('done','failed','cancelled') ORDER BY updated_at DESC, id DESC LIMIT -1 OFFSET ?`, max)
			if err != nil {
				return 0, err
			}
			for rows.Next() {
				var id string
				_ = rows.Scan(&id)
				doomed[id] = true
			}
			_ = rows.Close()
		}
		for id := range doomed {
			if _, err := tx.Exec(`DELETE FROM runs WHERE task_id=?`, id); err != nil {
				return 0, err
			}
			if _, err := tx.Exec(`DELETE FROM events WHERE task_id=?`, id); err != nil {
				return 0, err
			}
			if _, err := tx.Exec(`DELETE FROM tasks WHERE id=?`, id); err != nil {
				return 0, err
			}
		}
		return len(doomed), nil
	})
	return n, err
}
