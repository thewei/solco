/**
 * [INPUT]: apphook.ChatStreamEvent, Shell's live OMP transcript binding and chatstream.Buffer
 * [OUTPUT]: handleAppChatStream：先将 OMP 原生事件送给同进程 Remote Host 的独立有界流投影，再对桌面 Chat Bridge 做精确 Pane/Provider/Session 围栏与临时更新
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
	// The phone follows any Herdr-owned OMP Pane, including panes not open in
	// this desktop window. Its Host applies its own exact Provider/Pane/JSONL
	// identity fence before displaying a provisional turn.
	if s.remote.srv != nil {
		s.remote.srv.IngestOMPChatStream(event)
	}
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
	if s.chatStream.Desynced {
		// A dropped token invalidates the temporary text. Ask the canonical
		// JSONL watcher to catch up rather than showing a corrupt suffix.
		s.wakeLiveTail()
	}
	// The provider JSONL is still the authority: after message_end, nudge
	// the live file pump for early convergence, never force a full rescan.
	if event.Kind == "message_end" {
		s.wakeLiveTail()
	}
}
