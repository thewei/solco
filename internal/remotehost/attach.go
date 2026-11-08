package remotehost

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/thewei/solco/internal/chatattach"
)

/**
 * [INPUT]: 依赖 Options.Attach（App 注入的落盘函数，即 internal/chatattach）、Backend.Projection（确认窗格存在）、server.go 的 guard/resolveInstance
 * [OUTPUT]: 对外提供 POST /api/v3/panes/{paneID}/chat/attachments（scope terminal-input）：手机把附件以 base64 上传，Host 存到 Mac 的 ChatAttachments 缓存目录并回绝对路径，页面把它作为 `@path` 发给 Agent
 * [POS]: 与桌面 ChatBridge.SaveAttachment 同一存储、同一上限（14 天 / 200 MB / 单文件 32 MB）；Host 只落盘，不执行、不解析内容；Attach 为 nil 时 bootstrap 不声明 `attachments` 能力、该路由回 404 attach_unavailable；文件名/内容/路径不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// attachBodyLimit bounds one upload: a 32 MB file as base64 plus the JSON envelope.
const attachBodyLimit = (chatattach.MaxFile/3+1)*4 + 8<<10

type attachRequest struct {
	DataBase64 string `json:"dataBase64"`
	Mime       string `json:"mime,omitempty"`
	Name       string `json:"name,omitempty"`
}

type attachReply struct {
	Path string `json:"path"`
}

func (s *Server) handleChatAttach(w http.ResponseWriter, r *http.Request, _ Device) {
	if s.opts.Attach == nil {
		writeError(w, http.StatusNotFound, "attach_unavailable", "")
		return
	}
	var req attachRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, attachBodyLimit)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	instance, ok := s.resolveInstance(w, r)
	if !ok {
		return
	}
	paneID := r.PathValue("paneID")
	proj, err := s.opts.Backend.Projection(instance)
	if err != nil {
		slog.Warn("remote host attach: projection failed", "instance", instance, "error", err)
		writeError(w, http.StatusBadGateway, "backend_unavailable", "")
		return
	}
	found := false
	for _, p := range proj.Panes {
		if p.ID == paneID {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "unknown_pane", "")
		return
	}
	s.saveAttachment(w, req)
}

// saveAttachment decodes, bounds and stores one upload (the pane, or the draft it belongs to, is already checked).
func (s *Server) saveAttachment(w http.ResponseWriter, req attachRequest) {
	data, err := base64.StdEncoding.DecodeString(req.DataBase64)
	if err != nil || len(data) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if len(data) > chatattach.MaxFile {
		writeError(w, http.StatusRequestEntityTooLarge, "attachment_too_large", "")
		return
	}
	path, err := s.opts.Attach(data, req.Mime, req.Name)
	if err != nil {
		slog.Warn("remote host attach: save failed", "bytes", len(data), "error", err)
		writeError(w, http.StatusInternalServerError, "attach_failed", "")
		return
	}
	slog.Info("remote host attachment saved", "bytes", len(data))
	writeJSON(w, http.StatusOK, attachReply{Path: path})
}
