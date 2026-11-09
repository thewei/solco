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
 * [OUTPUT]: 对外提供 FeedbackSource、GET /api/v3/feedback[?thread=&q=]（scope view）、POST /api/v3/feedback/asks/{id}/answer 与 …/end、POST …/feedback/threads/delete 与 …/cleanup 与 …/retention（scope terminal-input；源还实现 feedback.Manager 时才有，capabilities.feedbackManage）、GET …/feedback/asks/{id}/thumbnails（scope view；已答 Ask 的图片缩略图，源须实现 feedback.Manager，无 capability 位：旧 Host 404，页面画占位）、GET …/feedback/context?thread=&kind=files|skills&q=（scope view；picker 候选，capabilities.feedbackContext；回答体可带 context 引用，手机永远看不到 Mac 绝对路径）、bootstrap 的 capabilities.feedback
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
	Answer(ctx context.Context, askID string, answers map[string]feedback.Answer, atts []feedback.Attachment, refs []feedback.ContextRef) error
	End(ctx context.Context, askID string) error
}

// feedbackAnswer is the phone's POST body.
type feedbackAnswer struct {
	Answers     map[string]feedback.Answer `json:"answers"`
	Attachments []feedback.Attachment      `json:"attachments,omitempty"`
	// Context are files, folders and Skills the user points the Agent at (kind + name; the broker resolves the paths).
	Context []feedback.ContextRef `json:"context,omitempty"`
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
	snap, err := feedback.ReadSnapshotQuery(ctx, src, r.URL.Query().Get("thread"), r.URL.Query().Get("q"))
	if err != nil {
		slog.Warn("remote host feedback read failed", "device", dev.ID, "error", err)
		writeError(w, http.StatusBadGateway, "feedback_failed", "")
		return
	}
	if snap.Detail != nil {
		s.markFeedbackSeen(snap.Detail.ID)
		// The phone never sees a Mac path: the project is its display name, and "open project" stays on the desktop.
		d := *snap.Detail
		d.ProjectKey, d.ProjectRoot, d.CallerInfo = "", "", ""
		d.Asks = stripContextPaths(d.Asks)
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
	s.finishFeedback(w, dev, "answer", s.opts.Feedback.Answer(ctx, r.PathValue("id"), req.Answers, req.Attachments, req.Context))
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

// stripContextPaths copies the asks without the absolute path of each context reference: a phone sees what the user
// picked (kind + name), never where it lives on the Mac.
func stripContextPaths(asks []*feedback.Ask) []*feedback.Ask {
	out := make([]*feedback.Ask, len(asks))
	for i, a := range asks {
		c := *a
		if len(a.Context) > 0 {
			c.Context = make([]feedback.ContextRef, len(a.Context))
			for j, ref := range a.Context {
				c.Context[j] = feedback.ContextRef{Kind: ref.Kind, Name: ref.Name}
			}
		}
		out[i] = &c
	}
	return out
}

// handleFeedbackContext answers GET /api/v3/feedback/context?thread=&kind=files|skills&q=: the picker's rows for the
// thread's project (names only, relative paths; needs the manager side, capabilities.feedbackContext).
func (s *Server) handleFeedbackContext(w http.ResponseWriter, r *http.Request, dev Device) {
	m := s.feedbackManager(w)
	if m == nil {
		return
	}
	q := r.URL.Query()
	if kind := q.Get("kind"); q.Get("thread") == "" || (kind != "files" && kind != "skills") {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), feedbackCallTimeout)
	defer cancel()
	items, err := m.Pick(ctx, q.Get("thread"), q.Get("kind"), q.Get("q"))
	if err != nil {
		s.finishFeedback(w, dev, "context", err)
		return
	}
	if items == nil {
		items = []feedback.PickItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleFeedbackThumbnails answers GET /api/v3/feedback/asks/{id}/thumbnails: the answered Ask's attached images as
// small JPEGs (a few KB each, never the 4 MiB originals), in attach order; a slot that could not be shrunk is empty.
func (s *Server) handleFeedbackThumbnails(w http.ResponseWriter, r *http.Request, dev Device) {
	m := s.feedbackManager(w)
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), feedbackCallTimeout)
	defer cancel()
	thumbs, err := m.Thumbnails(ctx, r.PathValue("id"))
	if err != nil {
		s.finishFeedback(w, dev, "thumbnails", err)
		return
	}
	if thumbs == nil {
		thumbs = []feedback.Attachment{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"thumbnails": thumbs})
}

// feedbackManageable says the source can also tidy the inbox (bootstrap's feedbackManage).
func (s *Server) feedbackManageable() bool {
	_, ok := s.opts.Feedback.(feedback.Manager)
	return ok
}

// feedbackManager is the broker's tidy-up side; nil when the source cannot (the routes then answer 404).
func (s *Server) feedbackManager(w http.ResponseWriter) feedback.Manager {
	if m, ok := s.opts.Feedback.(feedback.Manager); ok && s.opts.Feedback != nil {
		return m
	}
	writeError(w, http.StatusNotFound, "feedback_unavailable", "")
	return nil
}

// handleFeedbackDelete answers POST /api/v3/feedback/threads/delete: remove finished threads (a waiting one stays).
func (s *Server) handleFeedbackDelete(w http.ResponseWriter, r *http.Request, dev Device) {
	m := s.feedbackManager(w)
	if m == nil {
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || len(req.IDs) == 0 || len(req.IDs) > 500 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), feedbackCallTimeout)
	defer cancel()
	n, err := m.DeleteThreads(ctx, req.IDs)
	if err == nil {
		slog.Info("remote host feedback write", "device", dev.ID, "op", "delete", "threads", n)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": n})
		return
	}
	s.finishFeedback(w, dev, "delete", err)
}

// handleFeedbackCleanup answers POST /api/v3/feedback/threads/cleanup: count (dryRun) or remove the finished threads
// idle for more than olderThanDays.
func (s *Server) handleFeedbackCleanup(w http.ResponseWriter, r *http.Request, dev Device) {
	m := s.feedbackManager(w)
	if m == nil {
		return
	}
	var req struct {
		OlderThanDays int  `json:"olderThanDays"`
		DryRun        bool `json:"dryRun"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil || req.OlderThanDays < 1 || req.OlderThanDays > 3650 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), feedbackCallTimeout)
	defer cancel()
	n, err := m.Purge(ctx, req.OlderThanDays, req.DryRun)
	if err == nil {
		deleted := n
		if req.DryRun {
			deleted = 0
		}
		slog.Info("remote host feedback write", "device", dev.ID, "op", "cleanup", "matched", n, "dryRun", req.DryRun)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "matched": n, "deleted": deleted})
		return
	}
	s.finishFeedback(w, dev, "cleanup", err)
}

// handleFeedbackRetention answers POST /api/v3/feedback/retention: how many days finished threads are kept (0 = forever).
func (s *Server) handleFeedbackRetention(w http.ResponseWriter, r *http.Request, dev Device) {
	m := s.feedbackManager(w)
	if m == nil {
		return
	}
	var req struct {
		Days *int `json:"days"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil || req.Days == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), feedbackCallTimeout)
	defer cancel()
	s.finishFeedback(w, dev, "retention", m.SetRetention(ctx, *req.Days))
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
