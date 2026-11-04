// [INPUT]: Solco Assistant's immutable Fact ledger and one consumer's acknowledged sequence
// [OUTPUT]: Bounded oldest-first Activity page and explicit monotonic ACK without side effects on read
// [POS]: Per-device replayable progress activity. It is a system fact feed, never an OMP transcript.
// [PROTOCOL]: Update this header on changes, then check CLAUDE.md
package personalagent

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
)

const ActivityPageLimit = 50
const ActivityMaxPageLimit = 200

// Consumers are stable, non-secret opaque keys. Host-provided device IDs
// use the "device:" prefix; the Host must never accept one from the phone.
var activityConsumerRE = regexp.MustCompile(`^[a-zA-Z0-9_:-]{1,100}$`)

// ActivityPage is read-only and paged oldest-first. Replaying after the
// acknowledged cursor is safe: every item has a stable Fact ID and seq.
// HasMore means the next page exists; no client should infer completion or
// "delivered" merely because an HTTP request returned 200.
type ActivityPage struct {
	Facts   []Fact `json:"facts"`
	NextSeq int64  `json:"nextSeq"`
	AckSeq  int64  `json:"ackSeq"`
	HasMore bool   `json:"hasMore"`
}

func checkActivityConsumer(consumer string) error {
	if !activityConsumerRE.MatchString(consumer) {
		return fmt.Errorf("%w: invalid Activity consumer", ErrInvalid)
	}
	return nil
}

func (s *Store) Activity(assistantID, consumer string, after int64, limit int) (ActivityPage, error) {
	if after < 0 {
		return ActivityPage{}, fmt.Errorf("%w: activity cursor out of range", ErrInvalid)
	}
	return s.activityPage(assistantID, consumer, after, limit)
}

// UnreadActivity is one bounded read starting after this consumer's durable
// ACK. It avoids a second CLI invocation just to discover the cursor; unlike
// an inbox fetch, it still NEVER advances that cursor.
func (s *Store) UnreadActivity(assistantID, consumer string, limit int) (ActivityPage, error) {
	return s.activityPage(assistantID, consumer, -1, limit)
}

func (s *Store) activityPage(assistantID, consumer string, after int64, limit int) (ActivityPage, error) {
	page := ActivityPage{Facts: []Fact{}}
	if err := checkActivityConsumer(consumer); err != nil {
		return ActivityPage{}, err
	}
	if after < -1 || limit < 0 || limit > ActivityMaxPageLimit {
		return ActivityPage{}, fmt.Errorf("%w: activity cursor or limit out of range", ErrInvalid)
	}
	if limit == 0 {
		limit = ActivityPageLimit
	}
	if _, err := getAssistantTx(s.db, assistantID); err != nil {
		return ActivityPage{}, err
	}
	if err := s.db.QueryRow("SELECT COALESCE((SELECT ack_seq FROM activity_cursors WHERE assistant_id=? AND consumer=?),0)", assistantID, consumer).Scan(&page.AckSeq); err != nil {
		return ActivityPage{}, wrapBusy(err)
	}
	if after == -1 {
		after = page.AckSeq
	}
	page.NextSeq = after
	rows, err := s.db.Query(`SELECT seq,id,assistant_id,goal_id,task_id,kind,text,evidence,created_at
         FROM goal_facts WHERE assistant_id=? AND seq>? ORDER BY seq ASC LIMIT ?`, assistantID, after, limit+1)
	if err != nil {
		return ActivityPage{}, wrapBusy(err)
	}
	defer rows.Close()
	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.Seq, &f.ID, &f.AssistantID, &f.GoalID, &f.TaskID, &f.Kind, &f.Text, &f.Evidence, &f.CreatedAt); err != nil {
			return ActivityPage{}, err
		}
		if len(page.Facts) == limit {
			page.HasMore = true
			break
		}
		page.Facts = append(page.Facts, f)
		page.NextSeq = f.Seq
	}
	if err := rows.Err(); err != nil {
		return ActivityPage{}, wrapBusy(err)
	}
	return page, nil
}

// AckActivity marks the latest fact the caller really processed. Reads do
// not ACK. The seq must name a real fact of this Assistant (not one from
// another Assistant), never a guessed future watermark. The consumer's
// cursor can only increase, so out-of-order ACKs and network retries are safe.
func (s *Store) AckActivity(requestID, assistantID, consumer string, seq int64) (int64, bool, error) {
	if err := checkActivityConsumer(consumer); err != nil {
		return 0, false, err
	}
	if seq < 0 {
		return 0, false, fmt.Errorf("%w: invalid Activity sequence", ErrInvalid)
	}
	// Include the owning Assistant ID in the receipt result, so permanent
	// deletion can scrub this receipt along with Goal/Task results. A bare
	// numeric ACK would otherwise be orphaned in the idempotency ledger.
	type ackReceipt struct {
		AssistantID string `json:"assistantId"`
		AckSeq      int64  `json:"ackSeq"`
	}
	result, replayed, err := execute(s, requestID, "activity.ack", map[string]any{"assistantId": assistantID, "consumer": consumer, "seq": seq}, func(tx *sql.Tx) (ackReceipt, error) {
		if _, err := getAssistantTx(tx, assistantID); err != nil {
			return ackReceipt{}, err
		}
		var previous int64
		if err := tx.QueryRow("SELECT COALESCE((SELECT ack_seq FROM activity_cursors WHERE assistant_id=? AND consumer=?),0)", assistantID, consumer).Scan(&previous); err != nil {
			return ackReceipt{}, err
		}
		if seq <= previous {
			return ackReceipt{assistantID, previous}, nil
		}
		var present int
		if err := tx.QueryRow("SELECT 1 FROM goal_facts WHERE assistant_id=? AND seq=?", assistantID, seq).Scan(&present); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ackReceipt{}, fmt.Errorf("%w: Activity sequence does not belong to this Assistant", ErrInvalid)
			}
			return ackReceipt{}, err
		}
		if _, err := tx.Exec(`INSERT INTO activity_cursors(assistant_id,consumer,ack_seq,updated_at) VALUES(?,?,?,?)
                ON CONFLICT(assistant_id,consumer) DO UPDATE SET ack_seq=excluded.ack_seq,updated_at=excluded.updated_at`,
			assistantID, consumer, seq, s.nowMS()); err != nil {
			return ackReceipt{}, err
		}
		return ackReceipt{assistantID, seq}, nil
	})
	return result.AckSeq, replayed, err
}
