package nativeui

/**
 * [INPUT]: The loaded CommitInfo window (full parent hashes) and the set of merge commits the user folded
 * [OUTPUT]: gitFold — which commits a merge brought in (foldable), the commit list with folded merges' branches left out, and the parents the graph should follow
 * [POS]: Pure projection over the already loaded Commits window; no Git invocation, folds are remembered per repository in settings (Workbench.GraphFolded)
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
	"github.com/thewei/solco/internal/settings"
)

// gitFold answers, for one commit window, what folding a merge would hide.
type gitFold struct {
	commits  []gitworkbench.CommitInfo
	byHash   map[string]int
	children map[string][]string
	sets     map[string]map[string]bool
}

func newGitFold(commits []gitworkbench.CommitInfo) *gitFold {
	f := &gitFold{
		commits:  commits,
		byHash:   make(map[string]int, len(commits)),
		children: make(map[string][]string, len(commits)),
		sets:     map[string]map[string]bool{},
	}
	for i, cm := range commits {
		f.byHash[cm.Hash] = i
	}
	for _, cm := range commits {
		for _, p := range cm.Parents {
			if _, ok := f.byHash[p]; ok {
				f.children[p] = append(f.children[p], cm.Hash)
			}
		}
	}
	return f
}

// reach is every loaded commit reachable from the roots, stopping at (and
// excluding) the commits in stop.
func (f *gitFold) reach(roots []string, stop map[string]bool) map[string]bool {
	seen := map[string]bool{}
	queue := append([]string(nil), roots...)
	for len(queue) > 0 {
		h := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		i, ok := f.byHash[h]
		if !ok || seen[h] || stop[h] {
			continue
		}
		seen[h] = true
		queue = append(queue, f.commits[i].Parents...)
	}
	return seen
}

// set is the commits a merge brought in: reachable from its further parents
// but not from its first, and not the ancestor of anything that stays visible
// outside the merge — so folding never swallows a commit another line of
// history still builds on. Empty for a commit that is not a merge.
func (f *gitFold) set(hash string) map[string]bool {
	if got, ok := f.sets[hash]; ok {
		return got
	}
	var set map[string]bool
	if i, ok := f.byHash[hash]; ok && len(f.commits[i].Parents) > 1 {
		parents := f.commits[i].Parents
		main := f.reach(parents[:1], nil)
		main[hash] = true
		set = f.reach(parents[1:], main)
		for changed := true; changed; {
			changed = false
			for c := range set {
				for _, child := range f.children[c] {
					if child != hash && !set[child] {
						delete(set, c)
						changed = true
						break
					}
				}
			}
		}
	}
	f.sets[hash] = set
	return set
}

// foldable reports whether folding the merge would hide anything.
func (f *gitFold) foldable(hash string) bool { return len(f.set(hash)) > 0 }

// apply is the window with every folded merge's branch left out, plus how many
// commits each folded merge hides. Parents that were hidden are dropped from
// the surviving commits, so the graph follows what is shown.
func (f *gitFold) apply(folded map[string]bool) ([]gitworkbench.CommitInfo, map[string]int) {
	if len(folded) == 0 {
		return f.commits, nil
	}
	hidden := map[string]bool{}
	counts := map[string]int{}
	for _, cm := range f.commits { // newest first: an outer merge folds before the ones inside it
		if !folded[cm.Hash] || hidden[cm.Hash] {
			continue
		}
		set := f.set(cm.Hash)
		for h := range set {
			hidden[h] = true
		}
		counts[cm.Hash] = len(set)
	}
	out := make([]gitworkbench.CommitInfo, 0, len(f.commits)-len(hidden))
	for _, cm := range f.commits {
		if hidden[cm.Hash] {
			continue
		}
		if slices.ContainsFunc(cm.Parents, func(p string) bool { return hidden[p] }) {
			cm.Parents = slices.DeleteFunc(slices.Clone(cm.Parents), func(p string) bool { return hidden[p] })
		}
		out = append(out, cm)
	}
	return out, counts
}

// foldToggle is the merge row's fold control: an icon that folds the merged
// branch away (leaving how many commits it hides) or unfolds it again.
func (s *Shell) foldToggle(c *ui.Context, hash string, hiding int) {
	t := c.Theme()
	folded := hiding > 0
	svg, tip := iconFold, "Fold Merged Commits"
	if folded {
		svg, tip = iconUnfold, "Unfold Merged Commits"
	}
	ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		if folded {
			ui.Text(c, "+"+itoa(hiding)).FontSize(Typography().Micro).FontWeight(WeightSemibold).
				TextColor(t.TextMuted).SingleLine()
		}
		if gorexIconButton(c, gorexColorsOf(t.Dark), svg, tip, tip, false, gorexRowBtn, gorexRowGlyph).Clicked() {
			s.toggleFold(hash)
		}
	})
}

// graphFoldedSet is the merges this repository's graph folds (remembered per
// repository, like the hidden refs).
func (s *Shell) graphFoldedSet() map[string]bool {
	if s.git == nil || s.git.root == "" {
		return nil
	}
	list := s.settings.Workbench.GraphFolded[s.git.root]
	if len(list) == 0 {
		return nil
	}
	set := make(map[string]bool, len(list))
	for _, h := range list {
		set[h] = true
	}
	return set
}

// toggleFold folds or unfolds one merge.
func (s *Shell) toggleFold(hash string) {
	s.setFolded(hash, !s.graphFoldedSet()[hash])
}

func (s *Shell) setFolded(hash string, fold bool) {
	if s.git == nil || s.git.root == "" {
		return
	}
	root := s.git.root
	s.applySettings(func(st *settings.Settings) error {
		list := slices.DeleteFunc(slices.Clone(st.Workbench.GraphFolded[root]), func(h string) bool { return h == hash })
		if fold {
			list = append(list, hash)
		}
		all := st.Workbench.GraphFolded
		if all == nil {
			all = map[string][]string{}
		}
		if len(list) == 0 {
			delete(all, root)
		} else {
			all[root] = list
		}
		st.Workbench.GraphFolded = all
		return nil
	})
}

// unfoldFor unfolds every merge that hides hash, so a located commit is a row.
func (s *Shell) unfoldFor(hash string) {
	folded := s.graphFoldedSet()
	if len(folded) == 0 {
		return
	}
	fold := newGitFold(s.git.commits)
	for m := range folded {
		if fold.set(m)[hash] {
			s.setFolded(m, false)
		}
	}
}

// gitCommitView is everything the Commits list derives from the loaded
// window: the fold index, the commits left after folding, and their graph.
// Deriving it walks every loaded commit, so it is kept until the window or
// the folds change instead of being rebuilt every frame.
type gitCommitView struct {
	src    []gitworkbench.CommitInfo
	folded string
	fold   *gitFold
	shown  []gitworkbench.CommitInfo
	counts map[string]int
	graphs map[string]gitGraphRow
	// merged holds the commits the checked-out branch reaches; nil when no
	// branch is checked out (detached or unreadable), so nothing reads as
	// unmerged then.
	merged map[string]bool
}

// gitHeadAncestors is the set of commits reachable from the checked-out
// branch's tip within the loaded window, or nil when no loaded commit carries
// the HEAD ref.
func gitHeadAncestors(commits []gitworkbench.CommitInfo) map[string]bool {
	parents := make(map[string][]string, len(commits))
	var stack []string
	for _, cm := range commits {
		parents[cm.Hash] = cm.Parents
		for _, r := range cm.Refs {
			if r.Head {
				stack = append(stack, cm.Hash)
			}
		}
	}
	if len(stack) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(commits))
	for len(stack) > 0 {
		h := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[h] {
			continue
		}
		seen[h] = true
		stack = append(stack, parents[h]...)
	}
	return seen
}

func (s *Shell) commitView() *gitCommitView {
	src := s.git.commits
	list := s.settings.Workbench.GraphFolded[s.git.root]
	key := strings.Join(list, ",")
	if v := s.git.commitView; v != nil && v.folded == key && len(v.src) == len(src) &&
		(len(src) == 0 || &v.src[0] == &src[0]) {
		return v
	}
	v := &gitCommitView{src: src, folded: key, fold: newGitFold(src), merged: gitHeadAncestors(src)}
	folded := map[string]bool{}
	for _, h := range list {
		folded[h] = true
	}
	v.shown, v.counts = v.fold.apply(folded)
	rows := gitHistoryGraph(v.shown)
	v.graphs = make(map[string]gitGraphRow, len(rows))
	for i, cm := range v.shown {
		v.graphs[cm.Hash] = rows[i]
	}
	s.git.commitView = v
	return v
}
