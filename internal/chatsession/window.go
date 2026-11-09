package chatsession

import (
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 history.TranscriptMessage（LiveDecoder 的投影）、conversation.ItemsFromTranscript/DeriveTimeline
 * [OUTPUT]: 对外提供 MaxAccumulated、Window、Fold、FoldWithLimit（解码投影 → 一个渲染窗口的时间线/模型/上下文）、LastModel、LastContext
 * [POS]: Chat「尾部窗口 → 时间线」的唯一实现（2026-10-31 自 nativeui.applyLiveSync 抽出）：桌面 live 追尾与手机 Host 读同一个窗口规则（最新一页、新消息延长、超出渲染上界时前移）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// MaxAccumulated bounds how many messages a client window may accumulate
// before "Earlier messages" stops offering more (UI state never holds an
// unbounded transcript).
const MaxAccumulated = 4 * history.WindowLimit

// Window is one rendered slice of a followed transcript.
type Window struct {
	Turns         []conversation.TimelineTurn
	Model         string
	ContextTokens *int64
	// Start is the projection index the window begins at; feed it back into
	// the next Fold so new messages extend the window instead of sliding it.
	Start    int
	HasOlder bool
	// Messages is the window's committed row count.
	Messages int
}

// Fold derives the timeline of projection[start:]. start < 0 opens the newest
// page; the window is clamped to the render bound. pending is the provisional
// streaming tail (nil when settled): it renders uncommitted and marks the last
// turn live.
func Fold(projection []history.TranscriptMessage, pending *history.TranscriptMessage, start int) Window {
	return FoldWithLimit(projection, pending, start, history.WindowLimit)
}

// FoldWithLimit is Fold with a configurable message limit for the window.
func FoldWithLimit(projection []history.TranscriptMessage, pending *history.TranscriptMessage, start int, limit int) Window {
	if limit <= 0 {
		limit = history.WindowLimit
	}
	if start < 0 {
		start = max(0, len(projection)-limit)
	}
	if maxLimit := conversation.MaxBeforeAfter * 2; len(projection)-start > maxLimit {
		start = len(projection) - maxLimit
	}
	start = min(start, len(projection))
	messages := projection[start:]
	items := conversation.ItemsFromTranscript(messages)
	if pending != nil {
		items = append(items, conversation.ProvisionalItem(*pending))
	}
	turns := conversation.DeriveTimeline(items)
	if pending != nil && len(turns) > 0 {
		turns[len(turns)-1].Live = true
	}
	return Window{
		Turns:         turns,
		Model:         LastModel(items),
		ContextTokens: LastContext(messages),
		Start:         start,
		HasOlder:      start > 0 && len(messages) < MaxAccumulated,
		Messages:      len(messages),
	}
}

// LastModel picks the last model the window's items recorded.
func LastModel(items []conversation.ConversationItem) string {
	model := ""
	for _, item := range items {
		if item.Model != nil && *item.Model != "" {
			model = *item.Model
		}
	}
	return model
}

// LastContext picks the latest provider-recorded context size.
func LastContext(messages []history.TranscriptMessage) *int64 {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].ContextTokens != nil {
			return messages[i].ContextTokens
		}
	}
	return nil
}
