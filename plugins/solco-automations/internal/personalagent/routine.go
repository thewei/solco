package personalagent

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/schedule"
)

// U4: a Routine is a scheduled automation that belongs to an Assistant.
//
// The definition stays in automations.yaml and the one scheduler that fires it
// is RuleScheduler — a Routine adds no timer, no queue and no second file. What
// this file adds is the owner binding (routines table): who the work belongs to,
// who asked, the exact definition a person approved, and whether it was enabled.
// An Assistant may only DRAFT; a draft never fires. Enabling needs a person's
// answer to an approval card bound to the definition's hash — if the definition
// changes after that answer, the approval no longer matches and nothing is
// enabled. Results come back as tasks of the owning Assistant plus ledger facts.

// Routine states.
const (
	RoutineDraft     = "draft"     // proposed, never fires
	RoutineActive    = "active"    // approved by a person and owned by the rule scheduler
	RoutineStale     = "stale"     // the definition changed after the approval: ask again
	RoutineCancelled = "cancelled" // withdrawn before it was enabled
)

// Fact kinds a routine writes.
const (
	FactRoutine       = "routine"        // drafted, enabled, changed, withdrawn
	FactRoutineDone   = "routine_done"   // a scheduled run finished
	FactRoutineFailed = "routine_failed" // a scheduled run failed or is blocked
)

// Routine is the owner binding of one automation.
type Routine struct {
	Name          string `json:"name"`
	AssistantID   string `json:"assistantId"`
	RequestedBy   string `json:"requestedBy"` // assistant | user
	Source        string `json:"source"`      // chat | cli
	CorrelationID string `json:"correlationId,omitempty"`
	State         string `json:"state"`
	Revision      int    `json:"revision"`
	Approved      bool   `json:"approved"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
	DefHash       string `json:"-"`
}

const routineCols = `name,assistant_id,requested_by,source,correlation_id,state,revision,def_hash,approved,created_at,updated_at`

func scanRoutine(r rowScanner) (Routine, error) {
	var x Routine
	var ap int
	err := r.Scan(&x.Name, &x.AssistantID, &x.RequestedBy, &x.Source, &x.CorrelationID, &x.State, &x.Revision, &x.DefHash, &ap, &x.CreatedAt, &x.UpdatedAt)
	x.Approved = ap == 1
	return x, err
}

func getRoutineTx(q queryer, name string) (Routine, error) {
	x, err := scanRoutine(q.QueryRow(`SELECT `+routineCols+` FROM routines WHERE name=?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, wrapBusy(err)
}

// GetRoutine reads one routine.
func (s *Store) GetRoutine(name string) (Routine, error) { return getRoutineTx(s.db, name) }

// ListRoutines returns an Assistant's routines (everyone's when empty), newest first.
func (s *Store) ListRoutines(assistantID string) ([]Routine, error) {
	q, args := `SELECT `+routineCols+` FROM routines`, []any{}
	if assistantID != "" {
		q += ` WHERE assistant_id=?`
		args = append(args, assistantID)
	}
	rows, err := s.db.Query(q+` ORDER BY created_at DESC, name`, args...)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []Routine{}
	for rows.Next() {
		x, err := scanRoutine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// RoutineInput is what drafting records.
type RoutineInput struct {
	AssistantID   string
	Name          string
	RequestedBy   string
	Source        string
	CorrelationID string
	DefHash       string
}

// UpsertRoutine records a draft. A second draft of the same name by the same
// owner while it is not yet enabled replaces the first (revision + 1), which
// voids any approval card made for the older one.
func (s *Store) UpsertRoutine(requestID string, in RoutineInput) (Routine, bool, error) {
	if in.AssistantID == "" || strings.TrimSpace(in.Name) == "" || in.DefHash == "" {
		return Routine{}, false, ErrInvalid
	}
	if in.RequestedBy == "" {
		in.RequestedBy = "assistant"
	}
	if in.Source == "" {
		in.Source = "chat"
	}
	return execute(s, requestID, "routine.upsert", in, func(tx *sql.Tx) (Routine, error) {
		a, err := getAssistantTx(tx, in.AssistantID)
		if err != nil {
			return Routine{}, err
		}
		if a.Status != StatusActive {
			return Routine{}, ErrAssistantDisabled
		}
		now := s.nowMS()
		cur, err := getRoutineTx(tx, in.Name)
		switch {
		case errors.Is(err, ErrNotFound):
			_, err = tx.Exec(`INSERT INTO routines(`+routineCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
				in.Name, in.AssistantID, in.RequestedBy, in.Source, in.CorrelationID, RoutineDraft, 1, in.DefHash, 0, now, now)
			if err != nil {
				return Routine{}, err
			}
		case err != nil:
			return Routine{}, err
		case cur.AssistantID != in.AssistantID:
			return Routine{}, fmt.Errorf("%w: that routine belongs to another assistant", ErrInvalid)
		case cur.State == RoutineActive:
			return Routine{}, fmt.Errorf("%w: the routine is already enabled; change it from Automations", ErrBadState)
		default:
			if _, err := tx.Exec(`UPDATE routines SET requested_by=?, source=?, correlation_id=?, state=?, revision=revision+1, def_hash=?, approved=0, updated_at=? WHERE name=?`,
				in.RequestedBy, in.Source, in.CorrelationID, RoutineDraft, in.DefHash, now, in.Name); err != nil {
				return Routine{}, err
			}
		}
		if _, err := addFactTx(tx, Fact{AssistantID: in.AssistantID, Kind: FactRoutine, Text: fmt.Sprintf("Routine drafted: %s — waiting for your approval", in.Name)},
			fmt.Sprintf("routine-draft:%s:%s", in.Name, in.DefHash), now); err != nil {
			return Routine{}, err
		}
		return getRoutineTx(tx, in.Name)
	})
}

func (s *Store) setRoutineState(name, state string) error {
	_, err := s.db.Exec(`UPDATE routines SET state=?, updated_at=? WHERE name=?`, state, s.nowMS(), name)
	return wrapBusy(err)
}

// routineOwnerTx is the Assistant an enabled routine's runs belong to.
func routineOwnerTx(q queryer, name string) (string, bool) {
	var id string
	if err := q.QueryRow(`SELECT assistant_id FROM routines WHERE name=? AND state=?`, name, RoutineActive).Scan(&id); err != nil {
		return "", false
	}
	return id, true
}

// DefinitionHash identifies the parts of a definition a person approves: what
// runs, where, with which Agent and prompt, and when. The draft flag, the pause
// flag and the rationale are not part of it.
func DefinitionHash(a config.Automation) string {
	b, _ := json.Marshal(struct {
		Name, Cron, Repo, Workspace, WorkspaceID, Agent, Model, Template, Prompt, Workflow, MCPConfig, OnOverlap string
		Headless                                                                                                 bool
		AgentArgs                                                                                                []string
		Timeout, CatchUp                                                                                         int
	}{a.Name, a.Cron, a.Repo, string(a.Workspace), a.WorkspaceID, a.Agent, a.Model, a.Template, a.Prompt, a.Workflow, a.MCPConfig, a.OnOverlap,
		a.Headless, a.AgentArgs, a.TimeoutMinutes, a.CatchUpMinutes})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var cronZoneRe = regexp.MustCompile(`^(?:CRON_)?TZ=(\S+)\s`)

// RoutineAction is the sentence of the approval card: exactly what enabling means.
func RoutineAction(a config.Automation, next []time.Time) string {
	how := "in a Herdr pane"
	if a.Headless {
		how = "headless (no terminal prompts)"
	}
	when := ""
	if len(next) > 0 {
		when = " Next run " + next[0].Local().Format("2006-01-02 15:04 MST") + "."
		// A schedule pinned to a zone is approved in that zone, three runs deep.
		if m := cronZoneRe.FindStringSubmatch(a.Cron); m != nil {
			if loc, err := time.LoadLocation(m[1]); err == nil {
				var runs []string
				for _, n := range next {
					runs = append(runs, n.In(loc).Format("Mon 2006-01-02 15:04 MST"))
				}
				when = " Time zone " + m[1] + ". Next runs: " + strings.Join(runs, "; ") + "."
			}
		}
	}
	return fmt.Sprintf("Enable routine %q: run `%s` (cron) on %s with %s %s, unattended on this Mac.%s Prompt: %s",
		a.Name, a.Cron, a.Repo, a.Agent, how, when, clip(strings.Join(strings.Fields(a.Prompt), " "), 240))
}

// RoutineKeeper turns an approved routine into an enabled one. It is not a
// scheduler: it flips the automation's draft flag once, for exactly the
// definition that was approved, and restarts the rule's watermark at now so
// nothing from the draft period is replayed.
type RoutineKeeper struct {
	Store          *Store
	Load           func() (*config.Config, error)
	Approve        func(name string) error // legacy test seam; use ApproveChecked in production
	ApproveChecked func(name, expectedHash string) error
	Now            func() time.Time
}

// RoutineReport counts one keeper pass.
type RoutineReport struct{ Enabled, Stale, Withdrawn int }

func (k *RoutineKeeper) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

// ApproveRoutineDefinition atomically checks the exact normalized definition
// the Owner approved and enables only that version of automations.yaml.
func ApproveRoutineDefinition(name, expectedHash string) error {
	return config.ApproveIf(name, func(a config.Automation) bool {
		return DefinitionHash(a) == expectedHash
	})
}

// Tick enables every approved draft routine whose definition still matches.
func (k *RoutineKeeper) Tick() (RoutineReport, error) {
	var rep RoutineReport
	err := withRoutineMutationLock(func() error {
		var err error
		rep, err = k.tickLocked()
		return err
	})
	return rep, err
}

// reviewActive is the other half of the hash gate: the rule scheduler already
// skips an enabled Routine whose definition no longer matches what was approved,
// and this says so. The routine goes back to a draft of the edited definition
// with one new card; it runs again only after that yes. Every step is keyed by
// the new hash, so a repeated pass adds nothing.
func (k *RoutineKeeper) reviewActive(rep *RoutineReport) error {
	rows, err := k.Store.db.Query(`SELECT `+routineCols+` FROM routines WHERE state=? ORDER BY name`, RoutineActive)
	if err != nil {
		return wrapBusy(err)
	}
	var active []Routine
	for rows.Next() {
		x, err := scanRoutine(rows)
		if err != nil {
			rows.Close()
			return err
		}
		active = append(active, x)
	}
	rows.Close()
	for _, r := range active {
		cfg, err := k.Load()
		if err != nil {
			return err
		}
		a := cfg.Find(r.Name)
		if a == nil {
			if err := k.Store.setRoutineState(r.Name, RoutineCancelled); err != nil {
				return err
			}
			rep.Withdrawn++
			if err := k.Store.newFactFor(r.AssistantID, fmt.Sprintf("Routine %s was removed from Automations", r.Name), fmt.Sprintf("routine-gone:%s:%d", r.Name, r.Revision)); err != nil {
				return err
			}
			continue
		}
		hash := DefinitionHash(*a)
		if hash == r.DefHash {
			continue
		}
		if err := k.Store.setRoutineState(r.Name, RoutineStale); err != nil {
			return err
		}
		rep.Stale++
		if err := k.Store.newFactFor(r.AssistantID, fmt.Sprintf("Routine %s changed after you approved it — paused; approve the new version to run it again", r.Name), fmt.Sprintf("routine-edited:%s:%s", r.Name, hash)); err != nil {
			return err
		}
		next := []time.Time{}
		if sched, err := config.CronParser.Parse(a.WithDefaults().Cron); err == nil {
			t := k.now()
			for range 3 {
				t = sched.Next(t)
				next = append(next, t)
			}
		}
		reqID := fmt.Sprintf("routine-edit:%s:%s", r.Name, hash)
		if _, _, err := k.Store.UpsertRoutine(reqID+":r", RoutineInput{AssistantID: r.AssistantID, Name: r.Name,
			RequestedBy: "user", Source: "edit", DefHash: hash}); err != nil {
			return err
		}
		if _, _, err := k.Store.RequestApproval(reqID+":a", RequestApprovalInput{
			AssistantID: r.AssistantID, SubjectKind: "routine", SubjectID: r.Name,
			Action: RoutineAction(a.WithDefaults(), next), TTL: 7 * 24 * time.Hour,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (k *RoutineKeeper) tickLocked() (RoutineReport, error) {
	var rep RoutineReport
	if err := k.reviewActive(&rep); err != nil {
		return rep, err
	}
	rows, err := k.Store.db.Query(`SELECT `+routineCols+` FROM routines WHERE state=? AND approved=1 ORDER BY name`, RoutineDraft)
	if err != nil {
		return rep, wrapBusy(err)
	}
	var due []Routine
	for rows.Next() {
		x, err := scanRoutine(rows)
		if err != nil {
			rows.Close()
			return rep, err
		}
		due = append(due, x)
	}
	rows.Close()
	for _, r := range due {
		cfg, err := k.Load()
		if err != nil {
			return rep, err
		}
		a := cfg.Find(r.Name)
		fact := func(text, key string) error {
			return k.Store.newFactFor(r.AssistantID, text, key)
		}
		switch {
		case a == nil:
			if err := k.Store.setRoutineState(r.Name, RoutineCancelled); err != nil {
				return rep, err
			}
			rep.Withdrawn++
			if err := fact(fmt.Sprintf("Routine %s was removed from Automations before it started", r.Name), fmt.Sprintf("routine-gone:%s:%d", r.Name, r.Revision)); err != nil {
				return rep, err
			}
		case DefinitionHash(*a) != r.DefHash:
			if err := k.Store.setRoutineState(r.Name, RoutineStale); err != nil {
				return rep, err
			}
			rep.Stale++
			if err := fact(fmt.Sprintf("Routine %s changed after you approved it — not enabled; ask for a new approval", r.Name), fmt.Sprintf("routine-stale:%s:%d", r.Name, r.Revision)); err != nil {
				return rep, err
			}
		default:
			if _, err := k.Store.db.Exec(`UPDATE legacy_rules SET last_occurrence=?, updated_at=? WHERE name=?`, k.now().UnixMilli(), k.Store.nowMS(), r.Name); err != nil {
				return rep, wrapBusy(err)
			}
			if a.Draft {
				approve := k.ApproveChecked
				if approve == nil {
					if k.Approve != nil {
						// Retained for tests that supply a fake file writer. Real
						// daemon/API wiring always uses the conditional writer.
						approve = func(name, _ string) error { return k.Approve(name) }
					} else {
						approve = ApproveRoutineDefinition
					}
				}
				if err := approve(r.Name, r.DefHash); err != nil {
					if !errors.Is(err, config.ErrDefinitionChanged) {
						return rep, err
					}
					// The document changed after the initial check. Neither
					// a new prompt nor a different schedule may be enabled.
					if err := k.Store.setRoutineState(r.Name, RoutineStale); err != nil {
						return rep, err
					}
					rep.Stale++
					if err := fact(fmt.Sprintf("Routine %s changed during approval — not enabled; ask for a new approval", r.Name), fmt.Sprintf("routine-stale:%s:%d", r.Name, r.Revision)); err != nil {
						return rep, err
					}
					continue
				}
			}
			if err := k.Store.setRoutineState(r.Name, RoutineActive); err != nil {
				return rep, err
			}
			rep.Enabled++
			text := fmt.Sprintf("Routine enabled: %s", r.Name)
			if next, ok := schedule.NextRun(*a, k.now()); ok {
				text += " — next run " + next.Local().Format("2006-01-02 15:04 MST")
			}
			if err := fact(text, fmt.Sprintf("routine-on:%s:%d", r.Name, r.Revision)); err != nil {
				return rep, err
			}
		}
	}
	return rep, nil
}

func (s *Store) newFactFor(assistantID, text, key string) error {
	_, _, err := execute(s, "", "routine.fact", nil, func(tx *sql.Tx) (struct{}, error) {
		_, err := addFactTx(tx, Fact{AssistantID: assistantID, Kind: FactRoutine, Text: text}, key, s.nowMS())
		return struct{}{}, err
	})
	return err
}

// routineStopCode returns " (code)" when the task's newest run stopped on a
// stable gate code ("skill_not_granted: …"), never the free text after it.
func routineStopCode(tx *sql.Tx, taskID string) string {
	var msg string
	if err := tx.QueryRow(`SELECT error FROM runs WHERE task_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`, taskID).Scan(&msg); err != nil {
		return ""
	}
	code, _, found := strings.Cut(msg, ": ")
	if !found || code == "" {
		return ""
	}
	for _, c := range code {
		if (c < 'a' || c > 'z') && c != '_' {
			return ""
		}
	}
	return " (" + code + ")"
}

// routineEventFact words a rule task's event for its owner's ledger. Only the
// events that say something happened to a scheduled run speak.
func routineEventFact(kind, name string) (factKind, text string, ok bool) {
	switch kind {
	case "task.completed":
		return FactRoutineDone, fmt.Sprintf("Routine %s finished", name), true
	case "task.failed":
		return FactRoutineFailed, fmt.Sprintf("Routine %s failed", name), true
	case "task.blocked", "task.uncertain":
		return FactRoutineFailed, fmt.Sprintf("Routine %s needs you (%s)", name, strings.TrimPrefix(kind, "task.")), true
	}
	return "", "", false
}
