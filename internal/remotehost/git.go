package remotehost

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

/**
 * [INPUT]: 依赖 Options.Git（只读 Git 缝）、Backend.Projection（Workspace → 目录）、server.go 的 guard/resolveInstance/writeError、launch.go 的 workspaceCWD
 * [OUTPUT]: 对外提供 GitReader/GitStatus/GitFile/GitDiff/GitHunk/GitLine/ErrGitFileNotFound 与 GET /api/v3/workspaces/{id}/git、GET /api/v3/workspaces/{id}/git/diff?path=
 * [POS]: 手机「Changes」只读视图（Git 状态 + 单文件 unified diff）：Host 只解析 Workspace 目录并转发给 GitReader（具体实现在 internal/app，走桌面同一个 gitworkbench）；不暂存、不提交、不 fetch；非仓库 = 200 repo:false；日志只记设备、workspace、文件数，路径与 diff 文本永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// GitReader reads one Workspace directory's Git state. It never mutates.
type GitReader interface {
	// Status is the working tree against HEAD. A directory outside a repository is
	// GitStatus{Repo: false}, not an error.
	Status(ctx context.Context, dir string) (GitStatus, error)
	// Diff is one changed file's hunks (path must be one Status listed, else ErrGitFileNotFound).
	Diff(ctx context.Context, dir, path string) (GitDiff, error)
}

// ErrGitFileNotFound: the path is not a changed file of the repository (it was
// committed or reverted since the list was read).
var ErrGitFileNotFound = errors.New("git: no such changed file")

type GitUpstream struct {
	Name   string `json:"name"`
	Ahead  int    `json:"ahead"`
	Behind int    `json:"behind"`
}

type GitFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"` // modified|added|deleted|renamed|copied|typechange|conflicted|untracked
	Staged    bool   `json:"staged"`
	Unstaged  bool   `json:"unstaged"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
}

type GitStatus struct {
	WorkspaceID string       `json:"workspaceId"`
	Repo        bool         `json:"repo"`
	Branch      string       `json:"branch,omitempty"`
	Detached    bool         `json:"detached,omitempty"`
	Head        string       `json:"head,omitempty"` // short hash; empty on an unborn branch
	Upstream    *GitUpstream `json:"upstream,omitempty"`
	Files       []GitFile    `json:"files"`
	Additions   int          `json:"additions"`
	Deletions   int          `json:"deletions"`
	// Truncated: more files exist than the list carries.
	Truncated bool `json:"truncated,omitempty"`
}

// GitLine is one diff row: Kind is "+", "-" or " ".
type GitLine struct {
	Kind string `json:"k"`
	Old  int    `json:"o,omitempty"`
	New  int    `json:"n,omitempty"`
	Text string `json:"t"`
}

type GitHunk struct {
	Header string    `json:"header"` // "@@ -1,3 +1,4 @@ section"
	Lines  []GitLine `json:"lines"`
}

type GitDiff struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Binary bool   `json:"binary,omitempty"`
	// TooLarge: the file's diff is past what Git reads on demand; no hunks.
	TooLarge bool `json:"tooLarge,omitempty"`
	// Truncated: the hunks were cut to the wire budget.
	Truncated bool      `json:"truncated,omitempty"`
	Hunks     []GitHunk `json:"hunks"`
}

// gitWorkspaceDir resolves the {id} workspace to its directory, answering the refusal itself.
func (s *Server) gitWorkspaceDir(w http.ResponseWriter, r *http.Request) (dir, workspaceID string, ok bool) {
	if s.opts.Git == nil {
		writeError(w, http.StatusNotFound, "git_unavailable", "")
		return "", "", false
	}
	return s.workspaceFolder(w, r)
}

// workspaceFolder resolves the {id} workspace to its directory (Git and Files share it), answering the refusal itself.
func (s *Server) workspaceFolder(w http.ResponseWriter, r *http.Request) (dir, workspaceID string, ok bool) {
	instance, ok := s.resolveInstance(w, r)
	if !ok {
		return "", "", false
	}
	workspaceID = r.PathValue("id")
	proj, err := s.opts.Backend.Projection(instance)
	if err != nil {
		slog.Warn("remote host git: projection failed", "error", err)
		writeError(w, http.StatusBadGateway, "backend_unavailable", "")
		return "", "", false
	}
	cwd, found := workspaceCWD(proj.Projects, proj.Panes, workspaceID)
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "unknown_workspace", "")
		return "", "", false
	case cwd == "":
		writeError(w, http.StatusConflict, "workspace_no_directory", "")
		return "", "", false
	}
	return cwd, workspaceID, true
}

func (s *Server) handleGitStatus(w http.ResponseWriter, r *http.Request, dev Device) {
	dir, id, ok := s.gitWorkspaceDir(w, r)
	if !ok {
		return
	}
	status, err := s.opts.Git.Status(r.Context(), dir)
	if err != nil {
		slog.Warn("remote host git: status failed", "device", dev.ID, "workspace", id)
		writeError(w, http.StatusBadGateway, "git_failed", "")
		return
	}
	status.WorkspaceID = id
	if status.Files == nil {
		status.Files = []GitFile{}
	}
	slog.Info("remote host git status", "device", dev.ID, "workspace", id, "files", len(status.Files))
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleGitDiff(w http.ResponseWriter, r *http.Request, dev Device) {
	dir, id, ok := s.gitWorkspaceDir(w, r)
	if !ok {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" || len(path) > maxLaunchPath {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	diff, err := s.opts.Git.Diff(r.Context(), dir, path)
	switch {
	case errors.Is(err, ErrGitFileNotFound):
		writeError(w, http.StatusNotFound, "unknown_file", "")
		return
	case err != nil:
		slog.Warn("remote host git: diff failed", "device", dev.ID, "workspace", id)
		writeError(w, http.StatusBadGateway, "git_failed", "")
		return
	}
	if diff.Hunks == nil {
		diff.Hunks = []GitHunk{}
	}
	slog.Info("remote host git diff", "device", dev.ID, "workspace", id, "hunks", len(diff.Hunks))
	writeJSON(w, http.StatusOK, diff)
}
