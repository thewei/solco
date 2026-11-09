package remotehost

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/thewei/solco/internal/feedback"
)

/**
 * [INPUT]: 依赖 Options.Feedback（本机 Feedback broker 的回环客户端，桌面与常驻服务各自接线）、internal/feedback 的 ReadSnapshot 与模型、server.go 的 guard/writeJSON/writeError
 * [OUTPUT]: 对外提供 FeedbackSource、GET /api/v3/feedback[?thread=]（scope view）、POST /api/v3/feedback/asks/{id}/answer 与 …/end（scope terminal-input）、bootstrap 的 capabilities.feedback
 * [POS]: 手机「Feedback」页（2026-11-09 决策）：与桌面 /feedback 页读到同一个 feedback.Snapshot；线程与 Ask 状态的唯一所有者是 broker，Host 只转发；broker 不在 = 200 online:false（页面画离线态）而非错误；日志只记设备与计数，从不记问题、答案、描述或图片
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	feedbackCallTimeout = 10 * time.Second
	maxFeedbackBody     = 12 << 20 // answers plus a few base64 images
)

// FeedbackSource is the broker as the Host uses it; *feedback.Client satisfies it. nil = no Feedback view and
// bootstrap does not advertise it.
type FeedbackSource interface {
	feedback.Source
	Answer(ctx context.Context, askID string, answers map[string]feedback.Answer, atts []feedback.Attachment) error
	End(ctx context.Context, askID string) error
}

// feedbackAnswer is the phone's POST body.
type feedbackAnswer struct {
	Answers     map[string]feedback.Answer `json:"answers"`
	Attachments []feedback.Attachment      `json:"attachments,omitempty"`
}

// handleFeedback answers GET /api/v3/feedback: the thread list and, for ?thread=, that thread's timeline.
func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request, dev Device) {
	src := s.opts.Feedback
	if src == nil {
		writeError(w, http.StatusNotFound, "feedback_unavailable", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), feedbackCallTimeout)
	defer cancel()
	snap, err := feedback.ReadSnapshot(ctx, src, r.URL.Query().Get("thread"))
	if err != nil {
		slog.Warn("remote host feedback read failed", "device", dev.ID, "error", err)
		writeError(w, http.StatusBadGateway, "feedback_failed", "")
		return
	}
	if snap.Detail != nil {
		// The phone never sees a Mac path: the project is its display name, and "open project" stays on the desktop.
		d := *snap.Detail
		d.ProjectKey, d.ProjectRoot, d.CallerInfo = "", "", ""
		snap.Detail = &d
	}
	slog.Info("remote host feedback", "device", dev.ID, "threads", len(snap.Threads), "waiting", snap.Waiting)
	writeJSON(w, http.StatusOK, snap)
}

// handleFeedbackAnswer answers POST /api/v3/feedback/asks/{id}/answer (a late answer to an expired Ask included).
func (s *Server) handleFeedbackAnswer(w http.ResponseWriter, r *http.Request, dev Device) {
	var req feedbackAnswer
	if s.opts.Feedback == nil {
		writeError(w, http.StatusNotFound, "feedback_unavailable", "")
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFeedbackBody)).Decode(&req); err != nil || len(req.Answers) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), feedbackCallTimeout)
	defer cancel()
	s.finishFeedback(w, dev, "answer", s.opts.Feedback.Answer(ctx, r.PathValue("id"), req.Answers, req.Attachments))
}

// handleFeedbackEnd answers POST /api/v3/feedback/asks/{id}/end: close an Ask without an answer.
func (s *Server) handleFeedbackEnd(w http.ResponseWriter, r *http.Request, dev Device) {
	if s.opts.Feedback == nil {
		writeError(w, http.StatusNotFound, "feedback_unavailable", "")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), feedbackCallTimeout)
	defer cancel()
	s.finishFeedback(w, dev, "end", s.opts.Feedback.End(ctx, r.PathValue("id")))
}

func (s *Server) finishFeedback(w http.ResponseWriter, dev Device, op string, err error) {
	switch {
	case err == nil:
		slog.Info("remote host feedback write", "device", dev.ID, "op", op)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, feedback.ErrNotFound):
		writeError(w, http.StatusNotFound, "feedback_not_found", "")
	case errors.Is(err, feedback.ErrFinal):
		writeError(w, http.StatusConflict, "feedback_final", "")
	case errors.Is(err, feedback.ErrInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", "")
	case errors.Is(err, feedback.ErrOffline):
		writeError(w, http.StatusServiceUnavailable, "feedback_offline", "")
	default:
		slog.Warn("remote host feedback write failed", "device", dev.ID, "op", op, "error", err)
		writeError(w, http.StatusBadGateway, "feedback_failed", "")
	}
}
