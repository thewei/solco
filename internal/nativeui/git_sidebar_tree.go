package nativeui

/**
 * [INPUT]: Git metadata snapshot, existing Git mutations/confirmations, shared sidebar tree grid and native Sidebar controls
 * [OUTPUT]: Collapsible Worktrees/Branches/Remotes/Tags/Stashes navigation in the Git left rail; section hover "+" and right-click menus replace the old "···" popovers; one listNav focus group without entry focus (window-level j/k belong to the diff)
 * [POS]: Git sidebar presentation only; all Git truth and operations stay in gitworkbench
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// gitSectionAction is a section's one creation action: the "+" that shows
// under the pointer and the same entry in the section's context menu.
type gitSectionAction struct {
	label    string // accessibility label of the "+" button
	menuItem string // context-menu title
	disabled bool
	run      func()
}

// gitViewTabs is the Changes | Commits switch at the top of the Git sidebar:
// the shared segmented face with an icon on each segment and, on Changes, the
// count of changed files. (Commits, not History: History is the Sessions page.)
func (s *Shell) gitViewTabs(c *ui.Context) {
	t := c.Theme()
	chosen := 0
	if s.commitsMode {
		chosen = 1
	}
	changed := 0
	if snap := s.gitSnapshot(); snap != nil {
		changed = len(snap.Files)
	}
	tabs := ui.SegmentedBase(c, &chosen, 2)
	segmentedTrack(c, tabs, "Git view")
	tabs.Track.FillWidth().Children(func() {
		for i, tab := range []struct {
			label string
			svg   *ui.SVG
		}{{"Changes", iconFileDiff}, {"Commits", iconCommit}} {
			seg := segmentFace(c, tabs, i, i == chosen, tab.label).Grow(1).Justify(ui.Center)
			col := t.TextMuted
			if i == chosen || seg.Hovered() {
				col = t.Text
			}
			seg.Children(func() {
				ui.Icon(c, tab.svg).Size(gorexPaneGlyph, gorexPaneGlyph).TextColor(col).Shrink(0)
				ui.Text(c, tab.label).SingleLine().TextColor(col)
				if i == 0 && changed > 0 {
					ui.Text(c, itoa(changed)).FontSize(Typography().Caption).TextColor(t.TextMuted).SingleLine()
				}
			})
		}
	})
	if tabs.Track.Changed() {
		if chosen == 0 {
			s.commitsMode = false
			s.reviewLocalChanges()
		} else {
			s.showGitCommits()
		}
	}
}

// gitRefsFilterChanged starts a new search expanded, without carrying stale
// search-scoped folds into a different result set.
func (s *Shell) gitRefsFilterChanged() {
	for key := range s.gitTreeClosed {
		if strings.HasPrefix(key, "search/") {
			delete(s.gitTreeClosed, key)
		}
	}
}

func (s *Shell) gitRepositorySidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinHeight(0).Padding(7, sidebarGutter, 0).Gap(6).Children(func() {
		s.gitViewTabs(c)
		if s.git.root == "" {
			ui.Text(c, "No Git repository").Padding(10).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			s.navList(c, &s.nav.gitRefs, false, ui.Column(c).FillWidth().Padding(0, 0, 12), func() {
				s.gitSidebarSection(c, "Worktrees", len(s.git.worktrees), &gitSectionAction{
					label: "Add worktree", menuItem: "Add Worktree…", disabled: s.git.opBusy,
					run: func() {
						s.openTextDialog("git-add-worktree", "", "Add Worktree",
							"Absolute path (a branch named after the folder is created from HEAD)", "")
					},
				}, func() {
					shown := 0
					for _, wt := range s.git.worktrees {
						label := wt.Branch
						if label == "" {
							label = "(detached)"
						}
						if wt.Bare {
							label = "(bare)"
						}
						if !gitRefMatches(s.gitRefsFilter, label, wt.Path) {
							continue
						}
						shown++
						worktree := wt
						s.gitTreeRow(c, gitTreeItem{title: label, tooltip: wt.Path, kind: "Worktree", glyph: iconBranch}, func() {
							if !worktree.Bare {
								s.openExternalPath(worktree.Path)
							}
						}, func(m *ui.Menu) {
							if m.Item("Copy Path").Chosen() {
								s.copyToClipboard(worktree.Path)
							}
							if !worktree.Bare && m.Item("Reveal in Finder").Chosen() {
								s.openExternalPath(worktree.Path)
							}
						})
					}
					s.gitEmptySection(c, shown)
				})
				s.gitSidebarSection(c, "Branches", len(s.git.branches), &gitSectionAction{
					label: "New branch", menuItem: "New Branch…", disabled: s.git.branchBusy,
					run: s.openNewBranchDialog,
				}, func() {
					refs := make([]string, 0, len(s.git.branches))
					for _, b := range s.git.branches {
						refs = append(refs, b.Name)
					}
					s.gitRenderRefTree(c, "local", gitRefTree(refs, s.gitRefsFilter), 0, func(full string) {
						for _, branch := range s.git.branches {
							if branch.Name == full && !branch.Current {
								s.confirmSwitchBranch(full)
								return
							}
						}
					}, func(full string, m *ui.Menu) {
						var current bool
						for _, branch := range s.git.branches {
							if branch.Name == full {
								current = branch.Current
								break
							}
						}
						if !current {
							if m.Item("Switch to Branch").Chosen() {
								s.confirmSwitchBranch(full)
							}
							if m.Item("Merge into Current…").Chosen() {
								s.openMergeDialog(full)
							}
							if m.Item("Delete Branch…").Chosen() {
								s.deleteBranch(full)
							}
						}
						if m.Item("Copy Branch Name").Chosen() {
							s.copyToClipboard(full)
						}
					})
				})
				s.gitSidebarSection(c, "Remotes", len(s.git.remoteBranches), nil, func() {
					refs := make([]string, 0, len(s.git.remoteBranches))
					for _, b := range s.git.remoteBranches {
						refs = append(refs, b.Remote+"/"+b.Name)
					}
					s.gitRenderRefTree(c, "remote", gitRefTree(refs, s.gitRefsFilter), 0, func(full string) {
						s.copyToClipboard(full)
						s.pendingToast = "Copied remote-tracking ref"
					}, func(full string, m *ui.Menu) {
						if m.Item("Copy Remote-Tracking Ref").Chosen() {
							s.copyToClipboard(full)
						}
					})
				})
				s.gitSidebarSection(c, "Tags", len(s.git.tags), &gitSectionAction{
					label: "New tag", menuItem: "New Tag…", disabled: s.git.opBusy,
					run: func() { s.openCreateDialog("git-create-tag", "New Tag", "Tag name") },
				}, func() {
					refs := make([]string, 0, len(s.git.tags))
					for _, tag := range s.git.tags {
						refs = append(refs, tag.Name)
					}
					s.gitRenderRefTree(c, "tag", gitRefTree(refs, s.gitRefsFilter), 0, func(full string) {
						s.copyToClipboard(full)
					}, func(full string, m *ui.Menu) {
						if m.Item("Copy Tag Name").Chosen() {
							s.copyToClipboard(full)
						}
						if m.Item("Delete Tag…").Chosen() {
							s.deleteTagConfirm(full)
						}
					})
				})
				s.gitSidebarSection(c, "Stashes", len(s.git.stashes), &gitSectionAction{
					label: "Stash changes", menuItem: "Stash All Changes", disabled: s.git.opBusy || !s.dirtyWorktree(),
					run: s.stashChanges,
				}, func() {
					shown := 0
					for _, stash := range s.git.stashes {
						if !gitRefMatches(s.gitRefsFilter, stash.Ref, stash.Subject) {
							continue
						}
						shown++
						st := stash
						s.gitTreeRow(c, gitTreeItem{title: st.Ref + "  " + st.Subject, tooltip: st.Subject, kind: "Stash", glyph: iconBranch}, func() {
							s.copyToClipboard(st.Ref)
						}, func(m *ui.Menu) {
							if m.Item("Pop Stash").Chosen() {
								s.popStash(st.Ref)
							}
							if m.Item("Drop Stash…").Chosen() {
								s.dropStash(st.Ref)
							}
							if m.Item("Copy Stash Ref").Chosen() {
								s.copyToClipboard(st.Ref)
							}
						})
					}
					s.gitEmptySection(c, shown)
				})
			})
		})
	})
}

// gitSidebarSection is one collapsible group. Its title sits on the tree
// grid's title column; the creation "+" shows only under the pointer (its
// slot is always reserved, so the count never jumps) and the same action
// lives in the right-click menu.
func (s *Shell) gitSidebarSection(c *ui.Context, name string, count int, action *gitSectionAction, children func()) {
	key := s.gitFoldKey("section/" + name)
	closed := s.gitTreeClosed[key]
	spec := sidebarSectionSpec{Key: "git-section:" + name, Title: name, Count: count, Closed: closed, Nav: &s.nav.gitRefs}
	if action != nil {
		if !action.disabled {
			spec.Action = &treeRowAction{Label: action.label, Tooltip: action.menuItem, Glyph: iconPlus, Run: action.run}
		}
		spec.Menu = func(m *ui.Menu) {
			if m.Item(action.menuItem).Disabled(action.disabled).Chosen() {
				action.run()
			}
		}
	}
	if _, clicked := s.sidebarSectionHeader(c, spec); clicked {
		s.gitTreeClosed[key] = !s.gitTreeClosed[key]
	}
	if !closed {
		children()
	}
}
