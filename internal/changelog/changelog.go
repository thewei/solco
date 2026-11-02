package changelog

/**
 * [INPUT]: snapshot.md —— 发布流程（.agents/skills/solco-release）在发布时从仓库根
 * CHANGELOG.md 全量同步写入并随发布提交
 * [OUTPUT]: Current() —— 当前最新版本的分组改进（New / Fixed / Improved / Internal）
 *           All() —— 解析快照中的所有已发布版本列表
 *           Between(cur, target) —— 过滤出两个版本间的所有更新，用于跨版本总结
 *           CompareVersions(v1, v2) —— 语义化版本比对（-1, 0, 1）
 *           ParseReleases(text) —— 通用 Markdown 发布日志解析器
 * [POS]: Settings → About 页与更新检查系统的数据源。支持只读嵌入式快照与动态网络解析：
 * 离线包内有完整版本记录，在线时亦能解析 GitHub Release Notes
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	_ "embed"
	"strconv"
	"strings"
)

//go:embed snapshot.md
var snapshot string

// Group is one titled section of a release ("New", "Fixed", …).
type Group struct {
	Name  string
	Items []string
}

// Release is one section of the snapshot: version, date, groups.
type Release struct {
	Version string
	Date    string
	Groups  []Group
}

// All parses the embedded snapshot and returns all its releases, newest first.
func All() []Release {
	return ParseReleases(snapshot)
}

// Current parses the snapshot and returns its newest release.
func Current() (Release, bool) {
	all := All()
	if len(all) == 0 {
		return Release{}, false
	}
	return all[0], true
}

// Between returns all releases newer than currentVersion up to targetVersion (inclusive),
// ordered newest first. If targetVersion is empty, all releases newer than currentVersion
// are returned.
func Between(currentVersion, targetVersion string) []Release {
	var out []Release
	for _, rel := range All() {
		// Newer than currentVersion
		if CompareVersions(rel.Version, currentVersion) > 0 {
			// At or below targetVersion
			if targetVersion == "" || CompareVersions(rel.Version, targetVersion) <= 0 {
				out = append(out, rel)
			}
		}
	}
	return out
}

// ParseReleases parses multiple releases from markdown text.
// A `## v…` line opens a release, `### Name` opens a group, `- text` adds an item.
func ParseReleases(text string) []Release {
	var releases []Release
	var cur *Release
	var curGroup *Group

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "## v"):
			version, date, _ := strings.Cut(strings.TrimPrefix(trimmed, "## v"), "—")
			releases = append(releases, Release{
				Version: strings.TrimSpace(version),
				Date:    strings.TrimSpace(date),
			})
			cur = &releases[len(releases)-1]
			curGroup = nil
		case cur == nil:
			continue
		case strings.HasPrefix(trimmed, "### "):
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))
			cur.Groups = append(cur.Groups, Group{Name: name})
			curGroup = &cur.Groups[len(cur.Groups)-1]
		case strings.HasPrefix(trimmed, "- "):
			if curGroup != nil {
				curGroup.Items = append(curGroup.Items, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			}
		}
	}
	return releases
}

// CompareVersions compares two version strings (e.g. "0.12.0", "v0.11.0", "dev").
// Returns:
//
//	-1 if v1 < v2
//	 0 if v1 == v2
//	 1 if v1 > v2
func CompareVersions(v1, v2 string) int {
	v1 = strings.TrimPrefix(strings.TrimSpace(v1), "v")
	v2 = strings.TrimPrefix(strings.TrimSpace(v2), "v")

	if v1 == v2 {
		return 0
	}
	if v1 == "dev" {
		return -1
	}
	if v2 == "dev" {
		return 1
	}

	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	n := max(len(parts1), len(parts2))
	for i := 0; i < n; i++ {
		var n1, n2 int
		if i < len(parts1) {
			raw := strings.Split(parts1[i], "-")[0]
			n1, _ = strconv.Atoi(raw)
		}
		if i < len(parts2) {
			raw := strings.Split(parts2[i], "-")[0]
			n2, _ = strconv.Atoi(raw)
		}
		if n1 < n2 {
			return -1
		}
		if n1 > n2 {
			return 1
		}
	}
	return 0
}
