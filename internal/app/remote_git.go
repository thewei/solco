package app

import (
	"context"
	"fmt"

	"github.com/thewei/solco-gitkit/gitworkbench"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: 依赖 gitworkbench.Runner（Snapshot/ResolveRoot/Upstream/ReadFileDiff）、remotehost.GitReader 契约
 * [OUTPUT]: 对外提供 RemoteGit 的读面（实现 remotehost.GitReader：手机 Changes 只读视图；写面见 remote_git_write.go）
 * [POS]: 手机 Host 与桌面同一个本地 Git 域（gitworkbench）之间的只读适配——只读快照与单文件 diff，把 hunk 压成线协议行并按预算截断；不另起 Git 缓存
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	remoteGitMaxFiles = 500
	// A diff on the wire is bounded in rows and bytes; a phone does not need a 16 MiB patch.
	remoteGitMaxDiffLines = 3000
	remoteGitMaxDiffBytes = 400 << 10
	remoteGitMaxLineBytes = 2000
)

type RemoteGit struct {
	runner *gitworkbench.Runner
}

func NewRemoteGit() *RemoteGit { return &RemoteGit{runner: &gitworkbench.Runner{}} }

func (g *RemoteGit) Status(ctx context.Context, dir string) (remotehost.GitStatus, error) {
	root, err := g.runner.ResolveRoot(ctx, dir)
	if err != nil {
		if ge, ok := gitworkbench.GitError(err); ok && ge.Class == gitworkbench.ErrNotARepo {
			return remotehost.GitStatus{}, nil
		}
		return remotehost.GitStatus{}, err
	}
	snap, err := g.runner.Snapshot(ctx, root)
	if err != nil {
		return remotehost.GitStatus{}, err
	}
	out := remotehost.GitStatus{Repo: true, Branch: snap.Branch, Detached: snap.Branch == "HEAD", Head: shortHash(snap.Head),
		Additions: snap.TotalAdditions, Deletions: snap.TotalDeletions, Truncated: snap.Truncated}
	if out.Detached {
		out.Branch = ""
	}
	if up, _ := g.runner.Upstream(ctx, root); up.OK {
		out.Upstream = &remotehost.GitUpstream{Name: up.Remote + "/" + up.Branch, Ahead: up.Ahead, Behind: up.Behind}
	}
	for i, f := range snap.Files {
		if i == remoteGitMaxFiles {
			out.Truncated = true
			break
		}
		out.Files = append(out.Files, remotehost.GitFile{
			Path: f.Path, OldPath: f.OldPath, Status: string(f.Status), Staged: f.Staged, Unstaged: f.Unstaged,
			Additions: f.Additions, Deletions: f.Deletions, Binary: f.Binary,
		})
	}
	return out, nil
}

func (g *RemoteGit) Diff(ctx context.Context, dir, path string) (remotehost.GitDiff, error) {
	root, err := g.runner.ResolveRoot(ctx, dir)
	if err != nil {
		return remotehost.GitDiff{}, err
	}
	snap, err := g.runner.Snapshot(ctx, root)
	if err != nil {
		return remotehost.GitDiff{}, err
	}
	// The path must be one the snapshot lists: nothing else reaches Git.
	file, ok := snap.ByPath(path)
	if !ok {
		return remotehost.GitDiff{}, remotehost.ErrGitFileNotFound
	}
	out := remotehost.GitDiff{Path: file.Path, Status: string(file.Status), Binary: file.Binary, TooLarge: file.TooLarge}
	if file.Binary || file.TooLarge {
		return out, nil
	}
	hunks := file.Hunks
	if file.Deferred || (len(hunks) == 0 && file.Additions+file.Deletions > 0) {
		d, err := g.runner.ReadFileDiff(ctx, root, snap.DiffBase, file)
		if ge, isGit := gitworkbench.GitError(err); isGit && ge.Class == gitworkbench.ErrTooLarge {
			out.TooLarge = true
			return out, nil
		} else if err != nil {
			return remotehost.GitDiff{}, err
		}
		hunks = d.Hunks
	}
	out.Hunks, out.Truncated = wireHunks(hunks)
	return out, nil
}

// wireHunks flattens parsed hunks to wire rows within the row/byte budget.
func wireHunks(hunks []gitworkbench.Hunk) (out []remotehost.GitHunk, truncated bool) {
	rows, bytes := 0, 0
	for _, h := range hunks {
		header := fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		if h.Section != "" {
			header += " " + h.Section
		}
		wire := remotehost.GitHunk{Header: header}
		for _, l := range h.Lines {
			text := l.Text
			if len(text) > remoteGitMaxLineBytes {
				text, truncated = cutUTF8(text, remoteGitMaxLineBytes), true
			}
			if rows >= remoteGitMaxDiffLines || bytes+len(text) > remoteGitMaxDiffBytes {
				out = append(out, wire)
				return out, true
			}
			rows++
			bytes += len(text)
			wire.Lines = append(wire.Lines, remotehost.GitLine{Kind: lineKind(l.Kind), Old: l.OldLine, New: l.NewLine, Text: text})
		}
		out = append(out, wire)
	}
	return out, truncated
}

func lineKind(k gitworkbench.LineKind) string {
	switch k {
	case gitworkbench.KindAdd:
		return "+"
	case gitworkbench.KindDelete:
		return "-"
	}
	return " "
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

func cutUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}
