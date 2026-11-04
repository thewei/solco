/**
 * [INPUT]: apphook.ChatStreamEvent, Shell's live OMP transcript binding and chatstream.Buffer
 * [OUTPUT]: handleAppChatStream: exact Pane/Provider/Session fence and bounded, provisional Chat Bridge updates
 * [POS]: nativeui OMP live event projection; original Provider JSONL remains the only committed history
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"path/filepath"

	"github.com/thewei/solco/internal/chatstream"
	"github.com/thewei/solco/internal/history"
)

// handleAppChatStream runs only on the UI lane. The extension can claim an
// arbitrary session identity; it never creates a Chat bind or changes Herdr
// lifecycle. Only the *already bound*, exact OMP pane/session may see it.
func (s *Shell) handleAppChatStream(event chatstream.Event) {
	if event.Validate() != nil || s.chatReadOnly || s.chatLiveProvider != history.AgentOMP ||
		s.chatPaneID == "" || event.PaneID != s.chatPaneID || s.chatLivePath == "" ||
		filepath.Clean(event.SessionPath) != filepath.Clean(s.chatLivePath) {
		return
	}
	if s.chatStream == nil {
		s.chatStream = &chatstream.Buffer{}
	}
	if !s.chatStream.Apply(event) {
		return
	}
	s.publishChatStreamDelta()
	// The provider JSONL is still the authority: after message_end, nudge
	// the live file pump for early convergence, never force a full rescan.
	if event.Kind == "message_end" {
		s.wakeLiveTail()
	}
}
