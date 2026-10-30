package nativeui

/**
 * [INPUT]: gitworkbench.ListCommitGraph/ResolveCommit、设置里按仓库保存的隐藏 ref 清单、侧栏引用树节点
 * [OUTPUT]: 图谱与引用的联动——侧栏眼睛隐藏/显示分支（本地/远程叶子与整个目录，当前分支不可隐藏）、Show All 与 N hidden 计数、点引用定位到其提交（窗口按 git rev-list 算出的行号精确扩大，最多 20000）并高亮一瞬、仅重读图谱的 loadCommitGraph
 * [POS]: Commits 视图的图谱范围与定位呈现；可见性由 Git 的 --exclude 判定，隐藏清单只是本地偏好，不碰 Git 配置；没有第二套提交可达性推断
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
	"github.com/thewei/solco/internal/settings"
)

const (
	// gitGraphDefaultWindow is the commits the graph loads at first.
	gitGraphDefaultWindow = 200
	// gitGraphMargin is the rows kept below a commit the window was grown to.
	gitGraphMargin = 100
	// gitFlashFor is how long a located commit stays highlighted.
	gitFlashFor = 1200 * time.Millisecond
)

// gitRefNamespace maps a ref tree's scope to its full-name prefix.
var gitRefNamespace = map[string]string{
	"local":  gitworkbench.RefHeadsPrefix,
	"remote": gitworkbench.RefRemotesPrefix,
	"tag":    gitworkbench.RefTagsPrefix,
}

// graphWindow is the number of commits the graph reads now.
func (s *Shell) graphWindow() int { return max(s.git.graphLimit, gitGraphDefaultWindow) }

// graphHiddenList is the full ref names hidden in this repository's graph.
func (s *Shell) graphHiddenList() []string {
	if s.git == nil || s.git.root == "" {
		return nil
	}
	return s.settings.Workbench.GraphHidden[s.git.root]
}

func (s *Shell) graphHiddenSet() map[string]bool {
	set := map[string]bool{}
	for _, ref := range s.graphHiddenList() {
		set[ref] = true
	}
	return set
}

// graphHiddenCount counts the hidden refs that still exist.
func (s *Shell) graphHiddenCount() int {
	hidden := s.graphHiddenSet()
	if len(hidden) == 0 || s.git == nil {
		return 0
	}
	n := 0
	for _, b := range s.git.branches {
		if hidden[gitworkbench.RefHeadsPrefix+b.Name] {
			n++
		}
	}
	for _, b := range s.git.remoteBranches {
		if hidden[gitworkbench.RefRemotesPrefix+b.Remote+"/"+b.Name] {
			n++
		}
	}
	return n
}

// graphHideable lists the full refs a tree node stands for that the eye may
// hide: its own ref when it is a branch, and every branch below it. The
// checked-out branch never is one — HEAD is always in the graph.
func (s *Shell) graphHideable(scope string, n *gitRefNode) []string {
	if scope == "tag" {
		return nil
	}
	var refs []string
	var walk func(*gitRefNode)
	walk = func(n *gitRefNode) {
		if n.leaf && !(scope == "local" && s.isCurrentBranch(n.full)) {
			refs = append(refs, gitRefNamespace[scope]+n.full)
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(n)
	return refs
}

func (s *Shell) isCurrentBranch(name string) bool {
	for _, b := range s.git.branches {
		if b.Name == name {
			return b.Current
		}
	}
	return false
}

// setGraphHidden hides or shows refs in the graph, remembers the choice for
// this repository and re-reads the graph.
func (s *Shell) setGraphHidden(refs []string, hide bool) {
	if s.git == nil || s.git.root == "" || len(refs) == 0 {
		return
	}
	root := s.git.root
	s.applySettings(func(st *settings.Settings) error {
		set := map[string]bool{}
		for _, ref := range st.Workbench.GraphHidden[root] {
			set[ref] = true
		}
		for _, ref := range refs {
			if hide {
				set[ref] = true
			} else {
				delete(set, ref)
			}
		}
		list := make([]string, 0, len(set))
		for ref := range set {
			list = append(list, ref)
		}
		slices.Sort(list)
		all := st.Workbench.GraphHidden
		if all == nil {
			all = map[string][]string{}
		}
		if len(list) == 0 {
			delete(all, root)
		} else {
			all[root] = list
		}
		st.Workbench.GraphHidden = all
		return nil
	})
	s.loadCommitGraph(nil)
}

// showAllInGraph clears every hidden ref of this repository.
func (s *Shell) showAllInGraph() {
	s.setGraphHidden(slices.Clone(s.graphHiddenList()), false)
}

// loadCommitGraph re-reads only the commit graph (refs unchanged), keeping
// the old list on screen until the new one lands; after runs on the UI lane
// once it has.
func (s *Shell) loadCommitGraph(after func()) {
	if s.git == nil || s.git.root == "" {
		return
	}
	root, limit, hidden := s.git.root, s.graphWindow(), slices.Clone(s.graphHiddenList())
	gen := s.git.graphGen.Add(1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), gitworkbench.ReadTimeout*2)
		defer cancel()
		commits, err := s.git.runner.ListCommitGraph(ctx, root, gitworkbench.GraphQuery{Limit: limit, Hidden: hidden})
		s.applyGuarded(s.git.graphGen.Load, gen, func() {
			if err != nil || s.git == nil || root != s.git.root {
				return
			}
			s.git.commits = commits
			if after != nil {
				after()
			}
		})
	}()
}

// locateRef is the sidebar's single click on a ref: switch to Commits, select
// the commit the ref points at and scroll the graph to it. A hidden ref has no
// place in the graph, so it says so instead.
func (s *Shell) locateRef(full string) {
	if s.git == nil || s.git.root == "" || full == "" {
		return
	}
	if s.graphHiddenSet()[full] {
		s.pendingToast = "Hidden from the graph. Click its eye to show it."
		return
	}
	s.commitsMode = true
	root := s.git.root
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), gitworkbench.ReadTimeout)
		defer cancel()
		hash, err := s.git.runner.ResolveCommit(ctx, root, full)
		s.applyOnUI(func() {
			if s.git == nil || s.git.root != root {
				return
			}
			if err != nil {
				s.pendingToast = "That reference no longer exists."
				return
			}
			s.revealCommit(hash)
		})
	}()
}

// revealCommit selects a commit in the Commits view and queues the scroll and
// flash for the next frame. A commit past the loaded window grows the window
// to exactly reach it (and re-reads), or says that the graph never shows it.
func (s *Shell) revealCommit(hash string) { s.revealCommitReviewing(hash, hash) }

// revealCommitReviewing locates hash in the graph but opens review in the
// reviewer: a stash is found on the commit it was made on yet reviewed as
// itself.
func (s *Shell) revealCommitReviewing(hash, review string) {
	s.reveal(hash, review, false)
}

func (s *Shell) reveal(hash, review string, widened bool) {
	if s.git == nil || hash == "" {
		return
	}
	s.commitsMode = true
	s.gitCommitFilter = ""
	if !slices.ContainsFunc(s.git.commits, func(cm gitworkbench.CommitInfo) bool { return cm.Hash == hash }) {
		if widened {
			s.pendingToast = "That commit is not in the graph."
			return
		}
		s.widenGraphTo(hash, func() { s.reveal(hash, review, true) })
		return
	}
	s.unfoldFor(hash)
	s.reviewCommit(review)
	s.git.locateHash = hash
}

// widenGraphTo grows the graph window to the row the commit has (found by
// Git, not by stepping), re-reads the graph, then runs after.
func (s *Shell) widenGraphTo(hash string, after func()) {
	root, hidden := s.git.root, slices.Clone(s.graphHiddenList())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), gitworkbench.ReadTimeout*2)
		defer cancel()
		pos, err := s.git.runner.GraphPosition(ctx, root, gitworkbench.GraphQuery{Hidden: hidden}, hash)
		s.applyOnUI(func() {
			if s.git == nil || s.git.root != root {
				return
			}
			switch {
			case err != nil || pos < 0:
				s.pendingToast = "That commit is not in the graph."
			case pos >= gitworkbench.MaxGraphLimit:
				s.pendingToast = "That commit is older than the graph's reach (" + itoa(gitworkbench.MaxGraphLimit) + " commits)."
			default:
				s.git.graphLimit = min(max(pos+gitGraphMargin, s.graphWindow()), gitworkbench.MaxGraphLimit)
				s.loadCommitGraph(after)
			}
		})
	}()
}

// consumeLocate scrolls the open Commits list to the commit revealCommit
// queued, once it is a row, and starts its flash.
func (s *Shell) consumeLocate(entries []gitworkbench.CommitInfo) {
	hash := s.git.locateHash
	if hash == "" {
		return
	}
	for i, cm := range entries {
		if cm.Hash != hash {
			continue
		}
		s.commitsList.ScrollTo(i, ui.Center)
		s.git.locateHash = ""
		s.git.flashHash = hash
		time.AfterFunc(gitFlashFor, func() {
			s.applyOnUI(func() {
				if s.git != nil && s.git.flashHash == hash {
					s.git.flashHash = ""
				}
			})
		})
		return
	}
}
