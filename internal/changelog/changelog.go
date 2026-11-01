package changelog

/**
 * [INPUT]: snapshot.md —— 发布流程（.agents/skills/solco-release）在发布时从仓库根
 * CHANGELOG.md 截取当前版本小节写入并随发布提交
 * [OUTPUT]: Current() —— 当前版本的分组改进（New / Fixed / Improved / Internal）
 * [POS]: Settings → About 页的数据源。只读嵌入式快照：不读网络、不读运行时文件，
 * 离线包里永远有内容；解析规则与 CHANGELOG.md 的写作约定一一对应
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	_ "embed"
	"strings"
)

//go:embed snapshot.md
var snapshot string

// Group is one titled section of a release ("New", "Fixed", …).
type Group struct {
	Name  string
	Items []string
}

// Release is the top section of the snapshot: version, date, groups.
type Release struct {
	Version string
	Date    string
	Groups  []Group
}

// Current parses the snapshot and returns its newest release. A `## v…`
// line opens a release (a second one ends the first), `### Name` opens a
// group, `- text` adds an item to the open group. ok is false when the
// snapshot holds no release section at all.
func Current() (Release, bool) {
	var rel Release
	started := false
	var group *Group
	for _, line := range strings.Split(snapshot, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "## v"):
			if started {
				return rel, true // a second release header ends the first
			}
			started = true
			version, date, _ := strings.Cut(strings.TrimPrefix(line, "## v"), "—")
			rel.Version = strings.TrimSpace(version)
			rel.Date = strings.TrimSpace(date)
		case !started:
			continue
		case strings.HasPrefix(line, "### "):
			rel.Groups = append(rel.Groups, Group{Name: strings.TrimSpace(strings.TrimPrefix(line, "### "))})
			group = &rel.Groups[len(rel.Groups)-1]
		case strings.HasPrefix(line, "- "):
			if group != nil {
				group.Items = append(group.Items, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
			}
		}
	}
	return rel, started
}
