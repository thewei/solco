package remotehost

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

/**
 * [INPUT]: 依赖 Options.Sessions（桌面 Sessions 目录的只读数据缝，nativeui 提供）、server.go 的 guard/writeJSON/writeError
 * [OUTPUT]: 对外提供 SessionsQuery/SessionRow/SessionRunning/Sessions 与 GET /api/v3/sessions?q=&limit=、bootstrap 的 capabilities.sessions
 * [POS]: 手机「Sessions」只读视图（桌面 History 目录的元数据，不含对话正文）：搜索与“运行中”关联全部在缝里完成——
 *        运行中沿用桌面 Continue 的精确身份匹配（会话 id/path，绝不看 cwd/mtime），所以手机角标和桌面药丸永远一致；
 *        Host 只转发，不读会话文件、不启动任何东西；日志只记设备、条数与“是否带搜索词”，搜索词和标题永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	sessionsDefaultLimit = 50
	sessionsMaxLimit     = 100
	sessionsMaxQuery     = 100
)

// SessionsQuery is one list request: an optional search over titles and
// projects, and a bound.
type SessionsQuery struct {
	Search string
	Limit  int
}

// SessionRunning says an Agent in an open pane is running this session right
// now. State is the desktop's one status word: working|attention|review|idle.
type SessionRunning struct {
	Instance string `json:"instance"`
	PaneID   string `json:"paneId"`
	State    string `json:"state"`
}

// SessionRow is one catalog entry as the phone lists it: metadata only.
type SessionRow struct {
	ID        string          `json:"id"`
	Agent     string          `json:"agent"` // provider id (claude-code|codex|pi|omp|cursor|antigravity)
	Title     string          `json:"title"`
	Project   string          `json:"project,omitempty"`
	UpdatedMs int64           `json:"updatedMs,omitempty"`
	Messages  int64           `json:"messages,omitempty"`
	Running   *SessionRunning `json:"running,omitempty"`
}

// Sessions is the list answer, newest first, running sessions never dropped
// by the bound.
type Sessions struct {
	Sessions []SessionRow `json:"sessions"`
}

// handleSessions answers GET /api/v3/sessions.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request, dev Device) {
	if s.opts.Sessions == nil {
		writeError(w, http.StatusNotFound, "sessions_unavailable", "")
		return
	}
	query := SessionsQuery{Search: strings.TrimSpace(r.URL.Query().Get("q")), Limit: sessionsDefaultLimit}
	if len([]rune(query.Search)) > sessionsMaxQuery {
		writeError(w, http.StatusBadRequest, "bad_request", "q is too long")
		return
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be a positive integer")
			return
		}
		query.Limit = min(n, sessionsMaxLimit)
	}
	out, err := s.opts.Sessions(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "sessions_unavailable", "")
		return
	}
	if out.Sessions == nil {
		out.Sessions = []SessionRow{}
	}
	slog.Info("remote host sessions", "device", dev.ID, "count", len(out.Sessions), "search", query.Search != "")
	writeJSON(w, http.StatusOK, out)
}

// SessionsFunc is the Options.Sessions seam.
type SessionsFunc func(ctx context.Context, q SessionsQuery) (Sessions, error)
