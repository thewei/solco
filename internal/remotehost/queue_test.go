package remotehost

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

type fakeQueue struct {
	mu      sync.Mutex
	items   map[string]FollowUp
	enqueue []string // instance|pane|conversation|fingerprint|text
	err     error
}

func (f *fakeQueue) Enqueue(instance, paneID, conversationID, fingerprint, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if _, taken := f.items[conversationID]; taken {
		return ErrFollowUpFull
	}
	if f.items == nil {
		f.items = map[string]FollowUp{}
	}
	f.items[conversationID] = FollowUp{Text: text, State: "queued"}
	f.enqueue = append(f.enqueue, strings.Join([]string{instance, paneID, conversationID, fingerprint, text}, "|"))
	return nil
}

func (f *fakeQueue) Get(id string) (FollowUp, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[id]
	return item, ok
}

func (f *fakeQueue) Cancel(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[id]; !ok {
		return ErrFollowUpNone
	}
	delete(f.items, id)
	return nil
}

const queueCancelPath = "/api/v3/panes/w1:p1/chat/queue/cancel?instance=default"

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

func TestChatQueuesAFollowUpBehindARunningTurnAndCancelsIt(t *testing.T) {
	h, q := queuedHarness(t)
	if code, got := h.post(sendPath, h.token, `{"text":"then run the tests","queue":true}`); code != 200 {
		t.Fatalf("send = %d %s", code, got)
	}
	prompts, keys := h.sent()
	if len(prompts) != 0 || len(keys) != 0 {
		t.Fatalf("a queued follow-up must not reach the agent now: %v %v", prompts, keys)
	}
	if len(q.enqueue) != 1 {
		t.Fatalf("enqueue = %v", q.enqueue)
	}
	parts := strings.Split(q.enqueue[0], "|")
	if parts[0] != "default" || parts[1] != "w1:p1" || !strings.HasPrefix(parts[2], "live:") || !strings.HasSuffix(parts[2], ":term_a") || parts[3] == "" || parts[4] != "then run the tests" {
		t.Fatalf("enqueue = %q (conversation id is the desktop's, fingerprint is Herdr's agent_session)", parts)
	}
	if f := h.queuedFrame(); f == nil || f.Text != "then run the tests" || f.State != "queued" {
		t.Fatalf("frame = %+v", f)
	}
	if code, got := h.post(sendPath, h.token, `{"text":"another","queue":true}`); code != 409 || got != "queue_full|" {
		t.Fatalf("second = %d %q", code, got)
	}
	if code, got := h.post(queueCancelPath, h.token, `{}`); code != 200 {
		t.Fatalf("cancel = %d %s", code, got)
	}
	if h.queuedFrame() != nil {
		t.Fatal("a cancelled follow-up must leave the frame")
	}
	if code, got := h.post(queueCancelPath, h.token, `{}`); code != 404 || got != "no_queued|" {
		t.Fatalf("second cancel = %d %q", code, got)
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
