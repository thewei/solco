package nativeui

/**
 * [INPUT]: 依赖真实 Codex rollout 夹具、官方 MyGo headless Tester 与 HistoryListView 假体
 * [OUTPUT]: 验证行内详情：点击展开不跳页、再点收起、View Session 才进入详情、详情表字段与用量/路由行、命令构造、过期用量读取被代际丢弃
 * [POS]: Sessions 列表就地详情回归；用量每次展开现读原始会话文件，不缓存
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/history"
)

const previewTestKey = "codex:22222222-aaaa-bbbb-cccc-000000000002"

// TestHistoryRowClickUnfoldsDetailInPlace pins the row click: it unfolds the
// detail table under the row (no message excerpt), stays on the list, and
// folds again on a second click.
func TestHistoryRowClickUnfoldsDetailInPlace(t *testing.T) {
	shell, _, _ := newHistoryTestShell(t)
	shell.router.Replace(routeHistory)
	tester := ui.NewTester(shell.View, 1200, 800)
	tester.Frame()

	if tester.HasText("Session ID") {
		t.Fatalf("the table must not show before the row is opened; texts=%q", tester.Texts())
	}
	if err := tester.Click("Build history"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if shell.router.Path() != routeHistory {
		t.Fatalf("a row click navigated away: %q", shell.router.Path())
	}
	for _, want := range []string{"Time", "Folder", "Session ID", "Continue", "File", "Transcript", "View Session", "Export as Markdown", "Copy Resume Command"} {
		if !tester.HasText(want) {
			t.Fatalf("detail table missing %q; texts=%q", want, tester.Texts())
		}
	}
	if tester.HasText("Implemented.") {
		t.Fatalf("the message excerpt is gone; texts=%q", tester.Texts())
	}

	if err := tester.Click("Build history"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if tester.HasText("Session ID") || shell.hist.preview.key != "" {
		t.Fatalf("second click did not fold the detail; texts=%q", tester.Texts())
	}
}

// TestHistoryDetailOpensTranscriptOnlyOnRequest pins that View Session is the
// way into the conversation.
func TestHistoryDetailOpensTranscriptOnlyOnRequest(t *testing.T) {
	shell, _, _ := newHistoryTestShell(t)
	shell.router.Replace(routeHistory)
	tester := ui.NewTester(shell.View, 1200, 800)
	tester.Frame()
	if err := tester.Click("Build history"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("View Session"); err != nil {
		t.Fatal(err)
	}
	if got := shell.router.Path(); got != "/history/"+previewTestKey {
		t.Fatalf("View Session went to %q", got)
	}
}

// TestHistoryDetailShowsUsageAndRouting pins the usage and routing rows once
// the lazy read has landed.
func TestHistoryDetailShowsUsageAndRouting(t *testing.T) {
	shell, _, _ := newHistoryTestShell(t)
	shell.router.Replace(routeHistory)
	tester := ui.NewTester(shell.View, 1200, 800)
	tester.Frame()
	if err := tester.Click("Build history"); err != nil {
		t.Fatal(err)
	}
	shell.hist.preview.usage = &history.SessionUsage{
		Input: 1810, Output: 473, CacheRead: 6500,
		Routes: []history.UsageRoute{{Provider: "zhipu", Model: "glm-5.3-flash", Level: "high", Calls: 2, Tokens: 2283}},
	}
	tester.Frame()
	for _, want := range []string{"Usage", "Input 1.8k · Output 473 · Cache 6.5k", "Routed to", "zhipu/glm-5.3-flash", "high · 2 calls · 2.3k tokens"} {
		if !tester.HasText(want) {
			t.Fatalf("missing %q; texts=%q", want, tester.Texts())
		}
	}
}

// TestHistoryDetailInfoCardFoldsOnDetailPage pins the detail page's card: a
// strip, collapsed by default, that unfolds the same table.
func TestHistoryDetailInfoCardFoldsOnDetailPage(t *testing.T) {
	shell, _, _ := newHistoryTestShell(t)
	shell.router.Replace(routeHistory)
	shell.router.Push("/history/" + previewTestKey)
	tester := ui.NewTester(shell.View, 1200, 800)
	tester.Frame()
	if !tester.HasText("Session details") || tester.HasText("Session ID") {
		t.Fatalf("the card must start collapsed; texts=%q", tester.Texts())
	}
	if err := tester.Click("Session details"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("Session ID") || tester.HasText("View Session") {
		t.Fatalf("unfolded card wrong (no View Session on the detail page); texts=%q", tester.Texts())
	}
}

func TestSessionInfoFormatting(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.Local)
	ms := func(y int, m time.Month, d, h, min int) int64 {
		return time.Date(y, m, d, h, min, 0, 0, time.Local).UnixMilli()
	}
	for _, c := range []struct {
		from, to int64
		want     string
	}{
		{ms(2026, 10, 29, 12, 26), ms(2026, 10, 29, 12, 27), "Oct 29, 12:26 – 12:27"},
		{ms(2026, 10, 29, 12, 26), ms(2026, 10, 29, 12, 26), "Oct 29, 12:26"},
		{ms(2026, 10, 28, 22, 10), ms(2026, 10, 29, 1, 5), "Oct 28, 22:10 – Oct 29, 01:05"},
		{ms(2025, 3, 4, 9, 0), ms(2025, 3, 4, 9, 30), "Mar 4, 2025, 09:00 – 09:30"},
		{0, 0, ""},
	} {
		if got := sessionTimeSpan(c.from, c.to, now); got != c.want {
			t.Fatalf("span = %q, want %q", got, c.want)
		}
	}
	if got := sessionUsageLine(history.SessionUsage{Input: 900, Output: 40}); got != "Input 900 · Output 40" {
		t.Fatalf("no cache must drop the cache part: %q", got)
	}
}

func TestSessionCommands(t *testing.T) {
	pi := history.SessionMeta{Agent: history.AgentPi, ID: "abc", ProjectPath: "/work/it's here", FilePath: "/s/a b.jsonl"}
	if got, want := sessionResumeCommand(pi), `cd '/work/it'\''s here' && pi --session abc`; got != want {
		t.Fatalf("resume = %q, want %q", got, want)
	}
	label, command, ok := sessionSwitchCommand(pi)
	if !ok || label != "Continue in omp" || !strings.HasSuffix(command, `omp --resume '/s/a b.jsonl'`) {
		t.Fatalf("switch = %q %q %v", label, command, ok)
	}
	if _, _, ok := sessionSwitchCommand(history.SessionMeta{Agent: history.AgentCodex, FilePath: "/x"}); ok {
		t.Fatal("only the Pi family has a switch command")
	}
	if got := sessionResumeCommand(history.SessionMeta{Agent: history.AgentCodex, ID: "z"}); got != "codex resume z" {
		t.Fatalf("codex resume = %q", got)
	}
}

// TestHistoryDetailDropsSupersededUsage pins the generation guard: a read
// that lands after the detail was closed (or moved) must not refill it.
func TestHistoryDetailDropsSupersededUsage(t *testing.T) {
	shell, _, _ := newHistoryTestShell(t)
	shell.router.Replace(routeHistory)
	tester := ui.NewTester(shell.View, 1200, 800)
	tester.Frame()
	shell.hist.service = nil // keep the list as seeded
	generation := shell.hist.previewGen.Load()
	shell.openHistoryPreview(previewTestKey)
	if shell.hist.previewGen.Load() == generation {
		t.Fatal("an open must bump the generation")
	}
	shell.closeHistoryPreview()
	if shell.hist.preview.key != "" || shell.hist.preview.usage != nil {
		t.Fatalf("a closed detail was refilled: %+v", shell.hist.preview)
	}
}
