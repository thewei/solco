package nativeui

/**
 * [INPUT]: 依赖 internal/feedback 的 Client（回环 broker 的唯一读写口）与 Event、mygo.NewNotification、window_actions.go 的 activateWindow、window lane（applyOnWindow）
 * [OUTPUT]: 对外提供 WithFeedback（生产接线）、routeFeedback（/feedback）、Shell.openFeedback（通知/深链/Command Center 的唯一落点）、feedbackNotificationID/parseFeedbackNotificationID、parseFeedbackURL（solco://feedback?thread=&item=）、Shell.startFeedbackWatch（订阅 broker 事件流 → 系统通知）、Shell.feedbackAvailable
 * [POS]: Feedback 收件箱（2026-11-09 决策）的桌面一半：只是路由、通知与 WebView 页的宿主，数据与状态全在 broker（进程外、flock 单主）；通知与深链只带 id（thread/item），不带问题正文以外的任何东西——正文只进系统通知本身，从不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/commandcenter"
	"github.com/thewei/solco/internal/feedback"
)

const (
	routeFeedback = "/feedback"

	feedbackNotificationIDPrefix = "solco-feedback/"
	feedbackNotificationGroup    = "solco-feedback"
	feedbackWatchPause           = 3 * time.Second
)

// feedbackState is the Shell's side of the Feedback inbox: the broker client,
// the page's WebView and the one outstanding "land here" request.
type feedbackState struct {
	client *feedback.Client
	// webview is the page's embedded WebView; broken latches a failed creation.
	webview *mygo.WebView
	broken  bool
	// focus is where a notification or deep link wants the page to land; the
	// page's next Snapshot takes it (once).
	focus feedbackFocus
	// cancel stops the event-stream watcher.
	cancel context.CancelFunc
	// threads are the newest thread rows, kept for the Command Center (waiting
	// first); the watcher refreshes them off the window lane. Window lane only.
	threads []feedback.ThreadSummary
}

// feedbackPaletteMax bounds how many threads the Command Center can offer.
const feedbackPaletteMax = 60

// feedbackFocus names a thread and, optionally, one Ask inside it.
type feedbackFocus struct {
	Thread string `json:"thread"`
	Ask    string `json:"ask"`
}

// WithFeedback wires the Feedback inbox to the broker under dir
// (feedback.Dir(userDataDir)). A shell without it — every headless test —
// has no page, no watcher and no command.
func WithFeedback(dir string) ShellOption {
	return func(s *Shell) {
		if dir != "" {
			s.feedback.client = feedback.NewClient(dir)
		}
	}
}

func (s *Shell) feedbackAvailable() bool { return s.feedback.client != nil }

// feedbackNotificationID is the stable ID of one Ask's notification; MyGo
// hands it back verbatim on a click, across launches.
func feedbackNotificationID(thread, ask string) string {
	return feedbackNotificationIDPrefix + thread + "/" + ask
}

// parseFeedbackNotificationID decodes an ID this app issued; anything else
// fails closed.
func parseFeedbackNotificationID(id string) (thread, ask string, ok bool) {
	rest, ok := strings.CutPrefix(id, feedbackNotificationIDPrefix)
	if !ok {
		return "", "", false
	}
	thread, ask, _ = strings.Cut(rest, "/")
	if thread == "" || strings.Contains(ask, "/") {
		return "", "", false
	}
	return thread, ask, true
}

// parseFeedbackURL accepts solco://feedback and
// solco://feedback?thread=…&item=…; the ids are the whole payload.
func parseFeedbackURL(raw string) (feedbackFocus, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "solco" || u.Host != "feedback" {
		return feedbackFocus{}, false
	}
	q := u.Query()
	return feedbackFocus{Thread: q.Get("thread"), Ask: q.Get("item")}, true
}

// openFeedback is the one landing: record where the page should look, then
// show it. Call on the window lane. A page that is already on screen picks
// the focus up on its next poll; one that is not yet built reads it on its
// first Snapshot, so a cold start lands the same way.
func (s *Shell) openFeedback(focus feedbackFocus) {
	if !s.feedbackAvailable() {
		return
	}
	s.feedback.focus = focus
	if s.router.Path() != routeFeedback {
		s.router.Push(routeFeedback)
	}
}

// routeFeedbackNotification handles a click on a Feedback notification.
func (s *Shell) routeFeedbackNotification(id string) bool {
	thread, ask, ok := parseFeedbackNotificationID(id)
	if !ok {
		return false
	}
	s.activateWindow()
	s.applyOnWindow(func() { s.openFeedback(feedbackFocus{Thread: thread, Ask: ask}) })
	return true
}

// feedbackNotificationText words one Ask: the project and Agent in the title,
// the first line of the question as the body.
func feedbackNotificationText(e feedback.Event) (title, body string) {
	title = strings.TrimSpace(e.Project)
	if title == "" {
		title = "Agent"
	}
	if e.Client != "" {
		title += " · " + e.Client
	}
	body = e.Preview
	if body == "" {
		body = e.Title
	}
	return title, body
}

func showFeedbackNotification(e feedback.Event) {
	title, body := feedbackNotificationText(e)
	obj := mygo.NewNotification(mygo.NotificationOptions{
		ID:    feedbackNotificationID(e.ThreadID, e.AskID),
		Group: feedbackNotificationGroup,
		Title: title,
		Body:  body,
	})
	if obj != nil {
		obj.Show()
	}
}

// startFeedbackWatch follows the broker's event stream for the life of the
// window: a new Ask becomes a system notification. The broker may be absent
// (nobody owns it yet) or change owner; the loop just reconnects. Idempotent.
func (s *Shell) startFeedbackWatch() {
	if s.win == nil || s.feedback.client == nil || s.feedback.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.feedback.cancel = cancel
	client := s.feedback.client
	go func() {
		for ctx.Err() == nil {
			s.refreshFeedbackThreads(ctx, client)
			err := client.Stream(ctx, func(e feedback.Event) {
				s.refreshFeedbackThreads(ctx, client)
				if e.Type == "ask_created" {
					slog.Info("feedback ask notified", "thread", e.ThreadID)
					// Off the stream's reader: a notification call hops to the main thread and can
					// wait on the system's permission prompt, which must not stall later events.
					go showFeedbackNotification(e)
				}
			})
			if ctx.Err() != nil {
				return
			}
			s.applyOnWindow(func() { s.feedback.threads = nil })
			slog.Debug("feedback stream ended", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(feedbackWatchPause):
			}
		}
	}()
}

func (s *Shell) stopFeedbackWatch() {
	if s.feedback.cancel != nil {
		s.feedback.cancel()
		s.feedback.cancel = nil
	}
}

// feedbackWebviewFor returns the lazily created page WebView, or nil when no
// window exists or creation failed once (latched). Call on the UI lane.
func (s *Shell) feedbackWebviewFor() *mygo.WebView {
	st := &s.feedback
	if s.win == nil || st.broken {
		return nil
	}
	if st.webview == nil {
		view, err := s.win.NewWebView(mygo.WebViewOptions{URL: routeFeedback, BackgroundColor: webviewBackground})
		if err != nil {
			st.broken = true
			slog.Warn("create feedback webview failed", "error", err)
			return nil
		}
		view.Page().OnWillNavigate(func(e *mygo.NavigateEvent) {
			e.PreventDefault()
			if pageNavigationDecision(e.URL, e.UserInitiated) {
				go func() { _ = mygo.Shell.OpenExternal(e.URL) }()
			}
		})
		st.webview = view
	}
	return st.webview
}

// refreshFeedbackThreads re-reads the thread rows the Command Center offers.
func (s *Shell) refreshFeedbackThreads(ctx context.Context, client *feedback.Client) {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	threads, err := client.Threads(rctx, false, "")
	if err != nil {
		return
	}
	if len(threads) > feedbackPaletteMax {
		threads = threads[:feedbackPaletteMax]
	}
	s.applyOnWindow(func() {
		before := s.feedbackWaiting()
		s.feedback.threads = threads
		if s.feedbackWaiting() != before {
			s.fitQuickPanel() // the menu-bar panel's Feedback row appears / goes with the count
			s.invalidateQuickPanel()
		}
	})
}

// feedbackWaiting is how many Agent questions wait for an answer, from the
// thread rows the watcher keeps. Window lane only.
func (s *Shell) feedbackWaiting() int {
	n := 0
	for _, t := range s.feedback.threads {
		n += t.Waiting
	}
	return n
}

// quickPanelFeedbackHeight is the Feedback row's height in the menu-bar panel.
const quickPanelFeedbackHeight = 44

// quickPanelFeedback is the menu-bar panel's one line while Agent questions
// wait ("Feedback, N waiting"): drawn only then, with an Open that lands on
// the inbox the way a notification click does.
func (s *Shell) quickPanelFeedback(c *ui.Context) {
	n := s.feedbackWaiting()
	if n == 0 || !s.feedbackAvailable() {
		return
	}
	sp := Spacing()
	t := c.Theme()
	ui.Row(c.Key("qp-feedback")).FillWidth().Padding(sp.XS, sp.M, sp.XS).Gap(sp.S).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("Feedback, %d waiting", n)).FontSize(Typography().Caption).Grow(1).SingleLine().TextColor(t.TextMuted)
		if compactButton(c, "Open").Clicked() {
			s.hideQuickPanel()
			s.activateWindow()
			s.openFeedback(feedbackFocus{})
		}
	})
}

// feedbackThreadTarget is the Command Center target of one thread.
const feedbackThreadTargetPrefix = "feedback:"

// feedbackCommandActions lists the threads for the palette: only once the
// user types (QueryOnly), waiting ones first as the broker orders them.
func (s *Shell) feedbackCommandActions() []commandcenter.Action {
	if !s.feedbackAvailable() {
		return nil
	}
	actions := make([]commandcenter.Action, 0, len(s.feedback.threads))
	for _, t := range s.feedback.threads {
		title := t.Preview
		if t.Waiting == 0 || title == "" {
			title = t.Title
		}
		// Two projects often ask the same thing: the row names whose it is.
		if t.Project != "" {
			title = t.Project + " · " + title
		}
		actions = append(actions, commandcenter.Action{
			ID: "feedback.thread." + t.ID, Title: title, Section: "Feedback",
			Keywords: []string{"feedback", "ask", t.Project, t.Client, t.Title},
			Kind:     commandcenter.TargetRoute, TargetID: feedbackThreadTargetPrefix + t.ID,
			Available: true, QueryOnly: true,
		})
	}
	return actions
}
