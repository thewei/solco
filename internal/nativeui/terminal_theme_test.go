/**
 * [INPUT]: 依赖 internal/nativeui 的 applyGorexTerminalTheme/terminalThemeDisplay/terminalThemeChoices；依赖 third_party/mygo/plugins/terminal 的 GhosttyTheme/GhosttyThemes
 * [OUTPUT]: Ghostty 主题采纳回归——固定主题生效、未知主题回落 gorex 双配色并保留对比度保障、默认行为不变、picker 选项含 Default 与 Ghostty 全量、display 映射
 * [POS]: nativeui 终端主题采纳的回归文件，与 gorex_style.go 的 applyGorexTerminalTheme 同构；终端构建点回归在 terminal 相关测试里
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package nativeui

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
)

// A pinned theme replaces both palettes and keeps the contrast guard; the
// window's appearance no longer decides the colors.
func TestPinnedThemeOverridesGorexDarker(t *testing.T) {
	var opts terminal.Options
	applyGorexTerminalTheme(&opts, "dark", "Dracula")
	if opts.Theme == nil {
		t.Fatal("pinned theme: Theme is nil")
	}
	if opts.DarkTheme != nil {
		t.Fatal("pinned theme: DarkTheme set; a pinned theme ignores appearance")
	}
	if opts.MinimumContrast != 1.3 {
		t.Fatalf("pinned theme: MinimumContrast = %v, want 1.3", opts.MinimumContrast)
	}
}

// An unknown theme name (a deleted user theme file, say) falls back to the
// gorex palettes instead of an unthemed terminal.
func TestUnknownThemeFallsBackToGorex(t *testing.T) {
	var opts terminal.Options
	applyGorexTerminalTheme(&opts, "system", "Not A Theme")
	if opts.Theme == nil || opts.DarkTheme == nil {
		t.Fatal("unknown theme: gorex palettes not applied")
	}
	if opts.MinimumContrast != 1.3 {
		t.Fatalf("fallback: MinimumContrast = %v, want 1.3", opts.MinimumContrast)
	}
}

// The default keeps the follow-appearance gorex palettes.
func TestDefaultThemeFollowsAppearance(t *testing.T) {
	var opts terminal.Options
	applyGorexTerminalTheme(&opts, "system", "")
	if opts.Theme == nil || opts.DarkTheme == nil {
		t.Fatal("default: both palettes expected for the system appearance")
	}
	if opts.MinimumContrast != 1.3 {
		t.Fatalf("default: MinimumContrast = %v, want 1.3", opts.MinimumContrast)
	}
}

// The picker always offers the follow-appearance default plus every theme
// the Ghostty library (and the user's themes directory) carries.
func TestThemeChoicesCarryDefaultAndGhostty(t *testing.T) {
	choices := terminalThemeChoices()
	if len(choices) < 2 || choices[0] != terminalThemeDefault {
		t.Fatalf("choices[0] = %q, want %q first", choices[0], terminalThemeDefault)
	}
	if !slices.Contains(choices, "Dracula") {
		t.Fatal("Ghostty's library is missing from the choices")
	}
}

// The picker's display value maps the store's "" to the default entry.
func TestThemeDisplayMapsEmptyToDefault(t *testing.T) {
	if got := terminalThemeDisplay(""); got != terminalThemeDefault {
		t.Fatalf("display(\"\") = %q, want %q", got, terminalThemeDefault)
	}
	if got := terminalThemeDisplay("Dracula"); got != "Dracula" {
		t.Fatalf("display(Dracula) = %q", got)
	}
}
