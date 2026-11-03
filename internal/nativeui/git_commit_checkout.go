package nativeui

import (
	"context"

	"github.com/thewei/solco/internal/gitworkbench"
)

/**
 * [INPUT]: 提交行的双击、CommitRef 装饰（本地分支/远程分支）、gitworkbench.Runner 的 SwitchBranch/CheckoutRemoteBranch/CheckoutDetached
 * [OUTPUT]: checkoutCommitRow —— 双击提交即检出它（Fork 同款）：当前分支已在此 → 提示；有本地分支 → 切过去；只有远程分支 → 本地同名/建 tracking；无分支 → 游离 HEAD
 * [POS]: 提交列表的双击动作；不弹确认（Git 自己拒绝覆盖本地改动，拒绝时由 git_notice 弹窗列出文件），全走 runGitOp 的统一通道
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// commitCheckoutPlan says what a double-click on a commit does.
type commitCheckoutPlan struct {
	kind   string // "here", "branch", "remote", "detach"
	target string
}

// planCommitCheckout reads the commit's own refs the way Fork does: the
// checked-out branch means nothing to do, else a local branch is switched to,
// else a remote branch becomes (or reuses) a local one, else HEAD detaches.
func planCommitCheckout(cm gitworkbench.CommitInfo) commitCheckoutPlan {
	var local, remote string
	for _, r := range cm.Refs {
		switch r.Kind {
		case gitworkbench.RefBranch:
			if r.Head {
				return commitCheckoutPlan{kind: "here", target: r.Name}
			}
			if local == "" {
				local = r.Name
			}
		case gitworkbench.RefRemote:
			if remote == "" {
				remote = r.Name
			}
		}
	}
	switch {
	case local != "":
		return commitCheckoutPlan{kind: "branch", target: local}
	case remote != "":
		return commitCheckoutPlan{kind: "remote", target: remote}
	}
	return commitCheckoutPlan{kind: "detach", target: cm.Hash}
}

// checkoutCommitRow checks out what a commit row stands for.
func (s *Shell) checkoutCommitRow(cm gitworkbench.CommitInfo) {
	if s.git == nil || s.git.root == "" || s.git.opBusy || !gitworkbenchFullHash(cm.Hash) {
		return
	}
	plan := planCommitCheckout(cm)
	switch plan.kind {
	case "here":
		s.pendingToast = "Already on " + plan.target
	case "branch":
		s.runGitOp("Check Out Branch", func(ctx context.Context, root string) (string, error) {
			if _, err := s.git.runner.SwitchBranch(ctx, root, plan.target); err != nil {
				return "", err
			}
			return "Switched to " + plan.target, nil
		})
	case "remote":
		s.runCheckoutRemote(plan.target)
	default:
		s.runGitOp("Check Out Commit", func(ctx context.Context, root string) (string, error) {
			if err := s.git.runner.CheckoutDetached(ctx, root, plan.target); err != nil {
				return "", err
			}
			return "HEAD is detached at " + plan.target[:8], nil
		})
	}
}
