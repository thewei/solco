package nativeui

/**
 * [INPUT]: runGitOpBudget 的失败（gitworkbench.Explain 的人话、冲突路径、merge 是否停在半路）
 * [OUTPUT]: gitNoticeState + gitNoticeDialog：合并/变基没能直接完成时的说明弹窗
 * [POS]: 用户要求（2026-11-03）：不能直接 merge/rebase 时弹窗说清是冲突还是别的原因，再由用户处理——toast 会在 4 s 内消失、装不下文件列表；弹窗只读，不做任何 Git 调用
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco-gitkit/gitworkbench"
)

// integrationOps are the operations whose failure deserves a dialog.
var integrationOps = map[string]bool{
	"Fast-forward Merge": true,
	"Merge Commit":       true,
	"Rebase":             true,
	"Interactive Rebase": true,
	"Pull and Merge":     true,
	"Pull and Rebase":    true,
	"Squash Merge":       true,
}

// checkoutOps are the checkouts whose refusal by local files deserves a dialog
// naming those files (Fork's "can't switch" alert).
var checkoutOps = map[string]bool{
	"Check Out":        true,
	"Check Out Branch": true,
	"Check Out Commit": true,
}

// checkoutBlockedPaths lists the files Git names when it refuses a checkout
// because local changes (or untracked files) would be overwritten.
func checkoutBlockedPaths(err error) []string {
	ge, ok := gitworkbench.GitError(err)
	if !ok {
		return nil
	}
	var paths []string
	in := false
	for _, line := range strings.Split(ge.Stderr, "\n") {
		switch {
		case strings.Contains(line, "would be overwritten by checkout") || strings.Contains(line, "would be overwritten by merge"):
			in = true
		case in && strings.HasPrefix(line, "\t"):
			paths = append(paths, strings.TrimSpace(line))
		case in:
			in = false
		}
	}
	return paths
}

// autostashOps are the operations that can be retried with "Stash and reapply"
// when local changes block them: checkouts, pulls, merges and rebases.
var autostashOps = map[string]bool{
	"Check Out": true, "Check Out Branch": true, "Check Out Commit": true,
	"Pull": true, "Pull and Merge": true, "Pull and Rebase": true,
	"Fast-forward Merge": true, "Merge Commit": true,
	"Rebase": true, "Interactive Rebase": true,
}

// offerAutostash gives the open notice a "Stash and Reapply" button that runs
// the same operation again between a stash and its pop.
func (s *Shell) offerAutostash(name string, budget time.Duration, run func(ctx context.Context, root string) (string, error)) {
	n := &s.gitNotice
	n.retryLabel = "Stash and Reapply"
	n.retry = func() {
		s.runGitOpFlow(name, budget, func(ctx context.Context, root string) (string, error) {
			var notice string
			res, err := s.git.runner.WithAutostash(ctx, root, func() error {
				var e error
				notice, e = run(ctx, root)
				return e
			})
			if err != nil {
				return "", err
			}
			switch {
			case res.Kept:
				notice += " — your changes conflict with the result and stay in the stash"
			case res.Stashed:
				notice += " — your changes were reapplied"
			}
			return notice, nil
		}, true)
	}
}

// gitNoticeState is one open explanation of why a merge/rebase did not land.
type gitNoticeState struct {
	open  bool
	title string
	body  string
	paths []string
	// retry, when set, is an alternative way through (Stash and Reapply).
	retry      func()
	retryLabel string
}

// openGitNotice words the failure of an integration op. paused means a merge
// stopped half-way (MERGE_HEAD present): Local Changes then offers
// Continue/Abort; a stopped rebase is always aborted by the Runner.
func (s *Shell) openGitNotice(op string, err error, paths []string, paused bool) {
	n := &s.gitNotice
	n.paths = paths
	n.retry, n.retryLabel = nil, ""
	reason := gitFailureText(err)
	switch {
	case op == "Squash Merge" && len(paths) > 0:
		n.title = "Squash Merge Cancelled"
		n.body = "These files conflict, so the squash was cancelled and nothing changed. Merge with a merge commit instead to resolve them in Local Changes."
	case (op == "Rebase" || op == "Interactive Rebase" || op == "Pull and Rebase") && len(paths) > 0:
		n.title = "Rebase Stopped on Conflicts"
		n.body = "The rebase was cancelled and nothing changed. Merge instead, or rebase in the Terminal and resolve the conflicts there."
	case checkoutOps[op] && len(paths) > 0:
		n.title = "Can't Switch Yet"
		n.body = "These files have local changes that the checkout would overwrite. Commit, stash or discard them first, or let Solco stash them, switch and reapply them. Nothing was changed."
	case paused:
		n.title = "Merge Needs Your Help"
		n.body = "These files conflict. The merge is paused in your working tree: resolve them in Local Changes, then continue — or abort to go back."
	case op == "Fast-forward Merge":
		n.title = "Can't Fast-Forward"
		n.body = capitalizeFirst(reason)
	default:
		n.title = op + " Didn't Happen"
		n.body = capitalizeFirst(reason)
	}
	n.open = true
}

func capitalizeFirst(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

const gitNoticeMaxPaths = 8

func (s *Shell) gitNoticeDialog(c *ui.Context) {
	n := &s.gitNotice
	if !n.open {
		return
	}
	ui.Modal(c, &n.open, func() {
		ui.Column(c).Width(430).MaxWidth(480).Gap(Spacing().L).Children(func() {
			modalTitle(c, n.title)
			ui.Text(c, n.body).FontSize(Typography().Body)
			if len(n.paths) > 0 {
				ui.Column(c).Gap(Spacing().XS).Children(func() {
					for i, path := range n.paths {
						if i == gitNoticeMaxPaths {
							ui.Text(c, "and "+itoa(len(n.paths)-i)+" more").FontSize(Typography().Caption).TextColor(c.Theme().TextMuted)
							break
						}
						ui.Text(c, path).FontSize(Typography().BodySmall).Font(gdCodeFont()).SingleLine()
					}
				})
			}
			ui.Row(c).FillWidth().Justify(ui.End).Gap(Spacing().M).Children(func() {
				if n.retry != nil && regularButton(c, n.retryLabel).Clicked() {
					retry := n.retry
					n.open = false
					retry()
				}
				if regularPrimaryButton(c, "OK").AutoFocus().Clicked() {
					n.open = false
				}
			})
		})
	})
}
