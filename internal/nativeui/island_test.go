package nativeui

/**
 * [INPUT]: island.go、Settings → Notifications 的开关、MyGo Tester
 * [OUTPUT]: 默认关（提示无处可去、不建任何东西）、开关持久化、提示上岛 / 指针停留不收 / 到时收回 / Open 走系统横幅同一条路由 / ✕ 忽略 / 关开关立刻清空 的验收
 * [POS]: 刘海岛 P1 的无头测试：不建真窗口（s.win == nil），计时由测试直接调用 expire；真窗口的层级、点击穿透、全屏之上由 spike 与沙箱实机核对
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestIslandIsOffByDefaultAndTheSwitchPersists(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the notch island is macOS only")
	}
	s := NewShell()
	s.loading = false
	if s.settings.Notifications.Island {
		t.Fatal("the island is on by default")
	}
	s.islandNotify(islandAlert{ID: "solco-agent/i/t", Kind: islandAttention, Title: "omp"})
	if s.island != nil {
		t.Fatal("an alert reached an island that is switched off")
	}

	s.router.Replace("/settings/notifications")
	tester := ui.NewTester(s.View, 1200, 900)
	if !tester.HasText("Show alerts in the notch") {
		t.Fatalf("Notifications page lacks the switch: %q", tester.Texts())
	}
	if err := tester.Click("Show alerts in the notch"); err != nil {
		t.Fatal(err)
	}
	if !s.settings.Notifications.Island {
		t.Fatal("the switch did not turn the island on")
	}
	s.islandNotify(islandAlert{ID: "solco-agent/i/t", Kind: islandAttention, Title: "omp"})
	if s.island == nil {
		t.Fatal("an alert did not reach the island that is on")
	}
	if err := tester.Click("Show alerts in the notch"); err != nil {
		t.Fatal(err)
	}
	if s.settings.Notifications.Island {
		t.Fatal("the switch did not turn the island off")
	}
	if _, showing := s.island.toasts.Showing(); showing || s.island.open {
		t.Fatal("turning the switch off left the island showing")
	}
}

func islandOn() *Shell {
	s := NewShell()
	s.loading = false
	s.settings.Notifications.Island = true
	return s
}

func TestIslandAlertEndsOnTimeUnlessThePointerRestsOnIt(t *testing.T) {
	s := islandOn()
	s.islandNotify(islandAlert{ID: "a", Kind: islandReview, Title: "one"})
	s.islandNotify(islandAlert{ID: "b", Kind: islandReview, Title: "two"})
	h := s.island
	if a, ok := h.toasts.Showing(); !ok || a.ID != "a" || !h.open || h.toasts.Pending() != 1 {
		t.Fatalf("after two alerts: showing=%+v open=%v pending=%d", a, h.open, h.toasts.Pending())
	}
	h.cursor = func() (float32, float32) { return 10, 5 } // on the shape
	h.expire(h.gen)
	if a, _ := h.toasts.Showing(); a.ID != "a" {
		t.Fatal("the alert ended under the pointer")
	}
	h.cursor = func() (float32, float32) { return 900, 400 } // far away
	h.expire(h.gen)
	if a, _ := h.toasts.Showing(); a.ID != "b" {
		t.Fatalf("the next alert did not come on: %+v", a)
	}
	stale := h.gen - 1
	h.expire(stale)
	if a, _ := h.toasts.Showing(); a.ID != "b" {
		t.Fatal("a stale timer ended the wrong alert")
	}
	h.expire(h.gen)
	if _, showing := h.toasts.Showing(); showing || h.open {
		t.Fatal("the island did not close after the last alert")
	}
}

func TestIslandViewOpensThroughTheBannerRouteAndDismisses(t *testing.T) {
	s := islandOn()
	s.islandNotify(islandAlert{ID: "solco-agent/inst/term", Kind: islandAttention, Title: "omp", Body: "Needs attention"})
	s.islandNotify(islandAlert{ID: "solco-agent/inst/other", Kind: islandReview, Title: "claude", Body: "Ready"})
	h := s.island
	tester := ui.NewTester(h.view, islandWinW, int(h.winH()))
	for _, want := range []string{"omp", "Needs attention", "Open", "+1"} {
		if !tester.HasText(want) {
			t.Fatalf("island lacks %q: %q", want, tester.Texts())
		}
	}
	if err := tester.Click("Open"); err != nil {
		t.Fatal(err)
	}
	// The click took the banner's route (an Agent that is not there says so)
	// and the island moved on to the next alert.
	if s.pendingToast != "Agent has ended" {
		t.Fatalf("Open did not route like a banner click: toast = %q", s.pendingToast)
	}
	if a, _ := h.toasts.Showing(); a.ID != "solco-agent/inst/other" {
		t.Fatalf("after Open the island shows %+v", a)
	}
	if err := tester.Click("Dismiss"); err != nil {
		t.Fatal(err)
	}
	if _, showing := h.toasts.Showing(); showing || h.open {
		t.Fatal("Dismiss left the island open")
	}
}
