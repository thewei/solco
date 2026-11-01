package conversation

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/thewei/solco/internal/agent"
)
/**
 * [INPUT]: 纯领域包：QueueItem（request id、会话、窗格、身份指纹、正文）与 QueueState 生命周期
 * [OUTPUT]: FollowUpQueue——每会话一条按投递顺序排列的跟进列表（Enqueue 追加、Items/Head 读、Move 排序、
 *           CancelItem/CancelAll 撤回、Transition/ClaimDelivery/CompleteDelivery 投递状态机、
 *           Snapshot/Restore 台账持久化、QueuedConversations/HasPending worker 排水）；错误
 *           ErrNoQueueItem/ErrQueueFrozen/ErrAlreadyDelivered
 * [POS]: 0.6 §11 的多消息演进：一个会话可堆叠多条跟进，顺序即投递顺序，一条消息占用一个回合边界；
 *        已送达/已取消的条目即从列表移除，DeliveryUncertain 是冻结墓碑（阻塞追加、显式取消即确认，
 *        绝不自动重发、绝不假装未发出）；所有操作按 request id 键控，取消A/入队B/移动/认领互相竞态
 *        都不会让旧 worker 误发
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// QueueState is the follow-up queue lifecycle (0.6 §11.2). Blocked retains
// the queue but never sends; uncertainty never auto-retries.
type QueueState string

const (
	Queued                 QueueState = "queued"
	WaitingForTurnBoundary QueueState = "waiting-for-turn-boundary"
	Delivering             QueueState = "delivering"
	Delivered              QueueState = "delivered"
	Cancelled              QueueState = "cancelled"
	FailedRecoverable      QueueState = "failed-recoverable"
	DeliveryUncertain      QueueState = "delivery-uncertain"
)

// QueueItem is one queued semantic follow-up of a live Conversation (0.6
// §11.1); a conversation stacks several, in delivery order.
type QueueItem struct {
	RequestID      string
	ConversationID ConversationID
	AgentPaneID    string
	Fingerprint    string
	BaselineRev    uint64
	Text           string
	CreatedAt      time.Time
	State          QueueState
	Detail         string
	// Instance is the Herdr session the exact-target operations run against
	// (client routing fact for the transport; Herdr stays the authority).
	Instance string
}

// ErrNoQueueItem reports a missing queue entry.
var ErrNoQueueItem = errors.New("no queued item for this conversation")

// ErrQueueFrozen reports an enqueue behind a DeliveryUncertain tombstone:
// the uncertain send must be acknowledged (cancel the entry) first.
var ErrQueueFrozen = errors.New("delivery uncertain — cancel the failed entry before queueing more")

// ErrAlreadyDelivered reports a cancel/claim racing a completed delivery.
var ErrAlreadyDelivered = errors.New("follow-up already delivered")

// ConversationQueue is one conversation's ordered follow-ups (delivery
// order). Delivered and cancelled items leave the list at once; a
// DeliveryUncertain item stays as the frozen tombstone at the head.
type ConversationQueue struct {
	Items []*QueueItem
}

// FollowUpQueue is the per-conversation follow-up queue (0.6 §11): an
// ordered list per conversation, delivered one prompt per turn boundary in
// list order. Exactly one delivery worker can own the atomic claim; races
// cannot let an old worker send a moved or replaced item because every
// operation is keyed by item identity (request id), not by conversation
// alone.
type FollowUpQueue struct {
	mu     sync.Mutex
	queues map[ConversationID]*ConversationQueue
	// onChanged fires after every mutation, outside the mutex — the
	// persistence owner serializes the ledger across restarts (0.6 §9.3).
	onChanged func()
}

func NewFollowUpQueue() *FollowUpQueue {
	return &FollowUpQueue{queues: make(map[ConversationID]*ConversationQueue)}
}

// SetOnChanged wires the mutation observer (persistence). Must be called
// before concurrent use.
func (q *FollowUpQueue) SetOnChanged(fn func()) {
	q.mu.Lock()
	q.onChanged = fn
	q.mu.Unlock()
}

// changed reports the observer to call after the caller releases the mutex.
func (q *FollowUpQueue) changed() func() {
	return q.onChanged
}

// Enqueue appends one follow-up to the conversation's list (the stack
// behind the running turn). A conversation whose head is a
// DeliveryUncertain tombstone refuses new items (ErrQueueFrozen): the
// uncertain send must be acknowledged before more text stacks behind it.
func (q *FollowUpQueue) Enqueue(item QueueItem) error {
	if item.RequestID == "" || item.ConversationID == "" || item.Text == "" {
		return errors.New("queued follow-up requires request id, conversation and text")
	}
	q.mu.Lock()
	cq := q.queues[item.ConversationID]
	if cq == nil {
		cq = &ConversationQueue{}
		q.queues[item.ConversationID] = cq
	}
	if len(cq.Items) > 0 && cq.Items[0].State == DeliveryUncertain {
		q.mu.Unlock()
		return ErrQueueFrozen
	}
	if item.State == "" {
		item.State = Queued
	}
	item.CreatedAt = time.Now()
	stored := item
	cq.Items = append(cq.Items, &stored)
	changed := q.changed()
	q.mu.Unlock()
	if changed != nil {
		changed()
	}
	return nil
}

// Items copies the conversation's list in delivery order.
func (q *FollowUpQueue) Items(id ConversationID) ([]QueueItem, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	cq := q.queues[id]
	if cq == nil || len(cq.Items) == 0 {
		return nil, false
	}
	out := make([]QueueItem, 0, len(cq.Items))
	for _, item := range cq.Items {
		out = append(out, *item)
	}
	return out, true
}

// Head returns the conversation's first follow-up (the worker's entry).
func (q *FollowUpQueue) Head(id ConversationID) (QueueItem, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	cq := q.queues[id]
	if cq == nil || len(cq.Items) == 0 {
		return QueueItem{}, false
	}
	return *cq.Items[0], true
}

// find locates one item by request id; the caller holds the mutex.
func (q *FollowUpQueue) find(cq *ConversationQueue, requestID string) *QueueItem {
	for _, item := range cq.Items {
		if item.RequestID == requestID {
			return item
		}
	}
	return nil
}

// remove drops one item from the list; the caller holds the mutex.
func (q *FollowUpQueue) remove(cq *ConversationQueue, item *QueueItem) {
	for i, candidate := range cq.Items {
		if candidate == item {
			cq.Items = append(cq.Items[:i], cq.Items[i+1:]...)
			return
		}
	}
}

// Transition moves one item through a legal state and reports it.
func (q *FollowUpQueue) Transition(id ConversationID, requestID string, to QueueState, detail string) (QueueItem, error) {
	q.mu.Lock()
	cq := q.queues[id]
	var item *QueueItem
	if cq != nil {
		item = q.find(cq, requestID)
	}
	if item == nil {
		q.mu.Unlock()
		return QueueItem{}, ErrNoQueueItem
	}
	if !legalQueueTransition(item.State, to) {
		err := errors.New("illegal queue transition " + string(item.State) + " → " + string(to))
		q.mu.Unlock()
		return *item, err
	}
	item.State, item.Detail = to, detail
	changed := q.changed()
	q.mu.Unlock()
	if changed != nil {
		changed()
	}
	return *item, nil
}

func legalQueueTransition(from, to QueueState) bool {
	switch from {
	case Queued:
		return to == WaitingForTurnBoundary || to == Delivering || to == Cancelled
	case WaitingForTurnBoundary:
		return to == Delivering || to == Cancelled || to == FailedRecoverable
	case Delivering:
		return to == Delivered || to == DeliveryUncertain || to == FailedRecoverable
	case FailedRecoverable:
		return to == Queued || to == Cancelled
	case DeliveryUncertain:
		// The only way out of the tombstone is the explicit user
		// acknowledgment — never an auto retry, never a silent replace.
		return to == Cancelled
	default:
		return false
	}
}

// ClaimDelivery is the atomic delivery claim (0.6 §11.3): exactly one
// worker moves the named item into Delivering. A worker whose claim lost —
// because another worker took it or the item moved on — must not send.
func (q *FollowUpQueue) ClaimDelivery(id ConversationID, requestID string) (QueueItem, error) {
	q.mu.Lock()
	cq := q.queues[id]
	var item *QueueItem
	if cq != nil {
		item = q.find(cq, requestID)
	}
	if item == nil {
		q.mu.Unlock()
		return QueueItem{}, ErrNoQueueItem
	}
	switch item.State {
	case Queued, WaitingForTurnBoundary, FailedRecoverable:
		item.State = Delivering
		changed := q.changed()
		q.mu.Unlock()
		if changed != nil {
			changed()
		}
		return *item, nil
	default:
		q.mu.Unlock()
		return *item, ErrAlreadyDelivered
	}
}

// CompleteDelivery records the send result of the claimed item. A delivered
// item leaves the list at once (the next entry owns the following turn
// boundary); uncertainty stays as the frozen tombstone: it never auto-retries
// and never pretends the text was unsent.
func (q *FollowUpQueue) CompleteDelivery(id ConversationID, requestID string, delivered bool, detail string) (QueueItem, error) {
	q.mu.Lock()
	cq := q.queues[id]
	var item *QueueItem
	if cq != nil {
		item = q.find(cq, requestID)
	}
	if item == nil {
		q.mu.Unlock()
		return QueueItem{}, ErrNoQueueItem
	}
	if item.State != Delivering {
		err := errors.New("complete without an active delivery claim")
		q.mu.Unlock()
		return *item, err
	}
	if delivered {
		item.State = Delivered
	} else {
		item.State = DeliveryUncertain
	}
	item.Detail = detail
	if delivered {
		q.remove(cq, item)
	}
	changed := q.changed()
	q.mu.Unlock()
	if changed != nil {
		changed()
	}
	return *item, nil
}

// CancelItem withdraws one follow-up before its delivery commit. An
// uncertain item may be cancelled too: the cancel is the explicit
// acknowledgment of the tombstone, never a claim the text was unsent.
func (q *FollowUpQueue) CancelItem(id ConversationID, requestID string) (QueueItem, error) {
	q.mu.Lock()
	cq := q.queues[id]
	var item *QueueItem
	if cq != nil {
		item = q.find(cq, requestID)
	}
	if item == nil {
		q.mu.Unlock()
		return QueueItem{}, ErrNoQueueItem
	}
	switch item.State {
	case Queued, WaitingForTurnBoundary, FailedRecoverable:
		changed := q.cancelLocked(cq, item, "")
		q.mu.Unlock()
		if changed != nil {
			changed()
		}
		return *item, nil
	case DeliveryUncertain:
		changed := q.cancelLocked(cq, item, "uncertain delivery acknowledged")
		q.mu.Unlock()
		if changed != nil {
			changed()
		}
		return *item, nil
	case Delivering:
		err := errors.New("cancel raced the delivery commit")
		q.mu.Unlock()
		return *item, err
	default:
		q.mu.Unlock()
		return *item, ErrAlreadyDelivered
	}
}

// CancelAll withdraws every cancellable follow-up of the conversation and
// reports how many went; an item mid-delivery stays (its commit may not be
// raced). ErrNoQueueItem when the conversation holds nothing; ErrAlreadyDelivered
// when everything present was past its commit.
func (q *FollowUpQueue) CancelAll(id ConversationID) (int, error) {
	q.mu.Lock()
	cq := q.queues[id]
	if cq == nil || len(cq.Items) == 0 {
		q.mu.Unlock()
		return 0, ErrNoQueueItem
	}
	cancelled := 0
	kept := cq.Items[:0]
	for _, item := range cq.Items {
		switch item.State {
		case Queued, WaitingForTurnBoundary, FailedRecoverable:
			q.cancelLocked(cq, item, "")
			cancelled++
		case DeliveryUncertain:
			q.cancelLocked(cq, item, "uncertain delivery acknowledged")
			cancelled++
		default:
			kept = append(kept, item)
		}
	}
	cq.Items = kept
	changed := q.changed()
	q.mu.Unlock()
	if changed != nil {
		changed()
	}
	if cancelled == 0 {
		return 0, ErrAlreadyDelivered
	}
	return cancelled, nil
}

// cancelLocked marks the item cancelled, records the acknowledgment detail
// and drops it from the list; the caller holds the mutex and fires the
// returned observer afterwards.
func (q *FollowUpQueue) cancelLocked(cq *ConversationQueue, item *QueueItem, detail string) func() {
	item.State = Cancelled
	if detail != "" {
		item.Detail = detail
	}
	q.remove(cq, item)
	return q.changed()
}

// Move reorders one follow-up (delivery order = list order). Only items the
// worker has not claimed yet move (queued or recoverably failed); items
// waiting for or in delivery stay pinned to the list's head run, so a move
// can never jump the prompt that is already handed to the send path.
func (q *FollowUpQueue) Move(id ConversationID, requestID string, to int) error {
	q.mu.Lock()
	cq := q.queues[id]
	var item *QueueItem
	index := -1
	if cq != nil {
		for i, candidate := range cq.Items {
			if candidate.RequestID == requestID {
				item, index = candidate, i
				break
			}
		}
	}
	if item == nil {
		q.mu.Unlock()
		return ErrNoQueueItem
	}
	switch item.State {
	case Queued, FailedRecoverable:
	default:
		q.mu.Unlock()
		return errors.New("the follow-up is already claimed and cannot move")
	}
	pinned := 0
	for _, candidate := range cq.Items {
		if candidate.State == WaitingForTurnBoundary || candidate.State == Delivering {
			pinned++
			continue
		}
		break
	}
	if to < pinned || to >= len(cq.Items) {
		q.mu.Unlock()
		return errors.New("move target out of range")
	}
	if to != index {
		item = cq.Items[index]
		cq.Items = append(cq.Items[:index], cq.Items[index+1:]...)
		cq.Items = append(cq.Items, nil)
		copy(cq.Items[to+1:], cq.Items[to:])
		cq.Items[to] = item
	}
	changed := q.changed()
	q.mu.Unlock()
	if changed != nil {
		changed()
	}
	return nil
}

// SendabilityGate decides at commit time whether the queued item may be
// sent now (the service re-derives from live sendability — never from the
// composer label).
func SendabilityGate(sendability agent.AgentSendability) (bool, PromptDisposition) {
	switch DispositionFor(sendability) {
	case DispositionSentNow:
		return true, DispositionSentNow
	case DispositionQueuedAfterTurn:
		return false, DispositionQueuedAfterTurn
	case DispositionNeedsTerminal:
		return false, DispositionNeedsTerminal
	default:
		return false, DispositionUnknown
	}
}

// QueuedConversations lists the conversations whose head item is pending
// delivery (queued, waiting for a turn boundary, or recoverably failed) —
// the worker's drain snapshot. A conversation whose head is mid-delivery is
// inside a cycle already; one frozen by an uncertain tombstone is not
// drained.
func (q *FollowUpQueue) QueuedConversations() []ConversationID {
	q.mu.Lock()
	defer q.mu.Unlock()
	ids := make([]ConversationID, 0, len(q.queues))
	for id, cq := range q.queues {
		if len(cq.Items) == 0 {
			continue
		}
		switch cq.Items[0].State {
		case Queued, WaitingForTurnBoundary, FailedRecoverable:
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// HasPending reports whether the conversation still holds an item the
// worker should pick up after the current one settles (the next entry owns
// the following turn boundary).
func (q *FollowUpQueue) HasPending(id ConversationID) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	cq := q.queues[id]
	if cq == nil || len(cq.Items) == 0 {
		return false
	}
	switch cq.Items[0].State {
	case Queued, WaitingForTurnBoundary, FailedRecoverable:
		return true
	}
	return false
}

// Snapshot copies every ledger item — conversations in stable id order,
// each conversation's items in delivery order (all states; the persistence
// owner decides what a restart means for each).
func (q *FollowUpQueue) Snapshot() []QueueItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	ids := make([]ConversationID, 0, len(q.queues))
	for id := range q.queues {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	items := make([]QueueItem, 0)
	for _, id := range ids {
		for _, item := range q.queues[id].Items {
			items = append(items, *item)
		}
	}
	return items
}

// Restore replaces the ledger with the given items (restart load); the file
// order inside one conversation is the delivery order.
func (q *FollowUpQueue) Restore(items []QueueItem) {
	q.mu.Lock()
	q.queues = make(map[ConversationID]*ConversationQueue, len(items))
	for i := range items {
		item := items[i]
		cq := q.queues[item.ConversationID]
		if cq == nil {
			cq = &ConversationQueue{}
			q.queues[item.ConversationID] = cq
		}
		stored := item
		cq.Items = append(cq.Items, &stored)
	}
	changed := q.changed()
	q.mu.Unlock()
	if changed != nil {
		changed()
	}
}
