package remotehost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"
)

/**
 * [INPUT]: 依赖 Options.GitWrite（写 Git 缝）、git.go 的 GitReader/GitStatus/gitWorkspaceDir、server.go 的 guard/writeError/writeJSON
 * [OUTPUT]: 对外提供 GitWriter/GitCommit/GitSyncOp/GitWriteError 与 POST /api/v3/workspaces/{id}/git/stage|unstage|commit|fetch|pull|push
 * [POS]: 手机「Changes」的分阶段写（暂存/取消暂存/提交/Fetch/Pull/Push）：scope manage；Host 只校验请求形状并转发给 GitWriter（实现在 internal/app，走桌面同一个 gitworkbench.Runner，文件必须是快照列出的路径，提交先抓预检再原子提交，Pull 只 --ff-only）；每次成功都回新鲜 GitStatus；丢弃/重置/分支管理/冲突解决永不下放；错误只回稳定 code，Git 的 stderr（含路径）永不出 Host；日志只记设备、workspace、操作名与文件数，路径/提交信息永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	maxGitPaths      = 500
	maxGitSubjectLen = 200
	maxGitBody       = 16 << 10
)

// GitSyncOp is one explicit network operation.
type GitSyncOp string

const (
	GitFetch GitSyncOp = "fetch"
	GitPull  GitSyncOp = "pull"
	GitPush  GitSyncOp = "push"
)

// GitCommit is one validated commit intent: the listed files only. Head, when set, is the
// short HEAD hash the phone reviewed; a moved HEAD is git_stale.
type GitCommit struct {
	Subject string
	Body    string
	Paths   []string
	Head    string
}

// GitWriter changes one Workspace directory's repository. Every path must be one the
// current status lists (else ErrGitFileNotFound); refusals are *GitWriteError.
type GitWriter interface {
	Stage(ctx context.Context, dir string, paths []string) error
	Unstage(ctx context.Context, dir string, paths []string) error
	// Commit returns the short hash of the new commit.
	Commit(ctx context.Context, dir string, c GitCommit) (string, error)
	// Sync runs fetch, pull (fast-forward only) or push; head, when set, must still be HEAD.
	Sync(ctx context.Context, dir string, op GitSyncOp, head string) error
}

// GitWriteError is a refusal the Host relays with a stable code and no Git text.
type GitWriteError struct {
	// Code: git_stale | git_hook_failed | git_merge_in_progress | git_locked | git_rejected |
	// git_no_upstream | git_detached | git_nothing_to_commit | git_timeout | git_failed
	Code   string
	Status int
}

func (e *GitWriteError) Error() string { return e.Code }

type gitPathsRequest struct {
	Paths []string `json:"paths"`
}

type gitCommitRequest struct {
	Subject string   `json:"subject"`
	Body    string   `json:"body,omitempty"`
	Paths   []string `json:"paths"`
	Head    string   `json:"head,omitempty"`
}

type gitSyncRequest struct {
	Head string `json:"head,omitempty"`
}

// GitWriteResult is the repository after the change; Hash is set by a commit.
type GitWriteResult struct {
	Status GitStatus `json:"status"`
	Hash   string    `json:"hash,omitempty"`
}

func (s *Server) gitWriteDir(w http.ResponseWriter, r *http.Request) (dir, workspaceID string, ok bool) {
	if s.opts.Git == nil || s.opts.GitWrite == nil {
		writeError(w, http.StatusNotFound, "git_write_unavailable", "")
		return "", "", false
	}
	return s.workspaceFolder(w, r)
}

func validGitPaths(paths []string) bool {
	if len(paths) == 0 || len(paths) > maxGitPaths {
		return false
	}
	for _, p := range paths {
		if p == "" || len(p) > maxLaunchPath || strings.ContainsRune(p, 0) {
			return false
		}
	}
	return true
}

func validGitHead(head string) bool {
	if len(head) > 40 {
		return false
	}
	for _, c := range head {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validGitSubject(subject string) bool {
	if strings.TrimSpace(subject) == "" || utf8.RuneCountInString(subject) > maxGitSubjectLen {
		return false
	}
	for _, c := range subject {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// gitWriteDone answers one write: a refusal with its code, or the fresh status.
func (s *Server) gitWriteDone(w http.ResponseWriter, r *http.Request, dev Device, dir, id, op string, count int, hash string, err error) {
	var refusal *GitWriteError
	switch {
	case errors.Is(err, ErrGitFileNotFound):
		writeError(w, http.StatusNotFound, "unknown_file", "")
		return
	case errors.As(err, &refusal):
		slog.Info("remote host git write refused", "device", dev.ID, "workspace", id, "op", op, "code", refusal.Code)
		writeError(w, refusal.Status, refusal.Code, "")
		return
	case err != nil:
		slog.Warn("remote host git write failed", "device", dev.ID, "workspace", id, "op", op)
		writeError(w, http.StatusBadGateway, "git_failed", "")
		return
	}
	status, err := s.opts.Git.Status(r.Context(), dir)
	if err != nil {
		slog.Warn("remote host git write: status failed", "device", dev.ID, "workspace", id, "op", op)
		writeError(w, http.StatusBadGateway, "git_failed", "")
		return
	}
	status.WorkspaceID = id
	if status.Files == nil {
		status.Files = []GitFile{}
	}
	slog.Info("remote host git write", "device", dev.ID, "workspace", id, "op", op, "files", count)
	writeJSON(w, http.StatusOK, GitWriteResult{Status: status, Hash: hash})
}

func (s *Server) handleGitPaths(op string) func(http.ResponseWriter, *http.Request, Device) {
	return func(w http.ResponseWriter, r *http.Request, dev Device) {
		dir, id, ok := s.gitWriteDir(w, r)
		if !ok {
			return
		}
		var req gitPathsRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || !validGitPaths(req.Paths) {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		var err error
		if op == "stage" {
			err = s.opts.GitWrite.Stage(r.Context(), dir, req.Paths)
		} else {
			err = s.opts.GitWrite.Unstage(r.Context(), dir, req.Paths)
		}
		s.gitWriteDone(w, r, dev, dir, id, op, len(req.Paths), "", err)
	}
}

func (s *Server) handleGitCommit(w http.ResponseWriter, r *http.Request, dev Device) {
	dir, id, ok := s.gitWriteDir(w, r)
	if !ok {
		return
	}
	var req gitCommitRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20+maxGitBody)).Decode(&req); err != nil ||
		!validGitPaths(req.Paths) || !validGitSubject(req.Subject) || len(req.Body) > maxGitBody ||
		!utf8.ValidString(req.Body) || strings.ContainsRune(req.Body, 0) || !validGitHead(req.Head) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	hash, err := s.opts.GitWrite.Commit(r.Context(), dir, GitCommit{
		Subject: strings.TrimSpace(req.Subject), Body: req.Body, Paths: req.Paths, Head: req.Head,
	})
	s.gitWriteDone(w, r, dev, dir, id, "commit", len(req.Paths), hash, err)
}

func (s *Server) handleGitSync(op GitSyncOp) func(http.ResponseWriter, *http.Request, Device) {
	return func(w http.ResponseWriter, r *http.Request, dev Device) {
		dir, id, ok := s.gitWriteDir(w, r)
		if !ok {
			return
		}
		var req gitSyncRequest
		// The body is optional: an empty one asks for no HEAD fence.
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); (err != nil && !errors.Is(err, io.EOF)) || !validGitHead(req.Head) {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		err := s.opts.GitWrite.Sync(r.Context(), dir, op, req.Head)
		s.gitWriteDone(w, r, dev, dir, id, string(op), 0, "", err)
	}
}
