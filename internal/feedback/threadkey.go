package feedback

/**
 * [INPUT]: 项目根目录路径、MCP 握手的客户端名、Agent 自报的 caller_info
 * [OUTPUT]: 对外提供 ResolveProject（项目键，git worktree 折回主仓库）、ClientName、TitleFor、GapWindow
 * [POS]: 线程归属的纯函数部分（2026-11-09 决策）：同一线程 = 同一 projectKey + 同一客户端 + 距上次活动不超过 GapWindow，显式 thread_id / task_id 优先；
 *        不起 git 进程，只读 .git 文件（目录、gitdir 指针、commondir），读不到就退回路径本身
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// GapWindow is the longest quiet gap inside one automatically assigned thread.
const GapWindow = 60 * time.Minute

// ResolveProject returns the key that identifies a project across its git worktrees, the branch checked out in
// the given root, and whether that root is a linked worktree. An unreadable or non-git root is its own key.
func ResolveProject(root string) (key, branch string, worktree bool) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", "", false
	}
	root = filepath.Clean(root)
	dir := root
	for i := 0; i < 12; i++ {
		fi, err := os.Lstat(filepath.Join(dir, ".git"))
		if err == nil {
			if fi.IsDir() {
				return dir, readBranch(filepath.Join(dir, ".git", "HEAD")), false
			}
			gitdir := readGitdirPointer(dir)
			if gitdir == "" {
				return dir, "", false
			}
			branch = readBranch(filepath.Join(gitdir, "HEAD"))
			if b, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
				common := strings.TrimSpace(string(b))
				if !filepath.IsAbs(common) {
					common = filepath.Join(gitdir, common)
				}
				common = filepath.Clean(common)
				if filepath.Base(common) == ".git" {
					return filepath.Dir(common), branch, true
				}
			}
			return dir, branch, false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return root, "", false
}

func readGitdirPointer(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(b))
	const p = "gitdir:"
	if !strings.HasPrefix(line, p) {
		return ""
	}
	g := strings.TrimSpace(strings.TrimPrefix(line, p))
	if !filepath.IsAbs(g) {
		g = filepath.Join(dir, g)
	}
	return filepath.Clean(g)
}

func readBranch(headFile string) string {
	b, err := os.ReadFile(headFile)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	return strings.TrimPrefix(s, "ref: refs/heads/")
}

// clientFamilies folds the many spellings of one Agent client into one name, so "cursor-vscode" (the MCP handshake)
// and "Cursor Worker B6" (a free-text caller_info) land in the same thread rule.
var clientFamilies = []struct{ keyword, name string }{
	{"cursor", "Cursor"}, {"claude", "Claude Code"}, {"codex", "Codex"}, {"gemini", "Gemini"}, {"antigravity", "Antigravity"},
	{"opencode", "OpenCode"}, {"copilot", "Copilot"}, {"windsurf", "Windsurf"}, {"zed", "Zed"}, {"omp", "OMP"},
}

func clientFamily(s string) string {
	l := strings.ToLower(s)
	for _, f := range clientFamilies {
		if strings.Contains(l, f.keyword) {
			return f.name
		}
	}
	return ""
}

// ClientName is the name of the Agent client: the MCP handshake name when there is one (known families folded
// together), otherwise a family guessed from the free-text caller_info, otherwise "Agent".
func ClientName(client, callerInfo string) string {
	if c := strings.TrimSpace(client); c != "" {
		if f := clientFamily(c); f != "" {
			return f
		}
		return clipRunes(c, 60)
	}
	if f := clientFamily(callerInfo); f != "" {
		return f
	}
	return "Agent"
}

// TitleFor names a thread: what caller_info says after the client prefix, else the branch, else the project.
// A caller_info that is only a client label ("Cursor Agent") names nothing.
func TitleFor(callerInfo, client, branch, projectKey string) string {
	t := strings.TrimSpace(callerInfo)
	hadSep := false
	for _, sep := range []string{" - ", " – ", " — ", ": ", "："} {
		if i := strings.Index(t, sep); i > 0 {
			t = strings.TrimSpace(t[i+len(sep):])
			hadSep = true
			break
		}
	}
	if !hadSep && (clientFamily(t) != "" && len(strings.Fields(t)) <= 3 || strings.EqualFold(t, strings.TrimSpace(client))) {
		t = ""
	}
	if t == "" {
		switch {
		case branch != "":
			t = branch
		case projectKey != "":
			t = filepath.Base(projectKey)
		default:
			t = "Feedback"
		}
	}
	return clipRunes(t, 80)
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
