package nativeui

import (
	"context"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

/**
 * [INPUT]: gitService.stashes、Changes 快照文件、gitworkbench.Runner 的 StashPush/StashPushPaths/StashApply/StashPop/StashRename
 * [OUTPUT]: Save Stash 对话框（留言 + 文件勾选，即 Fork 的 Save/Partial Stash）、Apply Stash 对话框（"Delete stash after applying" 勾选）、Rename Stash 入口
 * [POS]: 对齐 Fork 的 stash 操作面（2026-11-03 用户要求）：Quick Stash = "Stash All Changes"；Save Stash / Partial Stash 合成一个对话框（全选即整体 stash）；Apply Stash 一个对话框覆盖 Apply 与 Pop；Rename Stash 用 StashRename。新文件默认一并 stash（-u，Solco 的 Changes 列表本就把它们列为改动），与 Fork 的"忽略新文件"有意不同
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type stashFileChoice struct {
	path string
	on   bool
}

type stashDialogState struct {
	open bool
	kind string // "save" | "apply"
	root string
	// save
	message string
	files   []stashFileChoice
	list    ui.ListState
	// apply
	hash        string
	title       string
	deleteAfter bool
}

// openSaveStashDialog asks for a message and the files to put away; every
// changed file starts ticked, so confirming stashes the whole change set.
func (s *Shell) openSaveStashDialog() {
	if s.git == nil || s.git.root == "" || s.git.opBusy {
		return
	}
	snap := s.gitSnapshot()
	if snap == nil || len(snap.Files) == 0 {
		return
	}
	files := make([]stashFileChoice, 0, len(snap.Files))
	for _, f := range snap.Files {
		files = append(files, stashFileChoice{path: f.Path, on: true})
	}
	s.gitMore.stash = stashDialogState{open: true, kind: "save", root: s.git.root, files: files}
}

// openApplyStashDialog is Fork's Apply Stash sheet for one stash.
func (s *Shell) openApplyStashDialog(st gitworkbench.Stash) {
	if s.git == nil || s.git.root == "" || s.git.opBusy {
		return
	}
	s.gitMore.stash = stashDialogState{open: true, kind: "apply", root: s.git.root, hash: st.Hash, title: stashTitle(st.Subject)}
}

// openRenameStash asks for a stash's new message; the stash is identified by
// its commit, which survives the list shifting.
func (s *Shell) openRenameStash(st gitworkbench.Stash) {
	if s.git == nil || s.git.root == "" || s.git.opBusy {
		return
	}
	s.openTextDialog("git-rename-stash", s.git.root+"\x00"+st.Hash, "Rename Stash", "Enter new stash message", stashTitle(st.Subject))
	s.dialogAction = "Rename"
}

func (s *Shell) stashDialog(c *ui.Context) {
	d := &s.gitMore.stash
	if !d.open {
		return
	}
	switch d.kind {
	case "apply":
		s.applyStashDialog(c, d)
	default:
		s.saveStashDialog(c, d)
	}
}

func (s *Shell) saveStashDialog(c *ui.Context, d *stashDialogState) {
	t := c.Theme()
	ticked := 0
	for _, f := range d.files {
		if f.on {
			ticked++
		}
	}
	submit := false
	ui.Modal(c, &d.open, func() {
		modalTitle(c, "Save Stash")
		ui.Column(c).Width(420).MaxWidth(480).Gap(Spacing().M).Children(func() {
			ui.Text(c, "Save your local changes to a new stash. Staged and unstaged changes are stashed, new files included.").
				FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			captionedField(c, "Stash message", "Optional.", func() {
				submit = textField(c.Key("stash-message"), &d.message).Label("Stash message").AutoFocus().Width(420).Submitted()
			})
			if len(d.files) > 1 {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Files").FontSize(Typography().BodySmall).FontWeight(WeightMedium)
					ui.Spacer(c)
					all := ticked == len(d.files)
					label := "Select all"
					if all {
						label = "Select none"
					}
					if regularButton(c, label).Clicked() {
						for i := range d.files {
							d.files[i].on = !all
						}
					}
				})
				rows := min(len(d.files), 8)
				ui.List(c, &d.list, len(d.files), func(i int) {
					if i < 0 || i >= len(d.files) {
						return
					}
					ui.Checkbox(c.Key("stash-file-"+d.files[i].path), &d.files[i].on, d.files[i].path)
				}).Height(float32(rows) * 30).MinHeight(0)
			}
			ui.Row(c).Gap(Spacing().M).Justify(ui.End).Children(func() {
				if regularButton(c, "Cancel").Clicked() {
					d.open = false
				}
				if regularPrimaryButton(c, "Stash").Disabled(ticked == 0).Clicked() {
					submit = true
				}
			})
		})
	})
	if !submit || ticked == 0 || !d.open {
		return
	}
	root, message := d.root, strings.TrimSpace(d.message)
	var paths []string
	for _, f := range d.files {
		if f.on {
			paths = append(paths, f.path)
		}
	}
	whole := len(paths) == len(d.files)
	d.open = false
	s.submitSaveStash(root, message, paths, whole)
}

func (s *Shell) submitSaveStash(root, message string, paths []string, whole bool) {
	if s.git == nil || s.git.root != root {
		s.pendingToast = "Repository changed. Reopen the dialog to continue."
		return
	}
	s.runGitOp("Stash", func(ctx context.Context, root string) (string, error) {
		if whole {
			if err := s.git.runner.StashPush(ctx, root, message); err != nil {
				return "", err
			}
			return "Changes stashed", nil
		}
		if err := s.git.runner.StashPushPaths(ctx, root, message, paths); err != nil {
			return "", err
		}
		return "Stashed " + pluralFiles(len(paths)), nil
	})
}

func (s *Shell) applyStashDialog(c *ui.Context, d *stashDialogState) {
	t := c.Theme()
	submit := false
	ui.Modal(c, &d.open, func() {
		modalTitle(c, "Apply Stash")
		ui.Column(c).Width(420).MaxWidth(480).Gap(Spacing().M).Children(func() {
			ui.Text(c, "Apply changes of the stash to your working directory.").FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			ui.Row(c).Gap(Spacing().S).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Stash:").FontSize(Typography().BodySmall).FontWeight(WeightMedium)
				ui.Text(c, d.title).FontSize(Typography().Body).SingleLine()
			})
			ui.Column(c).Gap(Spacing().XS).Children(func() {
				ui.Checkbox(c.Key("stash-delete-after"), &d.deleteAfter, "Delete stash after applying")
				ui.Text(c, "Stash will not be deleted if a conflict occurs.").FontSize(Typography().Caption).TextColor(t.TextMuted)
			})
			ui.Row(c).Gap(Spacing().M).Justify(ui.End).Children(func() {
				if regularButton(c, "Cancel").Clicked() {
					d.open = false
				}
				if regularPrimaryButton(c, "Apply").AutoFocus().Clicked() {
					submit = true
				}
			})
		})
	})
	if !submit || !d.open {
		return
	}
	root, hash, drop := d.root, d.hash, d.deleteAfter
	d.open = false
	if s.git == nil || s.git.root != root {
		s.pendingToast = "Repository changed. Reopen the dialog to continue."
		return
	}
	i := slices.IndexFunc(s.git.stashes, func(st gitworkbench.Stash) bool { return st.Hash == hash })
	if i < 0 {
		s.pendingToast = "That stash no longer exists."
		return
	}
	if drop {
		s.popStash(s.git.stashes[i].Ref)
	} else {
		s.applyStash(s.git.stashes[i].Ref)
	}
}

// renameStash completes the Rename Stash text dialog (target = root, hash).
func (s *Shell) renameStash(hash, message string) {
	s.runGitOp("Rename Stash", func(ctx context.Context, root string) (string, error) {
		if err := s.git.runner.StashRename(ctx, root, hash, message); err != nil {
			return "", err
		}
		return "Stash renamed", nil
	})
}

// recentStashItems is the Changes menu's list of the newest stashes, each
// opening the Apply Stash sheet (Fork's "Recent Stashes").
func (s *Shell) recentStashItems(m *ui.Menu) {
	const most = 5
	for i, st := range s.git.stashes {
		if i == most {
			break
		}
		title := stashTitle(st.Subject)
		if len([]rune(title)) > 48 {
			title = string([]rune(title)[:47]) + "…"
		}
		if m.Item(title).Disabled(s.git.opBusy).Chosen() {
			s.openApplyStashDialog(st)
		}
	}
}
