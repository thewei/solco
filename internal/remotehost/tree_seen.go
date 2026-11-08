package remotehost

import (
	"time"

	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: 依赖 herdr.Projection、Options.Now、Server.mu
 * [OUTPUT]: Server.stampAgents（每个 Herdr session 里「正跑着 Agent 的终端」→ 本 Host 第一次看到它的时刻）
 * [POS]: 手机 Chats 对「还没跑过一轮的会话」的唯一时钟（TreeAgent.FirstSeenMs，2026-11-07）：桌面没有会话文件/转换可给时 LastActive 为零，只有 Host 自己知道它是什么时候出现的；只在内存、不落盘，Host 重启后重新计时
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// stampAgents returns, for one Herdr session, when this Host first saw each terminal that runs an agent. A terminal
// that stops running one is forgotten, so the next agent there stamps afresh. It runs only after a successful
// projection read, so a failed read never makes the Host forget. The returned map is never mutated afterwards.
func (s *Server) stampAgents(instance string, p herdr.Projection) map[string]time.Time {
	now := s.opts.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.seen[instance]
	next := make(map[string]time.Time, len(prev)+1)
	for _, pane := range p.Panes {
		if pane.Agent == "" || pane.TerminalID == "" {
			continue
		}
		if at, ok := prev[pane.TerminalID]; ok {
			next[pane.TerminalID] = at
		} else {
			next[pane.TerminalID] = now
		}
	}
	s.seen[instance] = next
	return next
}
