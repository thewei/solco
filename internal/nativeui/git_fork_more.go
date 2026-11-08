package nativeui

/**
 * [INPUT]: gitService（upstream/runner）、gitworkbench.Runner 的 Pull/FetchAll/ResolveConflict、设置里的自动 Fetch 间隔（settings Git）
 * [OUTPUT]: 右侧面板仍在用的 Git 编排——Pull（快进）、"Commit and Push…" 的收尾 Push、文件菜单的共享一组（冲突文件的逐文件解决 + Show in Finder）、自动 Fetch 的定时器
 * [POS]: 全部走 runGitOp / runGitNetworkOp 的统一失败/刷新/toast 通道，不新增 Git 调用路径，规则与拒绝全在 solco-gitkit/gitworkbench；2026-11-08 G5 起，重置/补丁/对比/交互式变基/远程与子模块对话框/文件历史与 Blame/Tag/Reflog/Stash 对话框随 Git 整页退役，它们的家是 Solco Git
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
)

// gitMoreState is the presentation state of the Git actions below.
type gitMoreState struct {
	// pushAfterCommit makes the Commit dialog's success continue with a Push
	// ("Commit and Push…"); cleared whenever the dialog opens or is cancelled.
	pushAfterCommit bool

	autoFetchTimer *time.Timer
}

// ---- pull ------------------------------------------------------------------

// pullWith pulls the branch's upstream with one integration mode. The plain
// header Pull stays fast-forward only; Merge and Rebase live in the Actions
// menu and report conflicts through the integration notice.
func (s *Shell) pullWith(mode gitworkbench.PullMode, op string) {
	s.runGitNetworkOp(op, func(ctx context.Context, root string) (string, error) {
		remote := s.git.runner.DefaultRemote(ctx, root)
		branch := s.git.runner.CurrentBranch(ctx, root)
		if branch == "" {
			return "", errGitOp("detached HEAD: check out a branch first")
		}
		theirs := branch
		if up := s.git.upstream; up.OK && up.Remote != "" {
			remote, theirs = up.Remote, up.Branch
		}
		if err := s.git.runner.Pull(ctx, root, remote, theirs, mode); err != nil {
			return "", err
		}
		return "Pulled " + remote + "/" + theirs, nil
	})
}

// ---- Commit and Push -------------------------------------------------------

// afterCommitPush continues a "Commit and Push…" with the Push, once.
func (s *Shell) afterCommitPush() {
	if !s.gitMore.pushAfterCommit {
		return
	}
	s.gitMore.pushAfterCommit = false
	s.pushRepo()
}

// ---- file menus ------------------------------------------------------------

// gdFileItems is the file menus' shared group (one owner for the changes tree,
// the file card and the diff cell menus): resolving a conflicted file, and
// Show in Finder. File history, Blame and Stash File left with the Git page
// (2026-11-08): they read or park history, which lives in Solco Git.
func (s *Shell) gdFileItems(m *ui.Menu, cf *gitworkbench.ChangeFile) {
	if s.git == nil || s.git.root == "" || cf == nil {
		return
	}
	if cf.Status == gitworkbench.StatusConflicted {
		s.gdConflictItems(m, cf)
	}
	if m.Item("Show in Finder").Disabled(cf.Status == gitworkbench.StatusDeleted).Chosen() {
		s.openExternalPathParent(filepath.Join(s.git.root, cf.Path))
	}
}

// gdConflictItems resolves one conflicted file: keep either side, or accept
// the file as edited by hand.
func (s *Shell) gdConflictItems(m *ui.Menu, cf *gitworkbench.ChangeFile) {
	resolve := func(side gitworkbench.ConflictSide, done string) {
		path := cf.Path
		s.runGitOp("Resolve Conflict", func(ctx context.Context, root string) (string, error) {
			if err := s.git.runner.ResolveConflict(ctx, root, path, side); err != nil {
				return "", err
			}
			return done + path, nil
		})
	}
	if m.Item("Keep Current Branch's Version").Disabled(s.git.opBusy).Chosen() {
		resolve(gitworkbench.ConflictOurs, "Kept the current branch's version of ")
	}
	if m.Item("Take Incoming Version").Disabled(s.git.opBusy).Chosen() {
		resolve(gitworkbench.ConflictTheirs, "Took the incoming version of ")
	}
	if m.Item("Mark as Resolved").Disabled(s.git.opBusy).Chosen() {
		resolve(gitworkbench.ConflictMarked, "Marked resolved: ")
	}
	m.Separator()
}

// ---- auto-fetch ------------------------------------------------------------

var autoFetchChoices = []struct {
	label   string
	minutes int
}{{"Off", 0}, {"Every 5 minutes", 5}, {"Every 15 minutes", 15}, {"Every 30 minutes", 30}, {"Every hour", 60}}

func autoFetchLabel(minutes int) string {
	for _, ch := range autoFetchChoices {
		if ch.minutes == minutes {
			return ch.label
		}
	}
	return autoFetchChoices[0].label
}

func autoFetchLabels() []string {
	out := make([]string, len(autoFetchChoices))
	for i, ch := range autoFetchChoices {
		out[i] = ch.label
	}
	return out
}

// autoFetchInterval is the saved cadence; anything off the menu means off.
func (s *Shell) autoFetchInterval() time.Duration {
	for _, ch := range autoFetchChoices {
		if ch.minutes > 0 && ch.minutes == s.settings.Workbench.AutoFetchMinutes {
			return time.Duration(ch.minutes) * time.Minute
		}
	}
	return 0
}

// rearmAutoFetch (re)starts the one background timer from the saved cadence.
func (s *Shell) rearmAutoFetch() {
	m := &s.gitMore
	if m.autoFetchTimer != nil {
		m.autoFetchTimer.Stop()
		m.autoFetchTimer = nil
	}
	d := s.autoFetchInterval()
	if d == 0 {
		return
	}
	m.autoFetchTimer = time.AfterFunc(d, func() { s.applyOnWindow(s.autoFetchTick) })
}

// ensureAutoFetch arms the timer the first time a repository is read.
func (s *Shell) ensureAutoFetch() {
	if s.gitMore.autoFetchTimer == nil {
		s.rearmAutoFetch()
	}
}

// autoFetchTick fetches the open repository's remote quietly: no toast, no busy
// state, failures (offline, sign-in) are simply skipped. Fetch only — it never
// merges or pushes.
func (s *Shell) autoFetchTick() {
	s.rearmAutoFetch()
	if s.git == nil || s.git.root == "" || s.git.opBusy || s.git.committing || s.git.runner == nil {
		return
	}
	root := s.git.root
	remote := ""
	if up := s.git.upstream; up.OK {
		remote = up.Remote
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), gitNetworkTimeout)
		defer cancel()
		if remote == "" {
			remote = s.git.runner.DefaultRemote(ctx, root)
		}
		if remote == "" {
			return
		}
		if err := s.git.runner.Fetch(ctx, root, remote); err != nil {
			return
		}
		s.applyOnUI(func() {
			if s.git == nil || s.git.root != root || s.git.opBusy {
				return
			}
			s.git.cache.Invalidate(root)
			s.loadBranches()
		})
	}()
}
