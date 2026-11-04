package personalagent

import (
	"errors"
	"strconv"
	"time"
)

// GroupPump turns queued deliveries into real Agent work. It is deterministic:
// one pass reads the queue, starts the Group Agents targeted by each message
// (all members for a broadcast, explicitly mentioned members otherwise),
// and hands each running Agent the messages it has not seen. A quiet Group
// costs nothing.
type GroupPump struct {
	Store *Store
	// Submit hands text to the Agent in a pane (production: Herdr `agent
	// prompt`). A refusal such as busy/not-ready is simply retried later.
	Submit func(session, paneID, text string) error
	// Blocked reads the pane and says whether a startup dialog (a trust or MCP
	// question) is on screen. Text is never typed into a dialog: the delivery
	// waits for the person to answer it.
	Blocked func(session, paneID string) (what string, blocked bool)
	// DefaultAgent is the Herdr kind a Group Agent runs when the Assistant has
	// no preference of its own.
	DefaultAgent string
	// MaxAttempts bounds retries of one delivery (default 8); StartWait bounds
	// how long a Group Agent may take to finish its first turn (default 10 minutes).
	MaxAttempts int
	StartWait   time.Duration
	// Now is a test seam.
	Now func() time.Time
}

// maxGroupAgentStarts is how many times one delivery may ask for a Group Agent
// before it gives up: every start is a real Herdr Agent.
const maxGroupAgentStarts = 2

// GroupPumpReport counts what one pass did.
type GroupPumpReport struct {
	Started   int
	Delivered int
	Failed    int
}

func (p *GroupPump) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *GroupPump) maxAttempts() int {
	if p.MaxAttempts > 0 {
		return p.MaxAttempts
	}
	return 8
}

func (p *GroupPump) startWait() time.Duration {
	if p.StartWait > 0 {
		return p.StartWait
	}
	return 10 * time.Minute
}

type openDelivery struct {
	GroupDelivery
	groupID   string
	createdAt int64
	seq       int64
}

func (s *Store) openDeliveries(now int64, limit int) ([]openDelivery, error) {
	rows, err := s.db.Query(`SELECT d.id,d.message_id,d.assistant_id,d.state,d.attempts,d.group_id,d.created_at,m.seq
		FROM group_deliveries d JOIN group_messages m ON m.id=d.message_id
		WHERE d.state=? AND d.next_at<=? ORDER BY m.seq LIMIT ?`, DeliveryQueued, now, limit)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []openDelivery{}
	for rows.Next() {
		var d openDelivery
		if err := rows.Scan(&d.ID, &d.MessageID, &d.AssistantID, &d.State, &d.Attempts, &d.groupID, &d.createdAt, &d.seq); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, wrapBusy(rows.Err())
}

// settleDelivery moves a delivery to a state and, in the same transaction,
// writes the receipt the Group shows. event "" writes no receipt.
func (s *Store) settleDelivery(d openDelivery, state, event, lastError string, nextAt int64, bump bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return wrapBusy(err)
	}
	defer func() { _ = tx.Rollback() }()
	now := s.nowMS()
	attempts := 0
	if bump {
		attempts = 1
	}
	res, err := tx.Exec(`UPDATE group_deliveries SET state=?, attempts=attempts+?, next_at=?, last_error=?, updated_at=? WHERE id=? AND state IN (?,?)`,
		state, attempts, nextAt, lastError, now, d.ID, DeliveryQueued, DeliveryDelivering)
	if err != nil {
		return wrapBusy(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // somebody else settled it (a member was removed, a replay)
	}
	if event != "" {
		if err := insertFactKeyed(tx, d.groupID, event, d.AssistantID, d.ID, "fact_"+d.ID+"_"+event, now); err != nil {
			return wrapBusy(err)
		}
	}
	return wrapBusy(tx.Commit())
}

// claimDelivery marks a delivery `delivering` before anything is sent: a crash
// after this point leaves it `uncertain`, never silently re-sent.
func (s *Store) claimDelivery(id string) (bool, error) {
	res, err := s.db.Exec(`UPDATE group_deliveries SET state=?, updated_at=? WHERE id=? AND state=?`, DeliveryDelivering, s.nowMS(), id, DeliveryQueued)
	if err != nil {
		return false, wrapBusy(err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// markGroupAgentBlocked records that a live Agent sits on a dialog and tells the
// Group once.
func (s *Store) markGroupAgentBlocked(ga GroupAgent, what, key string) {
	res, err := s.db.Exec(`UPDATE group_agents SET state=?, revision=revision+1, updated_at=? WHERE group_id=? AND assistant_id=? AND state<>?`,
		GroupAgentBlocked, s.nowMS(), ga.GroupID, ga.AssistantID, GroupAgentBlocked)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 1 {
		s.groupFact(ga.GroupID, "agent_blocked", ga.AssistantID, what, "fact_blocked_"+key, s.nowMS())
	}
}

func (s *Store) setGroupAgentSeq(groupID, assistantID string, seq int64) {
	_, _ = s.db.Exec(`UPDATE group_agents SET last_seq=? WHERE group_id=? AND assistant_id=? AND last_seq<?`, seq, groupID, assistantID, seq)
}

// RecoverGroupDeliveries settles what a crash left claimed: the outcome is
// unknown, so each such delivery becomes `uncertain` with a visible receipt and
// the person decides whether to @ again.
func (s *Store) RecoverGroupDeliveries() (int, error) {
	rows, err := s.db.Query(`SELECT d.id,d.message_id,d.assistant_id,d.state,d.attempts,d.group_id,d.created_at,m.seq
		FROM group_deliveries d JOIN group_messages m ON m.id=d.message_id WHERE d.state=?`, DeliveryDelivering)
	if err != nil {
		return 0, wrapBusy(err)
	}
	var ds []openDelivery
	for rows.Next() {
		var d openDelivery
		if err := rows.Scan(&d.ID, &d.MessageID, &d.AssistantID, &d.State, &d.Attempts, &d.groupID, &d.createdAt, &d.seq); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ds = append(ds, d)
	}
	_ = rows.Close()
	for _, d := range ds {
		if err := s.settleDelivery(d, DeliveryUncertain, "delivery_uncertain", "interrupted", 0, false); err != nil {
			return 0, err
		}
	}
	return len(ds), nil
}

// Tick runs one pass.
func (p *GroupPump) Tick() (GroupPumpReport, error) {
	var rep GroupPumpReport
	s := p.Store
	now := p.now()
	list, err := s.openDeliveries(now.UnixMilli(), 20)
	if err != nil {
		return rep, err
	}
	for _, d := range list {
		fail := func(event, why string) {
			if err := s.settleDelivery(d, DeliveryFailed, event, why, 0, true); err == nil {
				rep.Failed++
			}
		}
		g, err := s.GetGroup(d.groupID)
		if err != nil {
			return rep, err
		}
		member := false
		for _, m := range g.Members {
			member = member || m.AssistantID == d.AssistantID
		}
		a, aerr := s.GetAssistant(d.AssistantID)
		if !member || aerr != nil || a.Status != StatusActive {
			_ = s.settleDelivery(d, DeliverySkipped, "delivery_skipped", "not deliverable", 0, false)
			continue
		}
		ga, err := s.GetGroupAgent(d.groupID, d.AssistantID)
		if err != nil {
			return rep, err
		}
		retryAt := now.Add(2 * time.Second).UnixMilli()
		switch ga.State {
		case GroupAgentNone, GroupAgentStopped, GroupAgentFailed:
			if d.Attempts >= maxGroupAgentStarts {
				fail("delivery_failed", "agent would not start")
				continue
			}
			agent := p.DefaultAgent
			if _, _, err := s.StartGroupAgent("grp-start-"+d.ID+"-"+strconv.Itoa(d.Attempts), d.groupID, d.AssistantID, agent, ""); err != nil {
				fail("delivery_failed", err.Error())
				continue
			}
			// The Agent coming up is the receipt; the delivery waits for it.
			_ = s.settleDelivery(d, DeliveryQueued, "agent_starting", "", retryAt, true)
			rep.Started++
		case GroupAgentStarting:
			if time.UnixMilli(ga.UpdatedAt).Add(p.startWait()).Before(now) {
				fail("delivery_failed", "agent did not come up in time")
				continue
			}
			_ = s.settleDelivery(d, DeliveryQueued, "", "", retryAt, false)
		case GroupAgentReady, GroupAgentBlocked:
			// A blocked Agent is tried too: if the person answered its dialog the
			// prompt is accepted and the Agent is ready again; if not, Herdr
			// refuses and the delivery waits, then fails with a visible receipt.
			if ga.PaneID == "" {
				_ = s.settleDelivery(d, DeliveryQueued, "", "", retryAt, false)
				continue
			}
			if d.seq <= ga.LastSeq {
				// The Agent's launch prompt already carried this message.
				_ = s.settleDelivery(d, DeliveryDelivered, "delivered", "", 0, false)
				rep.Delivered++
				continue
			}
			if p.Blocked != nil {
				if what, hit := p.Blocked(ga.HerdrSession, ga.PaneID); hit {
					if ga.State != GroupAgentBlocked {
						s.markGroupAgentBlocked(ga, what, d.ID)
					}
					if time.UnixMilli(d.createdAt).Add(p.startWait()).Before(now) {
						fail("delivery_failed", "the agent was waiting for you in its terminal")
						continue
					}
					_ = s.settleDelivery(d, DeliveryQueued, "", "", retryAt, false)
					continue
				}
			}
			text, last, err := s.groupDeliveryPrompt(ga, d.MessageID)
			if errors.Is(err, ErrNotFound) {
				_ = s.settleDelivery(d, DeliveryDelivered, "delivered", "", 0, false)
				rep.Delivered++
				continue
			}
			if err != nil {
				return rep, err
			}
			if ok, err := s.claimDelivery(d.ID); err != nil || !ok {
				continue
			}
			if p.Submit == nil {
				_ = s.settleDelivery(d, DeliveryQueued, "", "no submitter", retryAt, false)
				continue
			}
			if err := p.Submit(ga.HerdrSession, ga.PaneID, text); err != nil {
				// Busy / not ready / stalled: Herdr refused or could not confirm;
				// the text may or may not have been typed. A stalled prompt is
				// inconclusive, so it is never retried blindly: it is only retried
				// when Herdr said it did nothing.
				if submitRefused(err) {
					if d.Attempts+1 >= p.maxAttempts() {
						fail("delivery_failed", err.Error())
						continue
					}
					backoff := time.Duration(d.Attempts+1) * 5 * time.Second
					_ = s.settleDelivery(d, DeliveryQueued, "", err.Error(), now.Add(backoff).UnixMilli(), true)
					continue
				}
				_ = s.settleDelivery(d, DeliveryUncertain, "delivery_uncertain", err.Error(), 0, true)
				continue
			}
			s.setGroupAgentSeq(d.groupID, d.AssistantID, last)
			if ga.State == GroupAgentBlocked {
				_, _ = s.db.Exec(`UPDATE group_agents SET state=?, updated_at=? WHERE group_id=? AND assistant_id=? AND state=?`, GroupAgentReady, s.nowMS(), d.groupID, d.AssistantID, GroupAgentBlocked)
			}
			_ = s.settleDelivery(d, DeliveryDelivered, "delivered", "", 0, false)
			rep.Delivered++
		}
	}
	return rep, nil
}

// submitRefused reports whether a Submit error is Herdr declining before it
// typed anything (so the same text can safely be offered again).
var submitRefused = func(err error) bool { return errors.Is(err, ErrSubmitRefused) }

// ErrSubmitRefused marks a Submit failure after which nothing was typed.
var ErrSubmitRefused = errors.New("the agent could not take the message now")
