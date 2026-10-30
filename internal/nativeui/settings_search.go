package nativeui

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/usage"
)

/**
 * [INPUT]: 依赖 settingsSections 导航表、workboardSettingsSection 与门禁、usage.All 的家名、controls.go 的 searchField、components.go 的 navTileButtonDetail
 * [OUTPUT]: settingsEntry、settingsEntries（设置项标签索引：每个页面一条 + 每个静态设置行一条 + 配额每家一条）、matchSettings（大小写不敏感子串过滤）、Shell.settingsSearch（侧栏顶的搜索框与命中列表）
 * [POS]: 设置侧栏搜索（2026-10-30 UX audit 3，议题 4）：命中项是「设置项 + 所在页」，点击进入该页并清空搜索；索引是手写标签表，TestSettingsIndexCoversEveryRow 扫描源码里的 row("…") 防止漂移；动态行（Agents 的每个 agent、Work Board 插件设置）只以页面级条目出现
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// settingsEntry is one searchable setting: its label, the group it sits in
// and the page that holds it.
type settingsEntry struct {
	Path  string
	Group string
	Label string
}

// settingsLabels are the static rows of each page, by path. Keep in step
// with the row("…") calls; the index test enforces it.
var settingsLabels = map[string][][2]string{
	"/settings/general": {
		{"Appearance", "Theme"}, {"Appearance", "Sidebar density"}, {"Appearance", "Increase contrast"},
		{"Appearance", "Serif chat text"}, {"Appearance", "Window opacity"},
		{"Window", "Show Tabs in Sidebar"}, {"Window", "Restore window position"},
	},
	"/settings/terminal": {
		{"Font", "Font family"}, {"Font", "Font size"}, {"Font", "Line height"},
		{"Behavior", "Scrollback (MB)"}, {"Behavior", "Use Option as Alt"},
		{"Behavior", "Transparent background"}, {"Behavior", "Theme"},
	},
	"/settings/voice": {
		{"Transcription", "Endpoint"}, {"Transcription", "API key"}, {"Transcription", "Model"},
	},
	"/settings/providers": {
		{"", "Integration"}, {"", "Status hook"},
	},
	"/settings/git": {
		{"Commit message rules", "Commit message rules"}, {"Commit message generator", "Pi path"},
	},
	"/settings/diagnostics": {
		{"About", "Solco"}, {"About", "System"}, {"About", "Herdr"}, {"About", "Session"}, {"About", "Log file"},
		{"Compatibility", "Rich chat view"}, {"Compatibility", "Rich file preview"},
		{"", "Copy Diagnostics"}, {"Logs", "Logs"},
	},
	"/settings/workboard": {
		{"View", "Work board"}, {"Scope", "Applies to"},
	},
}

// settingsEntries is the whole index: each page by name, its rows, and one
// "Show in Quota" per provider. The Work Board page joins only while its
// plugin gate is open, like its navigation entry.
func (s *Shell) settingsEntries() []settingsEntry {
	var out []settingsEntry
	add := func(section settingsSection) {
		out = append(out, settingsEntry{Path: section.path, Label: section.label})
		for _, pair := range settingsLabels[section.path] {
			out = append(out, settingsEntry{Path: section.path, Group: pair[0], Label: pair[1]})
		}
		if section.path == usageSettingsPath {
			for _, id := range usage.All {
				out = append(out, settingsEntry{Path: section.path, Group: id.Title(), Label: "Show " + id.Title() + " in Quota"})
			}
		}
	}
	for _, section := range settingsSections {
		add(section)
	}
	if s.workboardAvailable() {
		add(workboardSettingsSection)
	}
	return out
}

// matchSettings keeps the entries whose label, group or page name contains
// q (case-insensitive), in index order.
func matchSettings(entries []settingsEntry, q string) []settingsEntry {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	var out []settingsEntry
	for _, e := range entries {
		page := strings.ToLower(settingsSectionFor(e.Path).label)
		if strings.Contains(strings.ToLower(e.Label), q) || strings.Contains(strings.ToLower(e.Group), q) || strings.Contains(page, q) {
			out = append(out, e)
		}
	}
	return out
}

// settingsSearch draws the search box at the head of the Settings sidebar
// and, while it holds text, the matching settings in place of the page list.
// It reports whether it replaced the list. Return opens the first hit.
func (s *Shell) settingsSearch(c *ui.Context) bool {
	field := searchField(c, &s.settingsQuery).Label("Search settings").Placeholder("Search settings…").FillWidth()
	hits := matchSettings(s.settingsEntries(), s.settingsQuery)
	if strings.TrimSpace(s.settingsQuery) == "" {
		return false
	}
	open := func(e settingsEntry) {
		s.settingsQuery = ""
		s.router.Push(e.Path)
	}
	if field.Submitted() && len(hits) > 0 {
		open(hits[0])
		return true
	}
	if len(hits) == 0 {
		ui.Text(c, "No matching settings").FontSize(Typography().BodySmall).TextColor(c.Theme().TextMuted).Padding(6, 8)
		return true
	}
	for i, e := range hits {
		section := settingsSectionFor(e.Path)
		detail := section.label
		if e.Label == section.label {
			detail = ""
		}
		btn := navTileButtonDetail(c.Key("settings-hit:"+itoa(i)), section.icon, section.tone, e.Label, detail, false)
		if btn.Clicked() {
			open(e)
			break
		}
	}
	return true
}
