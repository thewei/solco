package nativeui

/**
 * [INPUT]: 官方 MyGo headless Tester、Settings / Sessions 页面与 Local Changes 提交表单夹具
 * [OUTPUT]: 验证内容列在宽窗口里居中且封顶（Settings 760、Sessions 1040、提交表单 760），两侧留白对称
 * [POS]: 宽屏版式回归；列宽常量只在 ds_components.go 的 contentColumn 一处生效
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// renderedRect renders shell at width×900 and returns where the element
// showing text sits.
func renderedRect(t *testing.T, shell *Shell, width int, text string) ui.Rect {
	t.Helper()
	tester := ui.NewTester(shell.View, width, 900)
	tester.Frame()
	tester.Frame()
	rect, ok := tester.Find(text)
	if !ok {
		t.Fatalf("%q not found at width %d; texts=%q", text, width, tester.Texts())
	}
	return rect
}

// assertCentered checks that growing the window by delta moves a centered
// column's content by exactly delta/2 — the margins stay symmetric.
func assertCentered(t *testing.T, what string, narrow, wide, delta float32) {
	t.Helper()
	if moved := wide - narrow; moved < delta/2-1 || moved > delta/2+1 {
		t.Fatalf("%s moved %.1fpt when the window grew %.0fpt; want %.0f (a centered, capped column)", what, moved, delta, delta/2)
	}
}

func TestSettingsContentIsACenteredColumn(t *testing.T) {
	for _, route := range []string{"/settings/general", "/settings/git", "/settings/diagnostics", "/settings/about"} {
		shell := NewShell()
		shell.loading = false
		shell.router.Replace(route)
		anchor := map[string]string{"/settings/general": "Appearance", "/settings/git": "Commit message rules", "/settings/diagnostics": "Compatibility", "/settings/about": "GitHub Repository"}[route]
		narrow := renderedRect(t, shell, 1200, anchor)
		wide := renderedRect(t, shell, 1600, anchor)
		assertCentered(t, route+" content", narrow.X, wide.X, 400)
	}
}

func TestSessionsListIsACenteredColumn(t *testing.T) {
	shell := newMultiAgentHistoryShell(t)
	shell.router.Replace(routeHistory)
	const row = "Tighten sidebar spacing"
	// Both widths leave the route area wider than the 1040 cap.
	narrow := renderedRect(t, shell, 1500, row)
	wide := renderedRect(t, shell, 1900, row)
	assertCentered(t, "Sessions rows", narrow.X, wide.X, 400)
}

// The commit bar is a form, not a diff: dragged wide, the right panel keeps it
// at the readable width, centered in the card (2026-11-08: the bar moved from
// the Git page's center column into the panel).
func TestQuickCommitFormStopsAtTheReadableWidth(t *testing.T) {
	shell := gitSurfaceTestShell(t, t.TempDir())
	showChangesPanel(shell)
	shell.sidePanel.width = 1100
	tester := ui.NewTester(shell.View, 1900, 900)
	tester.Frame()
	tester.Frame()
	subject, ok := tester.Find("Commit subject draft")
	options, ok2 := tester.Find("Commit options")
	card, ok3 := tester.Find("Right panel")
	if !ok || !ok2 || !ok3 {
		t.Fatalf("commit bar or card missing (%v %v %v); texts=%q", ok, ok2, ok3, tester.Texts())
	}
	if card.W < float32(formColumnWidth)+100 {
		t.Fatalf("fixture: the card is only %.0fpt wide, too narrow to show the cap", card.W)
	}
	if row := options.X + options.W - subject.X; row > float32(formColumnWidth)+1 {
		t.Fatalf("the commit row is %.0fpt wide in a %.0fpt card; want it capped at %.0f", row, card.W, float32(formColumnWidth))
	}
	left, right := subject.X-card.X, card.X+card.W-(options.X+options.W)
	if d := left - right; d < -1 || d > 1 {
		t.Fatalf("the commit row is not centered in the card: %.1fpt left, %.1fpt right", left, right)
	}
}
