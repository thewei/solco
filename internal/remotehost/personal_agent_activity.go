// [INPUT]: authenticated personal-agent Host routes and the paired Device identity
// [OUTPUT]: bounded replay of committed Assistant facts, explicit per-device ACK
// [POS]: Mobile transport for Solco's Activity ledger; never starts or impersonates OMP
// [PROTOCOL]: Update this header on changes, then check CLAUDE.md
package remotehost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/thewei/solco/internal/personalagent"
)

const maxPAActivityPage = 100

// Device identity, not a phone-supplied string, owns both cursors.
func paActivityConsumer(dev Device) string       { return "device:" + dev.ID }
func paActivityNoticeConsumer(dev Device) string { return paActivityConsumer(dev) + ":notifications" }

// handlePAActivity is read-only. The client may start at zero or at the
// last ackSeq, re-read duplicates, and advance page-by-page without gaps.
// GET never acknowledges an Activity item.
func (s *Server) handlePAActivity(w http.ResponseWriter, r *http.Request, dev Device) {
	s.handlePAActivityFor(w, r, dev, paActivityConsumer(dev))
}

// Notification delivery is separately acknowledged from a person's explicit
// "Mark seen": a background notice can never silently consume their inbox.
// A single core read starts after this notification consumer's persisted ACK.
func (s *Server) handlePAActivityNotices(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	query := r.URL.Query()
	if len(query) > 1 || (len(query) == 1 && !query.Has("limit")) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxPAActivityPage {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		limit = parsed
	}
	page, err := c.UnreadActivity(r.Context(), id, paActivityNoticeConsumer(dev), limit)
	if err != nil {
		s.paRefuse(w, dev, "activity.notifications", err)
		return
	}
	writePAActivityPage(w, page)
}

func (s *Server) handlePAActivityFor(w http.ResponseWriter, r *http.Request, dev Device, consumer string) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	after := int64(0)
	limit := 50
	if raw := r.URL.Query().Get("after"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		after = parsed
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxPAActivityPage {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		limit = parsed
	}
	page, err := c.Activity(r.Context(), id, consumer, after, limit)
	if err != nil {
		s.paRefuse(w, dev, "activity", err)
		return
	}
	writePAActivityPage(w, page)
}

func writePAActivityPage(w http.ResponseWriter, page personalagent.ActivityPage) {
	if page.Facts == nil {
		page.Facts = []personalagent.Fact{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema":  1,
		"facts":   page.Facts,
		"nextSeq": page.NextSeq,
		"ackSeq":  page.AckSeq,
		"hasMore": page.HasMore,
	})
}

// The phone explicitly acknowledges after processing the facts. If the
// connection drops after commit but before a response, retry exactly the
// same requestId; the plugin returns the original receipt.
func (s *Server) handlePAActivityAck(w http.ResponseWriter, r *http.Request, dev Device) {
	s.handlePAActivityAckFor(w, r, dev, paActivityConsumer(dev))
}

func (s *Server) handlePAActivityNoticesAck(w http.ResponseWriter, r *http.Request, dev Device) {
	s.handlePAActivityAckFor(w, r, dev, paActivityNoticeConsumer(dev))
}

func (s *Server) handlePAActivityAckFor(w http.ResponseWriter, r *http.Request, dev Device, consumer string) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var body struct {
		RequestID string `json:"requestId"`
		Seq       int64  `json:"seq"`
	}
	// Reject unknown keys (including a forged consumer) instead of silently
	// ignoring them; consumer ownership comes exclusively from the Host.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPABody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if body.RequestID == "" || len(body.RequestID) > maxPARequestID || !paText(body.RequestID, maxPARequestID) || strings.ContainsAny(body.RequestID, "\r\n") {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if body.Seq < 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "activity.ack", func(ctx context.Context, c *personalagent.Client) (any, error) {
		seq, err := c.AckActivity(ctx, body.RequestID, id, consumer, body.Seq)
		return map[string]any{"schema": 1, "ackSeq": seq}, err
	})
}
