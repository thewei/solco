package nativeui

/**
 * [INPUT]: gitService（remotes/upstream/worktrees/stashes/submodules）、gitworkbench.Runner 的 Pull/FetchAll/MergeSquash/CheckoutDetached/RestoreFile/ApplyPatch/CreateTagAnnotated/DeleteRemoteTag/RemoveWorktree/ResolveConflict/Reflog/AddSubmodule/RemoveSubmodule/StashPushPaths/StashClear/ParseWebRepo
 * [OUTPUT]: 对齐 Fork 第三批的编排与界面——Pull 三模式与 Fetch All（Actions 菜单）、Squash Merge、分离 HEAD 检出、从提交还原文件、应用补丁、带说明的 Tag 对话框与删除远端 Tag、Worktree 移除/作为 Workspace 打开、逐文件冲突解决、Reflog 面板、子模块新增/移除、带说明/按文件 Stash 与清空、提交的托管站点链接
 * [POS]: 与 git_fork_more.go 同层；全部走 runGitOp 的统一失败/刷新/toast 通道，确认弹窗沿用 openConfirm（带 repo root 栅栏）；不新增 Git 调用路径，规则与拒绝全在 internal/gitworkbench
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

// ---- pull and fetch --------------------------------------------------------

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

func (s *Shell) fetchAll(prune bool) {
	s.runGitNetworkOp("Fetch", func(ctx context.Context, root string) (string, error) {
		if err := s.git.runner.FetchAll(ctx, root, prune); err != nil {
			return "", err
		}
		if prune {
			return "Fetched all remotes and removed stale branches", nil
		}
		return "Fetched all remotes", nil
	})
}

// ---- merge, checkout, restore, apply ---------------------------------------

// confirmMergeSquash folds a branch into the working tree as staged changes.
func (s *Shell) confirmMergeSquash(target string) {
	if s.git == nil || s.git.root == "" || target == "" || target == s.currentBranchName() || s.git.opBusy {
		return
	}
	s.openConfirm("git-merge-squash", target, "Squash “"+target+"” Into the Working Tree?",
		"All of its changes arrive as staged changes and no commit is made — review them, then Commit. Needs a clean working tree. If files conflict, the merge is cancelled and nothing changes.")
}

func (s *Shell) confirmCheckoutCommit(hash, subject string) {
	if s.git == nil || s.git.root == "" || s.git.opBusy || !gitworkbenchFullHash(hash) {
		return
	}
	s.openConfirm("git-checkout-detached", hash, "Check Out Commit “"+hash[:8]+"”?",
		"HEAD becomes detached at this commit. Commits made there belong to no branch until you create one. Git refuses if your local changes would be overwritten.\n\n"+subject)
}

func (s *Shell) confirmRestoreFile(hash, path string) {
	if s.git == nil || s.git.root == "" || s.git.opBusy || !gitworkbenchFullHash(hash) {
		return
	}
	s.openConfirm("git-restore-file", hash+"\x00"+path, "Restore “"+filepath.Base(path)+"” From "+hash[:8]+"?",
		"Overwrites your working copy of "+path+" with that commit's version. It stays unstaged, so you can still review or discard it.")
}

// applyPatchFile asks for a patch file and applies it to the working tree.
func (s *Shell) applyPatchFile() {
	if s.git == nil || s.git.root == "" || s.git.opBusy {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: s.win, Title: "Choose a patch to apply", Message: "The patch is applied to the working tree only — nothing is staged or committed.",
		})
		s.applyOnUI(func() {
			if err != nil || len(paths) == 0 || paths[0] == "" {
				return
			}
			file := paths[0]
			s.runGitOp("Apply Patch", func(ctx context.Context, root string) (string, error) {
				if err := s.git.runner.ApplyPatch(ctx, root, file); err != nil {
					return "", err
				}
				return "Applied " + filepath.Base(file) + " to the working tree", nil
			})
		})
	}()
}

// ---- tags ------------------------------------------------------------------

type tagDialogState struct {
	open    bool
	root    string
	hash    string // "" = HEAD
	name    string
	message string
}

// openTagDialog asks for a tag name and an optional message (a message makes
// an annotated tag); hash "" tags HEAD.
func (s *Shell) openTagDialog(hash string) {
	if s.git == nil || s.git.root == "" || s.git.opBusy || (hash != "" && !gitworkbenchFullHash(hash)) {
		return
	}
	s.gitMore.tag = tagDialogState{open: true, root: s.git.root, hash: hash}
}

func (s *Shell) tagDialog(c *ui.Context) {
	d := &s.gitMore.tag
	if !d.open {
		return
	}
	title := "New Tag"
	if d.hash != "" {
		title = "New Tag Here (" + d.hash[:8] + ")"
	}
	ui.Modal(c, &d.open, func() {
		modalTitle(c, title)
		submit := false
		ui.Column(c).Gap(Spacing().M).Children(func() {
			captionedField(c, "Tag name", "", func() {
				submit = textField(c.Key("tag-name"), &d.name).Label("Tag name").AutoFocus().Width(380).Submitted()
			})
			captionedField(c, "Message", "Optional. A message makes it an annotated tag.", func() {
				textArea(c.Key("tag-message"), &d.message).Label("Tag message").Width(380).Height(76)
			})
			ui.Row(c).Gap(Spacing().M).Justify(ui.End).Children(func() {
				if regularButton(c, "Cancel").Clicked() {
					d.open = false
				}
				if regularPrimaryButton(c, "Create").Disabled(strings.TrimSpace(d.name) == "").Clicked() {
					submit = true
				}
			})
		})
		name := strings.TrimSpace(d.name)
		if !submit || name == "" {
			return
		}
		root, hash, message := d.root, d.hash, d.message
		d.open = false
		s.createTagAt(root, hash, name, message)
	})
}

// createTagAt creates the tag for the repository the dialog was opened on; a
// submit after the context moved on is refused rather than applied elsewhere.
func (s *Shell) createTagAt(root, hash, name, message string) {
	if s.git == nil || s.git.root != root {
		s.pendingToast = "Repository changed. Reopen the dialog to continue."
		return
	}
	s.runGitOp("Create Tag", func(ctx context.Context, root string) (string, error) {
		if err := s.git.runner.CreateTagAnnotated(ctx, root, name, hash, message); err != nil {
			return "", err
		}
		return "Created tag " + name, nil
	})
}

// tagRemote is the remote a tag is pushed to / removed from: the branch's own
// upstream remote, else origin, else the first one.
func (s *Shell) tagRemote() string {
	if up := s.git.upstream; up.OK && up.Remote != "" {
		return up.Remote
	}
	first := ""
	for _, r := range s.git.remotes {
		if r.Name == "origin" {
			return r.Name
		}
		if first == "" {
			first = r.Name
		}
	}
	return first
}

func (s *Shell) confirmDeleteRemoteTag(name string) {
	if s.git == nil || s.git.root == "" || s.git.opBusy {
		return
	}
	remote := s.tagRemote()
	if remote == "" {
		s.pendingToast = "This repository has no remote."
		return
	}
	s.openConfirm("git-delete-remote-tag", remote+"\x00"+name, "Delete Tag “"+name+"” From "+remote+"?",
		"Removes the tag from the remote server for everyone. Your local tag stays.")
}

// ---- worktrees -------------------------------------------------------------

func (s *Shell) worktreeMenu(m *ui.Menu, wt gitworkbench.Worktree, main bool) {
	if !wt.Bare && m.Item("Open as Workspace").Disabled(s.git.opBusy).Chosen() {
		s.createWorkspaceAt(wt.Path)
	}
	if m.Item("Copy Path").Chosen() {
		s.copyToClipboard(wt.Path)
	}
	if !wt.Bare && m.Item("Reveal in Finder").Chosen() {
		s.openExternalPath(wt.Path)
	}
	if !main {
		m.Separator()
		if m.Item("Remove Worktree…").Disabled(s.git.opBusy).Chosen() {
			s.openConfirm("git-remove-worktree", wt.Path, "Remove Worktree “"+filepath.Base(wt.Path)+"”?",
				"Deletes the folder "+wt.Path+". Git refuses if it has uncommitted changes. The branch it had checked out stays.")
		}
	}
}

// ---- conflicts and stash by file -------------------------------------------

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

func (s *Shell) stashFile(path string) {
	s.runGitOp("Stash", func(ctx context.Context, root string) (string, error) {
		if err := s.git.runner.StashPushPaths(ctx, root, "", []string{path}); err != nil {
			return "", err
		}
		return "Stashed " + path, nil
	})
}

func (s *Shell) confirmDropAllStashes() {
	if s.git == nil || s.git.root == "" || s.git.opBusy || len(s.git.stashes) == 0 {
		return
	}
	s.openConfirm("git-stash-clear", "", "Drop All "+itoa(len(s.git.stashes))+" Stashes?",
		"Every stashed change is deleted permanently.")
}

// ---- reflog ----------------------------------------------------------------

type reflogSheetState struct {
	open    bool
	loading bool
	err     string
	entries []gitworkbench.ReflogEntry
	list    ui.ListState
	gen     int
}

func (s *Shell) openReflogSheet() {
	if s.git == nil || s.git.root == "" {
		return
	}
	r := &s.gitMore.reflog
	r.gen++
	gen := r.gen
	*r = reflogSheetState{open: true, loading: true, gen: gen}
	root := s.git.root
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), gitworkbench.ReadTimeout*2)
		defer cancel()
		entries, err := s.git.runner.Reflog(ctx, root, gitworkbench.MaxReflogEntries)
		s.applyOnUI(func() {
			r := &s.gitMore.reflog
			if r.gen != gen || s.git == nil || s.git.root != root {
				return
			}
			r.loading = false
			if err != nil {
				r.err = gitFailureText(err)
				return
			}
			r.entries = entries
		})
	}()
}

func (s *Shell) reflogSheet(c *ui.Context) {
	r := &s.gitMore.reflog
	if !r.open {
		return
	}
	t := c.Theme()
	ui.Modal(c, &r.open, func() {
		ui.Column(c).Width(760).MaxWidth(900).Height(520).MinHeight(240).MinWidth(0).Gap(Spacing().M).Children(func() {
			modalTitle(c, "Reflog")
			ui.Text(c, "Where HEAD has been, newest first. Right-click an entry to go back to it.").
				FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			switch {
			case r.loading:
				busyNote(c, "Reading…")
			case r.err != "":
				ui.Text(c, r.err).FontSize(Typography().Body).TextColor(t.Warning)
			case len(r.entries) == 0:
				ui.Text(c, "HEAD has not moved yet.").FontSize(Typography().Body).TextColor(t.TextMuted)
			default:
				ui.List(c, &r.list, len(r.entries), func(i int) {
					if i < 0 || i >= len(r.entries) {
						return
					}
					s.reflogRow(c, r.entries[i])
				}).Grow(1).MinHeight(0)
			}
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				if regularPrimaryButton(c, "Close").Clicked() {
					r.open = false
				}
			})
		})
	})
}

func (s *Shell) reflogRow(c *ui.Context, e gitworkbench.ReflogEntry) {
	t := c.Theme()
	row := ui.Row(c).Gap(8).Padding(4, 8).Radius(treeRowRadius).AlignItems(ui.Center).MinWidth(0)
	if row.Hovered() {
		row.Background(designTokens(t.Dark).SidebarHover)
	}
	if row.Clicked() {
		s.gitMore.reflog.open = false
		s.showGitCommits()
		s.reviewCommit(e.Hash)
	}
	tipped(c, row, e.Action+"\n"+e.Subject+" · "+gdCommitTime(e.Time)+"\n"+e.Selector+" · "+e.Hash)
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Check Out This Commit…").Disabled(s.git.opBusy).Chosen() {
			s.gitMore.reflog.open = false
			s.confirmCheckoutCommit(e.Hash, e.Subject)
		}
		if m.Item("New Branch Here…").Disabled(s.git.opBusy).Chosen() {
			s.gitMore.reflog.open = false
			s.openCreateAt("git-create-branch-at", e.Hash)
		}
		m.Submenu("Reset Current Branch to Here", func(sub *ui.Menu) {
			for _, mode := range []gitworkbench.ResetMode{gitworkbench.ResetSoft, gitworkbench.ResetMixed, gitworkbench.ResetHard} {
				if sub.Item(resetModeText[mode][0] + "…").Disabled(s.git.opBusy).Chosen() {
					s.gitMore.reflog.open = false
					s.confirmResetTo(e.Hash, e.Subject, mode)
				}
			}
		})
		m.Separator()
		if m.Item("Copy Commit Hash").Chosen() {
			s.copyToClipboard(e.Hash)
		}
	})
	kind, detail := reflogParts(e.Action)
	row.Children(func() {
		// kind chip · what happened · commit · how long ago: the selector
		// (HEAD@{n}) is in the tooltip, where nobody reads it by eye.
		ui.Row(c).Size(70, 18).Shrink(0).Center().Radius(treeRowRadius).
			Background(t.TextMuted.Alpha(0.14)).Children(func() {
			ui.Text(c, kind).FontSize(Typography().Caption).FontWeight(WeightMedium).TextColor(t.TextMuted).SingleLine()
		})
		ui.Text(c, detail).FontSize(Typography().Body).Grow(1).MinWidth(0).SingleLine()
		ui.Text(c, e.Hash[:8]).Font(gdCodeFont()).FontSize(Typography().BodySmall).TextColor(t.Accent).Shrink(0).SingleLine()
		ui.Text(c, gdRelativeTime(e.Time)).FontSize(Typography().BodySmall).TextColor(t.TextMuted).
			Width(72).TextAlign(ui.End).Shrink(0).SingleLine()
	})
}

// reflogParts splits a reflog message into the kind of move (its first word)
// and the rest: "rebase (finish): returning to x" → "rebase", "(finish) ·
// returning to x".
func reflogParts(action string) (kind, detail string) {
	head, msg, _ := strings.Cut(strings.TrimSpace(action), ": ")
	kind, mod, _ := strings.Cut(head, " ")
	switch {
	case kind == "":
		return "head", action
	case mod == "":
		return kind, msg
	case msg == "":
		return kind, mod
	}
	return kind, mod + " · " + msg
}

// ---- submodules ------------------------------------------------------------

type submoduleDialogState struct {
	open bool
	root string
	url  string
	path string
}

func (s *Shell) openAddSubmoduleDialog() {
	if s.git == nil || s.git.root == "" || s.git.opBusy {
		return
	}
	s.gitMore.submodule = submoduleDialogState{open: true, root: s.git.root}
}

// submoduleFolder guesses the folder from the address: the repository name.
func submoduleFolder(address string) string {
	address = strings.TrimSuffix(strings.TrimSpace(address), "/")
	address = strings.TrimSuffix(address, ".git")
	if i := strings.LastIndexAny(address, "/:"); i >= 0 {
		address = address[i+1:]
	}
	return address
}

func (s *Shell) submoduleDialog(c *ui.Context) {
	d := &s.gitMore.submodule
	if !d.open {
		return
	}
	ui.Modal(c, &d.open, func() {
		modalTitle(c, "Add Submodule")
		submit := false
		ui.Column(c).Gap(Spacing().M).Children(func() {
			captionedField(c, "Repository address", "", func() {
				submit = textField(c.Key("sub-url"), &d.url).Label("Repository address").Placeholder("https://github.com/you/lib.git").AutoFocus().Width(380).Submitted()
			})
			captionedField(c, "Folder", "Optional. Defaults to the repository name.", func() {
				submit = textField(c.Key("sub-path"), &d.path).Label("Submodule folder").Placeholder(fallbackText(submoduleFolder(d.url), "lib")).Width(380).Submitted() || submit
			})
			ui.Row(c).Gap(Spacing().M).Justify(ui.End).Children(func() {
				if regularButton(c, "Cancel").Clicked() {
					d.open = false
				}
				if regularPrimaryButton(c, "Add").Disabled(strings.TrimSpace(d.url) == "").Clicked() {
					submit = true
				}
			})
		})
		address := strings.TrimSpace(d.url)
		if !submit || address == "" {
			return
		}
		folder := strings.TrimSpace(d.path)
		if folder == "" {
			folder = submoduleFolder(address)
		}
		root := d.root
		d.open = false
		if s.git == nil || s.git.root != root {
			s.pendingToast = "Repository changed. Reopen the dialog to continue."
			return
		}
		s.runGitNetworkOp("Add Submodule", func(ctx context.Context, root string) (string, error) {
			if err := s.git.runner.AddSubmodule(ctx, root, address, folder); err != nil {
				return "", err
			}
			return "Added submodule " + folder + " — Commit to record it", nil
		})
	})
}

func (s *Shell) confirmRemoveSubmodule(path string) {
	if s.git == nil || s.git.root == "" || s.git.opBusy {
		return
	}
	s.openConfirm("git-remove-submodule", path, "Remove Submodule “"+path+"”?",
		"Unregisters it and deletes its folder; the removal is staged for the next Commit. Git refuses if the submodule has local changes.")
}

// ---- hosting links ---------------------------------------------------------

// commitWebRepo reads the remote a commit link points at: the branch's own
// remote, else origin. ok is false for a local-path or unparsable remote.
func (s *Shell) commitWebRepo() (gitworkbench.WebRepo, bool) {
	if s.git == nil {
		return gitworkbench.WebRepo{}, false
	}
	remote := s.tagRemote()
	if remote == "" {
		return gitworkbench.WebRepo{}, false
	}
	return gitworkbench.ParseWebRepo(s.remoteURL(remote))
}

// commitLinkItems adds "Copy Commit Link" / "Open on <Host>" to a commit menu
// when the remote is a web-hosted repository.
func (s *Shell) commitLinkItems(m *ui.Menu, hash string) {
	web, ok := s.commitWebRepo()
	if !ok {
		return
	}
	link := web.CommitURL(hash)
	if m.Item("Copy Commit Link").Chosen() {
		s.copyToClipboard(link)
	}
	if m.Item("Open on " + web.Name()).Chosen() {
		go func() { _ = mygo.Shell.OpenExternal(link) }()
	}
}
