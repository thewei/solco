package nativeui

/**
 * [INPUT]: gitService（remotes/submodules/branches/stashes）、gitworkbench.Runner 的 ResetTo/DeleteRemoteBranch/SetUpstream/FormatPatch/CompareToWorktree/FileHistory/Blame/Add/Set/Rename/RemoveRemote、UpdateSubmodule/RebasePlan/InteractiveRebase
 * [OUTPUT]: 对齐 Fork 第二批的编排与界面——提交右键的重置/补丁/对比/交互式变基、远程分支删除与跟踪、远程管理对话框、子模块、文件历史与 Blame 面板、交互式变基面板、Commit and Push
 * [POS]: 与 git_ref_actions.go 同层；全部走 runGitOp 的统一失败/刷新/toast 通道，不新增 Git 调用路径；确认弹窗沿用 openConfirm（带 repo root 栅栏）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

// gitMoreState is the presentation state of the sheets and dialogs below.
type gitMoreState struct {
	// pushAfterCommit makes the Commit dialog's success continue with a Push
	// ("Commit and Push…"); cleared whenever the dialog opens or is cancelled.
	pushAfterCommit bool

	remote remoteDialogState
	rebase rebaseSheetState
	file   fileSheetState

	autoFetchTimer *time.Timer
}

// ---- confirms --------------------------------------------------------------

func (s *Shell) confirmDeleteRemoteBranch(ref string) {
	if s.git == nil || s.git.root == "" || s.git.opBusy {
		return
	}
	s.openConfirm("git-delete-remote-branch", ref, "Delete Remote Branch “"+ref+"”?",
		"Removes the branch from the remote server for everyone. Local branches are not touched.")
}

var resetModeText = map[gitworkbench.ResetMode][2]string{
	gitworkbench.ResetSoft:  {"Soft", "Moves the current branch to this commit. The commits after it become staged changes."},
	gitworkbench.ResetMixed: {"Mixed", "Moves the current branch to this commit. The commits after it become unstaged changes; nothing is deleted."},
	gitworkbench.ResetHard:  {"Hard", "Moves the current branch to this commit and rewrites the files to match. Needs a clean working tree; the dropped commits stay in Git's reflog."},
}

func (s *Shell) confirmResetTo(hash, subject string, mode gitworkbench.ResetMode) {
	if s.git == nil || s.git.root == "" || s.git.opBusy || !gitworkbenchFullHash(hash) {
		return
	}
	text := resetModeText[mode]
	s.openConfirm("git-reset-"+string(mode), hash, "Reset Current Branch to Here ("+text[0]+")?",
		text[1]+"\n\n"+subject)
}

func (s *Shell) runReset(hash string, mode gitworkbench.ResetMode) {
	s.runGitOp("Reset", func(ctx context.Context, root string) (string, error) {
		previous, err := s.git.runner.ResetTo(ctx, root, hash, mode)
		if err != nil {
			return "", err
		}
		note := "Branch reset (" + string(mode) + ")"
		if len(previous) >= 7 {
			note += " — it was at " + previous[:7]
		}
		return note, nil
	})
}

func (s *Shell) runDeleteRemoteBranch(ref string) {
	remote, name, ok := strings.Cut(ref, "/")
	if !ok {
		return
	}
	s.runGitNetworkOp("Delete Remote Branch", func(ctx context.Context, root string) (string, error) {
		if err := s.git.runner.DeleteRemoteBranch(ctx, root, remote, name); err != nil {
			return "", err
		}
		return "Deleted " + ref + " on the server", nil
	})
}

// ---- tracking --------------------------------------------------------------

func (s *Shell) trackingMenu(m *ui.Menu, branch string) {
	var current string
	for _, b := range s.git.branches {
		if b.Name == branch {
			current = b.Upstream
		}
	}
	m.Submenu("Tracking", func(sub *ui.Menu) {
		shown := 0
		for _, rb := range s.git.remoteBranches {
			if shown == 40 {
				break
			}
			shown++
			ref := rb.Remote + "/" + rb.Name
			if sub.Item(ref).Checked(ref == current).Disabled(s.git.opBusy).Chosen() {
				s.setUpstream(branch, ref)
			}
		}
		if shown > 0 {
			sub.Separator()
		}
		if sub.Item("Stop Tracking").Disabled(current == "" || s.git.opBusy).Chosen() {
			s.runGitOp("Unset Upstream", func(ctx context.Context, root string) (string, error) {
				if err := s.git.runner.UnsetUpstream(ctx, root, branch); err != nil {
					return "", err
				}
				return branch + " no longer tracks anything", nil
			})
		}
	})
}

func (s *Shell) setUpstream(branch, ref string) {
	s.runGitOp("Set Upstream", func(ctx context.Context, root string) (string, error) {
		if err := s.git.runner.SetUpstream(ctx, root, branch, ref); err != nil {
			return "", err
		}
		return branch + " now tracks " + ref, nil
	})
}

// ---- patch and compare -----------------------------------------------------

// savePatch asks where to write one commit's patch, then writes it.
func (s *Shell) savePatch(hash, subject string) {
	if !gitworkbenchFullHash(hash) {
		return
	}
	s.savePatchFile("Save commit as patch", hash[:8]+"-"+patchFileStem(fallbackText(subject, hash[:8])),
		func(ctx context.Context, root string) ([]byte, error) {
			return s.git.runner.FormatPatch(ctx, root, hash)
		})
}

// saveStashPatch writes one stash entry (untracked files included) as a patch.
func (s *Shell) saveStashPatch(ref, subject string) {
	s.savePatchFile("Save stash as patch", "stash-"+patchFileStem(stashTitle(subject)),
		func(ctx context.Context, root string) ([]byte, error) { return s.git.runner.StashPatch(ctx, root, ref) })
}

// saveWorkingPatch writes the working changes — staged, unstaged and
// untracked — as one patch, without touching the repository's own index.
func (s *Shell) saveWorkingPatch() {
	branch := "changes"
	if snap := s.gitSnapshot(); snap != nil && snap.Branch != "" {
		branch = snap.Branch
	}
	s.savePatchFile("Save changes as patch", "changes-"+patchFileStem(branch),
		func(ctx context.Context, root string) ([]byte, error) { return s.git.runner.WorkingPatch(ctx, root) })
}

// patchFileStem makes a subject safe and short enough for a file name.
func patchFileStem(text string) string {
	name := strings.Map(func(r rune) rune {
		if r == '/' || r == ':' || r == '\\' || r == ' ' {
			return '-'
		}
		return r
	}, text)
	if len(name) > 60 {
		name = strings.ToValidUTF8(name[:60], "")
	}
	return name
}

// savePatchFile produces a patch off the UI thread, asks for a destination
// and writes it. A refusal or write failure is a toast, never silent.
func (s *Shell) savePatchFile(title, stem string, produce func(ctx context.Context, root string) ([]byte, error)) {
	if s.git == nil || s.git.root == "" {
		return
	}
	root := s.git.root
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), gitOpTimeout)
		defer cancel()
		patch, err := produce(ctx, root)
		s.applyOnUI(func() {
			if err != nil {
				s.pendingToast = "Save as Patch failed: " + gitFailureText(err)
				return
			}
			path, derr := mygo.Dialog.Save(mygo.SaveDialogOptions{
				Parent: s.win, Title: title, DefaultPath: stem + ".patch", ButtonLabel: "Save",
			})
			if derr != nil || path == "" {
				return
			}
			if werr := os.WriteFile(path, patch, 0o644); werr != nil {
				s.pendingToast = "Could not write the patch: " + werr.Error()
				return
			}
			s.pendingToast = "Patch saved to " + filepath.Base(path)
		})
	}()
}

// compareToWorktree reviews one commit against the working tree.
func (s *Shell) compareToWorktree(hash string) {
	s.reviewCommitAs(hash, true)
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

// ---- remotes ---------------------------------------------------------------

type remoteDialogState struct {
	open     bool
	mode     string // "add" | "url" | "rename"
	name     string
	url      string
	newName  string
	original string // the remote being edited
}

func (s *Shell) openRemoteDialog(mode, remote string) {
	if s.git == nil || s.git.root == "" || s.git.opBusy {
		return
	}
	d := &s.gitMore.remote
	*d = remoteDialogState{open: true, mode: mode, original: remote, newName: remote}
	if mode == "url" {
		for _, r := range s.git.remotes {
			if r.Name == remote {
				d.url = r.FetchURL
			}
		}
	}
}

func (s *Shell) fetchRemote(remote string) {
	s.runGitNetworkOp("Fetch", func(ctx context.Context, root string) (string, error) {
		if err := s.git.runner.Fetch(ctx, root, remote); err != nil {
			return "", err
		}
		return "Fetched " + remote, nil
	})
}

func (s *Shell) remoteMenu(m *ui.Menu, remote string) {
	if m.Item("Fetch").Disabled(s.git.opBusy).Chosen() {
		s.fetchRemote(remote)
	}
	m.Separator()
	if m.Item("Edit Remote URL…").Disabled(s.git.opBusy).Chosen() {
		s.openRemoteDialog("url", remote)
	}
	if m.Item("Rename Remote…").Disabled(s.git.opBusy).Chosen() {
		s.openRemoteDialog("rename", remote)
	}
	if m.Item("Remove Remote…").Disabled(s.git.opBusy).Chosen() {
		s.openConfirm("git-remove-remote", remote, "Remove Remote “"+remote+"”?",
			"Forgets the remote and its remote-tracking branches. Nothing changes on the server.")
	}
}

// remoteURL is the configured fetch address of a remote ("" when unknown).
func (s *Shell) remoteURL(name string) string {
	for _, r := range s.git.remotes {
		if r.Name == name {
			return r.FetchURL
		}
	}
	return ""
}

// remotesWithoutBranches are configured remotes nothing has been fetched
// from yet; the tree is built from branches, so they would otherwise be
// invisible.
func (s *Shell) remotesWithoutBranches() []gitworkbench.Remote {
	var out []gitworkbench.Remote
	for _, r := range s.git.remotes {
		has := false
		for _, b := range s.git.remoteBranches {
			if b.Remote == r.Name {
				has = true
				break
			}
		}
		if !has {
			out = append(out, r)
		}
	}
	return out
}

func (s *Shell) remoteDialog(c *ui.Context) {
	d := &s.gitMore.remote
	if !d.open {
		return
	}
	title, action := "Add Remote", "Add"
	switch d.mode {
	case "url":
		title, action = "Edit Remote URL", "Save"
	case "rename":
		title, action = "Rename Remote", "Rename"
	}
	ui.Modal(c, &d.open, func() {
		modalTitle(c, title)
		submit := false
		ui.Column(c).Gap(Spacing().M).Children(func() {
			switch d.mode {
			case "add":
				f := textField(c.Key("remote-name"), &d.name).Label("Remote name").Placeholder("origin").AutoFocus().Width(380)
				u := textField(c.Key("remote-url"), &d.url).Label("Address").Placeholder("https://github.com/you/repo.git or git@host:you/repo.git").Width(380)
				submit = f.Submitted() || u.Submitted()
			case "url":
				ui.Text(c, d.original).FontSize(Typography().Body).FontWeight(WeightSemibold)
				u := textField(c.Key("remote-url"), &d.url).Label("Address").AutoFocus().Width(380)
				submit = u.Submitted()
			case "rename":
				f := textField(c.Key("remote-new"), &d.newName).Label("New name for " + d.original).AutoFocus().Width(380)
				submit = f.Submitted()
			}
			ui.Row(c).Gap(Spacing().M).Justify(ui.End).Children(func() {
				if regularButton(c, "Cancel").Clicked() {
					d.open = false
				}
				if regularPrimaryButton(c, action).Clicked() {
					submit = true
				}
			})
		})
		if !submit {
			return
		}
		mode, name, url, newName, original := d.mode, strings.TrimSpace(d.name), strings.TrimSpace(d.url), strings.TrimSpace(d.newName), d.original
		d.open = false
		switch mode {
		case "add":
			s.runGitOp("Add Remote", func(ctx context.Context, root string) (string, error) {
				if err := s.git.runner.AddRemote(ctx, root, name, url); err != nil {
					return "", err
				}
				return "Added remote " + name + " — Fetch it to see its branches", nil
			})
		case "url":
			s.runGitOp("Edit Remote", func(ctx context.Context, root string) (string, error) {
				if err := s.git.runner.SetRemoteURL(ctx, root, original, url); err != nil {
					return "", err
				}
				return "Updated " + original, nil
			})
		case "rename":
			s.runGitOp("Rename Remote", func(ctx context.Context, root string) (string, error) {
				if err := s.git.runner.RenameRemote(ctx, root, original, newName); err != nil {
					return "", err
				}
				return "Renamed " + original + " to " + newName, nil
			})
		}
	})
}

// ---- submodules ------------------------------------------------------------

func submoduleBadge(st gitworkbench.SubmoduleState) string {
	switch st {
	case gitworkbench.SubmoduleUninitialized:
		return "not set up"
	case gitworkbench.SubmoduleModified:
		return "moved"
	case gitworkbench.SubmoduleConflict:
		return "conflict"
	}
	return ""
}

func (s *Shell) submodulesSection(c *ui.Context) {
	if len(s.git.submodules) == 0 {
		return
	}
	s.gitSidebarSection(c, "Submodules", len(s.git.submodules), nil, func() {
		shown := 0
		for _, sm := range s.git.submodules {
			if !gitRefMatches(s.gitRefsFilter, sm.Path, sm.Describe) {
				continue
			}
			shown++
			sub := sm
			s.gitTreeRow(c, gitTreeItem{title: filepath.Base(sub.Path), tooltip: sub.Path + "  " + sub.Commit[:min(8, len(sub.Commit))],
				kind: "Submodule", glyph: iconFolder, badge: submoduleBadge(sub.State)}, func() {},
				func(m *ui.Menu) {
					update := "Update Submodule"
					if sub.State == gitworkbench.SubmoduleUninitialized {
						update = "Set Up Submodule"
					}
					if m.Item(update).Disabled(s.git.opBusy).Chosen() {
						s.runGitNetworkOp("Update Submodule", func(ctx context.Context, root string) (string, error) {
							if err := s.git.runner.UpdateSubmodule(ctx, root, sub.Path); err != nil {
								return "", err
							}
							return sub.Path + " is at the commit this repository records", nil
						})
					}
					if m.Item("Show in Finder").Disabled(sub.State == gitworkbench.SubmoduleUninitialized).Chosen() {
						s.openExternalPathParent(filepath.Join(s.git.root, sub.Path))
					}
					m.Separator()
					if m.Item("Copy Path").Chosen() {
						s.copyToClipboard(sub.Path)
					}
				})
		}
		s.gitEmptySection(c, shown)
	})
}

// ---- file history and blame ------------------------------------------------

type fileSheetState struct {
	open    bool
	mode    string // "history" | "blame"
	path    string
	loading bool
	err     string
	commits []gitworkbench.CommitInfo
	blame   []gitworkbench.BlameLine
	list    ui.ListState
	gen     int
}

func (s *Shell) openFileSheet(mode, path, rev string) {
	if s.git == nil || s.git.root == "" {
		return
	}
	f := &s.gitMore.file
	f.gen++
	gen := f.gen
	*f = fileSheetState{open: true, mode: mode, path: path, loading: true, gen: gen}
	root := s.git.root
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), gitworkbench.ReadTimeout*2)
		defer cancel()
		var (
			commits []gitworkbench.CommitInfo
			blame   []gitworkbench.BlameLine
			err     error
		)
		if mode == "history" {
			commits, err = s.git.runner.FileHistory(ctx, root, path, 300)
		} else {
			blame, err = s.git.runner.Blame(ctx, root, rev, path)
		}
		s.applyOnUI(func() {
			f := &s.gitMore.file
			if f.gen != gen || s.git == nil || s.git.root != root {
				return
			}
			f.loading = false
			if err != nil {
				f.err = gitFailureText(err)
				return
			}
			f.commits, f.blame = commits, blame
		})
	}()
}

func (s *Shell) fileSheet(c *ui.Context) {
	f := &s.gitMore.file
	if !f.open {
		return
	}
	t := c.Theme()
	title := "History of " + filepath.Base(f.path)
	if f.mode == "blame" {
		title = "Blame " + filepath.Base(f.path)
	}
	ui.Modal(c, &f.open, func() {
		ui.Column(c).Width(760).MaxWidth(900).Height(520).MinHeight(240).MinWidth(0).Gap(Spacing().M).Children(func() {
			modalTitle(c, title)
			ui.Text(c, f.path).FontSize(Typography().Caption).TextColor(t.TextMuted).SingleLine()
			switch {
			case f.loading:
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, "Reading…").FontSize(Typography().Body).TextColor(t.TextMuted)
				})
			case f.err != "":
				ui.Text(c, f.err).FontSize(Typography().Body).TextColor(t.Warning)
			case f.mode == "history":
				s.fileHistoryList(c)
			default:
				s.blameList(c)
			}
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				if regularPrimaryButton(c, "Close").Clicked() {
					f.open = false
				}
			})
		})
	})
}

func (s *Shell) openCommitFromSheet(hash string) {
	s.gitMore.file.open = false
	s.revealCommitReviewing(hash, hash)
}

func (s *Shell) fileHistoryList(c *ui.Context) {
	t := c.Theme()
	f := &s.gitMore.file
	if len(f.commits) == 0 {
		ui.Text(c, "No commits touch this file yet.").FontSize(Typography().Body).TextColor(t.TextMuted)
		return
	}
	ui.List(c, &f.list, len(f.commits), func(i int) {
		if i < 0 || i >= len(f.commits) {
			return
		}
		cm := f.commits[i]
		row := ui.Row(c).Gap(8).Padding(4, 8).Radius(treeRowRadius).AlignItems(ui.Center).MinWidth(0)
		if row.Hovered() {
			row.Background(designTokens(t.Dark).SidebarHover)
		}
		if row.Clicked() {
			s.openCommitFromSheet(cm.Hash)
		}
		row.Children(func() {
			ui.Text(c, cm.Short).Font(gdCodeFont()).FontSize(Typography().BodySmall).TextColor(t.TextMuted).Width(64).SingleLine()
			ui.Text(c, cm.Subject).FontSize(Typography().Body).Grow(1).MinWidth(0).SingleLine()
			ui.Text(c, cm.Author).FontSize(Typography().BodySmall).TextColor(t.TextMuted).Width(110).SingleLine()
			ui.Text(c, gdRelativeTime(cm.Time)).FontSize(Typography().BodySmall).TextColor(t.TextMuted).Width(70).SingleLine()
		})
	}).Grow(1).MinHeight(0)
}

func (s *Shell) blameList(c *ui.Context) {
	t := c.Theme()
	f := &s.gitMore.file
	if len(f.blame) == 0 {
		ui.Text(c, "Nothing to annotate.").FontSize(Typography().Body).TextColor(t.TextMuted)
		return
	}
	ui.List(c, &f.list, len(f.blame), func(i int) {
		if i < 0 || i >= len(f.blame) {
			return
		}
		bl := f.blame[i]
		row := ui.Row(c).Gap(8).Padding(0, 8).AlignItems(ui.Center).MinWidth(0)
		if row.Hovered() {
			row.Background(designTokens(t.Dark).SidebarHover)
		}
		if row.Clicked() {
			s.openCommitFromSheet(bl.Hash)
		}
		tipped(c, row, bl.Summary+"\n"+bl.Author+" · "+gdCommitTime(bl.Time))
		row.Children(func() {
			ui.Text(c, bl.Short).Font(gdCodeFont()).FontSize(Typography().BodySmall).TextColor(t.Accent).Width(58).SingleLine()
			ui.Text(c, bl.Author).FontSize(Typography().BodySmall).TextColor(t.TextMuted).Width(96).SingleLine()
			ui.Text(c, itoa(bl.Line)).Font(gdCodeFont()).FontSize(Typography().BodySmall).TextColor(t.TextMuted).Width(36).SingleLine()
			ui.Text(c, bl.Text).Font(gdCodeFont()).FontSize(Typography().BodySmall).Grow(1).MinWidth(0).SingleLine()
		})
	}).Grow(1).MinHeight(0)
}

// ---- interactive rebase ----------------------------------------------------

type rebaseRow struct {
	commit  gitworkbench.RebaseCommit
	action  string
	message string
}

type rebaseSheetState struct {
	open      bool
	loading   bool
	base      string
	baseShort string
	rows      []rebaseRow
	list      ui.ListState
	gen       int
}

var rebaseActions = []string{"pick", "reword", "squash", "fixup", "drop"}

func (s *Shell) openRebaseSheet(base, short string) {
	if s.git == nil || s.git.root == "" || s.git.opBusy || !gitworkbenchFullHash(base) {
		return
	}
	r := &s.gitMore.rebase
	r.gen++
	gen := r.gen
	*r = rebaseSheetState{open: true, loading: true, base: base, baseShort: short, gen: gen}
	root := s.git.root
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), gitworkbench.ReadTimeout*2)
		defer cancel()
		plan, err := s.git.runner.RebasePlan(ctx, root, base)
		s.applyOnUI(func() {
			r := &s.gitMore.rebase
			if r.gen != gen || s.git == nil || s.git.root != root {
				return
			}
			r.loading = false
			if err != nil {
				r.open = false
				s.pendingToast = "Interactive Rebase: " + gitFailureText(err)
				return
			}
			for _, c := range plan {
				r.rows = append(r.rows, rebaseRow{commit: c, action: "pick", message: c.Message})
			}
		})
	}()
}

func (r *rebaseSheetState) move(i, delta int) {
	j := i + delta
	if i < 0 || j < 0 || i >= len(r.rows) || j >= len(r.rows) {
		return
	}
	r.rows[i], r.rows[j] = r.rows[j], r.rows[i]
}

// steps turns the sheet into the Runner's plan.
func (r *rebaseSheetState) steps() []gitworkbench.RebaseStep {
	out := make([]gitworkbench.RebaseStep, 0, len(r.rows))
	for _, row := range r.rows {
		out = append(out, gitworkbench.RebaseStep{Hash: row.commit.Hash, Action: gitworkbench.RebaseAction(row.action), Message: row.message})
	}
	return out
}

func (s *Shell) rebaseSheet(c *ui.Context) {
	r := &s.gitMore.rebase
	if !r.open {
		return
	}
	t := c.Theme()
	ui.Modal(c, &r.open, func() {
		ui.Column(c).Width(720).MaxWidth(820).Height(560).MinHeight(260).MinWidth(0).Gap(Spacing().M).Children(func() {
			modalTitle(c, "Interactive Rebase")
			if r.loading {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, "Reading commits…").FontSize(Typography().Body).TextColor(t.TextMuted)
				})
				return
			}
			ui.Text(c, itoa(len(r.rows))+" commits after "+r.baseShort+", oldest first. Pick an action for each; ↑ ↓ reorder. If anything conflicts, the rebase is cancelled and nothing changes.").
				FontSize(Typography().BodySmall).TextColor(t.TextMuted).MaxWidth(680)
			published := 0
			for _, row := range r.rows {
				if row.commit.Published {
					published++
				}
			}
			if published > 0 {
				ui.Text(c, itoa(published)+" of these commits are already on a remote. Rewriting them means the branch can no longer be pushed without forcing, which Solco does not do.").
					FontSize(Typography().BodySmall).TextColor(t.Warning).MaxWidth(680)
			}
			ui.List(c, &r.list, len(r.rows), func(i int) {
				if i < 0 || i >= len(r.rows) {
					return
				}
				s.rebaseRowView(c, r, i)
			}).Grow(1).MinHeight(0)
			ok := s.rebaseValid()
			ui.Row(c).FillWidth().Gap(Spacing().M).Justify(ui.End).Children(func() {
				if regularButton(c, "Cancel").Clicked() {
					r.open = false
				}
				if regularPrimaryButton(c, "Rebase").Disabled(!ok || s.git.opBusy).Clicked() {
					base, steps := r.base, r.steps()
					r.open = false
					s.runGitOp("Interactive Rebase", func(ctx context.Context, root string) (string, error) {
						if err := s.git.runner.InteractiveRebase(ctx, root, base, steps); err != nil {
							return "", err
						}
						return "Rebase done", nil
					})
				}
			})
		})
	})
}

// rebaseValid mirrors the Runner's cheap rules so the button says no early.
func (s *Shell) rebaseValid() bool {
	r := &s.gitMore.rebase
	kept := 0
	for _, row := range r.rows {
		switch row.action {
		case "squash", "fixup":
			if kept == 0 {
				return false
			}
		case "reword":
			if strings.TrimSpace(row.message) == "" {
				return false
			}
		}
		if row.action != "drop" {
			kept++
		}
	}
	return kept > 0
}

func (s *Shell) rebaseRowView(c *ui.Context, r *rebaseSheetState, i int) {
	t := c.Theme()
	row := &r.rows[i]
	c = c.Key("rebase-" + row.commit.Hash)
	ui.Column(c).FillWidth().Padding(3, 4).Gap(4).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			k := gorexColorsOf(t.Dark)
			if i == 0 {
				gorexIconButtonDisabled(c, k, iconChevronUp, "Move Up", "Move Up", gorexRowBtn, gorexRowGlyph)
			} else if gorexIconButton(c, k, iconChevronUp, "Move Up", "Move Up", false, gorexRowBtn, gorexRowGlyph).Clicked() {
				r.move(i, -1)
			}
			if i == len(r.rows)-1 {
				gorexIconButtonDisabled(c, k, iconChevronDown, "Move Down", "Move Down", gorexRowBtn, gorexRowGlyph)
			} else if gorexIconButton(c, k, iconChevronDown, "Move Down", "Move Down", false, gorexRowBtn, gorexRowGlyph).Clicked() {
				r.move(i, 1)
			}
			selectField(c, &row.action, rebaseActions).Width(96).Label("Action for " + row.commit.Short)
			ui.Text(c, row.commit.Short).Font(gdCodeFont()).FontSize(Typography().BodySmall).TextColor(t.TextMuted).Width(62).SingleLine()
			subject := ui.Text(c, row.commit.Subject).FontSize(Typography().Body).Grow(1).MinWidth(0).SingleLine()
			if row.action == "drop" {
				subject.TextColor(t.TextMuted)
			}
		})
		if row.action == "reword" {
			textArea(c, &row.message).FillWidth().Height(76).Label("New message for " + row.commit.Short)
		}
	})
}

// ---- dialogs entry ---------------------------------------------------------

func (s *Shell) gitMoreDialogs(c *ui.Context) {
	s.remoteDialog(c)
	s.rebaseSheet(c)
	s.fileSheet(c)
}

// gdFileLogItems is the file menus' shared Finder / History / Blame group (one
// owner for the changes tree, the file card and the diff cell menus).
func (s *Shell) gdFileLogItems(m *ui.Menu, cf *gitworkbench.ChangeFile) {
	if s.git == nil || s.git.root == "" || cf == nil {
		return
	}
	commit := s.gdReviewingCommit()
	deleted := cf.Status == gitworkbench.StatusDeleted
	tracked := !cf.Untracked && (commit || cf.Status != gitworkbench.StatusAdded)
	if !commit && m.Item("Show in Finder").Disabled(deleted).Chosen() {
		s.openExternalPathParent(filepath.Join(s.git.root, cf.Path))
	}
	if m.Item("File History…").Disabled(!tracked).Chosen() {
		s.openFileSheet("history", cf.Path, "")
	}
	if m.Item("Blame…").Disabled(!tracked || (deleted && !commit)).Chosen() {
		rev := ""
		if commit {
			rev = s.git.commitHash
		}
		s.openFileSheet("blame", cf.Path, rev)
	}
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
