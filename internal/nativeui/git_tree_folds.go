package nativeui

import (
	"strings"

	"github.com/thewei/solco/internal/settings"
)

/**
 * [INPUT]: 侧栏 Git 引用树的折叠键（section/<名>、local|remote|tag/<完整名>），settings.Workbench.TreeFolds
 * [OUTPUT]: gitFolded/setGitFolded —— 区块与树文件夹的折叠状态，按仓库根持久化；默认只展开 Branches，其余区块折叠；搜索期间的折叠只在会话内
 * [POS]: 侧栏引用树的唯一折叠状态来源；只存与默认不同的项，纯展示偏好，不碰 Git
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// gitSectionsClosedByDefault are the sidebar sections that start folded: the
// Branches section is the one a repository's user opens first (2026-11-03,
// user request).
var gitSectionsClosedByDefault = map[string]bool{
	"section/Worktrees":  true,
	"section/Remotes":    true,
	"section/Tags":       true,
	"section/Stashes":    true,
	"section/Submodules": true,
}

// gitFolded tells whether a tree row or section is folded: the user's saved
// choice for this repository, else the default. A search-scoped key lives in
// the session only (a filtered tree starts fully open).
func (s *Shell) gitFolded(key string) bool {
	if strings.HasPrefix(key, "search/") {
		return s.gitTreeClosed[key]
	}
	if s.git != nil {
		if v, ok := s.settings.Workbench.TreeFolds[s.git.root][key]; ok {
			return v
		}
	}
	return gitSectionsClosedByDefault[key]
}

// setGitFolded folds or opens one row or section and remembers it per
// repository; a choice equal to the default is not stored.
func (s *Shell) setGitFolded(key string, closed bool) {
	if strings.HasPrefix(key, "search/") {
		if closed {
			s.gitTreeClosed[key] = true
		} else {
			delete(s.gitTreeClosed, key)
		}
		return
	}
	if s.git == nil || s.git.root == "" || s.gitFolded(key) == closed {
		return
	}
	root := s.git.root
	s.applySettings(func(st *settings.Settings) error {
		all := st.Workbench.TreeFolds
		if all == nil {
			all = map[string]map[string]bool{}
		}
		repo := make(map[string]bool, len(all[root])+1)
		for k, v := range all[root] {
			repo[k] = v
		}
		if closed == gitSectionsClosedByDefault[key] {
			delete(repo, key)
		} else {
			repo[key] = closed
		}
		if len(repo) == 0 {
			delete(all, root)
		} else {
			all[root] = repo
		}
		st.Workbench.TreeFolds = all
		return nil
	})
}
