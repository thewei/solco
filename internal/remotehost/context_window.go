package remotehost

import (
	"context"
	"sync"
	"time"

	"github.com/thewei/solco/internal/agentcatalog"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 一个 Chat 会话的 Provider/转录路径/模型
 * [OUTPUT]: Server.chatState——chatsession.State 加上 contextWindow（上下文窗口 token 数，Agent 未声明时缺省）
 * [POS]: 手机上下文环的窗口来源；与桌面 ChatBridge.ContextUsage 同一套来源（日志自述窗口优先，pi/omp 取模型表），只多一层按会话缓存，避免每帧整读会话文件
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	contextWindowTTL     = time.Minute
	contextWindowTimeout = time.Second
	contextWindowMaxKeys = 64
)

type contextWindowKey struct {
	provider history.AgentID
	path     string
	model    string
}

type contextWindowEntry struct {
	at     time.Time
	window int64
}

type contextWindowCache struct {
	mu      sync.Mutex
	entries map[contextWindowKey]contextWindowEntry
}

// readContextWindow is the uncached read (a test seam): the window the session's own log states, else the
// model listing's. 0 = the agent states none.
var readContextWindow = func(ctx context.Context, provider history.AgentID, path, model string) int64 {
	if usage, err := history.ReadUsage(ctx, provider, path); err == nil && usage.Window > 0 {
		return usage.Window
	}
	return agentcatalog.ModelWindow(ctx, provider, model)
}

func (c *contextWindowCache) get(ctx context.Context, now time.Time, key contextWindowKey) int64 {
	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if ok && now.Sub(entry.at) < contextWindowTTL {
		return entry.window
	}
	rctx, cancel := context.WithTimeout(ctx, contextWindowTimeout)
	defer cancel()
	window := readContextWindow(rctx, key.provider, key.path, key.model)
	if rctx.Err() != nil { // a read that ran out of time answers nothing now and is tried again next time
		return entry.window
	}
	c.mu.Lock()
	if c.entries == nil || len(c.entries) >= contextWindowMaxKeys {
		c.entries = map[contextWindowKey]contextWindowEntry{}
	}
	c.entries[key] = contextWindowEntry{at: now, window: window}
	c.mu.Unlock()
	return window
}

// chatState is reader.State plus the context window; the window is read only for a conversation that
// already reports a context size, and cached for a minute per session.
func (s *Server) chatState(ctx context.Context, target chatTarget, reader *chatsession.Reader) chatsession.State {
	now := s.opts.Now()
	state := reader.State(now)
	state.Turns = s.visibleTurns(state.Turns)
	if state.ContextTokens != nil {
		state.ContextWindow = s.windows.get(ctx, now, contextWindowKey{provider: target.Provider, path: reader.Path(), model: state.Model})
	}
	return state
}
