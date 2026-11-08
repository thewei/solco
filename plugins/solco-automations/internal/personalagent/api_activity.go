// [INPUT]: assistant activity list/ack commands, sequence and consumer
// [OUTPUT]: Explicit cursor-based replay with bounded pages and durable, idempotent ACK
// [POS]: Assistant CLI reads/writes to the existing fact ledger; never injects OMP messages
// [PROTOCOL]: Update this header on changes, then check CLAUDE.md
package personalagent

import (
	"strconv"
)

func (a *API) activity(args []string, body []byte) (any, error) {
	if len(args) < 2 {
		return nil, coded("invalid", "usage: assistant activity <list|ack> <assistantId>")
	}
	op, id := args[0], args[1]
	if len(args) > 2 && op == "ack" {
		return nil, coded("invalid", "activity ack takes no flags")
	}
	switch op {
	case "list":
		afterValue, rest := flagValue(args[2:], "--after")
		limitValue, rest := flagValue(rest, "--limit")
		consumer, rest := flagValue(rest, "--consumer")
		unread := false
		for i, v := range rest {
			if v == "--unread" {
				unread = true
				rest = append(rest[:i:i], rest[i+1:]...)
				break
			}
		}
		if len(rest) != 0 || consumer == "" || (unread && afterValue != "") {
			return nil, coded("invalid", "activity list needs --consumer and either --after or --unread")
		}
		after := int64(0)
		limit := 0
		if afterValue != "" {
			n, err := strconv.ParseInt(afterValue, 10, 64)
			if err != nil || n < 0 {
				return nil, coded("invalid", "--after must be a nonnegative sequence")
			}
			after = n
		}
		if limitValue != "" {
			n, err := strconv.Atoi(limitValue)
			if err != nil || n <= 0 || n > ActivityMaxPageLimit {
				return nil, coded("invalid", "--limit must be 1..200")
			}
			limit = n
		}
		if unread {
			page, err := a.Store.UnreadActivity(id, consumer, limit)
			return ok("activity", page), err
		}
		page, err := a.Store.Activity(id, consumer, after, limit)
		return ok("activity", page), err
	case "ack":
		var in struct {
			RequestID string `json:"requestId"`
			Consumer  string `json:"consumer"`
			Seq       int64  `json:"seq"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		if in.RequestID == "" {
			return nil, coded("invalid", "requestId is required")
		}
		seq, replayed, err := a.Store.AckActivity(in.RequestID, id, in.Consumer, in.Seq)
		return ok("ackSeq", seq, "replayed", replayed), err
	default:
		return nil, coded("invalid", "unknown activity operation %q", op)
	}
}
