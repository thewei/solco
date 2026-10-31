package nativeui

/**
 * [INPUT]: 依赖 HistoryListView.List（只读会话目录）、Shell 的 agentCardForPane/agentCWD/chatPaneSourceless、bindLiveFile
 * [OUTPUT]: 对外提供 chatLinkCandidates（空窗 Agent 的候选会话）、linkChatSession（用户确认后才绑定）、chatSessionLinked
 * [POS]: Chat 会话绑定自愈（2026-10-31）：Agent 已有会话但 Herdr/hook 没报告身份时，页面只“提议”同目录同 Provider 的最近会话，用户点了才跟读；
 *        绝不静默绑定，测得身份一到就让位。只读 history 目录，不发 Herdr 方法。
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"strings"
	"time"

	"github.com/thewei/solco/internal/history"
)

const (
	// linkedFingerprintPrefix marks a live tail the user chose by hand, as
	// opposed to one resolved from the agent's measured session identity.
	linkedFingerprintPrefix = "linked:"
	// linkCandidateWindow bounds how old a suggested session may be.
	linkCandidateWindow = 24 * time.Hour
	maxLinkCandidates   = 3
)

// chatSessionLinked reports a live tail the user linked by hand.
func (s *Shell) chatSessionLinked() bool {
	return strings.HasPrefix(s.chatLiveFingerprint, linkedFingerprintPrefix)
}

// chatLinkCandidates lists recent sessions of the pane's provider under the
// pane's folder — only while the pane is sourceless (an agent without a
// reported session identity). Newest first, at most maxLinkCandidates.
func (s *Shell) chatLinkCandidates() []history.SessionSummary {
	if !s.chatPaneSourceless() || s.chatReadOnly || s.hist.service == nil {
		return nil
	}
	card, ok := s.agentCardForPane(s.chatPaneID)
	if !ok || card.Provider == "" {
		return nil
	}
	cwd := s.agentCWD(card)
	if cwd == "" {
		return nil
	}
	cutoff := time.Now().Add(-linkCandidateWindow).UnixMilli()
	var out []history.SessionSummary
	found, err := s.hist.service.List(context.Background(), history.HistoryQuery{ProjectPath: cwd, Provider: card.Provider, Limit: 12})
	if err != nil {
		return nil
	}
	for _, summary := range found {
		meta := summary.Meta
		if meta.Archived || meta.FilePath == "" || meta.UpdatedAt < cutoff {
			continue
		}
		out = append(out, summary)
		if len(out) == maxLinkCandidates {
			break
		}
	}
	return out
}

// linkChatSession starts following the candidate's transcript file after the
// user's confirmation. The key must be one of the current candidates, so a
// stale or invented key binds nothing.
func (s *Shell) linkChatSession(key string) bool {
	for _, summary := range s.chatLinkCandidates() {
		if summary.Meta.Key != key {
			continue
		}
		card, ok := s.agentCardForPane(s.chatPaneID)
		if !ok {
			return false
		}
		return s.bindLiveFile(card.Key, card.Provider, summary.Meta.FilePath, linkedFingerprintPrefix+key)
	}
	return false
}
