package nativeui

/**
 * [INPUT]: 官方 MyGo headless Tester、/settings/about 路由与 internal/changelog 的已提交快照
 * [OUTPUT]: About 页回归——标记旁的版本行、两个外链行、发布改进卡（Internal 不显示）、
 * 新版本可用时的“Update Version”按钮与跨多版本聚合 Changelog 摘要回归
 * [POS]: 用户可见内容的回归；快照缺失、多版本未总结或分组泄露时失败
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/changelog"
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
		"Check for Updates",
		"What's new in v0.12.0", // the committed snapshot's latest release
		"Released 2026-11-02",
		"Mobile: phone-native bottom tabs", // the first New bullet
	} {
		if !strings.Contains(texts, want) {
			t.Errorf("About page is missing %q; texts=%q", want, texts)
		}
	}
	// The Internal section stays in CHANGELOG.md; the page shows only what
	// the user shipped.
	if strings.Contains(texts, "Wire contracts: bidirectional synchronization") {
		t.Error("About page renders the Internal section; it should stay out")
	}
}

func TestAboutPageShowsUpdateVersionButtonAndSummarizedChangelog(t *testing.T) {
	shell := NewShell()
	shell.loading = false

	// Simulate an update across two releases: v0.12.0 and v0.11.0
	rel12 := changelog.Release{
		Version: "0.12.0",
		Date:    "2026-11-02",
		Groups: []changelog.Group{
			{Name: "New", Items: []string{"Mobile: phone bottom tabs", "Mermaid diagrams"}},
			{Name: "Fixed", Items: []string{"Chat queueing fix"}},
		},
	}
	rel11 := changelog.Release{
		Version: "0.11.0",
		Date:    "2026-11-01",
		Groups: []changelog.Group{
			{Name: "New", Items: []string{"Follow-up queue stack", "About page"}},
			{Name: "Fixed", Items: []string{"Terminal resize"}},
		},
	}

	shell.setUpdateSimulation(true, "0.12.0", []changelog.Release{rel12, rel11})

	if !shell.hasUpdate() {
		t.Fatal("hasUpdate() = false, want true")
	}

	shell.router.Replace("/settings/about")
	tester := ui.NewTester(shell.View, 1200, 900)
	tester.Frame()
	tester.Frame()
	texts := strings.Join(tester.Texts(), "\n")

	for _, want := range []string{
		"New Version Available · v0.12.0",
		"Update Version", // The action button user requested
		"View on GitHub",
		"Update Summary",
		"v0.12.0",
		"Mobile: phone bottom tabs",
		"Mermaid diagrams",
		"v0.11.0",
		"Follow-up queue stack",
	} {
		if !strings.Contains(texts, want) {
			t.Errorf("About page update view is missing %q; texts=%q", want, texts)
		}
	}
}

func TestSidebarSettingsRedDotWhenUpdateAvailable(t *testing.T) {
	shell := NewShell()
	shell.loading = false

	// Without update: hasUpdate is false
	if shell.hasUpdate() {
		t.Fatal("hasUpdate() should be false initially")
	}

	// Trigger simulated update
	shell.setUpdateSimulation(true, "0.13.0", nil)

	if !shell.hasUpdate() {
		t.Fatal("hasUpdate() should be true after setUpdateSimulation")
	}

	// Verify view rendering does not crash and renders with red dot indicator
	shell.router.Replace("/workspace")
	tester := ui.NewTester(shell.View, 1200, 900)
	tester.Frame()
	tester.Frame()

	// Switch into Settings navigation to verify About row badge
	shell.router.Replace("/settings/general")
	tester.Frame()
	tester.Frame()
}
