package personalagent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

// routineIntent is committed to SQLite before touching automations.yaml.
// It contains the immutable Owner-facing approval action and the request's
// idempotency key; resuming days later must not mint a new card or silently
// change what the original card said.
type routineIntent struct {
	RequestID string       `json:"requestId"`
	Input     RoutineInput `json:"input"`
	Action    string       `json:"action"`
	Next      []int64      `json:"next"`
}

func (s *Store) recordRoutineIntent(in routineIntent) error {
	if in.RequestID == "" || in.Input.Name == "" || in.Input.AssistantID == "" ||
		in.Input.DefHash == "" || in.Action == "" {
		return ErrInvalid
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO routine_intents(name,payload,created_at) VALUES(?,?,?)`,
		in.Input.Name, string(b), s.nowMS())
	if err != nil {
		return fmt.Errorf("routine %q already has a pending write or its ledger is unavailable: %w", in.Input.Name, wrapBusy(err))
	}
	return nil
}

func (s *Store) dropRoutineIntent(name string) error {
	_, err := s.db.Exec(`DELETE FROM routine_intents WHERE name=?`, name)
	return wrapBusy(err)
}

func (s *Store) pendingRoutineIntents() ([]routineIntent, error) {
	rows, err := s.db.Query(`SELECT payload FROM routine_intents ORDER BY created_at,name`)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	var intents []routineIntent
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var in routineIntent
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			return nil, fmt.Errorf("unreadable routine intent: %w", err)
		}
		if in.Input.Name == "" || in.Input.DefHash == "" || in.RequestID == "" {
			return nil, fmt.Errorf("%w: incomplete routine intent", ErrInvalid)
		}
		intents = append(intents, in)
	}
	return intents, wrapBusy(rows.Err())
}

// completeRoutineIntent may be re-run any number of times after an abrupt
// process exit. Each of its three SQLite effects has a stable requestId;
// none of them approves a Routine, only requests an Owner approval card.
func (s *Store) completeRoutineIntent(in routineIntent, cfg *config.Config, last map[string]time.Time, now time.Time) (Routine, Approval, bool, error) {
	x := cfg.Find(in.Input.Name)
	if x == nil || !x.Draft || DefinitionHash(*x) != in.Input.DefHash {
		return Routine{}, Approval{}, false, config.ErrDefinitionChanged
	}
	r, replayed, err := s.UpsertRoutine(in.RequestID+":r", in.Input)
	if err != nil {
		return Routine{}, Approval{}, false, err
	}
	if _, _, err := s.AdoptLegacy(in.RequestID+":d", in.Input.Name, cfg, last, now); err != nil {
		return r, Approval{}, replayed, err
	}
	ap, _, err := s.RequestApproval(in.RequestID+":a", RequestApprovalInput{
		AssistantID: in.Input.AssistantID, SubjectKind: "routine", SubjectID: in.Input.Name,
		Action: in.Action, TTL: 7 * 24 * time.Hour,
	})
	return r, ap, replayed, err
}

// RecoverRoutineIntents runs before the CLI serves requests and before the
// scheduler starts ticking. A crash after YAML or after any SQLite stage rolls
// forward to an approval *request*, never an automatic approval or dispatch.
// If an external editor has changed the definition, recovery refuses to
// overwrite it and leaves the durable intent as a legacy-dispatch reservation.
func (s *Store) RecoverRoutineIntents() (int, error) {
	recovered := 0
	err := withRoutineMutationLock(func() error {
		var quarantined []string
		intents, err := s.pendingRoutineIntents()
		if err != nil {
			return err
		}
		for _, in := range intents {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			x := cfg.Find(in.Input.Name)
			if x == nil {
				// Crash before YAML replacement, or the entry was removed.
				// No YAML operation may be invented by recovery.
				if err := s.dropRoutineIntent(in.Input.Name); err != nil {
					return err
				}
				recovered++
				continue
			}
			if DefinitionHash(*x) != in.Input.DefHash {
				// Keep the reservation so the old scheduler cannot execute
				// a name whose ownership is not fully reconciled. A broken
				// draft must not stop recovering unrelated Assistants.
				quarantined = append(quarantined, in.Input.Name)
				continue
			}
			if !x.Draft {
				r, err := s.GetRoutine(in.Input.Name)
				if err == nil && r.State == RoutineActive && r.Approved && r.DefHash == in.Input.DefHash {
					if err := s.dropRoutineIntent(in.Input.Name); err != nil {
						return err
					}
					recovered++
					continue
				}
				if err != nil && !errors.Is(err, ErrNotFound) {
					return err
				}
				// Generic Automations approval can clear the YAML Draft flag
				// without an Owner decision. Restore it through a conditional
				// *locked* mutation, not a stale read followed by config.Put.
				if err := config.RestoreDraftIf(in.Input.Name, func(a config.Automation) bool {
					return DefinitionHash(a) == in.Input.DefHash
				}); err != nil {
					return err
				}
				cfg, err = config.Load()
				if err != nil {
					return err
				}
			}
			if _, _, _, err := s.completeRoutineIntent(in, cfg, nil, s.now()); err != nil {
				return fmt.Errorf("recover routine %s: %w", in.Input.Name, err)
			}
			if err := s.dropRoutineIntent(in.Input.Name); err != nil {
				return err
			}
			recovered++
		}
		if len(quarantined) > 0 {
			return fmt.Errorf("%w: interrupted draft(s) %v changed on disk; left reserved from scheduling", config.ErrDefinitionChanged, quarantined)
		}
		return nil
	})
	return recovered, err
}

// LegacyReservations are the rules the old scheduler must leave alone.
// Includes intent-only entries that have made it to YAML but have not yet
// made it to the Assistant's adopted legacy_rules ledger.
func (s *Store) LegacyReservations() (map[string]bool, error) {
	owned, err := s.OwnedLegacy()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT name FROM routine_intents`)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		owned[name] = true
	}
	return owned, wrapBusy(rows.Err())
}

// routineApprovalReceipt recovers the original frozen wording for a validated
// routine.upsert replay. The caller must first verify that the :r receipt
// matches the current request body, so this lookup cannot authorize a new one.
func (s *Store) routineApprovalReceipt(requestID string) (Approval, bool, error) {
	var kind, raw string
	err := s.db.QueryRow(`SELECT kind,result FROM receipts WHERE request_id=?`, requestID+":a").Scan(&kind, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Approval{}, false, nil
	}
	if err != nil {
		return Approval{}, false, wrapBusy(err)
	}
	if kind != "approval.request" {
		return Approval{}, false, ErrRequestConflict
	}
	var out Approval
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return Approval{}, false, err
	}
	return out, true, nil
}
