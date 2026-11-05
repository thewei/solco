/**
 * [INPUT]: apphook.ChatStreamEvent, Shell's live OMP/Pi transcript binding and chatstream.Buffer
 * [OUTPUT]: handleAppChatStream：先将 OMP 原生事件送给同进程 Remote Host 的独立有界流投影，再对桌面 Chat Bridge 做精确 Pane/Provider/Session 围栏与临时更新
 * [POS]: nativeui OMP/Pi live event projection; original Provider JSONL remains the only committed history
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"os"
	"path/filepath"
	"time"

	"github.com/thewei/solco/internal/herdr"

	"github.com/thewei/solco/internal/chatstream"
	"github.com/thewei/solco/internal/history"
)

// chatRunFence tracks only the last assistant message identity and run-end
// sequence. It intentionally does not retain token text or an Agent runtime.
type chatRunFence struct {
	epoch, messageID string
	seq              uint64
	completed        bool
	seen             time.Time
}

const maxChatRunFences = 48

func (s *Shell) streamRunMatchesPane(e chatstream.Event) bool {
	agent := s.paneAgent(e.PaneID)
	if agent == nil {
		return false
	}
	provider, ok := history.ParseAgentID(agent.Kind)
	if !ok || string(provider) != e.Agent {
		return false
	}
	if identity := agent.AgentSession; identity != nil {
		declared, known := history.ParseAgentID(identity.Agent)
		if known && declared == provider {
			home, err := os.UserHomeDir()
			if err == nil {
				path, err := history.ResolveLiveSource(home, provider, identity.Kind, identity.Source, identity.Value)
				if err == nil && filepath.Clean(path) == filepath.Clean(e.SessionPath) {
					return true
				}
			}
		}
	}
	// A visible live Chat has already been identity-bound by the exact
	// transcript resolver. This also covers the brief moment when the
	// native Herdr agent_session projection has not been refreshed yet.
	return !s.chatReadOnly && e.PaneID == s.chatPaneID &&
		string(s.chatLiveProvider) == e.Agent && s.chatLivePath != "" &&
		filepath.Clean(s.chatLivePath) == filepath.Clean(e.SessionPath)
}

func (s *Shell) observeChatRunStatus(e chatstream.Event) {
	if (e.Kind != "message_start" && e.Kind != "agent_end") || s.statusStore == nil || !s.streamRunMatchesPane(e) {
		return
	}
	key := e.Agent + "\x00" + e.PaneID + "\x00" + filepath.Clean(e.SessionPath)
	if s.chatRunFences == nil {
		s.chatRunFences = make(map[string]chatRunFence)
	}
	old, exists := s.chatRunFences[key]
	if exists && (e.Epoch < old.epoch || (e.Epoch == old.epoch && e.Seq <= old.seq)) {
		return
	}
	if e.Kind == "message_start" {
		s.chatRunFences[key] = chatRunFence{epoch: e.Epoch, messageID: e.MessageID, seq: e.Seq, seen: time.Now()}
		if len(s.chatRunFences) > maxChatRunFences {
			var earliest string
			for k, v := range s.chatRunFences {
				if earliest == "" || v.seen.Before(s.chatRunFences[earliest].seen) {
					earliest = k
				}
			}
			delete(s.chatRunFences, earliest)
		}
		if effective, applied := s.statusStore.ApplySolcoHookStatus(e.PaneID, "working", time.Now()); applied {
			s.upsertProjectionAgent(e.PaneID, func(agent *herdr.Agent) { agent.Status = effective })
			s.applyPaneStatus(e.PaneID, effective)
		}
		return
	}
	// No guessed end on message_end, no end without this process's prior
	// message_start, no duplicate or delayed completion for another run.
	if !exists || old.completed || e.Epoch != old.epoch || e.MessageID != old.messageID {
		return
	}
	old.completed, old.seq, old.seen = true, e.Seq, time.Now()
	s.chatRunFences[key] = old
	effective := s.statusStore.ApplyVerifiedAgentEnd(e.PaneID)
	s.upsertProjectionAgent(e.PaneID, func(agent *herdr.Agent) { agent.Status = effective })
	s.applyPaneStatus(e.PaneID, effective)
}

// handleAppChatStream runs on the UI lane. Agent lifecycle events are
// observed for every identity-verified Pane; text overlays only the one
// exact currently bound Chat conversation.
func (s *Shell) handleAppChatStream(event chatstream.Event) {
	if event.Validate() != nil {
		return
	}
	s.observeChatRunStatus(event)
	// Mobile streaming is dispatched from AppHook's own socket lane, not
	// here: this callback only overlays text in the visible desktop Chat.
	if s.chatReadOnly || (s.chatLiveProvider != history.AgentOMP && s.chatLiveProvider != history.AgentPi) ||
		event.Agent != string(s.chatLiveProvider) ||
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
	if event.Kind == "message_end" || event.Kind == "agent_end" {
		s.wakeLiveTail()
	}
}
