package nativeui

import (
	"context"
	"errors"

	"github.com/thewei/solco/internal/agentcatalog"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 绑定会话的 context tokens（转录）、会话文件的 history.ReadUsage、pi/omp 的模型表窗口
 * [OUTPUT]: ChatBridge.ContextUsage——上下文卡片的数据（已用 / 窗口 / 四项 token / 缓存命中率）
 * [POS]: Chat 输入区上下文环弹出卡片的只读数据源（designs/chat-context 方案 B）；卡片打开时现读一次，不轮询、不落盘、不估费用
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// ChatContextUsage is what the context card shows. Window is 0 when the
// agent states none (Claude Code, and Codex before its first report): the
// page then draws no bar and no percentage, only the used count.
type ChatContextUsage struct {
	// Used is the provider-recorded size of the current request context.
	Used   int64 `json:"used"`
	Window int64 `json:"window"`
	// The session's token totals (history.ReadUsage); all 0 when the source
	// is unsupported or unreadable.
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	// CacheHitRate is cache read over every input token the model was billed
	// for the session (input + cache read + cache write), in percent; nil
	// when no input was recorded.
	CacheHitRate *float64 `json:"cacheHitRate,omitempty"`
}

// buildContextUsage assembles the card from its three sources. A window the
// log itself states wins over the model listing's.
func buildContextUsage(used *int64, usage history.SessionUsage, listed int64) ChatContextUsage {
	out := ChatContextUsage{
		Input: usage.Input, Output: usage.Output, CacheRead: usage.CacheRead, CacheWrite: usage.CacheWrite,
		Window: listed,
	}
	if used != nil {
		out.Used = *used
	}
	if usage.Window > 0 {
		out.Window = usage.Window
	}
	if input := usage.Input + usage.CacheRead + usage.CacheWrite; input > 0 {
		rate := float64(usage.CacheRead) / float64(input) * 100
		out.CacheHitRate = &rate
	}
	return out
}

// ContextUsage reads the bound live session's usage once (the card's open)
// and returns it with the window the agent states. Read-only; a source
// without usage yields the used count alone.
func (b *ChatBridge) ContextUsage(ctx context.Context) (ChatContextUsage, error) {
	var (
		provider history.AgentID
		path     string
		model    string
		used     *int64
	)
	b.shell.callOnWindow(func() {
		s := b.shell
		if s.chatReadOnly {
			return
		}
		if card, ok := s.agentCardForPane(s.chatPaneID); ok {
			provider = card.Provider
		}
		path, model, used = s.chatLivePath, s.chatModel, s.chatContextTokens
	})
	var usage history.SessionUsage
	if provider != "" && path != "" {
		read, err := history.ReadUsage(ctx, provider, path)
		if err != nil && !errors.Is(err, history.ErrNoUsage) {
			return buildContextUsage(used, history.SessionUsage{}, 0), nil
		}
		usage = read
	}
	return buildContextUsage(used, usage, agentcatalog.ModelWindow(ctx, provider, model)), nil
}
