package remotehost

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// fakeQueue is the smallest in-memory FollowUps: per-conversation items in
// delivery order with deterministic ids (req-q1, req-q2, …), plus an err
// override for the refusals the real adapter reports.
type fakeQueue struct {
	mu      sync.Mutex
	items   map[string][]FollowUpItem
	next    int
	err     error
	enqueue []string // instance|pane|conversation|fingerprint|text
}

func (f *fakeQueue) Enqueue(instance, paneID, conversationID, fingerprint, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if f.items == nil {
		f.items = map[string][]FollowUpItem{}
	}
	f.next++
	f.enqueue = append(f.enqueue, strings.Join([]string{instance, paneID, conversationID, fingerprint, text}, "|"))
	f.items[conversationID] = append(f.items[conversationID], FollowUpItem{ID: fmt.Sprintf("req-q%d", f.next), Text: text, State: "queued"})
	return nil
}

func (f *fakeQueue) Items(conversationID string) ([]FollowUpItem, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := f.items[conversationID]
	if len(items) == 0 {
		return nil, false
	}
	out := make([]FollowUpItem, len(items))
	copy(out, items)
	return out, true
}

func (f *fakeQueue) CancelItem(conversationID, requestID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := f.items[conversationID]
	for i, item := range items {
		if item.ID == requestID {
			f.items[conversationID] = append(items[:i], items[i+1:]...)
			return nil
		}
	}
	return ErrFollowUpNone
}

func (f *fakeQueue) Cancel(conversationID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.items[conversationID]) == 0 {
		return ErrFollowUpNone
	}
	delete(f.items, conversationID)
	return nil
}

const (
	queueCancelPath     = "/api/v3/panes/w1:p1/chat/queue/cancel?instance=default"
	queueItemCancelPath = "/api/v3/panes/w1:p1/chat/queue/item/%s/cancel?instance=default"
)

func queuedHarness(t *testing.T) (*harness, *fakeQueue) {
	t.Helper()
	h, _ := chatHarness(t)
	h.setBlockedStatus("working")
	q := &fakeQueue{}
	h.srv.opts.Queue = q
	return h, q
}

func (h *harness) queuedFrame() *queuedFrame {
	h.t.Helper()
	resp := h.get(chatPath, h.token, nil)
	var body struct {
		Queued *queuedFrame `json:"queued"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		h.t.Fatal(err)
	}
	return body.Queued
}

func TestChatQueuesFollowUpsBehindARunningTurnAndCancelsThem(t *testing.T) {
	h, q := queuedHarness(t)
	for _, text := range []string{"then run the tests", "and lint"} {
		if code, got := h.post(sendPath, h.token, fmt.Sprintf(`{"text":%q,"queue":true}`, text)); code != 200 {
			t.Fatalf("send %q = %d %s", text, code, got)
		}
	}
	prompts, keys := h.sent()
	if len(prompts) != 0 || len(keys) != 0 {
		t.Fatalf("queued follow-ups must not reach the agent now: %v %v", prompts, keys)
	}
	if len(q.enqueue) != 2 {
		t.Fatalf("enqueue = %v", q.enqueue)
	}
	parts := strings.Split(q.enqueue[0], "|")
	if parts[0] != "default" || parts[1] != "w1:p1" || !strings.HasPrefix(parts[2], "live:") || !strings.HasSuffix(parts[2], ":term_a") || parts[3] == "" || parts[4] != "then run the tests" {
		t.Fatalf("enqueue = %q (conversation id is the desktop's, fingerprint is Herdr's agent_session)", parts)
	}
	f := h.queuedFrame()
	if f == nil || len(f.Items) != 2 || f.Items[0].ID != "req-q1" || f.Items[0].Text != "then run the tests" || f.Items[0].State != "queued" || f.Items[1].ID != "req-q2" || f.Items[1].Text != "and lint" {
		t.Fatalf("frame = %+v", f)
	}
	if code, got := h.post(fmt.Sprintf(queueItemCancelPath, "req-q1"), h.token, `{}`); code != 200 {
		t.Fatalf("item cancel = %d %s", code, got)
	}
	if f := h.queuedFrame(); f == nil || len(f.Items) != 1 || f.Items[0].ID != "req-q2" {
		t.Fatalf("frame after item cancel = %+v", f)
	}
	if code, got := h.post(fmt.Sprintf(queueItemCancelPath, "req-q1"), h.token, `{}`); code != 404 || got != "no_queued|" {
		t.Fatalf("second item cancel = %d %q", code, got)
	}
	if code, got := h.post(queueCancelPath, h.token, `{}`); code != 200 {
		t.Fatalf("cancel = %d %s", code, got)
	}
	if h.queuedFrame() != nil {
		t.Fatal("cancelled follow-ups must leave the frame")
	}
	if code, got := h.post(queueCancelPath, h.token, `{}`); code != 404 || got != "no_queued|" {
		t.Fatalf("second cancel = %d %q", code, got)
	}
}

func TestChatQueueRefusesBehindAnUncertainDelivery(t *testing.T) {
	h, q := queuedHarness(t)
	q.err = ErrFollowUpFrozen
	if code, got := h.post(sendPath, h.token, `{"text":"x","queue":true}`); code != 409 || got != "queue_blocked|" {
		t.Fatalf("frozen = %d %q", code, got)
	}
}

func TestChatQueueIsRefusedWithoutAQueueOrAnIdentity(t *testing.T) {
	h, _ := chatHarness(t)
	h.setBlockedStatus("working")
	if code, got := h.post(sendPath, h.token, `{"text":"x","queue":true}`); code != 409 || got != "queue_unavailable|" {
		t.Fatalf("no queue = %d %q", code, got)
	}
	q := &fakeQueue{err: errors.New("boom")}
	h.srv.opts.Queue = q
	if code, got := h.post(sendPath, h.token, `{"text":"x","queue":true}`); code != 502 || !strings.HasPrefix(got, "queue_failed|") {
		t.Fatalf("failing queue = %d %q", code, got)
	}
	if code, _ := h.post(queueCancelPath, h.token, `{}`); code != 404 {
		t.Fatalf("cancel of nothing = %d", code)
	}
	if code, _ := h.post(sendPath, "", `{"text":"x","queue":true}`); code != 401 {
		t.Fatalf("unauthenticated = %d", code)
	}
}

func TestChatQueueFlagIsIgnoredWhenTheAgentIsReady(t *testing.T) {
	h, q := queuedHarness(t)
	h.setBlockedStatus("idle")
	if code, _ := h.post(sendPath, h.token, `{"text":"go","queue":true}`); code != 200 {
		t.Fatal("send failed")
	}
	if prompts, _ := h.sent(); len(prompts) != 1 || len(q.enqueue) != 0 {
		t.Fatalf("a ready agent gets the prompt now: prompts=%v enqueue=%v", prompts, q.enqueue)
	}
}

func TestBootstrapAdvertisesTheQueueOnlyWhenWired(t *testing.T) {
	h, _ := queuedHarness(t)
	has := func() bool {
		resp := h.get("/api/v3/bootstrap", h.token, nil)
		var body struct {
			Capabilities map[string]bool `json:"capabilities"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return body.Capabilities["queue"]
	}
	if !has() {
		t.Fatal("queue capability missing")
	}
	h.srv.opts.Queue = nil
	if has() {
		t.Fatal("queue capability without a queue")
	}
}
