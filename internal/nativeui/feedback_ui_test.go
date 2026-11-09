package nativeui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/internal/commandcenter"
	"github.com/thewei/solco/internal/feedback"
)

/**
 * [INPUT]: feedback_ui.go、feedback_bridge.go、一个真实的 feedback broker（临时目录里的 feedback.Run）
 * [OUTPUT]: Feedback 桌面一半的回归——通知 ID 与 solco://feedback 往返且对陌生输入关闭、点击/深链/Command Center 落在同一处、落点只交付一次、桥读写走 broker、broker 不在时不报错
 * [POS]: feedback_ui.go / feedback_bridge.go 的行为锁；通知点到哪里、页面读到什么在这里失败
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestFeedbackNotificationIDRoundTripsAndFailsClosed(t *testing.T) {
	thread, ask, ok := parseFeedbackNotificationID(feedbackNotificationID("th_1", "a_9"))
	if !ok || thread != "th_1" || ask != "a_9" {
		t.Fatalf("round trip = %q %q %v", thread, ask, ok)
	}
	for _, id := range []string{"", "solco-feedback/", "solco-feedback//a", "solco-feedback/t/a/b", "solco-agent/i/t", "other"} {
		if _, _, ok := parseFeedbackNotificationID(id); ok {
			t.Errorf("%q parsed as a feedback id", id)
		}
	}
}

func TestParseFeedbackURL(t *testing.T) {
	if f, ok := parseFeedbackURL("solco://feedback?thread=th_1&item=a_2"); !ok || f.Thread != "th_1" || f.Ask != "a_2" {
		t.Errorf("deep link = %+v %v", f, ok)
	}
	if f, ok := parseFeedbackURL("solco://feedback"); !ok || f.Thread != "" {
		t.Errorf("bare link = %+v %v", f, ok)
	}
	for _, raw := range []string{"", "solco://reveal?tab=1", "https://feedback?thread=x", "feedback://thread"} {
		if _, ok := parseFeedbackURL(raw); ok {
			t.Errorf("%q parsed as a feedback link", raw)
		}
	}
}

func TestFeedbackNotificationTextIsProjectAgentAndFirstLine(t *testing.T) {
	title, body := feedbackNotificationText(feedback.Event{Project: "solco", Client: "Cursor", Preview: "Keep it?", Title: "ignored"})
	if title != "solco · Cursor" || body != "Keep it?" {
		t.Errorf("got %q / %q", title, body)
	}
	if title, _ := feedbackNotificationText(feedback.Event{}); title != "Agent" {
		t.Errorf("empty event title = %q", title)
	}
}

func feedbackShell(t *testing.T) (*Shell, *feedback.Client) {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = feedback.Run(ctx, feedback.Options{Dir: dir, Retry: 20 * time.Millisecond}) }()
	client := feedback.NewClient(dir)
	deadline := time.Now().Add(5 * time.Second)
	for client.Health(ctx) != nil {
		if time.Now().After(deadline) {
			t.Fatal("broker did not come up")
		}
		time.Sleep(20 * time.Millisecond)
	}
	s := NewShell(WithFeedback(dir))
	return s, client
}

func newAsk(t *testing.T, c *feedback.Client, root string) (askID, threadID string) {
	t.Helper()
	askID, threadID, err := c.Create(context.Background(), feedback.CreateInput{
		Client: "Cursor", ProjectRoot: root, Description: "ctx", TimeoutMs: 60_000,
		Questions: []feedback.Question{{ID: "q1", Text: "Keep it?", Type: "single", Options: []string{"yes", "no"}, Required: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return askID, threadID
}

func TestFeedbackLandingsMeetOnTheSamePage(t *testing.T) {
	s, _ := feedbackShell(t)
	s.routeAgentNotification(feedbackNotificationID("th_1", "a_1"))
	if s.router.Path() != routeFeedback || s.feedback.focus != (feedbackFocus{Thread: "th_1", Ask: "a_1"}) {
		t.Fatalf("notification: route %q focus %+v", s.router.Path(), s.feedback.focus)
	}
	s.router.Push(routeWorkspace)
	s.RouteExternalURL("solco://feedback?thread=th_2&item=a_2")
	if s.router.Path() != routeFeedback || s.feedback.focus.Thread != "th_2" {
		t.Fatalf("deep link: route %q focus %+v", s.router.Path(), s.feedback.focus)
	}
	s.router.Push(routeWorkspace)
	s.executeCommandCenterResult(commandcenter.Result{Action: commandcenter.Action{Kind: commandcenter.TargetRoute, TargetID: feedbackThreadTargetPrefix + "th_3"}})
	if s.router.Path() != routeFeedback || s.feedback.focus.Thread != "th_3" {
		t.Fatalf("palette: route %q focus %+v", s.router.Path(), s.feedback.focus)
	}
}

func TestShellWithoutFeedbackIgnoresLandingsAndOffersNoCommand(t *testing.T) {
	s := NewShell()
	s.routeAgentNotification(feedbackNotificationID("th_1", "a_1"))
	if s.router.Path() == routeFeedback {
		t.Error("a shell without the broker opened the page")
	}
	for _, a := range s.buildCommandCenterActions() {
		if a.ID == "app.feedback" && a.Available {
			t.Error("Feedback command available without a broker")
		}
	}
}

func TestFeedbackBridgeReadsAnswersAndDeliversFocusOnce(t *testing.T) {
	s, client := feedbackShell(t)
	root := t.TempDir()
	askID, threadID := newAsk(t, client, root)
	b := NewFeedbackBridge(s)
	ctx := context.Background()

	view, err := b.Snapshot(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !view.Online || len(view.Threads) != 1 || view.Waiting != 1 || view.Detail != nil {
		t.Fatalf("list view = %+v", view)
	}

	s.openFeedback(feedbackFocus{Thread: threadID, Ask: askID})
	view, err = b.Snapshot(ctx, "", "")
	if err != nil || view.Focus == nil || view.Focus.Ask != askID || view.Selected != threadID || view.Detail == nil || len(view.Detail.Asks) != 1 {
		t.Fatalf("focused view = %+v, %v", view, err)
	}
	if again, _ := b.Snapshot(ctx, threadID, ""); again.Focus != nil {
		t.Error("the landing was delivered twice")
	}

	if err := b.Answer(ctx, askID, map[string]feedback.Answer{"q1": {Selected: []string{"yes"}}}, nil); err != nil {
		t.Fatal(err)
	}
	view, _ = b.Snapshot(ctx, threadID, "")
	if view.Waiting != 0 || view.Detail.Asks[0].State != feedback.StateAnswered {
		t.Fatalf("after answering: %+v", view)
	}
	if err := b.End(ctx, askID); err == nil {
		t.Error("ending an answered Ask succeeded")
	}
	// A thread the broker no longer has is not an error: the page falls back to the list.
	if view, err := b.Snapshot(ctx, "th_gone", ""); err != nil || view.Detail != nil || view.Selected != "" {
		t.Errorf("gone thread: %+v, %v", view, err)
	}
}

func TestFeedbackBridgeManagesThreadsAndSearches(t *testing.T) {
	s, client := feedbackShell(t)
	askID, threadID := newAsk(t, client, t.TempDir())
	b := NewFeedbackBridge(s)
	ctx := context.Background()

	if n, err := b.DeleteThreads(ctx, []string{threadID}); err != nil || n != 0 {
		t.Fatalf("a waiting thread was deleted: %d, %v", n, err)
	}
	if err := b.Answer(ctx, askID, map[string]feedback.Answer{"q1": {Selected: []string{"yes"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if view, err := b.Snapshot(ctx, "", "Keep it"); err != nil || len(view.Threads) != 1 || !view.CanManage {
		t.Fatalf("search by question: %+v, %v", view, err)
	}
	if view, _ := b.Snapshot(ctx, "", "zzz-no-match"); len(view.Threads) != 0 {
		t.Fatalf("search matched nothing yet listed %d", len(view.Threads))
	}
	if n, err := b.Cleanup(ctx, 30, true); err != nil || n != 0 {
		t.Fatalf("a fresh thread counted as old: %d, %v", n, err)
	}
	if n, err := b.DeleteThreads(ctx, []string{threadID}); err != nil || n != 1 {
		t.Fatalf("delete finished thread: %d, %v", n, err)
	}
}

func TestFeedbackBridgeOpenProjectUsesTheBrokersFolder(t *testing.T) {
	s, client := feedbackShell(t)
	root := t.TempDir()
	_, threadID := newAsk(t, client, root)
	var revealed []string
	prev := revealInFinder
	revealInFinder = func(p string) { revealed = append(revealed, p) }
	t.Cleanup(func() { revealInFinder = prev })

	b := NewFeedbackBridge(s)
	if err := b.OpenProject(context.Background(), threadID, "finder"); err != nil {
		t.Fatal(err)
	}
	if len(revealed) != 1 || !strings.HasSuffix(revealed[0], root[strings.LastIndex(root, "/")+1:]) {
		t.Errorf("revealed %q, want the thread's project folder %q", revealed, root)
	}
	if err := b.OpenProject(context.Background(), threadID, "elsewhere"); err == nil {
		t.Error("an unknown target was accepted")
	}
}

func TestFeedbackBridgeSnapshotWithNoBrokerIsOfflineNotAnError(t *testing.T) {
	s := NewShell(WithFeedback(t.TempDir()))
	view, err := NewFeedbackBridge(s).Snapshot(context.Background(), "", "")
	if err != nil || view.Online {
		t.Fatalf("offline view = %+v, %v", view, err)
	}
	if err := NewFeedbackBridge(s).End(context.Background(), "a_1"); !errors.Is(err, feedback.ErrOffline) {
		t.Errorf("End offline = %v", err)
	}
}

func TestFeedbackThreadsOnlyShowInThePaletteOnceTyped(t *testing.T) {
	s := NewShell(WithFeedback(t.TempDir()))
	s.feedback.threads = []feedback.ThreadSummary{{ID: "th_1", Project: "solco", Client: "Cursor", Title: "Cursor in solco", Preview: "Keep it?", Waiting: 1}}
	idx := commandcenter.Build(s.buildCommandCenterActions())
	for _, r := range idx.Query("", commandcenter.ScopeAll) {
		if r.Action.Section == "Feedback" {
			t.Fatalf("empty query lists %q", r.Action.Title)
		}
	}
	found := false
	for _, r := range idx.Query("feed", commandcenter.ScopeAll) {
		if r.Action.Section == "Feedback" && r.Action.Title == "solco · Keep it?" {
			found = true
		}
	}
	if !found {
		t.Error("typing \"feed\" did not list the waiting thread")
	}
}

func TestQuickPanelGrowsByTheFeedbackRowOnlyWhileQuestionsWait(t *testing.T) {
	s := NewShell(WithFeedback(t.TempDir()))
	base := s.quickPanelContentHeight()
	if s.feedbackWaiting() != 0 {
		t.Fatalf("waiting = %d on a quiet inbox", s.feedbackWaiting())
	}
	s.feedback.threads = []feedback.ThreadSummary{{ID: "th_1", Waiting: 2}, {ID: "th_2", Waiting: 0}, {ID: "th_3", Waiting: 1}}
	if s.feedbackWaiting() != 3 {
		t.Fatalf("waiting = %d, want the sum 3", s.feedbackWaiting())
	}
	if got := s.quickPanelContentHeight(); got != min(base+quickPanelFeedbackHeight, QuickPanelHeight) {
		t.Errorf("height = %d, base %d: the row is not accounted for", got, base)
	}
	plain := NewShell()
	plain.feedback.threads = s.feedback.threads
	if plain.quickPanelContentHeight() != base {
		t.Error("a shell without a broker grew a Feedback row")
	}
}
