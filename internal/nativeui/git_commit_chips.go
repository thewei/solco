package nativeui

/**
 * [INPUT]: Commit rows' real %D decorations, the repository's stash list (with each stash's base commit) and worktrees
 * [OUTPUT]: The label chips on a Commits row — branch, remote branch, tag, stash and worktree, each with the same glyph the sidebar row of that kind wears — and the sidebar single click's commit for worktrees and stashes
 * [POS]: Presentation of refs already known to Git; chips are never guessed from subjects, and no second ref registry exists
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

// gitChip is one label on a commit row.
type gitChip struct {
	text, tip string
	glyph     *ui.SVG
	branch    bool
	// target is the sidebar row the chip stands for; review is the commit a
	// click on it opens when that is not the row's own (a stash).
	target gitSidebarTarget
	review string
}

// gitSidebarTarget names a sidebar ref row (and what must be open to show it),
// so a chip can reveal the entry it labels.
type gitSidebarTarget struct {
	section string   // section title: Worktrees, Branches, Remotes, Tags, Stashes
	folders []string // closed-state keys of the tree folders above the row
	key     string
}

// gitRefTarget is the row of a ref in its slash-grouped tree.
func gitRefTarget(section, scope, full string) gitSidebarTarget {
	parts := strings.Split(full, "/")
	t := gitSidebarTarget{section: section, key: gitRefRowKey(parts[len(parts)-1], full)}
	for i := 1; i < len(parts); i++ {
		t.folders = append(t.folders, scope+"/"+strings.Join(parts[:i], "/"))
	}
	return t
}

// revealInSidebar selects and scrolls to the sidebar row of a chip, opening the
// section and folders above it and dropping a search that would hide it.
func (s *Shell) revealInSidebar(t gitSidebarTarget) {
	if t.key == "" {
		return
	}
	s.gitRefsFilter = ""
	delete(s.gitTreeClosed, s.gitFoldKey("section/"+t.section))
	for _, folder := range t.folders {
		delete(s.gitTreeClosed, s.gitFoldKey(folder))
	}
	s.gitRefsSelected, s.gitRefsReveal = t.key, t.key
}

// gitChipIndex holds what the repository attaches to commits beyond the %D
// decorations: stashes sit on the commit they were made on, a detached
// worktree on its HEAD, and a branch checked out in another worktree says so.
type gitChipIndex struct {
	stashes    map[string][]gitworkbench.Stash
	detached   map[string][]gitworkbench.Worktree
	checkedOut map[string]string // branch name → worktree path
}

func (s *Shell) newGitChipIndex() gitChipIndex {
	x := gitChipIndex{
		stashes:    map[string][]gitworkbench.Stash{},
		detached:   map[string][]gitworkbench.Worktree{},
		checkedOut: map[string]string{},
	}
	for _, st := range s.git.stashes {
		if st.Base != "" {
			x.stashes[st.Base] = append(x.stashes[st.Base], st)
		}
	}
	for _, wt := range s.git.worktrees {
		switch {
		case wt.Bare:
		case wt.Detached && wt.Head != "":
			x.detached[wt.Head] = append(x.detached[wt.Head], wt)
		case wt.Branch != "" && !s.isCurrentBranch(wt.Branch):
			x.checkedOut[wt.Branch] = wt.Path
		}
	}
	return x
}

// chips lists a commit's labels: the refs Git reports (hidden ones skipped),
// then detached worktrees, then stashes.
func (x gitChipIndex) chips(cm gitworkbench.CommitInfo, hidden map[string]bool) []gitChip {
	var out []gitChip
	for _, r := range cm.Refs {
		if hidden[r.Full] {
			continue
		}
		chip := gitChip{text: r.Name, tip: r.Name}
		switch r.Kind {
		case gitworkbench.RefBranch:
			chip.branch, chip.glyph = true, iconBranch
			chip.target = gitRefTarget("Branches", "local", r.Name)
			if path, ok := x.checkedOut[r.Name]; ok {
				chip.glyph, chip.tip = iconWorktree, r.Name+"\nChecked out in "+tildePath(path)
			}
		case gitworkbench.RefRemote:
			chip.glyph = iconCloud
			chip.target = gitRefTarget("Remotes", "remote", r.Name)
		default:
			chip.glyph = iconTag
			chip.target = gitRefTarget("Tags", "tag", r.Name)
		}
		out = append(out, chip)
	}
	for _, wt := range x.detached[cm.Hash] {
		out = append(out, gitChip{text: filepath.Base(wt.Path), tip: tildePath(wt.Path) + "\nDetached worktree", glyph: iconWorktree,
			target: gitSidebarTarget{section: "Worktrees", key: gitRefRowKey(gitWorktreeLabel(wt), wt.Path)}})
	}
	for _, st := range x.stashes[cm.Hash] {
		out = append(out, gitChip{text: st.Ref, tip: st.Ref + "\n" + strings.TrimSpace(st.Subject), glyph: iconStash,
			target: gitSidebarTarget{section: "Stashes", key: gitRefRowKey(st.Ref+"  "+st.Subject, st.Subject)}, review: st.Hash})
	}
	return out
}

// gitCommitChipsShown is how many chips a row spells out; the rest fold into
// one "+N" chip whose tooltip lists them.
const gitCommitChipsShown = 3

// gitCommitChips draws a row's chips; click runs when one is clicked.
func gitCommitChips(c *ui.Context, chips []gitChip, accent ui.Color, click func(gitChip)) {
	t := c.Theme()
	for i, chip := range chips {
		if i == gitCommitChipsShown {
			var rest []string
			for _, r := range chips[i:] {
				rest = append(rest, r.text)
			}
			tipped(c, ui.Text(c, "+"+itoa(len(rest))).FontSize(Typography().Micro).FontWeight(WeightSemibold).
				Padding(2, 5).Radius(3).Background(t.SurfaceHover).TextColor(accent).SingleLine(), strings.Join(rest, "\n"))
			return
		}
		bg, fg := t.SurfaceHover, accent
		if chip.branch {
			bg, fg = t.SurfacePressed, t.Text
		}
		el := ui.Row(c).Gap(3).AlignItems(ui.Center).Padding(2, 5).Radius(3).Background(bg).Shrink(0).MaxWidth(150).Children(func() {
			ui.Icon(c, chip.glyph).FontSize(10).TextColor(fg).Shrink(0)
			ui.Text(c, chip.text).FontSize(Typography().Micro).FontWeight(WeightSemibold).TextColor(fg).SingleLine().MinWidth(0)
		})
		tipped(c, el, chip.tip)
		if el.Clicked() {
			click(chip)
		}
	}
}
