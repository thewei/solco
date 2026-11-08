package app

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/thewei/solco-gitkit/gitworkbench"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: 依赖 gitworkbench.Runner（Snapshot/Stage/Unstage/CapturePreflight/Commit/Fetch/PullFFOnly/Push/DefaultRemote/CurrentBranch/Upstream）、remotehost.GitWriter 契约
 * [OUTPUT]: 对外提供 RemoteGit 的写面（实现 remotehost.GitWriter：手机 Changes 的暂存/取消暂存/提交/Fetch/Pull/Push）
 * [POS]: 手机 Host 与桌面同一个本地 Git 域的写适配——与桌面 git_operations.go 同一套参数（有 upstream 跟 upstream，否则 DefaultRemote + CurrentBranch，Pull 仅 --ff-only，首次 Push 设上游）；路径必须是当前快照列出的文件，提交先抓预检再走 Runner 的原子事务；HEAD 栅栏（手机审阅时的短哈希）移动即 git_stale；Git 的 stderr 只用于分类，永不出本包；丢弃/重置/分支管理不在此面
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func gitRefusal(code string, status int) error {
	return &remotehost.GitWriteError{Code: code, Status: status}
}

// listedPaths checks every path against the snapshot: nothing else reaches Git.
func listedPaths(snap *gitworkbench.ChangesSnapshot, paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		file, ok := snap.ByPath(p)
		if !ok {
			return nil, remotehost.ErrGitFileNotFound
		}
		if !seen[file.Path] {
			seen[file.Path] = true
			out = append(out, file.Path)
		}
	}
	return out, nil
}

func (g *RemoteGit) snapshot(ctx context.Context, dir string) (string, *gitworkbench.ChangesSnapshot, error) {
	root, err := g.runner.ResolveRoot(ctx, dir)
	if err != nil {
		return "", nil, err
	}
	snap, err := g.runner.Snapshot(ctx, root)
	return root, snap, err
}

func (g *RemoteGit) Stage(ctx context.Context, dir string, paths []string) error {
	root, snap, err := g.snapshot(ctx, dir)
	if err != nil {
		return err
	}
	listed, err := listedPaths(snap, paths)
	if err != nil {
		return err
	}
	return mapGitError(g.runner.Stage(ctx, root, listed))
}

func (g *RemoteGit) Unstage(ctx context.Context, dir string, paths []string) error {
	root, snap, err := g.snapshot(ctx, dir)
	if err != nil {
		return err
	}
	listed, err := listedPaths(snap, paths)
	if err != nil {
		return err
	}
	return mapGitError(g.runner.Unstage(ctx, root, snap.NoHead, listed))
}

func (g *RemoteGit) Commit(ctx context.Context, dir string, c remotehost.GitCommit) (string, error) {
	root, snap, err := g.snapshot(ctx, dir)
	if err != nil {
		return "", err
	}
	if c.Head != "" && shortHash(snap.Head) != c.Head {
		return "", gitRefusal("git_stale", http.StatusConflict)
	}
	listed, err := listedPaths(snap, c.Paths)
	if err != nil {
		return "", err
	}
	pre, err := g.runner.CapturePreflight(ctx, root, snap, listed)
	if err != nil {
		return "", mapGitError(err)
	}
	res, err := g.runner.Commit(ctx, root, gitworkbench.CommitDraft{Subject: c.Subject, Body: c.Body, Paths: listed}, pre, snap)
	if err != nil {
		return "", mapGitError(err)
	}
	return shortHash(res.Hash), nil
}

func (g *RemoteGit) Sync(ctx context.Context, dir string, op remotehost.GitSyncOp, head string) error {
	root, err := g.runner.ResolveRoot(ctx, dir)
	if err != nil {
		return err
	}
	if head != "" {
		facts, err := g.runner.Facts(ctx, root)
		if err != nil {
			return mapGitError(err)
		}
		if shortHash(facts.Head) != head {
			return gitRefusal("git_stale", http.StatusConflict)
		}
	}
	remote := g.runner.DefaultRemote(ctx, root)
	switch op {
	case remotehost.GitFetch:
		return mapGitError(g.runner.Fetch(ctx, root, remote))
	case remotehost.GitPull, remotehost.GitPush:
		branch := g.runner.CurrentBranch(ctx, root)
		if branch == "" {
			return gitRefusal("git_detached", http.StatusConflict)
		}
		up, _ := g.runner.Upstream(ctx, root)
		if op == remotehost.GitPull {
			if !up.OK {
				return gitRefusal("git_no_upstream", http.StatusConflict)
			}
			// The branch's own upstream decides where it pulls from and pushes
			// to, as on the desktop: the first remote is only the guess for a
			// branch that has none.
			if up.Remote != "" {
				remote = up.Remote
			}
			return mapGitError(g.runner.PullFFOnly(ctx, root, remote, up.Branch))
		}
		theirs := branch
		if up.OK && up.Remote != "" {
			remote, theirs = up.Remote, up.Branch
		}
		return mapGitError(g.runner.PushTo(ctx, root, remote, branch, theirs, !up.OK))
	}
	return errors.New("unknown git sync op")
}

// mapGitError turns a Git failure into a stable refusal code. Git's own text
// is read here to classify and goes no further.
func mapGitError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gitworkbench.ErrStale) {
		return gitRefusal("git_stale", http.StatusConflict)
	}
	ge, ok := gitworkbench.GitError(err)
	if !ok {
		return err
	}
	switch ge.Class {
	case gitworkbench.ErrStaleState:
		return gitRefusal("git_stale", http.StatusConflict)
	case gitworkbench.ErrHookFailed:
		return gitRefusal("git_hook_failed", http.StatusConflict)
	case gitworkbench.ErrMergeInProgess, gitworkbench.ErrConflict:
		return gitRefusal("git_merge_in_progress", http.StatusConflict)
	case gitworkbench.ErrLocked:
		return gitRefusal("git_locked", http.StatusConflict)
	case gitworkbench.ErrTimeout:
		return gitRefusal("git_timeout", http.StatusGatewayTimeout)
	case gitworkbench.ErrRefusal:
		return gitRefusal("git_rejected", http.StatusConflict)
	}
	s := strings.ToLower(ge.Stderr)
	if strings.Contains(s, "rejected") || strings.Contains(s, "non-fast-forward") ||
		strings.Contains(s, "not possible to fast-forward") || strings.Contains(s, "diverge") {
		return gitRefusal("git_rejected", http.StatusConflict)
	}
	return err
}
