package remotehost

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/internal/feedback"
	"github.com/thewei/solco/internal/notifyhook"
)

// hookCatcher is a webhook endpoint the Host may call.
type hookCatcher struct {
	mu   sync.Mutex
	got  []notifyhook.Event
	auth string
	srv  *httptest.Server
}

func newHookCatcher(t *testing.T) *hookCatcher {
	t.Helper()
	c := &hookCatcher{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev notifyhook.Event
		_ = json.NewDecoder(r.Body).Decode(&ev)
		c.mu.Lock()
		c.got, c.auth = append(c.got, ev), r.Header.Get("Authorization")
		c.mu.Unlock()
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *hookCatcher) events() []notifyhook.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]notifyhook.Event(nil), c.got...)
}

func (c *hookCatcher) waitFor(t *testing.T, n int) []notifyhook.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.events(); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("webhook got %d events, want %d", len(c.events()), n)
	return nil
}

func useChannel(h *harness, c *hookCatcher, grace time.Duration) {
	h.srv.opts.Channels = func() ([]notifyhook.Target, time.Duration) {
		return []notifyhook.Target{{Name: "test", URL: c.srv.URL, Token: "tok"}}, grace
	}
}

var askEvent = feedback.Event{Type: "ask_created", ThreadID: "t_1", AskID: "a_1", Project: "solco", Client: "Claude Code", Preview: "Which database should the service use?"}

func TestChannelCalledAtOnceWhenNoPhoneIsConnected(t *testing.T) {
	h, f := feedbackHarness(t)
	f.detail.Asks = []*feedback.Ask{{ID: "a_1", ThreadID: "t_1", Description: "Chose between Postgres and SQLite."}}
	c := newHookCatcher(t)
	useChannel(h, c, time.Hour)
	h.srv.startFeedbackWatch()
	defer h.srv.stopFeedbackWatch()
	f.events <- askEvent
	ev := c.waitFor(t, 1)[0]
	if ev.Kind != "question" || ev.Title != "solco · Claude Code" || ev.Text != "Which database should the service use?" || ev.Item != "a_1" || ev.Thread != "t_1" {
		t.Fatalf("event %+v", ev)
	}
	if ev.Link != "solco://feedback?ask=a_1&thread=t_1" || ev.Summary != "Chose between Postgres and SQLite." {
		t.Fatalf("link %q summary %q", ev.Link, ev.Summary)
	}
	if c.auth != "Bearer tok" {
		t.Errorf("auth %q", c.auth)
	}
}

func TestChannelWaitsForAConnectedPhoneAndSkipsWhenItOpensTheThread(t *testing.T) {
	h, f := feedbackHarness(t)
	c := newHookCatcher(t)
	useChannel(h, c, 150*time.Millisecond)
	_, unsubscribe := h.srv.subscribeNotices() // a phone is connected
	defer unsubscribe()
	h.srv.startFeedbackWatch()
	defer h.srv.stopFeedbackWatch()
	f.events <- askEvent
	time.Sleep(40 * time.Millisecond)
	if n := len(c.events()); n != 0 {
		t.Fatalf("called before the grace ran out: %d", n)
	}
	h.srv.markFeedbackSeen("t_1") // the phone opened the thread
	time.Sleep(400 * time.Millisecond)
	if n := len(c.events()); n != 0 {
		t.Fatalf("called although the phone opened the thread: %d", n)
	}
}

func TestChannelCallsAfterGraceWhenTheConnectedPhoneDoesNotOpenIt(t *testing.T) {
	h, f := feedbackHarness(t)
	c := newHookCatcher(t)
	useChannel(h, c, 100*time.Millisecond)
	_, unsubscribe := h.srv.subscribeNotices()
	defer unsubscribe()
	h.srv.startFeedbackWatch()
	defer h.srv.stopFeedbackWatch()
	f.events <- askEvent
	c.waitFor(t, 1)
}

func TestChannelSkippedWhenTheAskIsSettledWithinGrace(t *testing.T) {
	h, f := feedbackHarness(t)
	c := newHookCatcher(t)
	useChannel(h, c, 150*time.Millisecond)
	_, unsubscribe := h.srv.subscribeNotices()
	defer unsubscribe()
	h.srv.startFeedbackWatch()
	defer h.srv.stopFeedbackWatch()
	f.events <- askEvent
	time.Sleep(40 * time.Millisecond)
	f.events <- feedback.Event{Type: "ask_resolved", ThreadID: "t_1", AskID: "a_1"}
	time.Sleep(400 * time.Millisecond)
	if n := len(c.events()); n != 0 {
		t.Fatalf("called for a settled Ask: %d", n)
	}
}

func TestOtherEventsCallChannelsOnlyWithoutAPhone(t *testing.T) {
	h := newHarness(t)
	c := newHookCatcher(t)
	useChannel(h, c, time.Hour)

	h.srv.NotifyAutomation("started", "Nightly", "r1") // a start is not a call for the user
	h.srv.NotifyAutomation("failed", "Nightly", "r1")
	h.srv.NotifyAgent("Claude Code needs input", "Allow npm test?")
	h.srv.NotifyPersonalAgent(2, 0) // first call only records
	h.srv.NotifyPersonalAgent(2, 1)
	got := c.waitFor(t, 3)
	time.Sleep(50 * time.Millisecond)
	got = c.events()
	if len(got) != 3 {
		t.Fatalf("events %+v", got)
	}
	kinds := map[string]string{}
	for _, e := range got {
		kinds[e.Kind] = e.Title + "|" + e.Text
	}
	if kinds["automation"] != "Nightly|Failed" || kinds["agent"] != "Claude Code needs input|Allow npm test?" || kinds["assistant"] != "Assistants|1 Assistant needs you" {
		t.Fatalf("kinds %v", kinds)
	}

	_, unsubscribe := h.srv.subscribeNotices() // now a phone is there to be told
	defer unsubscribe()
	h.srv.NotifyAutomation("failed", "Nightly", "r2")
	h.srv.NotifyAgent("x", "y")
	time.Sleep(100 * time.Millisecond)
	if n := len(c.events()); n != 3 {
		t.Fatalf("called with a phone connected: %d", n)
	}
}

func TestNoChannelsNeverCallsOut(t *testing.T) {
	h := newHarness(t)
	h.srv.NotifyAgent("a", "b") // no Options.Channels: must not panic or block
	h.srv.opts.Channels = func() ([]notifyhook.Target, time.Duration) { return nil, 0 }
	h.srv.NotifyAgent("a", "b")
}
