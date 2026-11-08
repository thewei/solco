package nativeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: icons.go 的 iconAgent / iconSparkles、page_settings.go 的 settingsSections
 * [OUTPUT]: 测试（无导出符号）
 * [POS]: 2026-11-07 第二、三轮决定的图标分配守卫：通用 Agent 与 Assistants 不再共用一个图标，Settings 导航里每个分区各有自己的图标
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestAgentsAndAssistantsDoNotShareAGlyph(t *testing.T) {
	if iconAgent == iconSparkles {
		t.Fatal("the generic Agent glyph and the Assistants glyph must be different icons")
	}
	byLabel := map[string]settingsSection{}
	for _, sec := range settingsSections {
		byLabel[sec.label] = sec
	}
	agents, archived := byLabel["Agents"], byLabel["Archived Assistants"]
	if agents.icon != iconAgent {
		t.Error("Settings ▸ Agents must wear the generic Agent glyph")
	}
	if archived.icon != iconSparkles {
		t.Error("Settings ▸ Archived Assistants must wear the Assistants glyph (sparkles), not the Agent one")
	}
	seen := map[*ui.SVG]string{}
	for _, sec := range settingsSections {
		if prev, dup := seen[sec.icon]; dup {
			t.Errorf("Settings sections %q and %q share one icon", prev, sec.label)
		}
		seen[sec.icon] = sec.label
	}
}
