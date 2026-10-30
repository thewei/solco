package nativeui

/**
 * [INPUT]: 一个列表的搜索状态（headerSearch）与摘要内容、gorex 图标按钮
 * [OUTPUT]: gitListHead —— Git 列表（Changes 文件、Commits 提交、提交文件）共用的一行头：搜索钮 + 摘要，点开才在其下展开搜索字段；filteredCommits —— Commits 搜索的唯一筛选
 * [POS]: Git 面板的列表头呈现；与侧栏头共用 headerSearch，常驻搜索框不再占行
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/gitworkbench"
)

// gitListHead is the one-row head of a Git list: the search button, then the
// list's summary. The search field shows beneath the row only once opened.
func (s *Shell) gitListHead(c *ui.Context, search *headerSearch, padX float32, summary func()) {
	k := gorexColorsOf(c.Theme().Dark)
	ui.Column(c).FillWidth().Shrink(0).Children(func() {
		ui.Row(c).FillWidth().Height(32).Padding(0, padX).Gap(6).AlignItems(ui.Center).Children(func() {
			search.button(c, k)
			summary()
		})
		if *search.Open {
			ui.Row(c).FillWidth().Padding(0, padX, 6).Children(func() { search.field(c) })
		}
	})
}

// filteredCommits is the history the Commits search leaves: subject, author
// or a hash prefix match.
func (s *Shell) filteredCommits() []gitworkbench.CommitInfo {
	q := strings.ToLower(strings.TrimSpace(s.gitCommitFilter))
	if q == "" {
		return s.git.commits
	}
	var entries []gitworkbench.CommitInfo
	for _, cm := range s.git.commits {
		if strings.Contains(strings.ToLower(cm.Subject), q) ||
			strings.Contains(strings.ToLower(cm.Author), q) ||
			strings.HasPrefix(cm.Hash, q) {
			entries = append(entries, cm)
		}
	}
	return entries
}
