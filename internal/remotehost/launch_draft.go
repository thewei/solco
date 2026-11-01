package remotehost

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 Options.Launcher（可启动的 kind）、Backend.Projection（Workspace 目录）、catalog.go/files.go/attach.go 的 writeCatalog/writeFiles/saveAttachment
 * [OUTPUT]: 对外提供 GET /api/v3/launch/catalog?kind=&workspaceId=|path=、GET /api/v3/launch/files?workspaceId=|path=&token=（scope view）、POST /api/v3/launch/attachments（scope terminal-input）
 * [POS]: 手机「新会话草稿页」的无 pane 版本：新会话还没有 pane，`/` `$` 目录、`@` 补全与附件按「kind + 文件夹」寻址，答复与 pane 版逐字节同形（同一个 writer）；目录来自已有 Workspace 或手机给出的绝对路径，只读列名/存文件，不启动任何东西
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// draftFolder resolves the target a draft names: an existing Workspace's directory or an absolute path.
// A refusal has already been written when ok is false.
func (s *Server) draftFolder(w http.ResponseWriter, r *http.Request, instance string) (string, bool) {
	q := r.URL.Query()
	workspaceID, path := q.Get("workspaceId"), q.Get("path")
	if (workspaceID == "") == (path == "") || (path != "" && !validLaunchPath(path)) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return "", false
	}
	if path != "" {
		return path, true
	}
	proj, err := s.opts.Backend.Projection(instance)
	if err != nil {
		slog.Warn("remote host draft: projection failed", "error", err)
		writeError(w, http.StatusBadGateway, "backend_unavailable", "")
		return "", false
	}
	cwd, found := workspaceCWD(proj.Projects, proj.Panes, workspaceID)
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "unknown_workspace", "")
	case cwd == "":
		writeError(w, http.StatusConflict, "workspace_no_directory", "")
	default:
		return cwd, true
	}
	return "", false
}

func (s *Server) draftAvailable(w http.ResponseWriter) bool {
	if s.opts.Launcher == nil {
		writeError(w, http.StatusNotFound, "launch_unavailable", "")
		return false
	}
	return true
}

func (s *Server) handleDraftCatalog(w http.ResponseWriter, r *http.Request, _ Device) {
	if !s.draftAvailable(w) {
		return
	}
	kind := r.URL.Query().Get("kind")
	known := false
	for _, k := range s.opts.Launcher.Kinds() {
		known = known || (k.ID == kind && kind != LaunchTerminal)
	}
	if _, agent := history.ParseAgentID(kind); !known || !agent {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	instance, ok := s.resolveInstance(w, r)
	if !ok {
		return
	}
	if cwd, ok := s.draftFolder(w, r, instance); ok {
		s.writeCatalog(w, r, kind, cwd)
	}
}

func (s *Server) handleDraftFiles(w http.ResponseWriter, r *http.Request, _ Device) {
	if !s.draftAvailable(w) {
		return
	}
	instance, ok := s.resolveInstance(w, r)
	if !ok {
		return
	}
	if cwd, ok := s.draftFolder(w, r, instance); ok {
		s.writeFiles(w, r, cwd)
	}
}

func (s *Server) handleDraftAttach(w http.ResponseWriter, r *http.Request, _ Device) {
	if s.opts.Attach == nil {
		writeError(w, http.StatusNotFound, "attach_unavailable", "")
		return
	}
	var req attachRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, attachBodyLimit)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.saveAttachment(w, req)
}
