package conversation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeDeliveryTransport scripts every §11.3 seam deterministically.
type fakeDeliveryTransport struct {
	mu           sync.Mutex
	waitErr      error
	fingerprint  string
	fingerprintX func() string
	deliverErr   error
	deliverCalls int
	delivered    []string
}

func (t *fakeDeliveryTransport) WaitTurnBoundary(ctx context.Context, item QueueItem) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.waitErr
}

func (t *fakeDeliveryTransport) OccupantFingerprint(ctx context.Context, item QueueItem) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fingerprintX != nil {
		return t.fingerprintX(), nil
	}
	return t.fingerprint, nil
}

func (t *fakeDeliveryTransport) Deliver(ctx context.Context, item QueueItem) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.deliverCalls++
	t.delivered = append(t.delivered, item.Text)
	return t.deliverErr
}

func (t *fakeDeliveryTransport) IsUncertain(err error) bool {
	var uncertain *fakeUncertainDelivery
	return errors.As(err, &uncertain)
}

type fakeUncertainDelivery struct{}

func (e *fakeUncertainDelivery) Error() string { return "delivery uncertain" }

func queuedItem(id ConversationID) QueueItem {
	return QueueItem{
		RequestID:      "req-1",
		ConversationID: id,
		AgentPaneID:    "pane-7",
		Fingerprint:    "fp-1",
		Text:           "continue the work",
		Instance:       "inst-1",
	}
}

func stackedItem(id ConversationID, requestID, text string) QueueItem {
	item := queuedItem(id)
	item.RequestID = requestID
	item.Text = text
	return item
}

func deliveryWorker(queue *FollowUpQueue, transport DeliveryTransport, states *[]QueueState) *DeliveryWorker {
	var wake chan struct{}
	worker := NewDeliveryWorker(queue, transport, NewReservations(), wake)
	if states != nil {
		worker.OnState = func(item QueueItem) {
			*states = append(*states, item.State)
		}
	}
	return worker
}

// TestDeliveryWorkerHappyPath pins the §11.3 pipeline: queued → waiting →
// claim → exactly one prompt → Delivered, with the state machine observed.
func TestDeliveryWorkerHappyPath(t *testing.T) {
	id := ConversationID("live:claude-code:pane-7")
	queue := NewFollowUpQueue()
	if err := queue.Enqueue(queuedItem(id)); err != nil {
		t.Fatal(err)
	}
	transport := &fakeDeliveryTransport{fingerprint: "fp-1"}
	var states []QueueState
	worker := deliveryWorker(queue, transport, &states)

	outcome := worker.DeliverOnce(context.Background(), id)
	if !outcome.Sent || outcome.Retained || outcome.Reason != "" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if transport.deliverCalls != 1 {
		t.Fatalf("deliver calls = %d", transport.deliverCalls)
	}
	// A delivered item leaves the list at once.
	if _, ok := queue.Head(id); ok {
		t.Fatal("the delivered item must leave the list")
	}
	if len(states) < 3 || states[0] != WaitingForTurnBoundary || states[len(states)-1] != Delivered {
		t.Fatalf("observed states = %v", states)
	}

	// An empty list is inert to further cycles.
	again := worker.DeliverOnce(context.Background(), id)
	if again.Sent || transport.deliverCalls != 1 {
		t.Fatalf("delivered item re-sent: %+v calls %d", again, transport.deliverCalls)
	}
}

// TestDeliveryWorkerDrainsOnePerBoundary pins the stack semantics: one
// prompt per cycle, in list order, and More reports the follower that owns
// the next turn boundary.
func TestDeliveryWorkerDrainsOnePerBoundary(t *testing.T) {
	id := ConversationID("live:claude-code:pane-7")
	queue := NewFollowUpQueue()
	_ = queue.Enqueue(stackedItem(id, "req-1", "first"))
	_ = queue.Enqueue(stackedItem(id, "req-2", "second"))
	_ = queue.Enqueue(stackedItem(id, "req-3", "third"))
	transport := &fakeDeliveryTransport{fingerprint: "fp-1"}
	worker := deliveryWorker(queue, transport, nil)

	// The worker's first cycle takes the head: one prompt, and More
	// reports the follower that owns the next turn boundary.
	outcome := worker.DeliverOnce(context.Background(), id)
	if !outcome.Sent || !outcome.More {
		t.Fatalf("first delivery = %+v, want More", outcome)
	}
	// While the second entry waits on its boundary it stays pinned; the
	// tail may reorder below the pinned run but never jump it.
	_, _ = queue.Transition(id, "req-2", WaitingForTurnBoundary, "worker cycle took the item")
	if err := queue.Move(id, "req-3", 0); err == nil {
		t.Fatal("a queued item must not jump above the pinned head run")
	}
	if err := queue.Enqueue(stackedItem(id, "req-4", "fourth")); err != nil {
		t.Fatal(err)
	}
	if err := queue.Move(id, "req-4", 1); err != nil {
		t.Fatal(err)
	}
	outcome = worker.DeliverOnce(context.Background(), id)
	if !outcome.Sent || !outcome.More {
		t.Fatalf("second delivery = %+v, want More", outcome)
	}
	outcome = worker.DeliverOnce(context.Background(), id)
	if !outcome.Sent || !outcome.More {
		t.Fatalf("third delivery = %+v, want More", outcome)
	}
	outcome = worker.DeliverOnce(context.Background(), id)
	if !outcome.Sent || outcome.More {
		t.Fatalf("last delivery = %+v, want no More", outcome)
	}
	if transport.deliverCalls != 4 {
		t.Fatalf("deliver calls = %d", transport.deliverCalls)
	}
	// The reordered stack delivered in list order, one prompt per cycle.
	if got := transport.delivered; got[0] != "first" || got[1] != "second" || got[2] != "fourth" || got[3] != "third" {
		t.Fatalf("delivery order = %v", got)
	}
	if _, ok := queue.Head(id); ok {
		t.Fatal("the drained list must be empty")
	}
}

// TestDeliveryWorkerUncertainFreezesTheTail pins the frozen stack: an
// uncertain head holds every follower back — nothing sends behind a send
// that may or may not have happened — until the entries are acknowledged.
func TestDeliveryWorkerUncertainFreezesTheTail(t *testing.T) {
	id := ConversationID("live:claude-code:pane-7")
	queue := NewFollowUpQueue()
	_ = queue.Enqueue(stackedItem(id, "req-1", "first"))
	_ = queue.Enqueue(stackedItem(id, "req-2", "second"))
	transport := &fakeDeliveryTransport{fingerprint: "fp-1", deliverErr: &fakeUncertainDelivery{}}
	worker := deliveryWorker(queue, transport, nil)

	outcome := worker.DeliverOnce(context.Background(), id)
	if !outcome.Sent || outcome.More {
		t.Fatalf("uncertain head = %+v, want frozen (no More)", outcome)
	}
	items, _ := queue.Items(id)
	if len(items) != 2 || items[0].State != DeliveryUncertain || items[1].State != Queued {
		t.Fatalf("frozen stack = %+v", items)
	}
	// A further cycle must not deliver the tail.
	transport.deliverErr = nil
	if again := worker.DeliverOnce(context.Background(), id); again.Sent || transport.deliverCalls != 1 {
		t.Fatalf("tail sent behind an uncertain delivery: %+v", again)
	}
	if queue.HasPending(id) {
		t.Fatal("a frozen conversation must not report pending work")
	}
	// The acknowledgment unfreezes: both entries leave, the tail never sent.
	if _, err := queue.CancelItem(id, "req-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.CancelItem(id, "req-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := queue.Items(id); ok {
		t.Fatal("acknowledged stack must be empty")
	}
}

// TestDeliveryWorkerBlockedRetains pins §11.4: blocked or timed-out targets
// retain the queue and never send.
func TestDeliveryWorkerBlockedRetains(t *testing.T) {
	id := ConversationID("live:claude-code:pane-7")
	queue := NewFollowUpQueue()
	_ = queue.Enqueue(queuedItem(id))
	transport := &fakeDeliveryTransport{waitErr: errors.New("turn boundary timeout")}
	worker := deliveryWorker(queue, transport, nil)

	outcome := worker.DeliverOnce(context.Background(), id)
	if outcome.Sent || !outcome.Retained {
		t.Fatalf("outcome = %+v", outcome)
	}
	if transport.deliverCalls != 0 {
		t.Fatal("retained item must not send")
	}
	item, _ := queue.Head(id)
	if item.State != WaitingForTurnBoundary {
		t.Fatalf("retained state = %q", item.State)
	}

	// A later cycle with a settled target delivers.
	transport.waitErr = nil
	transport.fingerprint = "fp-1"
	outcome = worker.DeliverOnce(context.Background(), id)
	if !outcome.Sent {
		t.Fatalf("retry after settle = %+v", outcome)
	}
}

// TestDeliveryWorkerIdentityChangeFailsClosed pins §11.4: a changed or
// unprovable occupant fails closed into recoverable text — never sends —
// and the item can be requeued afterwards.
func TestDeliveryWorkerIdentityChangeFailsClosed(t *testing.T) {
	id := ConversationID("live:claude-code:pane-7")
	queue := NewFollowUpQueue()
	_ = queue.Enqueue(queuedItem(id))
	transport := &fakeDeliveryTransport{fingerprint: "fp-other"}
	worker := deliveryWorker(queue, transport, nil)

	outcome := worker.DeliverOnce(context.Background(), id)
	if outcome.Sent {
		t.Fatal("identity change must never send")
	}
	item, _ := queue.Head(id)
	if item.State != FailedRecoverable {
		t.Fatalf("state = %q", item.State)
	}

	// Unprovable occupant (empty fingerprint) also fails closed.
	queue2 := NewFollowUpQueue()
	_ = queue2.Enqueue(queuedItem(id))
	transport2 := &fakeDeliveryTransport{fingerprint: ""}
	worker2 := deliveryWorker(queue2, transport2, nil)
	outcome = worker2.DeliverOnce(context.Background(), id)
	if outcome.Sent {
		t.Fatal("sessionless occupant must never send")
	}
	if item, _ := queue2.Head(id); item.State != FailedRecoverable {
		t.Fatalf("sessionless state = %q", item.State)
	}

	// Recoverable failure can be requeued and then delivers.
	if _, err := queue.Transition(id, item.RequestID, Queued, "user retried"); err != nil {
		t.Fatal(err)
	}
	transport.fingerprint = "fp-1"
	if outcome := worker.DeliverOnce(context.Background(), id); !outcome.Sent {
		t.Fatalf("requeued delivery = %+v", outcome)
	}
}

// TestDeliveryWorkerUncertainTombstone pins the tombstone: uncertainty
// lands in DeliveryUncertain, is never auto-retried, and no later cycle
// sends a duplicate.
func TestDeliveryWorkerUncertainTombstone(t *testing.T) {
	id := ConversationID("live:claude-code:pane-7")
	queue := NewFollowUpQueue()
	_ = queue.Enqueue(queuedItem(id))
	transport := &fakeDeliveryTransport{fingerprint: "fp-1", deliverErr: &fakeUncertainDelivery{}}
	worker := deliveryWorker(queue, transport, nil)

	outcome := worker.DeliverOnce(context.Background(), id)
	if !outcome.Sent {
		t.Fatal("uncertainty means the prompt may have arrived: Sent stays true")
	}
	item, _ := queue.Head(id)
	if item.State != DeliveryUncertain {
		t.Fatalf("state = %q", item.State)
	}

	// Never auto-retried.
	transport.deliverErr = nil
	again := worker.DeliverOnce(context.Background(), id)
	if again.Sent || transport.deliverCalls != 1 {
		t.Fatalf("uncertain tombstone retried: %+v calls %d", again, transport.deliverCalls)
	}
}

// TestDeliveryWorkerDefiniteRejection pins the recoverable path: a definite
// rejection keeps the recoverable text on the item.
func TestDeliveryWorkerDefiniteRejection(t *testing.T) {
	id := ConversationID("live:claude-code:pane-7")
	queue := NewFollowUpQueue()
	_ = queue.Enqueue(queuedItem(id))
	transport := &fakeDeliveryTransport{fingerprint: "fp-1", deliverErr: errors.New("prompt refused by provider")}
	worker := deliveryWorker(queue, transport, nil)

	outcome := worker.DeliverOnce(context.Background(), id)
	if !outcome.Sent || outcome.Retained {
		t.Fatalf("outcome = %+v", outcome)
	}
	item, _ := queue.Head(id)
	if item.State != FailedRecoverable || item.Detail != "prompt refused by provider" {
		t.Fatalf("recovered item = %+v", item)
	}
}

// TestDeliveryWorkerClaimLost pins the race guards: an item already claimed
// by another worker is left alone, and a stale worker cycle cannot send a
// request it no longer owns.
func TestDeliveryWorkerClaimLost(t *testing.T) {
	id := ConversationID("live:claude-code:pane-7")
	queue := NewFollowUpQueue()
	_ = queue.Enqueue(queuedItem(id))
	transport := &fakeDeliveryTransport{fingerprint: "fp-1"}
	worker := deliveryWorker(queue, transport, nil)

	// Another worker wins the claim first.
	if _, err := queue.ClaimDelivery(id, "req-1"); err != nil {
		t.Fatal(err)
	}
	outcome := worker.DeliverOnce(context.Background(), id)
	if outcome.Sent || transport.deliverCalls != 0 {
		t.Fatalf("lost claim sent: %+v", outcome)
	}
	// The owning worker completes normally.
	if _, err := queue.CompleteDelivery(id, "req-1", true, ""); err != nil {
		t.Fatal(err)
	}

	// cancel-A/enqueue-B: the old worker's claim (request-id keyed) cannot
	// deliver the replacement — the cancelled item is gone, the claim misses.
	queue2 := NewFollowUpQueue()
	_ = queue2.Enqueue(queuedItem(id))
	first, _ := queue2.Head(id)
	if _, err := queue2.CancelItem(id, first.RequestID); err != nil {
		t.Fatal(err)
	}
	replacement := queuedItem(id)
	replacement.RequestID = "req-2"
	_ = queue2.Enqueue(replacement)
	// The stale worker cycle still holds first.RequestID: its claim loses.
	if _, err := queue2.ClaimDelivery(id, first.RequestID); err == nil {
		t.Fatal("stale claim must lose against the replacement")
	}
	item, _ := queue2.Head(id)
	if item.RequestID != "req-2" || item.State != Queued {
		t.Fatalf("replacement item = %+v", item)
	}
}

// gateReservation blocks the worker until the test releases it, proving the
// reservation is held across the delivery section.
type gateReservation struct {
	acquired chan struct{}
	release  chan struct{}
}

func (g *gateReservation) Lock()   { close(g.acquired); <-g.release }
func (g *gateReservation) Unlock() {}

// TestDeliveryWorkerReservationHeld pins the per-conversation coordination:
// another holder of the conversation reservation defers the worker's
// delivery.
func TestDeliveryWorkerReservationHeld(t *testing.T) {
	id := ConversationID("live:claude-code:pane-7")
	queue := NewFollowUpQueue()
	_ = queue.Enqueue(queuedItem(id))
	transport := &fakeDeliveryTransport{fingerprint: "fp-1"}
	reservations := NewReservations()
	gate := &gateReservation{acquired: make(chan struct{}), release: make(chan struct{})}
	reservations.locks.Store(string(id), gate)
	worker := NewDeliveryWorker(queue, transport, reservations, nil)

	done := make(chan DeliveryOutcome, 1)
	go func() { done <- worker.DeliverOnce(context.Background(), id) }()

	select {
	case <-gate.acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never acquired the conversation reservation")
	}
	if transport.deliverCalls != 0 {
		t.Fatal("delivery ran before the reservation was granted")
	}
	close(gate.release)
	select {
	case outcome := <-done:
		if !outcome.Sent {
			t.Fatalf("post-release outcome = %+v", outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish after release")
	}
}

// TestDeliveryWorkerConcurrentClaim pins §11.4: two workers racing one item
// produce exactly one delivery.
func TestDeliveryWorkerConcurrentClaim(t *testing.T) {
	id := ConversationID("live:claude-code:pane-7")
	queue := NewFollowUpQueue()
	_ = queue.Enqueue(queuedItem(id))
	transport := &fakeDeliveryTransport{fingerprint: "fp-1"}
	reservations := NewReservations()
	worker := NewDeliveryWorker(queue, transport, reservations, nil)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.DeliverOnce(context.Background(), id)
		}()
	}
	wg.Wait()
	if transport.deliverCalls != 1 {
		t.Fatalf("deliver calls = %d, want exactly 1", transport.deliverCalls)
	}
	if _, ok := queue.Head(id); ok {
		t.Fatal("the delivered item must leave the list")
	}
}

// TestReservationsFor pins the coordinator: one lock per conversation,
// distinct across conversations, nil for empty.
func TestReservationsFor(t *testing.T) {
	reservations := NewReservations()
	first := reservations.For("live:claude-code:a")
	if first == nil {
		t.Fatal("non-empty conversation must return a lock")
	}
	if reservations.For("live:claude-code:a") != first {
		t.Fatal("same conversation must return the same lock")
	}
	if reservations.For("live:claude-code:b") == first {
		t.Fatal("different conversations must not share a lock")
	}
	if reservations.For("") != nil {
		t.Fatal("empty conversation must return nil")
	}
}

// TestQueuedConversationsSnapshot pins the drain snapshot: only pending
// states are listed, in stable order, and a mid-delivery head is left to
// its cycle.
func TestQueuedConversationsSnapshot(t *testing.T) {
	queue := NewFollowUpQueue()
	a := ConversationID("live:claude-code:a")
	b := ConversationID("live:claude-code:b")
	c := ConversationID("live:claude-code:c")
	_ = queue.Enqueue(queuedItem(b))
	_ = queue.Enqueue(queuedItem(a))
	_ = queue.Enqueue(queuedItem(c))
	if _, err := queue.CompleteDelivery(c, "req-1", true, ""); err == nil {
		t.Fatal("complete without claim must fail")
	}
	claimed, err := queue.ClaimDelivery(c, "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.CompleteDelivery(c, "req-1", true, ""); err != nil {
		t.Fatalf("complete after claim: %v (%+v)", err, claimed)
	}
	ids := queue.QueuedConversations()
	if len(ids) != 2 || ids[0] != a || ids[1] != b {
		t.Fatalf("queued snapshot = %v", ids)
	}
}

// TestRunRetriesARetainedItemUntilTheTurnBoundary pins the long-turn case: the
// first bounded wait times out (retained), nothing wakes the worker again, and
// yet the follow-up is delivered once the boundary is reached.
func TestRunRetriesARetainedItemUntilTheTurnBoundary(t *testing.T) {
	id := ConversationID("live:omp:pane-7")
	queue := NewFollowUpQueue()
	if err := queue.Enqueue(queuedItem(id)); err != nil {
		t.Fatal(err)
	}
	transport := &fakeDeliveryTransport{fingerprint: "fp-1", waitErr: errors.New("wait timed out")}
	wake := make(chan struct{}, 1)
	worker := NewDeliveryWorker(queue, transport, NewReservations(), wake)
	worker.RetryAfter = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Run(ctx)
	wake <- struct{}{}

	time.Sleep(40 * time.Millisecond) // several retained cycles
	transport.mu.Lock()
	calls := transport.deliverCalls
	transport.waitErr = nil // the turn ended
	transport.mu.Unlock()
	if calls != 0 {
		t.Fatalf("delivered %d prompts before the turn boundary", calls)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		transport.mu.Lock()
		done := transport.deliverCalls == 1
		transport.mu.Unlock()
		if done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	t.Fatalf("never delivered; calls %d, queue %+v", transport.deliverCalls, queue.Snapshot())
}

// TestRetainedItemFailsClosedAfterTheCap pins the bound: a boundary that never
// comes ends as recoverable text, not an endless silent queue.
func TestRetainedItemFailsClosedAfterTheCap(t *testing.T) {
	id := ConversationID("live:omp:pane-7")
	queue := NewFollowUpQueue()
	if err := queue.Enqueue(queuedItem(id)); err != nil {
		t.Fatal(err)
	}
	transport := &fakeDeliveryTransport{fingerprint: "fp-1", waitErr: errors.New("pane gone")}
	var states []QueueState
	worker := deliveryWorker(queue, transport, &states)
	for i := 1; i < MaxRetainedCycles; i++ {
		if !worker.DrainAll(context.Background()) {
			t.Fatalf("cycle %d: a retained item must ask for another cycle", i)
		}
	}
	if worker.DrainAll(context.Background()) {
		t.Fatal("the capped cycle must stop retrying")
	}
	item, _ := queue.Head(id)
	if item.State != FailedRecoverable || transport.deliverCalls != 0 {
		t.Fatalf("state %s, prompts %d", item.State, transport.deliverCalls)
	}
	if states[len(states)-1] != FailedRecoverable {
		t.Fatalf("the UI never saw the failure: %v", states)
	}
}

// TestCancelledItemStopsTheRetries: a withdrawn follow-up leaves nothing to retry.
func TestCancelledItemStopsTheRetries(t *testing.T) {
	id := ConversationID("live:omp:pane-7")
	queue := NewFollowUpQueue()
	if err := queue.Enqueue(queuedItem(id)); err != nil {
		t.Fatal(err)
	}
	transport := &fakeDeliveryTransport{fingerprint: "fp-1", waitErr: errors.New("wait timed out")}
	worker := deliveryWorker(queue, transport, nil)
	if !worker.DrainAll(context.Background()) {
		t.Fatal("first cycle must retain")
	}
	if _, err := queue.CancelAll(id); err != nil {
		t.Fatal(err)
	}
	if worker.DrainAll(context.Background()) {
		t.Fatal("a cancelled item must not ask for another cycle")
	}
	if len(worker.retained) != 0 {
		t.Fatalf("retained counters leaked: %v", worker.retained)
	}
}
