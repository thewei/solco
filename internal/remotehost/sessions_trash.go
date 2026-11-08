package remotehost

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

/**
 * [INPUT]: 依赖 Options.Trasher（桌面 Sessions Trash 的唯一外部缝，nativeui 提供，背后是 internal/sessiontrash）、server.go 的 guard/writeJSON/writeError
 * [OUTPUT]: 对外提供 Trasher/TrashEntry/TrashError 与 POST /api/v3/sessions/{id}/trash、GET /api/v3/sessions/trash、POST /api/v3/sessions/trash/{entry}/restore、bootstrap 的 capabilities.sessionTrash
 * [POS]: 手机 Sessions 的回收站只开放“可逆的两步”：移入与放回；永久删除/清空仍只在桌面（带确认框），所以手机上的任何操作都能被撤销。
 *        搬文件、拒绝规则（运行中/刚写入、路径必须在 Provider 根内、不覆盖）全部在 Trasher 背后，与桌面同一个 sessiontrash.Store；Host 只转发，不读会话文件；
 *        日志只记设备与结果码，会话 id、条目标题永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// TrashEntry is one Trash row as the phone lists it: metadata only. ID names
// the Trash entry (what restore takes), not the session it came from.
type TrashEntry struct {
	ID        string `json:"id"`
	Agent     string `json:"agent"`
	Title     string `json:"title"`
	Project   string `json:"project,omitempty"`
	DeletedMs int64  `json:"deletedMs,omitempty"`
	Messages  int64  `json:"messages,omitempty"`
}

// TrashError is a refusal with a stable code: unknown_session (404),
// session_in_use (409: an Agent runs it or it was written to a moment ago),
// session_exists (409: restore would overwrite a file), unknown_entry (404),
// trash_failed (502).
type TrashError struct {
	Code   string
	Status int
	Detail string
}

func (e *TrashError) Error() string { return e.Code + ": " + e.Detail }

// Trasher is the Options.Trasher seam over the desktop's Sessions Trash.
type Trasher interface {
	// Trash moves one cataloged session into the Trash and names the entry.
	Trash(ctx context.Context, sessionID string) (entryID string, err error)
	// List is the Trash's entries, newest deletion first.
	List(ctx context.Context) ([]TrashEntry, error)
	// Restore puts one entry back where it came from.
	Restore(ctx context.Context, entryID string) error
}

type trashList struct {
	Entries []TrashEntry `json:"entries"`
}

type trashAnswer struct {
	Entry string `json:"entry"`
}

func (s *Server) trashSeam(w http.ResponseWriter) Trasher {
	if s.opts.Trasher == nil {
		writeError(w, http.StatusNotFound, "trash_unavailable", "")
		return nil
	}
	return s.opts.Trasher
}

// refuse answers a Trasher failure with its own code, or a generic 502.
func (s *Server) refuseTrash(w http.ResponseWriter, dev Device, op string, err error) {
	var te *TrashError
	if errors.As(err, &te) {
		slog.Warn("remote host trash refused", "device", dev.ID, "op", op, "code", te.Code)
		writeError(w, te.Status, te.Code, truncate(te.Detail, 200))
		return
	}
	slog.Warn("remote host trash failed", "device", dev.ID, "op", op)
	writeError(w, http.StatusBadGateway, "trash_failed", "")
}

// handleSessionTrash answers POST /api/v3/sessions/{id}/trash.
func (s *Server) handleSessionTrash(w http.ResponseWriter, r *http.Request, dev Device) {
	trasher := s.trashSeam(w)
	if trasher == nil {
		return
	}
	id := r.PathValue("id")
	if id == "" || len(id) > 256 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	entry, err := trasher.Trash(context.WithoutCancel(r.Context()), id)
	if err != nil {
		s.refuseTrash(w, dev, "trash", err)
		return
	}
	slog.Info("remote host session trashed", "device", dev.ID)
	writeJSON(w, http.StatusOK, trashAnswer{Entry: entry})
}

// handleTrashList answers GET /api/v3/sessions/trash.
func (s *Server) handleTrashList(w http.ResponseWriter, r *http.Request, dev Device) {
	trasher := s.trashSeam(w)
	if trasher == nil {
		return
	}
	entries, err := trasher.List(r.Context())
	if err != nil {
		s.refuseTrash(w, dev, "list", err)
		return
	}
	if entries == nil {
		entries = []TrashEntry{}
	}
	slog.Info("remote host trash list", "device", dev.ID, "count", len(entries))
	writeJSON(w, http.StatusOK, trashList{Entries: entries})
}

// handleTrashRestore answers POST /api/v3/sessions/trash/{entry}/restore.
func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request, dev Device) {
	trasher := s.trashSeam(w)
	if trasher == nil {
		return
	}
	entry := r.PathValue("entry")
	if entry == "" || len(entry) > 128 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if err := trasher.Restore(context.WithoutCancel(r.Context()), entry); err != nil {
		s.refuseTrash(w, dev, "restore", err)
		return
	}
	slog.Info("remote host session put back", "device", dev.ID)
	writeJSON(w, http.StatusOK, trashAnswer{Entry: entry})
}
