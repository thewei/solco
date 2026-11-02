/**
 * [INPUT]: 依赖 agent.AgentKey, herdr.Agent, history.ResolveLiveSource, history.NewLivePump, chatsession.Fold
 * [OUTPUT]: 对外提供 Shell.openChat, Shell.bindLiveAgent, Shell.bindLiveFile, Shell.stopLiveTail, Shell.applyLiveSync, Shell.wakeLiveTail
 * [POS]: Chat 增量尾部绑定与实时数据流调度器，负责活体 Agent 转录与 Shell 聊天视图的生命周期同步
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

// liveChatBackstop is the production live-tail timer backstop (0.6 §7.4):
// projection reconciles send the event wake; the timer only catches missed
// events. No high-frequency polling anywhere.
const liveChatBackstop = 2 * time.Second

// agentSessionIdentity returns the measured provider session identity of one
// live Agent from the current Herdr projection, failing closed when the
// agent is gone or its identity does not name a known provider.
func (s *Shell) agentSessionIdentity(key agent.AgentKey) (history.AgentID, *herdr.AgentSessionIdentity) {
	for _, runtimeAgent := range s.projection.Agents {
		if runtimeAgent.TerminalID != key.TerminalID || runtimeAgent.AgentSession == nil {
			continue
		}
		provider, ok := history.ParseAgentID(runtimeAgent.AgentSession.Agent)
		if !ok {
			return "", nil
		}
		return provider, runtimeAgent.AgentSession
	}
	return "", nil
}

// openChat routes to the Chat surface bound to one card through the single
// bind path: live agents stream their provider transcript tail; history
// conversations read through HistoryService. It activates WorkspaceSurfaceChat
// inside the workspace for a seamless in-situ experience and records the
// pane-surface memory (Chat on success, Terminal on the fallback).
func (s *Shell) openChat(card agent.AgentCardModel) {
	if s.holdCommitNavigation() {
		return
	}
	s.chatPaneID = card.PaneID
	if card.PaneID != "" {
		s.selectPane(card.PaneID)
		s.rememberPaneSurface(card.PaneID, WorkspaceSurfaceChat)
	}
	s.checkTerminalDialog()
	if s.bindLiveAgent(card.Key) {
		if s.router.Path() != routeWorkspace {
			s.router.Push(routeWorkspace)
		}
		s.surface.openChat(s.workspaceContext())
		return
	}
	if card.ConversationID != "" {
		s.bindChatConversation(card.ConversationID)
		if s.router.Path() != routeWorkspace {
			s.router.Push(routeWorkspace)
		}
		s.surface.openChat(s.workspaceContext())
		return
	}
	// A fresh agent whose session file is not written yet opens Chat on its
	// own invitation: the first prompt is what creates the session.
	if s.liveSourcePending(card.Key) {
		if s.router.Path() != routeWorkspace {
			s.router.Push(routeWorkspace)
		}
		s.surface.openChat(s.workspaceContext())
		s.rebindWorkspaceChat()
		return
	}
	// No live session identity and no history conversation: navigating to
	// /chat would render its empty state behind the user's back (2026-10-06
	// F58). Land on the agent's Terminal with a hint instead.
	s.status = "No chat source for this agent yet — opened its Terminal."
	s.pendingToast = s.status // the status strip only renders on the empty-terminal branch
	if card.PaneID != "" {
		s.rememberPaneSurface(card.PaneID, WorkspaceSurfaceTerminal)
	}
	if s.router.Path() != routeWorkspace {
		s.router.Push(routeWorkspace)
	}
}

// bindLiveAgent streams one live Agent's provider-owned transcript into the
// Chat timeline (CONV-08, CONV-11..19): the measured session identity
// resolves to the exact source file and the incremental decoder pump owns
// the tail until the conversation switches. It reports whether the live
// binding took over; unresolvable or unsupported identities fall back to the
// history bind path instead of guessing a source.
func (s *Shell) bindLiveAgent(key agent.AgentKey) bool {
	provider, identity := s.agentSessionIdentity(key)
	if provider == "" || identity == nil {
		return false
	}
	fingerprint := conversation.SessionIdentity{
		Provider: string(provider),
		Kind:     identity.Kind,
		Source:   identity.Source,
		Value:    identity.Value,
	}.Fingerprint()
	if s.chatLiveCancel != nil && s.chatLiveFingerprint == fingerprint {
		// Already bound to this exact session; the pane rule still decides
		// whether the pending submission survives the rebind (0.6 §12).
		s.retainPendingEchoForPane(s.chatPaneID)
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	path, err := history.ResolveLiveSource(home, provider, identity.Kind, identity.Source, identity.Value)
	if err != nil {
		return false
	}
	// A fresh omp/pi reports the session file it is about to write; the file
	// appears with the first prompt. Binding a tail to a file that is not
	// there would wait on it forever (Loading conversation…), so the pane
	// stays sourceless until it exists.
	if _, err := os.Stat(path); err != nil {
		return false
	}
	return s.bindLiveFile(key, provider, path, fingerprint)
}

// liveSourcePending reports an agent that named its session transcript but
// has not written it yet (a fresh session before the first prompt).
func (s *Shell) liveSourcePending(key agent.AgentKey) bool {
	provider, identity := s.agentSessionIdentity(key)
	if provider == "" || identity == nil {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	path, err := history.ResolveLiveSource(home, provider, identity.Kind, identity.Source, identity.Value)
	if err != nil {
		// A source that cannot even be located yet (Cursor's chat store
		// appears with the first prompt) is pending, not absent for good.
		return errors.Is(err, fs.ErrNotExist)
	}
	_, err = os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// liveConversationKey is the conversation id a live bind carries
// ("live:<provider>:<terminal>"); the drawer ask matches against it.
func liveConversationKey(provider history.AgentID, terminalID string) string {
	return "live:" + string(provider) + ":" + terminalID
}

// bindLiveFile starts the incremental tail of one provider transcript file
// for the card's pane. bindLiveAgent resolves the file from the measured
// session identity; linkChatSession (chat_link.go) hands it a file the user
// confirmed — the one tail path either way.
func (s *Shell) bindLiveFile(key agent.AgentKey, provider history.AgentID, path, fingerprint string) bool {
	decoder, err := history.NewLiveDecoder(provider)
	if err != nil {
		return false
	}

	s.stopLiveTail()
	if s.chatOpenCancel != nil {
		s.chatOpenCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	generation := s.chatGen.Add(1)
	wake := make(chan struct{}, 1)
	backstop := liveChatBackstop
	if s.chatLiveBackstopOverride > 0 {
		backstop = s.chatLiveBackstopOverride
	}
	pump := history.NewLivePump(history.NewLiveWatcher(history.NewLiveSource(provider, path), decoder), wake)
	pump.Backstop = backstop
	pump.OnSync = func(sync history.LiveSync, syncErr error) {
		if syncErr != nil || ctx.Err() != nil || !sync.HasUpdates() {
			return
		}
		s.applyOnWindow(func() {
			if ctx.Err() != nil || generation != s.chatGen.Load() {
				return
			}
			s.applyLiveSync(decoder)
		})
	}
	s.chatLiveCancel = cancel
	s.chatLiveWake = wake
	s.chatLivePump = pump
	s.chatLiveFingerprint = fingerprint
	s.chatLivePath = path
	s.chatLiveProvider = provider
	s.clearChatSubagents()
	s.chatConversationID = liveConversationKey(provider, key.TerminalID)
	if strings.HasPrefix(s.chatOutcome, "Cannot queue") {
		// The unlinked-window refusal is moot once the transcript is bound.
		s.chatOutcome = ""
	}
	s.chatLoading = true
	s.chatTurns = nil
	s.chatWindowMessages = nil
	s.chatWindowStart = 0
	s.chatHasOlder = false
	s.chatLiveStart = -1
	s.chatLiveDecoder = decoder
	s.chatContextTokens = nil
	s.chatModel = ""
	// A same-pane rebind preserves the pending submission (0.6 §12); any
	// other pane's rebind dropped it at openChat's pane assignment.
	s.retainPendingEchoForPane(s.chatPaneID)
	s.appendPendingEchoTurn()
	s.consumeSubagentDrawerAsk()
	s.publishChatBridge()
	// The first hydration runs on the bind lane (synchronous in headless
	// tests, window-marshalled in the app); the pump goroutine only reacts
	// to later wakes and the backstop.
	pump.PumpOnce()
	go pump.Run(ctx)
	return true
}

// wakeLiveTail nudges the bound live pump after a projection change (the
// event wake of 0.6 §7.4; the timer backstop stays the safety net).
// Non-blocking by contract: the UI lane never waits on the pump.
func (s *Shell) wakeLiveTail() {
	if s.chatLiveWake == nil {
		return
	}
	select {
	case s.chatLiveWake <- struct{}{}:
	default:
	}
}

// liveFingerprintForPane resolves the current occupant's session
// fingerprint for one pane — the enqueue-time identity coordinate the
// delivery worker revalidates against (0.6 §11.3).
func (s *Shell) liveFingerprintForPane(paneID string) string {
	for _, runtimeAgent := range s.projection.Agents {
		if runtimeAgent.PaneID != paneID || runtimeAgent.AgentSession == nil {
			continue
		}
		provider, ok := history.ParseAgentID(runtimeAgent.AgentSession.Agent)
		if !ok {
			return ""
		}
		return conversation.SessionIdentity{
			Provider: string(provider),
			Kind:     runtimeAgent.AgentSession.Kind,
			Source:   runtimeAgent.AgentSession.Source,
			Value:    runtimeAgent.AgentSession.Value,
		}.Fingerprint()
	}
	return ""
}

// stopLiveTail cancels a bound live pump and clears its binding facts.
func (s *Shell) stopLiveTail() {
	if s.chatLiveCancel != nil {
		s.chatLiveCancel()
		s.chatLiveCancel = nil
	}
	s.chatLiveWake = nil
	s.chatLivePump = nil
	s.chatLiveFingerprint = ""
	s.chatLivePath = ""
	s.chatLiveProvider = ""
	s.clearChatSubagents()
	s.chatLiveCommitted = 0
	s.chatLiveDecoder = nil
	s.chatModel = ""
	s.chatContextTokens = nil
}

// retainPendingEchoForPane keeps a pending submission across a rebind only
// when the identity pins the same pane (0.6 §12, the History Composer
// continuation case); any other rebind drops the unrelated echo.
func (s *Shell) retainPendingEchoForPane(paneID string) {
	if s.chatPendingEcho != nil && s.chatPendingEcho.PaneID != paneID {
		s.chatPendingEcho = nil
	}
}

// applyLiveSync refolds the bounded live tail window into chat turns: the
// pending echo is reconciled against the committed projection first (0.6
// §12), then the windowed projection is mapped through the shared timeline
// derivation, the provisional streaming tail renders uncommitted, and an
// unconsumed pending submission renders as the tail row.
func (s *Shell) applyLiveSync(decoder *history.LiveDecoder) {
	s.chatLoading = false
	projection := decoder.Projection()
	s.chatLiveCommitted = len(projection)
	if echo, consumed := conversation.ReconcilePendingEcho(projection, s.chatPendingEcho); consumed {
		s.chatPendingEcho = echo
	}
	// The same window a history-bound chat opens with (chatsession.Fold): the
	// newest page, earlier ones behind "Earlier messages"; the start is fixed
	// once chosen (new messages extend it) until it outgrows the render bound.
	window := chatsession.Fold(projection, decoder.PendingTail(), s.chatLiveStart)
	s.chatLiveStart = window.Start
	s.chatWindowStart = window.Start
	s.chatHasOlder = window.HasOlder
	s.chatTurns = window.Turns
	s.chatModel = window.Model
	s.chatContextTokens = window.ContextTokens
	s.appendPendingEchoTurn()
	s.reconcileChatQuestion()
	s.publishChatBridge()
	s.pokeChatSubagents()
}

// chatLiveLoadOlder pulls the previous page of a live-followed chat out of
// the decoder's own projection (it already holds the whole transcript, so no
// history read is needed) and refolds the window.
func (s *Shell) chatLiveLoadOlder() {
	if s.chatLiveDecoder == nil || s.chatLiveStart <= 0 {
		return
	}
	s.chatLiveStart = max(0, s.chatLiveStart-chatOlderPage)
	s.applyLiveSync(s.chatLiveDecoder)
}

// chatPaneSourceless reports a bound Agent pane that has no chat source yet
// (a fresh agent before its first prompt).
func (s *Shell) chatPaneSourceless() bool {
	return s.chatPaneID != "" && s.chatSourcelessPaneID == s.chatPaneID && s.chatConversationID == ""
}

// appendPendingEchoTurn renders the unconsumed pending submission as the
// timeline tail; once consumed, the committed provider row is the only
// visible User row — never a duplicate.
func (s *Shell) appendPendingEchoTurn() {
	if s.chatPendingEcho == nil {
		return
	}
	s.chatTurns = append(s.chatTurns, conversation.TimelineTurn{
		UserRow: true,
		Text:    s.chatPendingEcho.Text,
	})
}
