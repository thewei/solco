// [INPUT]: authenticated mobile Assistant and a person's explicit review of an uncertain OMP report delivery
// [OUTPUT]: read-only report delivery status and request-id-fenced skip/retry of one exact fact watermark
// [POS]: Host transport only; the plugin's SQLite and original Herdr Coordinator remain authoritative
// [PROTOCOL]: Update this header on changes, then check CLAUDE.md
package remotehost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/thewei/solco/internal/personalagent"
)

func (s *Server) handlePAReport(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	report, err := c.CoordinatorReport(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "coordinator.report", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "report": report})
}

func (s *Server) handlePAReportResolve(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var in struct {
		RequestID  string `json:"requestId"`
		ThroughSeq int64  `json:"throughSeq"`
		Action     string `json:"action"`
		Confirmed  bool   `json:"confirmed"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPABody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if in.RequestID == "" || len(in.RequestID) > maxPARequestID || !paText(in.RequestID, maxPARequestID) ||
		in.ThroughSeq <= 0 || !in.Confirmed || (in.Action != "skip" && in.Action != "retry") {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "coordinator.report.resolve", func(ctx context.Context, c *personalagent.Client) (any, error) {
		report, err := c.ResolveCoordinatorReport(ctx, in.RequestID, id, in.ThroughSeq, in.Action, in.Confirmed)
		return map[string]any{"schema": 1, "report": report}, err
	})
}
