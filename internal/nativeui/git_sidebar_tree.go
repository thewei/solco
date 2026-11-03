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
	"github.com/thewei/solco/internal/gitworkbench"
)

// gitSectionAction is a section's one creation action: the "+" that shows
// under the pointer and the same entry in the section's context menu.
type gitSectionAction struct {
	label    string // accessibility label of the "+" button
	menuItem string // context-menu title
	disabled bool
	run      func()
}

// segmentTab is one segment of segmentTabs: a glyph and a label.
type segmentTab struct {
	label string
	svg   *ui.SVG
}

// segmentTabs is the sidebar's two-way switch — Changes | Commits at the top of
// Git, Files | Content under the Files search: the shared segmented face with
// an icon on each segment. badge, when given, returns a count shown after a
// segment's label (0 shows none). It reports whether the choice changed.
func segmentTabs(c *ui.Context, name string, chosen *int, items []segmentTab, badge func(i int) int) bool {
	t := c.Theme()
	tabs := ui.SegmentedBase(c, chosen, len(items))
	segmentedTrack(c, tabs, name)
	tabs.Track.FillWidth().Children(func() {
		for i, tab := range items {
			seg := segmentFace(c, tabs, i, i == *chosen, tab.label).Grow(1).Justify(ui.Center)
			col := t.TextMuted
			if i == *chosen || seg.Hovered() {
				col = t.Text
			}
			seg.Children(func() {
				ui.Icon(c, tab.svg).Size(gorexPaneGlyph, gorexPaneGlyph).TextColor(col).Shrink(0)
				ui.Text(c, tab.label).SingleLine().TextColor(col)
				if badge != nil {
					if n := badge(i); n > 0 {
						ui.Text(c, itoa(n)).FontSize(Typography().Caption).TextColor(t.TextMuted).SingleLine()
					}
				}
			})
		}
	})
	return tabs.Track.Changed()
}

// gitViewTabs is the Changes | Commits switch at the top of the Git sidebar,
// (Commits, not History: History is the Sessions page.) It carries no count:
// the changed-file total already heads the file column and the Unstaged
// section, and a badge here squeezed "Changes" into "Chang…".
func (s *Shell) gitViewTabs(c *ui.Context) {
	chosen := 0
	if s.commitsMode {
		chosen = 1
	}
	moved := segmentTabs(c, "Git view", &chosen, []segmentTab{{"Changes", iconFileDiff}, {"Commits", iconCommit}}, nil)
	if moved {
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
						label := gitWorktreeLabel(wt)
						if !gitRefMatches(s.gitRefsFilter, label, wt.Path) {
							continue
						}
						shown++
						worktree := wt
						item := gitTreeItem{title: label, tooltip: wt.Path, kind: "Worktree", glyph: iconWorktree}
						switch {
						case wt.Bare:
						case wt.Branch != "":
							item.ref = gitworkbench.RefHeadsPrefix + wt.Branch
						case wt.Head != "":
							item.commit = wt.Head
						}
						s.gitTreeRow(c, item, func() {
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
							if m.Item("Rebase Current Onto This…").Disabled(s.git.opBusy).Chosen() {
								s.confirmRebaseOnto(full)
							}
							m.Separator()
						} else if s.git.upstream.OK {
							// What Fork's current-branch menu offers beside the toolbar.
							if m.Item("Pull (Fast-Forward Only)").Disabled(s.git.opBusy).Chosen() {
								s.pullRepo()
							}
							if m.Item("Push").Disabled(s.git.opBusy || s.git.upstream.Diverged()).Chosen() {
								s.pushRepo()
							}
							m.Separator()
						}
						if m.Item("Rename Branch…").Disabled(s.git.opBusy).Chosen() {
							s.openRenameBranch(full)
						}
						s.trackingMenu(m, full)
						if !current && m.Item("Delete Branch…").Chosen() {
							s.deleteBranch(full)
						}
						m.Separator()
						if m.Item("Copy Branch Name").Chosen() {
							s.copyToClipboard(full)
						}
					})
				})
				s.gitSidebarSection(c, "Remotes", len(s.git.remoteBranches)+len(s.remotesWithoutBranches()), &gitSectionAction{
					label: "Add remote", menuItem: "Add Remote…", disabled: s.git.opBusy,
					run: func() { s.openRemoteDialog("add", "") },
				}, func() {
					refs := make([]string, 0, len(s.git.remoteBranches))
					for _, b := range s.git.remoteBranches {
						refs = append(refs, b.Remote+"/"+b.Name)
					}
					empties := s.remotesWithoutBranches()
					for _, rm := range empties {
						if !gitRefMatches(s.gitRefsFilter, rm.Name, rm.FetchURL) {
							continue
						}
						remote := rm
						s.gitTreeRow(c, gitTreeItem{title: remote.Name, tooltip: remote.FetchURL, kind: "Remote", glyph: iconCloud, badge: "not fetched"},
							func() {}, func(m *ui.Menu) { s.remoteMenu(m, remote.Name) })
					}
					if len(refs) == 0 {
						if len(empties) == 0 {
							s.gitEmptySection(c, 0)
						}
						return
					}
					s.gitRenderRefTree(c, "remote", gitRefTree(refs, s.gitRefsFilter), 0, func(full string) {
						s.copyToClipboard(full)
						s.pendingToast = "Copied remote-tracking ref"
					}, func(full string, m *ui.Menu) {
						if m.Item("Check Out…").Disabled(s.git.opBusy).Chosen() {
							s.checkoutRemoteBranch(full)
						}
						if s.currentBranchName() != "" {
							if m.Item("Merge into Current…").Disabled(s.git.opBusy).Chosen() {
								s.openMergeDialog(full)
							}
							if m.Item("Rebase Current Onto This…").Disabled(s.git.opBusy).Chosen() {
								s.confirmRebaseOnto(full)
							}
						}
						m.Separator()
						if m.Item("Delete Remote Branch…").Disabled(s.git.opBusy).Chosen() {
							s.confirmDeleteRemoteBranch(full)
						}
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
						if m.Item("Push Tag").Disabled(s.git.opBusy).Chosen() {
							s.pushTag(full)
						}
						m.Separator()
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
						s.gitTreeRow(c, gitTreeItem{title: stashTitle(st.Subject), tooltip: st.Ref + "  " + st.Subject, kind: "Stash", glyph: iconStash, commit: st.Base, review: st.Hash}, func() {
							s.copyToClipboard(st.Ref)
						}, func(m *ui.Menu) {
							if m.Item("Apply Stash").Disabled(s.git.opBusy).Chosen() {
								s.applyStash(st.Ref)
							}
							if m.Item("Pop Stash").Chosen() {
								s.popStash(st.Ref)
							}
							if m.Item("Save as Patch…").Chosen() {
								s.saveStashPatch(st.Ref, st.Subject)
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
				s.submodulesSection(c)
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
	// An empty section is its header and the 0: opening it only to read
	// "None" cost a row per Remotes/Tags/Stashes.
	closed := s.gitTreeClosed[key] || (count == 0 && strings.TrimSpace(s.gitRefsFilter) == "")
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

// gitWorktreeLabel is a worktree row's title: its branch, or what it lacks.
func gitWorktreeLabel(wt gitworkbench.Worktree) string {
	switch {
	case wt.Bare:
		return "(bare)"
	case wt.Branch == "":
		return "(detached)"
	}
	return wt.Branch
}
