package nativeui

/**
 * [INPUT]: gitworkbench Runner/selected repository snapshot, native dialogs and UI dispatcher
 * [OUTPUT]: background Stage/Unstage/Discard, Fetch/Pull/Push, FF/no-FF/Squash Merge, branch delete and merge/rebase Continue-Abort execution with failure refresh;
 * network ops (Fetch/Pull/Push) run under the Runner's 120s network budget; every op logs
 * start/duration/outcome class (never stderr, never repository content), and a click landing
 * while an op runs answers with a toast instead of vanishing
 * [POS]: presentation action orchestration only; sole Git command authority remains solco-gitkit/gitworkbench
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/thewei/solco-gitkit/gitworkbench"
)

// Explicit repository operations behind the Changes surface, orchestrated
// over the gitworkbench Runner: every action below runs in a background lane
// on explicit user intent, refreshes the snapshot, and reports through a
// toast. Destructive actions reach here only through the confirm dialog.

// gitOpTimeout bounds one working-tree operation.
const gitOpTimeout = gitworkbench.MutateTimeout

// gitNetworkTimeout bounds one explicit fetch/pull/push: the Runner's own
// network budget. The 30s working-tree budget must not parent these —
// Runner.run only tightens a parent deadline, never extends one — so the
// old shared 30s parent killed every push that took longer than half a
// minute, and large pushes could then never land again (each failure left
// the pile, and the payload, bigger).
const gitNetworkTimeout = gitworkbench.NetworkTimeout

// runGitOp runs one repository mutation off the UI lane and reports the
// outcome as a toast; successful ops refresh the snapshot.
func (s *Shell) runGitOp(name string, run func(ctx context.Context, root string) (string, error)) {
	s.runGitOpBudget(name, gitOpTimeout, run)
}

// runGitNetworkOp is runGitOp under the network budget, for Fetch/Pull/Push.
func (s *Shell) runGitNetworkOp(name string, run func(ctx context.Context, root string) (string, error)) {
	s.runGitOpBudget(name, gitNetworkTimeout, run)
}

func (s *Shell) runGitOpBudget(name string, budget time.Duration, run func(ctx context.Context, root string) (string, error)) {
	s.runGitOpFlow(name, budget, run, false)
}

// runGitOpFlow is the one runner. retried marks a run that already went
// through "Stash and reapply", whose refusal is not offered that way again.
func (s *Shell) runGitOpFlow(name string, budget time.Duration, run func(ctx context.Context, root string) (string, error), retried bool) {
	if s.git == nil || s.git.root == "" {
		return
	}
	if s.git.opBusy {
		// The buttons dim while an op runs, but a click can still land
		// (stale frame, menu entry). Swallowing it silently is what made
		// Push feel dead; say what is happening instead.
		s.pendingToast = "Another Git operation is still running."
		return
	}
	root := s.git.root
	s.git.opBusy = true
	slog.Info("git op start", "op", name, "root", root)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		started := time.Now()
		notice, err := run(ctx, root)
		var conflictPaths []string
		var mergePaused bool
		if err != nil && integrationOps[name] {
			if ge, ok := gitworkbench.GitError(err); ok && len(ge.Paths) > 0 {
				conflictPaths = ge.Paths
			} else if st, serr := s.git.runner.SequencerState(ctx, root); serr == nil {
				conflictPaths, mergePaused = st.UnmergedPaths, st.Kind == gitworkbench.SequencerMerge && st.Unmerged > 0
			}
		}
		slog.Info("git op done", "op", name, "duration_ms", time.Since(started).Milliseconds(), "ok", err == nil, "err_class", gitErrClass(err))
		offerAutostash := !retried && autostashOps[name] && gitworkbench.LocalChangesBlocked(err)
		s.applyOnUI(func() {
			s.git.opBusy = false
			if s.git == nil || s.git.root != root {
				return
			}
			switch {
			case err != nil && offerAutostash && len(conflictPaths) == 0 && !mergePaused:
				// Local changes stand in the way: say so, and offer Fork's
				// "Stash and reapply" as the way through.
				s.openGitNotice(name, err, checkoutBlockedPaths(err), false)
				s.offerAutostash(name, budget, run)
			case err != nil && integrationOps[name]:
				s.openGitNotice(name, err, conflictPaths, mergePaused)
			case err != nil && checkoutOps[name] && len(checkoutBlockedPaths(err)) > 0:
				s.openGitNotice(name, err, checkoutBlockedPaths(err), false)
			case err != nil:
				s.pendingToast = name + " failed: " + gitFailureText(err)
				// A raw git exit line tells a user nothing here; the one
				// refusal a sync button can produce on purpose deserves its
				// own sentence (device E2E 2026-11-03).
				if name == "Pull" && s.git.upstream.OK && s.git.upstream.Ahead > 0 && s.git.upstream.Behind > 0 {
					s.pendingToast = "Pull failed: local and " + s.git.upstream.Branch + " have diverged — merge or rebase first"
				}
				// A failed Git mutation can leave MERGE_HEAD/REVERT_HEAD and
				// conflict markers: the re-read below shows them rather than a
				// stale clean state from before the attempted operation.
			case notice != "":
				s.pendingToast = notice
			}
			s.git.cache.Invalidate(root)
			s.git.preflight = nil
			s.ensureGitSnapshot(true)
			s.loadBranches()
		})
	}()
}

// gitErrClass names the coarse failure class for logs without echoing any
// stderr text (remote URLs and ref names stay out of the log).
func gitErrClass(err error) string {
	if err == nil {
		return ""
	}
	if ge, ok := gitworkbench.GitError(err); ok {
		return string(ge.Class)
	}
	return "error"
}

// gitFailureText is the one sentence a failed Git operation shows: Git's own
// reason read from its output (rejected, sign-in, network, remote hook …),
// else the domain's refusal text, else the first error line. "exit status 1"
// alone told a user nothing (device E2E 2026-11-03: Push "did not pass").
func gitFailureText(err error) string {
	if reason := gitworkbench.Explain(err); reason != "" {
		return reason
	}
	if ge, ok := gitworkbench.GitError(err); ok && ge.Stderr == "" {
		if inner := errors.Unwrap(ge); inner != nil {
			return strings.TrimSpace(inner.Error())
		}
	}
	return firstErrLine(err)
}

func firstErrLine(err error) string {
	line := err.Error()
	if i := strings.IndexByte(line, '\n'); i > 0 {
		line = line[:i]
	}
	if len(line) > 200 {
		line = line[:200]
	}
	return strings.TrimSpace(line)
}

// stageFiles moves the worktree changes of paths into the index.
func (s *Shell) stageFiles(paths []string) {
	if len(paths) == 0 {
		return
	}
	s.runGitOp("Stage", func(ctx context.Context, root string) (string, error) {
		if err := s.git.runner.Stage(ctx, root, paths); err != nil {
			return "", err
		}
		return "Staged " + pluralFiles(len(paths)), nil
	})
}

// unstageFiles moves the index changes of paths back to the worktree.
func (s *Shell) unstageFiles(paths []string) {
	if len(paths) == 0 {
		return
	}
	noHead := s.git.snapshot != nil && s.git.snapshot.NoHead
	s.runGitOp("Unstage", func(ctx context.Context, root string) (string, error) {
		if err := s.git.runner.Unstage(ctx, root, noHead, paths); err != nil {
			return "", err
		}
		return "Unstaged " + pluralFiles(len(paths)), nil
	})
}

// discardChanges throws away worktree changes after the confirm dialog.
// kind "git-discard" carries tracked paths, "git-delete-untracked" carries
// untracked files (deleted, not recoverable — hence the confirmation).
func (s *Shell) discardChanges(paths []string) {
	tracked, untracked := s.splitTrackedUntracked(paths)
	if len(tracked) > 0 {
		s.openConfirm("git-discard", joinPathsTarget(tracked),
			"Discard Changes in "+pluralFiles(len(tracked))+"?",
			"This can't be undone.")
	}
	if len(untracked) > 0 {
		s.openConfirm("git-delete-untracked", joinPathsTarget(untracked),
			"Delete "+pluralFiles(len(untracked))+"?",
			"Untracked files can't be recovered.")
	}
}

func (s *Shell) fetchRepo() {
	s.runGitNetworkOp("Fetch", func(ctx context.Context, root string) (string, error) {
		remote := s.git.runner.DefaultRemote(ctx, root)
		if err := s.git.runner.Fetch(ctx, root, remote); err != nil {
			return "", err
		}
		return "Fetched " + remote, nil
	})
}

// pullRepo is the header Pull: fast-forward only. The branch's own upstream
// decides where it pulls from — the first remote and the same-named branch
// are only the guess for a branch that has none (pullWith).
func (s *Shell) pullRepo() { s.pullWith(gitworkbench.PullFastForward, "Pull") }

func (s *Shell) pushRepo() {
	s.runGitNetworkOp("Push", func(ctx context.Context, root string) (string, error) {
		remote := s.git.runner.DefaultRemote(ctx, root)
		branch := s.git.runner.CurrentBranch(ctx, root)
		if branch == "" {
			return "", errGitOp("detached HEAD: check out a branch first")
		}
		setUpstream := !s.git.upstream.OK
		theirs := branch
		if up := s.git.upstream; up.OK && up.Remote != "" {
			remote, theirs = up.Remote, up.Branch
		}
		if err := s.git.runner.PushTo(ctx, root, remote, branch, theirs, setUpstream); err != nil {
			return "", err
		}
		notice := "Pushed " + remote + "/" + theirs
		if setUpstream {
			notice += " (tracking set)"
		}
		return notice, nil
	})
}

func (s *Shell) deleteBranch(name string) {
	s.openConfirm("git-delete-branch", name, "Delete Branch “"+name+"”?",
		"Git refuses if it has unmerged commits.")
}

// confirmMergeBranch only offers a non-rewriting, fast-forward-only merge.
// A divergent target cannot create a surprise merge commit through this path.
func (s *Shell) confirmMergeBranch(target string) {
	if s.git == nil || s.git.root == "" || target == "" || target == s.currentBranchName() {
		return
	}
	s.openConfirm("git-merge-ff", target, "Fast-Forward Merge “"+target+"”?",
		"Moves the current branch forward to "+target+". Refused if the branches have diverged or the working tree is dirty.")
}

// openMergeDialog lets one branch-menu action explain both strategies
// before the user reaches a transaction confirmation.
func (s *Shell) openMergeDialog(target string) {
	if s.git == nil || s.git.root == "" || target == "" || target == s.currentBranchName() || s.git.opBusy {
		return
	}
	s.mergeTarget = target
	s.mergeRepoRoot = s.git.root
	s.mergeStrategy = 0
	s.mergeDialogOpen = true
}

// confirmMergeNoFFBranch is separate from the original safe FF-only path.
// This operation may leave conflicts; Local Changes exposes resolution.
func (s *Shell) confirmMergeNoFFBranch(target string) {
	if s.git == nil || s.git.root == "" || target == "" || target == s.currentBranchName() {
		return
	}
	s.openConfirm("git-merge-no-ff", target, "Create Merge Commit With “"+target+"”?",
		"Merges "+target+" into the current branch. If files conflict, resolve them in Local Changes, then continue or abort.")
}

// confirmMergeSquash folds a branch into the working tree as staged changes.
func (s *Shell) confirmMergeSquash(target string) {
	if s.git == nil || s.git.root == "" || target == "" || target == s.currentBranchName() || s.git.opBusy {
		return
	}
	s.openConfirm("git-merge-squash", target, "Squash “"+target+"” Into the Working Tree?",
		"All of its changes arrive as staged changes and no commit is made — review them, then Commit. Needs a clean working tree. If files conflict, the merge is cancelled and nothing changes.")
}

func (s *Shell) continueSequencer() {
	if s.git == nil || !s.git.sequencerChecked || s.git.sequencer.Unmerged != 0 {
		return
	}
	kind := s.git.sequencer.Kind
	if kind == gitworkbench.SequencerNone || kind == gitworkbench.SequencerUnsupported {
		return
	}
	s.runGitOp("Continue "+string(kind), func(ctx context.Context, root string) (string, error) {
		if err := s.git.runner.FinishSequencer(ctx, root, kind, false); err != nil {
			return "", err
		}
		return "Completed " + string(kind), nil
	})
}

// sequencerTitle capitalizes the sequencer kind for the confirm title, so
// dialog titles share the Title Case of the confirm family (F158).
func sequencerTitle(kind gitworkbench.SequencerKind) string {
	if kind == "" {
		return ""
	}
	s := string(kind)
	return strings.ToUpper(s[:1]) + s[1:]
}

func (s *Shell) confirmAbortSequencer() {
	if s.git == nil || !s.git.sequencerChecked {
		return
	}
	kind := s.git.sequencer.Kind
	if kind == gitworkbench.SequencerNone || kind == gitworkbench.SequencerUnsupported {
		return
	}
	s.openConfirm("git-sequencer-abort", string(kind), "Abort "+sequencerTitle(kind)+"?",
		"Returns to the state before the operation. Edits made while resolving conflicts are lost.")
}

// confirmations dispatch (from dialogs.submitConfirm).

func (s *Shell) runGitConfirm(kind, target string) {
	switch kind {
	case "git-switch-branch":
		s.switchBranch(target)
	case "git-discard":
		s.runGitOp("Discard", func(ctx context.Context, root string) (string, error) {
			paths := splitPathsTarget(target)
			if err := s.git.runner.Discard(ctx, root, paths); err != nil {
				return "", err
			}
			return "Discarded changes in " + pluralFiles(len(paths)), nil
		})
	case "git-delete-untracked":
		s.runGitOp("Delete", func(ctx context.Context, root string) (string, error) {
			paths := splitPathsTarget(target)
			if err := s.git.runner.DeleteUntracked(root, paths); err != nil {
				return "", err
			}
			return "Deleted " + pluralFiles(len(paths)), nil
		})
	case "git-delete-branch":
		s.runGitOp("Delete Branch", func(ctx context.Context, root string) (string, error) {
			if err := s.git.runner.DeleteBranch(ctx, root, target); err != nil {
				return "", err
			}
			return "Deleted branch " + target, nil
		})
	case "git-merge-ff":
		s.runGitOp("Fast-forward Merge", func(ctx context.Context, root string) (string, error) {
			if err := s.git.runner.MergeFastForward(ctx, root, target); err != nil {
				return "", err
			}
			return "Fast-forwarded current branch to " + target, nil
		})
	case "git-merge-no-ff":
		s.runGitOp("Merge Commit", func(ctx context.Context, root string) (string, error) {
			if err := s.git.runner.MergeNoFastForward(ctx, root, target); err != nil {
				return "", err
			}
			return "Merged branch " + target, nil
		})
	case "git-merge-squash":
		s.runGitOp("Squash Merge", func(ctx context.Context, root string) (string, error) {
			if err := s.git.runner.MergeSquash(ctx, root, target); err != nil {
				return "", err
			}
			return "Squashed " + target + " into the working tree — review and Commit", nil
		})
	case "git-sequencer-abort":
		s.runGitOp("Abort "+target, func(ctx context.Context, root string) (string, error) {
			if err := s.git.runner.FinishSequencer(ctx, root, gitworkbench.SequencerKind(target), true); err != nil {
				return "", err
			}
			return "Aborted " + target, nil
		})
	}
}

// amendPrefill loads HEAD's message into the Commit surface for an amend.
func (s *Shell) amendPrefill() {
	if s.git == nil || s.git.root == "" {
		return
	}
	root := s.git.root
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), gitworkbench.ReadTimeout)
		defer cancel()
		subject, body, err := s.git.runner.LastCommitMessage(ctx, root)
		s.applyOnUI(func() {
			if err != nil || s.git == nil || s.git.root != root {
				return
			}
			if strings.TrimSpace(s.surface.commit.Subject) == "" {
				s.surface.commit.Subject = subject
			}
			if strings.TrimSpace(s.surface.commit.Body) == "" {
				s.surface.commit.Body = body
			}
		})
	}()
}

// pluralFiles writes "1 file" / "N files": the one way a file count is spelled.
func pluralFiles(n int) string { return itoa(n) + " file" + pluralS(n) }

// splitTrackedUntracked sorts paths by their snapshot status: untracked
// files delete from disk, tracked ones restore from HEAD.
func (s *Shell) splitTrackedUntracked(paths []string) (tracked, untracked []string) {
	for _, p := range paths {
		if cf, ok := s.selectedChangeFileInfo(p); ok && cf.Untracked {
			untracked = append(untracked, p)
		} else {
			tracked = append(tracked, p)
		}
	}
	return tracked, untracked
}

func joinPathsTarget(paths []string) string { return strings.Join(paths, "\x00") }
func splitPathsTarget(target string) []string {
	return strings.Split(target, "\x00")
}

type errGitOp string

func (e errGitOp) Error() string { return string(e) }
