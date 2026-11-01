package nativeui

/**
 * [INPUT]: 官方 MyGo headless Tester、/settings/about 路由与 internal/changelog 的已提交快照
 * [OUTPUT]: About 页回归——标记旁的版本行、两个外链行、发布改进卡（Internal 不显示）
 * [POS]: 用户可见内容的回归；快照缺失或分组泄露时失败
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAboutPageShowsVersionLinksAndReleaseNotes(t *testing.T) {
	shell := NewShell()
	shell.loading = false
	shell.router.Replace("/settings/about")
	tester := ui.NewTester(shell.View, 1200, 900)
	tester.Frame()
	tester.Frame()
	texts := strings.Join(tester.Texts(), "\n")

	for _, want := range []string{
		"Solco",
		"Version ", // the running version ("dev" headless)
		"GitHub Repository",
		"Release Notes",
		"What's new in v0.11.0", // the committed snapshot's release
		"Released 2026-11-01",
		"Chat: a new chat view", // the first New bullet
	} {
		if !strings.Contains(texts, want) {
			t.Errorf("About page is missing %q; texts=%q", want, texts)
		}
	}
	// The Internal section stays in CHANGELOG.md; the page shows only what
	// the user shipped.
	if strings.Contains(texts, "Site: dark redesign") {
		t.Error("About page renders the Internal section; it should stay out")
	}
}
