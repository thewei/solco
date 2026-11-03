package nativeui

/**
 * [INPUT]: Git reference names, native list rows, local repo projection and selection state
 * [OUTPUT]: Slash-grouped, filterable, collapsible Git ref tree rendering helpers
 * [POS]: Git reference tree reuse only, no read-time Git IO or mutation authority
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"cmp"
	"slices"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

// gitRefNode is a disposable slash-delimited tree. The only persistent state
// is the collapsed-folder key, never a copy of the repository reference data.
type gitRefNode struct {
	name     string
	full     string
	children []*gitRefNode
	leaf     bool
}

func gitRefTree(names []string, filter string) []*gitRefNode {
	var roots []*gitRefNode
	q := strings.ToLower(strings.TrimSpace(filter))
	for _, full := range names {
		if q != "" && !strings.Contains(strings.ToLower(full), q) {
			continue
		}
		siblings := &roots
		prefix := ""
		for _, name := range strings.Split(full, "/") {
			if name == "" {
				continue
			}
			if prefix != "" {
				prefix += "/"
			}
			prefix += name
			var node *gitRefNode
			for _, candidate := range *siblings {
				if candidate.name == name {
					node = candidate
					break
				}
			}
			if node == nil {
				node = &gitRefNode{name: name, full: prefix}
				*siblings = append(*siblings, node)
			}
			siblings = &node.children
			if prefix == full {
				node.leaf = true
			}
		}
	}
	var sortTree func([]*gitRefNode)
	sortTree = func(nodes []*gitRefNode) {
		sort.Slice(nodes, func(i, j int) bool {
			ai, aj := len(nodes[i].children) > 0, len(nodes[j].children) > 0
			if ai != aj {
				return ai
			}
			return strings.ToLower(nodes[i].name) < strings.ToLower(nodes[j].name)
		})
		for _, node := range nodes {
			sortTree(node.children)
		}
	}
	sortTree(roots)
	return roots
}

func gitRefMatches(filter string, values ...string) bool {
	q := strings.ToLower(strings.TrimSpace(filter))
	if q == "" {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), q) {
			return true
		}
	}
	return false
}

func (s *Shell) gitEmptySection(c *ui.Context, count int) {
	if count > 0 {
		return
	}
	label := "None"
	if strings.TrimSpace(s.gitRefsFilter) != "" {
		label = "No matches"
	}
	ui.Text(c, label).Padding(2, 0, 4, treeGlyphX(0)).FontSize(Typography().BodySmall).TextColor(c.Theme().TextMuted)
}

// A filtered tree starts fully expanded, yet a user's click must still fold
// it visibly. Query-scoped keys keep search-only folds separate from normal
// browsing and prevent old closed folders from hiding matching results.
func (s *Shell) gitFoldKey(normal string) string {
	if q := strings.ToLower(strings.TrimSpace(s.gitRefsFilter)); q != "" {
		return "search/" + q + "/" + normal
	}
	return normal
}

// gitRefKinds names a ref tree's rows for assistive technology and tests.
var gitRefKinds = map[string]string{"local": "Branch", "remote": "Remote branch", "tag": "Tag"}

// gitRenderRefTree draws one slash-grouped ref tree from level (0 for the
// rows directly under a section).
func (s *Shell) gitRenderRefTree(c *ui.Context, scope string, roots []*gitRefNode, level int, click func(string), menu func(string, *ui.Menu)) {
	if len(roots) == 0 {
		s.gitEmptySection(c, 0)
		return
	}
	leafGlyph := iconBranch
	switch scope {
	case "tag":
		leafGlyph = iconTag
	case "remote":
		leafGlyph = iconCloud
	}
	var visit func(*gitRefNode, int)
	visit = func(n *gitRefNode, level int) {
		folder := len(n.children) > 0
		key := s.gitFoldKey(scope + "/" + n.full)
		closed := s.gitTreeClosed[key]
		value := n.full
		// HEAD/current branch is identified by the snapshot, never inferred from name.
		current := false
		if scope == "local" {
			for _, b := range s.git.branches {
				if b.Name == value {
					current = b.Current
					break
				}
			}
		}
		item := gitTreeItem{title: n.name, tooltip: value, kind: gitRefKinds[scope], glyph: leafGlyph,
			level: level, folder: folder, closed: closed, current: current}
		if current && !folder {
			item.badge = upstreamBadge(s.git.upstream)
		}
		if scope == "local" && !current && !folder {
			item.hint = "Double-click to check out " + value
		}
		remoteRoot := scope == "remote" && folder && level == 0
		if folder {
			item.kind, item.glyph = "Folder", iconFolder
			if remoteRoot {
				item.kind, item.glyph = "Remote", iconCloud
				if url := s.remoteURL(n.full); url != "" {
					item.tooltip = url
				}
			}
		} else {
			item.ref = gitRefNamespace[scope] + value
		}
		var eye *gitEye
		if refs := s.graphHideable(scope, n); len(refs) > 0 {
			hidden := s.graphHiddenSet()
			allHidden := !slices.ContainsFunc(refs, func(ref string) bool { return !hidden[ref] })
			eye = &gitEye{hidden: allHidden, toggle: func() { s.setGraphHidden(refs, !allHidden) }}
			item.eye = eye
		}
		s.gitTreeRow(c, item, func() {
			if folder {
				s.gitTreeClosed[key] = !s.gitTreeClosed[key]
			} else {
				click(value)
			}
		}, func(m *ui.Menu) {
			if n.leaf {
				menu(value, m)
			}
			if remoteRoot {
				s.remoteMenu(m, n.full)
				m.Separator()
			}
			if eye != nil && m.Item(eye.label()).Chosen() {
				eye.toggle()
			}
			if folder && m.Item("Collapse Folder").Chosen() {
				s.gitTreeClosed[key] = true
			}
		})
		if folder && !closed {
			for _, child := range n.children {
				visit(child, level+1)
			}
		}
	}
	for _, root := range roots {
		visit(root, level)
	}
}

// gitTreeItem is one row of a Git sidebar tree.
type gitTreeItem struct {
	title, tooltip, kind    string
	glyph                   *ui.SVG
	level                   int
	folder, closed, current bool
	// badge is a short muted fact at the row's right end (the current
	// branch's ahead/behind against its upstream, as Fork's "1↓1↑").
	badge string
	// hint is said once, on the first click that only selects the row, to
	// tell what a double click will do (check out a branch).
	hint string
	// ref is the full ref a single click locates in the graph (leaf rows);
	// commit is the commit it locates when the row has no ref of its own (a
	// stash, a detached worktree).
	ref, commit string
	// review, when set, is what the reviewer opens for the located commit.
	review string
	// eye hides the row's refs from the graph; nil where nothing can be hidden.
	eye *gitEye
}

// gitEye is a row's graph-visibility control: hidden is its current state,
// toggle flips it.
type gitEye struct {
	hidden bool
	toggle func()
}

func (e *gitEye) label() string {
	if e.hidden {
		return "Show in Graph"
	}
	return "Hide from Graph"
}

// gitTreeRow draws one row on the shared sidebar grid. A stash subject can
// repeat; its title carries the unique stash reference. A leaf row is
// deliberate: one click selects it, only a double click or Enter runs action
// (folders keep folding on one click, which changes nothing in the repo).
func (s *Shell) gitTreeRow(c *ui.Context, it gitTreeItem, action func(), menu func(*ui.Menu)) {
	var trailing func()
	if it.current || it.badge != "" {
		trailing = func() {
			if it.badge != "" {
				ui.Text(c, it.badge).FontSize(Typography().Caption).TextColor(c.Theme().TextMuted).Shrink(0)
			}
			if it.current {
				ui.Icon(c, iconCheck).FontSize(11).TextColor(c.Theme().Text).Shrink(0)
			}
		}
	}
	key := gitRefRowKey(it.title, it.tooltip)
	var eyeAction *treeRowAction
	if it.eye != nil {
		glyph := iconEye
		if it.eye.hidden {
			glyph = iconEyeOff
		}
		eyeAction = &treeRowAction{Label: it.eye.label(), Glyph: glyph, Run: it.eye.toggle}
	}
	row, clicked := s.sidebarTreeRow(c, sidebarTreeSpec{
		Action: eyeAction, ActionSticky: it.eye != nil && it.eye.hidden, Dim: it.eye != nil && it.eye.hidden,
		Key: key, Title: it.title, Label: it.kind + " " + it.title,
		Tooltip: it.tooltip, Level: it.level, Folder: it.folder, Expanded: !it.closed,
		Glyph: it.glyph, Current: it.current, Trailing: trailing, Menu: menu, Nav: &s.nav.gitRefs,
		Deliberate: true, Selected: s.gitRefsSelected == key,
		// Folders open and close with motion; typing in the filter swaps whole
		// result sets, which should not animate row by row.
		Motion: strings.TrimSpace(s.gitRefsFilter) == "",
		OnSelect: func() {
			s.gitRefsSelected = key
			if it.hint != "" && !s.gitRefsHinted {
				s.gitRefsHinted = true
				s.pendingToast = it.hint
			}
			// Located last: its own notice (a hidden ref) outranks the hint.
			if it.ref != "" {
				s.locateRef(it.ref)
			} else if it.commit != "" {
				s.commitsMode = true
				s.revealCommitReviewing(it.commit, cmp.Or(it.review, it.commit))
			}
		},
	})
	if s.gitRefsReveal == key {
		row.ScrollIntoView()
		s.gitRefsReveal = ""
	}
	if clicked {
		action()
	}
}

// gitRefRowKey is a ref row's identity: its title and tooltip, which no two
// rows of one tree share.
func gitRefRowKey(title, tooltip string) string { return "git-ref:" + title + "@" + tooltip }

// upstreamBadge is "↑1 ↓2" for a branch ahead/behind its upstream, "" when
// level or untracked.
func upstreamBadge(st gitworkbench.UpstreamStatus) string {
	if !st.OK {
		return ""
	}
	var parts []string
	if st.Ahead > 0 {
		parts = append(parts, "↑"+itoa(st.Ahead))
	}
	if st.Behind > 0 {
		parts = append(parts, "↓"+itoa(st.Behind))
	}
	return strings.Join(parts, " ")
}
