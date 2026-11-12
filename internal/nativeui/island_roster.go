package nativeui

/**
 * [INPUT]: StatusCenterSnapshot（菜单栏面板同一份快照）、feedback.ThreadSummary / feedback.Thread、agentNotificationID / feedbackNotificationID
 * [OUTPUT]: 对外提供 islandRoster / islandRow / islandAsk、buildIslandRoster、islandAskOf
 * [POS]: 刘海岛 P2 的名册纯模型（2026-11-12 决策，方向 C）：Needs you / Working / Idle 三组，与菜单栏面板同一份事实；Feedback 提问若只有一个单选问题就带行内选项。纯投影、无 IO、不持有状态
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/feedback"
)

// Rows each group shows before it says "+N more".
const (
	islandNeedsMax   = 4
	islandWorkingMax = 3
	islandIdleMax    = 2

	islandOptionsMax = 4  // more chips than this do not fit one line
	islandOptionLen  = 28 // a longer label is a sentence: answer it in Feedback
)

// islandAsk is what the island needs to answer one Feedback Ask in a click: a
// single question with a few short options.
type islandAsk struct {
	Thread   string
	Ask      string
	Question string
	Options  []string
}

// islandRow is one line of the roster.
type islandRow struct {
	Key   string // stable across rebuilds: widget keys and "sent" memory hang on it
	Title string
	Sub   string
	Kind  islandKind
	Route string     // the notification ID Open follows; "" when there is nowhere to go
	Ask   *islandAsk // chips when set
}

// islandRoster is the three groups; More counts what a group cut off.
type islandRoster struct {
	Needs, Working, Idle             []islandRow
	NeedsMore, WorkingMore, IdleMore int
}

func (r islandRoster) Empty() bool {
	return len(r.Needs)+len(r.Working)+len(r.Idle) == 0
}

func capRows(rows []islandRow, max int) ([]islandRow, int) {
	if len(rows) <= max {
		return rows, 0
	}
	return rows[:max], len(rows) - max
}

// buildIslandRoster groups the waiting Feedback threads and the Agents of the
// snapshot. Feedback goes first in Needs: an Agent is blocked on it.
func buildIslandRoster(snap StatusCenterSnapshot, threads []feedback.ThreadSummary, asks map[string]islandAsk) islandRoster {
	var needs, working, idle []islandRow
	for _, t := range threads {
		if t.Waiting == 0 {
			continue
		}
		title := strings.TrimSpace(t.Project)
		if title == "" {
			title = "Feedback"
		}
		if t.Client != "" {
			title += " · " + t.Client
		}
		row := islandRow{
			Key: "fb:" + t.ID, Title: title, Sub: firstNonEmpty(t.Preview, t.Title), Kind: islandFeedback,
			Route: feedbackNotificationID(t.ID, ""),
		}
		if a, ok := asks[t.ID]; ok {
			row.Ask = &a
			row.Route = feedbackNotificationID(t.ID, a.Ask)
		}
		needs = append(needs, row)
	}
	for _, e := range snap.Entries {
		card := e.Card
		row := islandRow{
			Key:   "ag:" + card.Key.TerminalID,
			Title: card.Title,
			Sub:   strings.Trim(card.ProjectName+" · "+agent.OperationalLabel(card.Attention), " ·"),
			Route: agentNotificationID(card.Key.InstanceID, card.Key.TerminalID),
		}
		switch e.Bucket {
		case agent.BucketNeedsAttention:
			row.Kind = islandAttention
			needs = append(needs, row)
		case agent.BucketReadyForReview:
			row.Kind = islandReview
			needs = append(needs, row)
		case agent.BucketWorking:
			working = append(working, row)
		default:
			idle = append(idle, row)
		}
	}
	var r islandRoster
	r.Needs, r.NeedsMore = capRows(needs, islandNeedsMax)
	r.Working, r.WorkingMore = capRows(working, islandWorkingMax)
	r.Idle, r.IdleMore = capRows(idle, islandIdleMax)
	return r
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// islandAskOf reads the Ask a thread waits on and says whether the island can
// answer it in a click: the newest pending Ask (the one the row's words come
// from), if it has exactly one single-choice question of two to four short
// options. An older pending Ask stays for Feedback itself.
func islandAskOf(t feedback.Thread) (islandAsk, bool) {
	var pending *feedback.Ask
	for _, a := range t.Asks {
		if a.State == feedback.StatePending && (pending == nil || a.CreatedAt >= pending.CreatedAt) {
			pending = a
		}
	}
	if pending == nil || len(pending.Questions) != 1 {
		return islandAsk{}, false
	}
	q := pending.Questions[0]
	if q.Type != "single" || len(q.Options) < 2 || len(q.Options) > islandOptionsMax {
		return islandAsk{}, false
	}
	for _, o := range q.Options {
		if len([]rune(o)) > islandOptionLen {
			return islandAsk{}, false
		}
	}
	return islandAsk{Thread: t.ID, Ask: pending.ID, Question: q.ID, Options: q.Options}, true
}
