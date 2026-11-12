package nativeui

/**
 * [INPUT]: island.go、Settings → Notifications 的开关、MyGo Tester
 * [OUTPUT]: 悬停 300 ms 展开 / 离开 300 ms 收回 / 经过不展开 / 提示上停留展开名册 / 关开关即停 / 名册行内作答（成功、失败）/ 名册 Open 走通知路由 / 空名册，以及：默认关（提示无处可去、不建任何东西）、开关持久化、提示上岛 / 指针停留不收 / 到时收回 / Open 走系统横幅同一条路由 / ✕ 忽略 / 关开关立刻清空 的验收
 * [POS]: 刘海岛 P1+P2 的无头测试：不建真窗口（s.win == nil），计时由测试直接调用 expire / poll（指针与时钟由 cursor / now 缝注入）；真窗口的层级、点击穿透、全屏之上由 spike 与沙箱实机核对
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/feedback"
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

// islandNotched is an island on a display with a 200×32 housing, the pointer
// and the clock in the test's hands; positions are relative to the centre.
func islandNotched(t *testing.T) (*islandHost, *float32, *float32, *time.Time) {
	t.Helper()
	s := islandOn()
	h := s.islandHostOf()
	h.w0, h.h0, h.notch = 200, 32, true
	x, y := new(float32), new(float32)
	now := time.Unix(1000, 0)
	h.cursor = func() (float32, float32) { return *x, *y }
	h.now = func() time.Time { return now }
	return h, x, y, &now
}

func TestIslandRosterOpensAfterTheRestAndClosesAfterTheLeave(t *testing.T) {
	h, x, y, now := islandNotched(t)
	*x, *y = 20, 10 // on the housing
	h.poll()
	if h.roster || h.enteredAt.IsZero() {
		t.Fatalf("the first sight of the pointer opened it: roster=%v", h.roster)
	}
	*now = now.Add(299 * time.Millisecond)
	h.poll()
	if h.roster {
		t.Fatal("opened before the pointer had rested 300 ms")
	}
	*now = now.Add(time.Millisecond)
	h.poll()
	if !h.roster || !h.open {
		t.Fatal("did not open after 300 ms of rest")
	}

	*x, *y = 20, 500 // far below the shape
	h.poll()
	*now = now.Add(299 * time.Millisecond)
	h.poll()
	if !h.roster {
		t.Fatal("closed before the pointer had been away 300 ms")
	}
	*x, *y = 20, h.rosterH-10 // back on it: the leave timer starts over
	h.poll()
	if !h.leftAt.IsZero() {
		t.Fatal("coming back did not reset the leave timer")
	}
	*x, *y = 20, 500
	h.poll()
	*now = now.Add(300 * time.Millisecond)
	h.poll()
	if h.roster || h.open {
		t.Fatal("did not close after 300 ms away")
	}
}

func TestIslandPointerPassingByDoesNotOpenIt(t *testing.T) {
	h, x, y, now := islandNotched(t)
	*x, *y = 0, 10
	h.poll()
	*x, *y = 700, 300 // gone before the rest
	*now = now.Add(200 * time.Millisecond)
	h.poll()
	*x, *y = 0, 10
	*now = now.Add(200 * time.Millisecond)
	h.poll()
	*now = now.Add(200 * time.Millisecond)
	h.poll()
	if h.roster {
		t.Fatal("a pointer that left and came back opened it on the first visit's time")
	}
}

func TestIslandRestOnAnAlertOpensTheRosterInItsPlace(t *testing.T) {
	h, x, y, now := islandNotched(t)
	h.s.islandNotify(islandAlert{ID: "a", Kind: islandReview, Title: "one"})
	h.s.islandNotify(islandAlert{ID: "b", Kind: islandReview, Title: "two"})
	*x, *y = 100, h.h0+islandRowH/2 // on the alert's row
	h.poll()
	*now = now.Add(300 * time.Millisecond)
	h.poll()
	if !h.roster {
		t.Fatal("resting on the alert did not open the roster")
	}
	if _, showing := h.toasts.Showing(); showing || h.toasts.Pending() != 0 {
		t.Fatal("alerts stayed queued behind the roster")
	}
	h.push(islandAlert{ID: "c", Kind: islandFeedback, Title: "three"})
	if _, showing := h.toasts.Showing(); showing {
		t.Fatal("an alert replaced the open roster")
	}
}

func TestIslandSwitchOffStopsEverything(t *testing.T) {
	h, x, y, now := islandNotched(t)
	*x, *y = 0, 10
	h.poll()
	*now = now.Add(300 * time.Millisecond)
	h.poll()
	h.s.settings.Notifications.Island = false
	h.s.islandSync()
	if h.roster || h.open || !h.enteredAt.IsZero() {
		t.Fatal("switching off left the roster open")
	}
	*now = now.Add(time.Second)
	h.poll()
	if h.roster {
		t.Fatal("a poll opened the roster while the switch is off")
	}
}

func TestIslandRosterAnswersAFeedbackAskInTheRow(t *testing.T) {
	h, _, _, _ := islandNotched(t)
	s := h.s
	s.feedback.threads = []feedback.ThreadSummary{{ID: "th_1", Project: "solco", Client: "Cursor", Preview: "Keep it?", Waiting: 1}}
	h.asks["th_1"] = islandAsk{Thread: "th_1", Ask: "a_1", Question: "q1", Options: []string{"Yes", "No"}}
	var gotAsk, gotOpt string
	var fail error
	h.answerFn = func(a islandAsk, option string) error { gotAsk, gotOpt = a.Ask, option; return fail }
	h.openRoster()

	tester := ui.NewTester(h.view, islandWinW, int(h.winH()))
	for _, want := range []string{"Needs you", "solco · Cursor", "Keep it?", "Yes", "No", "Open", "Open Solco"} {
		if !tester.HasText(want) {
			t.Fatalf("roster lacks %q: %q", want, tester.Texts())
		}
	}
	if tester.HasText("Nothing is waiting for you") {
		t.Fatal("says nothing waits while a question does")
	}
	if err := tester.Click("Yes"); err != nil {
		t.Fatal(err)
	}
	if gotAsk != "a_1" || gotOpt != "Yes" {
		t.Fatalf("answer = %q %q", gotAsk, gotOpt)
	}
	if !tester.HasText("Sent · Yes") || tester.HasText("No") {
		t.Fatalf("the row did not say Sent: %q", tester.Texts())
	}

	// An answer that does not go says so and offers Open instead.
	h.sent = map[string]string{}
	fail = errors.New("broker down")
	h.answer(h.asks["th_1"], "No")
	tester = ui.NewTester(h.view, islandWinW, int(h.winH()))
	if !tester.HasText("Not sent — use Open to answer") {
		t.Fatalf("a failed answer is silent: %q", tester.Texts())
	}
}

func TestIslandRosterOpenFollowsTheNotificationRoute(t *testing.T) {
	h, _, _, _ := islandNotched(t)
	h.s.feedback.threads = []feedback.ThreadSummary{{ID: "th_1", Project: "solco", Waiting: 1}}
	h.openRoster()
	h.s.feedback.client = feedback.NewClient(t.TempDir()) // only its presence matters: the route needs a broker to land on
	tester := ui.NewTester(h.view, islandWinW, int(h.winH()))
	if err := tester.Click("Open solco"); err != nil {
		t.Fatal(err)
	}
	if h.roster {
		t.Fatal("Open left the roster up")
	}
	if h.s.router.Path() != routeFeedback || h.s.feedback.focus.Thread != "th_1" {
		t.Fatalf("Open did not go to the thread: %+v", h.s.feedback.focus)
	}
}

func TestIslandEmptyRosterSaysSo(t *testing.T) {
	h, _, _, _ := islandNotched(t)
	h.openRoster()
	tester := ui.NewTester(h.view, islandWinW, int(h.winH()))
	if !tester.HasText("Nothing is waiting for you") || !tester.HasText("Open Solco") {
		t.Fatalf("empty roster: %q", tester.Texts())
	}
	if want := h.h0 + islandPadTop + islandEmptyH + islandFootH; h.rosterH != want {
		t.Fatalf("height = %v, want %v", h.rosterH, want)
	}
}
