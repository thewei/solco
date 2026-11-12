package nativeui

/**
 * [INPUT]: 无（纯数据）
 * [OUTPUT]: 对外提供 islandKind（attention/feedback/review/automation）、islandAlert、islandToasts（Push / Next / Finish / Showing / Pending）
 * [POS]: 刘海岛（2026-11-12 决策）的提示队列：一次只显示一条，其余按 优先级 → 先到先显示 排队；同一 ID 的再次到达只更新文字。纯投影，无 IO、无计时器、无窗口；窗口与计时在 island.go
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// islandKind is what an alert is about; it picks the dot's tone and the
// order alerts wait in.
type islandKind int

const (
	// islandAttention is an Agent that needs the user (blocked, asks).
	islandAttention islandKind = iota
	// islandFeedback is a question from Solco Feedback.
	islandFeedback
	// islandReview is an Agent that finished or is ready.
	islandReview
	// islandAutomation is an Automation run that failed.
	islandAutomation
)

// rank orders the queue: what blocks an Agent first, then finished work,
// then Automation failures (the design's priority rule; equal ranks keep
// arrival order).
func (k islandKind) rank() int {
	switch k {
	case islandAttention, islandFeedback:
		return 0
	case islandReview:
		return 1
	default:
		return 2
	}
}

// islandAlert is one thing worth the notch. ID is the notification ID the
// system banner would carry: routeAgentNotification opens it.
type islandAlert struct {
	ID    string
	Kind  islandKind
	Title string
	Body  string
}

// islandQueueMax bounds the waiting alerts; a burst past it drops the least
// urgent, oldest one — the notch is a glance, not an inbox.
const islandQueueMax = 8

// islandToasts is the alert on show and the ones waiting behind it.
type islandToasts struct {
	showing *islandAlert
	queue   []islandAlert
}

// Push adds an alert. The same ID as the one on show or waiting replaces its
// words in place and keeps its turn.
func (t *islandToasts) Push(a islandAlert) {
	if t.showing != nil && t.showing.ID == a.ID {
		t.showing.Title, t.showing.Body, t.showing.Kind = a.Title, a.Body, a.Kind
		return
	}
	for i := range t.queue {
		if t.queue[i].ID == a.ID {
			t.queue[i] = a
			return
		}
	}
	// Insert after the last alert of the same or a higher rank: urgent ones
	// pass the waiting, equals keep arrival order.
	at := len(t.queue)
	for i, q := range t.queue {
		if q.Kind.rank() > a.Kind.rank() {
			at = i
			break
		}
	}
	t.queue = append(t.queue, islandAlert{})
	copy(t.queue[at+1:], t.queue[at:])
	t.queue[at] = a
	if len(t.queue) > islandQueueMax {
		t.queue = t.queue[:islandQueueMax]
	}
}

// Next puts the head of the queue on show and returns it; false when none
// waits (the one on show, if any, is untouched).
func (t *islandToasts) Next() (islandAlert, bool) {
	if len(t.queue) == 0 {
		return islandAlert{}, false
	}
	a := t.queue[0]
	t.queue = t.queue[1:]
	t.showing = &a
	return a, true
}

// Finish takes the alert on show away (opened, dismissed or timed out).
func (t *islandToasts) Finish() { t.showing = nil }

// Showing is the alert on show.
func (t *islandToasts) Showing() (islandAlert, bool) {
	if t.showing == nil {
		return islandAlert{}, false
	}
	return *t.showing, true
}

// Pending is how many wait behind the one on show.
func (t *islandToasts) Pending() int { return len(t.queue) }

// Clear drops everything (the switch went off).
func (t *islandToasts) Clear() { t.showing, t.queue = nil, nil }
