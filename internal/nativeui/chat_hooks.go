package nativeui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/apphook"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/chatstream"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/herdrhooks"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 apphook（进程内 IPC Hook Server）、herdrhooks spool（可选兼容向后尾部）、runtime.ReadPane（pane.read 可见文本）、InteractionBroker
 * [OUTPUT]: 提供 Shell hook 层：直接 Hook 状态/会话更新与 OMP Chat 事件分流、blocked 触发的交互检测 → 结构化 PendingInteraction、非 blocked 的 pane 级取消、实时投影重推
 * [POS]: Solco 自有 Unix Socket Hook 优先级高于 Herdr spool fallback；两个来源显式分支再汇入 agentstatus 唯一状态仲裁器，Herdr 仍掌管 runtime
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// AttachHookServer starts direct hook ingestion and its legacy spool fallback.
// A resident Host may still be surrendering the primary socket when the GUI
// starts; we retry *only* the direct listener after the Host lease is claimed.
func (s *Shell) AttachHookServer() {
	s.ensureAppHookServer()
	s.AttachHookSpool()
}

// ensureAppHookServer is safe to call after a resident Host yields its lease.
// Never attach the legacy spool twice: its lifecycle is independent.
func (s *Shell) ensureAppHookServer() {
	if s.hookServer == nil {
		server := apphook.NewServer(apphook.DefaultSocketPath(), apphook.Handler{
			OnStatus: func(evt apphook.StatusEvent) {
				s.applyOnWindow(func() { s.handleAppHookStatus(evt) })
			},
			OnSession: func(evt apphook.SessionEvent) {
				s.applyOnWindow(func() { s.handleAppHookSession(evt) })
			},
			OnChatStream: func(evt chatstream.Event) {
				// Remote Host owns a concurrency-safe, bounded stream projection.
				// Fan out from the IPC lane, not the MyGo UI lane: a busy
				// window must not delay the phone's Token updates.
				if host := s.remoteChatHost.Load(); host != nil {
					host.IngestOMPChatStream(evt)
				}
				s.applyOnWindow(func() { s.handleAppChatStream(evt) })
			},
		})
		if runtimeSock := apphook.DefaultRuntimeSocketPath(); runtimeSock != "" {
			server.SetSymlinkPath(runtimeSock)
		}
		if err := server.Start(); err != nil {
			slog.Warn("apphook server start failed", "error", err)
		} else {
			s.hookServer = server
		}
	}
}

// AttachHookSpool starts the hook spool tailer (production only: the spool
// exists when the Solco herdr plugin is linked). Hook events reconcile
// the interaction lifecycle and republish the chat projection. The tailer
// reads only NEW lines; agents already blocked at attach get a detection
// pass here (restart mid-prompt) — the authoritative pass runs once the
// first projection lands (applyProjection), because at attach time the
// bootstrap has not fetched any agents yet.
func (s *Shell) AttachHookSpool() {
	stop, err := herdrhooks.Start(herdrhooks.DefaultSpoolPath(), func(event herdrhooks.Event) {
		s.applyOnWindow(func() { s.handleHookEvent(event) })
	})
	if err != nil {
		slog.Warn("hook spool attach failed", "error", err)
		return
	}
	slog.Info("hook spool tailer attached", "spool", herdrhooks.DefaultSpoolPath())
	s.hooksStop = stop
	s.dispatch(func() { s.reconcileBlockedInteractions() })
}

// reconcileBlockedInteractions runs one detection pass over every agent the
// current projection reports as blocked. Idempotent: panes with a pending
// interaction are skipped, so repeated applies never duplicate cards.
func (s *Shell) reconcileBlockedInteractions() {
	// A card whose agent has moved on (answered in the terminal, finished,
	// gone) must retire: only the OSC 7501 path cancelled before, so agents
	// reporting through Herdr hooks left a stale question behind.
	for _, pending := range s.interactionBroker.Pending() {
		if pending.Picker || pending.ParkedWorking {
			continue // retired by watchPicker / the transcript, not by the agent's status
		}
		if agent := s.paneAgent(pending.AgentPaneID); agent == nil || agent.Status != "blocked" {
			s.interactionBroker.CancelForPane(pending.AgentPaneID)
		}
	}
	for index := range s.projection.Agents {
		agent := &s.projection.Agents[index]
		if agent.Status != "blocked" || agent.PaneID == "" {
			continue
		}
		if !s.shouldProbeBlockedPane(agent.PaneID) {
			continue
		}
		paneID := agent.PaneID
		s.dispatch(func() { s.detectPaneInteraction(paneID) })
	}
	s.checkTerminalDialog()
}

// interactionMissHold is how long a pane whose screen showed no prompt shape
// is left alone by the projection-driven pass. Without it every Herdr event
// re-read the screen of each blocked agent that merely waits on something
// Chat cannot structure (an RPC and a log line per event).
const interactionMissHold = 3 * time.Second

// paneMisses remembers, per pane, when a screen read last found no prompt.
// dispatch runs detection on goroutines, so it carries its own lock.
type paneMisses struct {
	mu sync.Mutex
	at map[string]time.Time
}

func (m *paneMisses) recent(pane string, now time.Time, within time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	at, ok := m.at[pane]
	return ok && now.Sub(at) < within
}

func (m *paneMisses) miss(pane string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.at == nil {
		m.at = map[string]time.Time{}
	}
	m.at[pane] = now
}

func (m *paneMisses) clear(pane string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.at, pane)
}

// shouldProbeBlockedPane reports whether the projection-driven pass should
// read this blocked pane's screen: not while a card is already pending for
// it, and not right after a read that found no prompt.
func (s *Shell) shouldProbeBlockedPane(paneID string) bool {
	if len(s.interactionBroker.PendingForPane(paneID)) > 0 {
		return false
	}
	return !s.interactionMisses.recent(paneID, timeNow(), interactionMissHold)
}

// handleAppHookStatus applies a directly reported agent status event onto
// the shell projection and interaction broker (UI lane).
// upsertProjectionAgent locates the projection agent bound to a pane, or
// creates one from the pane's row when the hook event arrived first; the
// mutation applies in place and always bumps the revision. The two app-hook
// handlers used to inline this locate-or-create twice (F171).
func (s *Shell) upsertProjectionAgent(paneID string, mutate func(*herdr.Agent)) {
	// An agent report is proof of life: the pane cannot be sitting at its
	// bare shell prompt anymore, so the scan's idle measurement is stale.
	delete(s.paneShellIdle, paneID)
	for i := range s.projection.Agents {
		agent := &s.projection.Agents[i]
		if agent.PaneID == paneID {
			mutate(agent)
			agent.Revision++
			return
		}
	}
	var termID, projID, tabID string
	for _, p := range s.projection.Panes {
		if p.ID == paneID {
			termID = p.TerminalID
			projID = p.ProjectID
			tabID = p.TabID
			break
		}
	}
	created := herdr.Agent{
		TerminalID: termID,
		ProjectID:  projID,
		TabID:      tabID,
		PaneID:     paneID,
		Revision:   1,
	}
	mutate(&created)
	s.projection.Agents = append(s.projection.Agents, created)
}

// chatSessionIdentity builds the apphook-sourced session identity carried
// by status and session events.
func chatSessionIdentity(appAgent, sessionID string) *herdr.AgentSessionIdentity {
	if sessionID == "" {
		return nil
	}
	return &herdr.AgentSessionIdentity{
		Agent:  appAgent,
		Kind:   "id",
		Source: "apphook",
		Value:  sessionID,
	}
}

// hookIdentity is a hook-reported session identity with the terminal it was
// reported for.
type hookIdentity struct {
	identity   herdr.AgentSessionIdentity
	terminalID string
}

func (s *Shell) hookKey(paneID string) string { return s.activeInstance + "/" + paneID }

// rememberHookIdentity keeps the newest hook-reported session identity of a
// pane (call after the projection agent exists). Herdr's agent list carries
// no session for some providers (Claude Code), so every projection refresh
// would otherwise erase the only thing that binds Chat to the transcript;
// applyProjection re-stamps it and the session file persists it.
func (s *Shell) rememberHookIdentity(paneID string, identity *herdr.AgentSessionIdentity) {
	if s.hookIdentities == nil {
		s.hookIdentities = map[string]hookIdentity{}
	}
	entry := hookIdentity{identity: *identity}
	if agent := s.paneAgent(paneID); agent != nil {
		entry.terminalID = agent.TerminalID
	}
	s.hookIdentities[s.hookKey(paneID)] = entry
}

// restampHookIdentities gives agents the refreshed projection reports
// without a session identity the one their hooks announced, as long as it
// names the same provider and the same terminal (a pane reused by another
// agent keeps nothing).
func (s *Shell) restampHookIdentities() {
	for i := range s.projection.Agents {
		agent := &s.projection.Agents[i]
		entry, found := s.hookIdentities[s.hookKey(agent.PaneID)]
		if !found || agent.AgentSession != nil {
			continue
		}
		if entry.terminalID != "" && agent.TerminalID != "" && entry.terminalID != agent.TerminalID {
			continue
		}
		have, ok := history.ParseAgentID(agent.Kind)
		want, wantOK := history.ParseAgentID(entry.identity.Agent)
		if ok && wantOK && have == want {
			identity := entry.identity
			agent.AgentSession = &identity
		}
	}
}

func (s *Shell) handleAppHookStatus(evt apphook.StatusEvent) {
	if evt.PaneID == "" {
		return
	}
	slog.Debug("hook status event", "pane", evt.PaneID, "status", evt.Status, "agent", evt.Agent)

	// The status lane is arbitrated per pane (OSC 7501 vs hooks); the
	// session identity riding the same event is hook-only and always
	// passes, even when the status is yielded to the protocol stream.
	effective, applied := s.statusStore.ApplySolcoHookStatus(evt.PaneID, evt.Status, time.Now())
	if applied {
		slog.Debug("hook status applied", "pane", evt.PaneID, "status", effective)
	} else {
		slog.Debug("hook status yielded to osc7501", "pane", evt.PaneID, "hook", evt.Status, "effective", effective)
	}

	// Direct update to the in-memory projection (locate-or-create in one
	// place; see upsertProjectionAgent).
	session := chatSessionIdentity(evt.Agent, evt.SessionID)
	s.upsertProjectionAgent(evt.PaneID, func(agent *herdr.Agent) {
		if applied {
			agent.Status = effective
		}
		if agent.Kind == "" && evt.Agent != "" {
			agent.Kind = evt.Agent
		}
		if agent.AgentSession == nil {
			agent.AgentSession = session
		}
	})
	if session != nil {
		previous, hadPrevious := s.hookIdentities[s.hookKey(evt.PaneID)]
		s.rememberHookIdentity(evt.PaneID, session)
		if !hadPrevious || previous.identity.Value != session.Value {
			s.refreshUsageProjection(true)
		}
	}
	if evt.Text != "" {
		if a := s.paneAgent(evt.PaneID); a != nil && a.TerminalID != "" {
			k := agent.AgentKey{InstanceID: s.activeInstance, TerminalID: a.TerminalID}
			if s.agentSessionTitle[k] == "" {
				if clean := history.CleanTitleCandidate(evt.Text); clean != "" {
					if s.agentSessionTitle == nil {
						s.agentSessionTitle = make(map[agent.AgentKey]string)
					}
					s.agentSessionTitle[k] = clean
					s.reconcileWorkbench()
				}
			}
		}
	}

	if !applied {
		s.scheduleProjectionRefresh()
		return
	}
	s.applyPaneStatus(evt.PaneID, effective)
}

// handleAppHookSession updates the agent session identity directly in the
// shell projection (UI lane).
func (s *Shell) handleAppHookSession(evt apphook.SessionEvent) {
	if evt.PaneID == "" {
		return
	}
	slog.Info("hook session event", "pane", evt.PaneID, "agent", evt.Agent, "session_id", evt.AgentSessionID)

	source := evt.Source
	if source == "" {
		source = "apphook"
	}
	val := evt.AgentSessionID
	kind := "id"
	if evt.AgentSessionPath != "" {
		val = evt.AgentSessionPath
		kind = "path"
	}

	sessionIdentity := &herdr.AgentSessionIdentity{
		Agent:  evt.Agent,
		Kind:   kind,
		Source: source,
		Value:  val,
	}

	s.upsertProjectionAgent(evt.PaneID, func(agent *herdr.Agent) {
		agent.AgentSession = sessionIdentity
		if agent.Kind == "" && evt.Agent != "" {
			agent.Kind = evt.Agent
		}
		if agent.Status == "" {
			agent.Status = "idle"
		}
	})

	// Every tool call reports the same session; only a pane's new identity
	// can have a fresh transcript to index. (The projection's own identity
	// cannot tell: a status event may have stamped it first.)
	previous, hadPrevious := s.hookIdentities[s.hookKey(evt.PaneID)]
	s.rememberHookIdentity(evt.PaneID, sessionIdentity)
	if !hadPrevious || previous.identity.Value != val {
		// A fresh session identity may already have a catalog row (or gains
		// one moments later): re-resolve the History-meta projection now so
		// the Agent card title follows the session without waiting out the
		// usage lane's one-minute throttle.
		s.refreshUsageProjection(true)
		// The fresh session's transcript lands on disk moments later, and a
		// scan is the only thing that indexes it into the catalog the title
		// and chat-binding lanes read; a no-op while a scan is running, so
		// one more round follows once the first message is on disk.
		s.startHistoryScan()
		time.AfterFunc(8*time.Second, func() { s.applyOnWindow(s.startHistoryScan) })
	}
	s.publishChatBridge()
}

// handleHookEvent is the legacy spool tailer's dispatch (UI lane).
func (s *Shell) handleHookEvent(event herdrhooks.Event) {
	if event.Name != "pane.agent_status_changed" {
		return
	}
	var envelope herdrhooks.Envelope
	if err := json.Unmarshal(event.Payload, &envelope); err != nil || len(envelope.Data) == 0 {
		slog.Warn("hook event undecodable", "event", event.Name)
		return
	}
	var payload herdrhooks.StatusPayload
	if err := json.Unmarshal(envelope.Data, &payload); err != nil || payload.PaneID == "" {
		slog.Warn("hook status payload undecodable", "event", event.Name)
		return
	}
	// Herdr's forwarded spool is a fallback only. It must never enter
	// handleAppHookStatus: that is our privileged direct-hook lane and
	// carries Solco-owned session identity and workbench semantics.
	s.handleHerdrHookStatus(payload.PaneID, apphook.NormalizeStatus(payload.AgentStatus))
}

// handleHerdrHookStatus is deliberately separate from Solco's hook path:
// Herdr provides runtime facts, but its plugin Hook cannot overwrite a
// direct Solco Hook or valid native OSC Program Status source.
func (s *Shell) handleHerdrHookStatus(paneID, status string) {
	if paneID == "" {
		return
	}
	effective, applied := s.statusStore.ApplyHerdrHookStatus(paneID, status, time.Now())
	if !applied {
		return
	}
	if s.paneAgent(paneID) != nil {
		s.upsertProjectionAgent(paneID, func(agent *herdr.Agent) {
			agent.Status = effective
		})
		s.applyPaneStatus(paneID, effective)
	}
}

// chatRefreshDebounce coalesces hook-driven projection refetches; status
// transitions arrive in tight working→idle pairs and one refresh covers both.
const chatRefreshDebounce = 150 * time.Millisecond

// scheduleProjectionRefresh refetches the session snapshot off the UI lane
// and applies it when it differs from the shell's copy. This is the hook
// layer's second job: it keeps the runtime status honest even when the
// socket event watcher missed a transition (the spool is written by Herdr
// itself, so it is the authoritative fallback feed).
func (s *Shell) scheduleProjectionRefresh() {
	if s.runtime == nil || s.activeInstance == "" {
		return
	}
	s.chatRefreshMu.Lock()
	if s.chatRefreshPending {
		s.chatRefreshMu.Unlock()
		return
	}
	s.chatRefreshPending = true
	s.chatRefreshMu.Unlock()
	time.AfterFunc(chatRefreshDebounce, func() {
		instance := s.activeInstance
		projection, err := s.runtime.Projection(instance)
		s.applyOnWindow(func() {
			s.chatRefreshMu.Lock()
			s.chatRefreshPending = false
			s.chatRefreshMu.Unlock()
			if err != nil || instance != s.activeInstance {
				return
			}
			if projectionEquivalent(projection, s.projection) {
				return
			}
			s.applyProjection(projection, false)
		})
	})
}

// projectionEquivalent compares the fields the hook layer refreshes for:
// the per-agent status surface. Structural changes always apply.
func projectionEquivalent(a, b herdr.Projection) bool {
	if len(a.Agents) != len(b.Agents) || len(a.Panes) != len(b.Panes) || len(a.Tabs) != len(b.Tabs) {
		return false
	}
	for index := range a.Agents {
		if a.Agents[index].PaneID != b.Agents[index].PaneID ||
			a.Agents[index].Status != b.Agents[index].Status ||
			a.Agents[index].Revision != b.Agents[index].Revision {
			return false
		}
	}
	return true
}

// detectPaneInteraction reads the pane's visible TUI and publishes a
// structured interaction when one of the known prompt shapes is showing.
// Idempotent per pane: a pending interaction short-circuits the read.
func (s *Shell) detectPaneInteraction(paneID string) {
	if s.runtime == nil || s.activeInstance == "" {
		return
	}
	if len(s.interactionBroker.PendingForPane(paneID)) > 0 {
		return
	}
	// A question the transcript already states (omp/pi `ask`) needs no
	// screen read.
	if s.publishTranscriptQuestion(paneID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	screen, err := s.runtime.ReadPane(ctx, s.activeInstance, paneID, "visible")
	if err != nil || strings.TrimSpace(screen) == "" {
		slog.Warn("interaction detect read failed", "pane", paneID, "error", err)
		return
	}
	// Cursor Agent / Antigravity CLI shell-permission sheets carry their own
	// keys (never a persistent "always allow" row).
	if interaction, hit := chatsession.DetectAgentPrompt(screen); hit {
		s.publishInteraction(paneID, interaction)
		return
	}
	kind, prompt, options, ok := chatsession.DetectScreenPrompt(screen)
	if !ok {
		s.interactionMisses.miss(paneID, timeNow())
		slog.Debug("interaction detect found no prompt shape", "pane", paneID)
		return
	}
	s.interactionMisses.clear(paneID)
	interaction := conversation.PendingInteraction{Kind: kind, Prompt: prompt, Options: options}
	interaction.ParkedWorking = s.parkedAsk(paneID)
	if chatsession.IsOmpReviewSheet(screen) {
		interaction.EscapeOption = "Cancel"
	}
	s.publishInteraction(paneID, interaction)
}

// redetectAfterAnswer covers a screen-detected prompt whose answer leaves the
// agent blocked on a follow-up (Claude Code's tabbed AskUserQuestion moves to
// the next question, then to its Submit review): the status never changes, so
// no projection edge would look again. One delayed read re-publishes it.
func (s *Shell) redetectAfterAnswer(paneID string) {
	time.AfterFunc(1500*time.Millisecond, func() {
		s.applyOnWindow(func() {
			agent := s.paneAgent(paneID)
			switch {
			case agent == nil:
			case agent.Status == "blocked":
				s.interactionMisses.clear(paneID) // the answer changed the screen: look again at once
				s.reconcileBlockedInteractions()
			case s.parkedAsk(paneID):
				// Pi's dialog moved on to its Submit review while the host
				// still says `working`: read the screen for it.
				s.interactionMisses.clear(paneID)
				s.dispatch(func() { s.detectPaneInteraction(paneID) })
			}
		})
	})
}

// publishTranscriptQuestion publishes the chat pane's unanswered `ask` call
// as a structured question. Cheap (no pane read): the live tail also calls
// it while the pane is blocked, because the call can land in the transcript
// a beat after the blocked status. Reports whether one was published.
func (s *Shell) publishTranscriptQuestion(paneID string) bool {
	if s.chatPaneID != paneID || s.chatReadOnly || len(s.interactionBroker.PendingForPane(paneID)) > 0 {
		return false
	}
	ask, ok := chatsession.TranscriptQuestion(s.chatTurns)
	if !ok {
		return false
	}
	if s.chatAskTool != ask.ToolID {
		s.chatAskTool, s.chatAskStep, s.chatAskDone = ask.ToolID, 0, false
	}
	s.chatAskTotal = len(ask.Questions)
	if s.chatAskDone || s.chatAskStep >= len(ask.Questions) {
		return false
	}
	question := ask.Questions[s.chatAskStep]
	interaction := conversation.PendingInteraction{
		Kind:          conversation.InteractionQuestion,
		Prompt:        question.Prompt,
		Options:       question.Options,
		Header:        question.Header,
		Descriptions:  question.Descriptions,
		Start:         question.Start,
		Multi:         question.Multi,
		CommitRow:     question.CommitRow,
		ParkedWorking: ask.ParksWorking,
	}
	interaction.Step, interaction.Steps = s.chatAskStep, len(ask.Questions)
	s.publishInteraction(paneID, interaction)
	return true
}

// reconcileChatQuestion is the live tail's hook: a blocked chat pane whose
// transcript just gained its `ask` call gets the question card.
func (s *Shell) reconcileChatQuestion() {
	if s.chatPaneID == "" {
		return
	}
	parked := s.parkedAsk(s.chatPaneID)
	if !parked {
		// A card born from a parked dialog (Pi's ask_user_question) retires
		// when its call has a result — answered or declined in the terminal.
		for _, pending := range s.interactionBroker.PendingForPane(s.chatPaneID) {
			if pending.ParkedWorking {
				s.interactionBroker.CancelForPane(s.chatPaneID)
				break
			}
		}
	}
	if agent := s.paneAgent(s.chatPaneID); agent != nil && (agent.Status == "blocked" || parked) {
		s.publishTranscriptQuestion(s.chatPaneID)
	}
}

// parkedAsk reports a chat pane whose transcript ends on an unanswered
// ask_user_question call: Pi's dialog is open although its host still reads
// `working` (the extension sends no blocked signal).
func (s *Shell) parkedAsk(paneID string) bool {
	if s.chatPaneID != paneID || s.chatReadOnly {
		return false
	}
	ask, ok := chatsession.TranscriptQuestion(s.chatTurns)
	return ok && ask.ParksWorking
}

// publishInteraction stamps the pane/conversation identity on a detected
// interaction and registers it with the broker.
func (s *Shell) publishInteraction(paneID string, interaction conversation.PendingInteraction) {
	conversationID := conversation.ConversationID("pane:" + paneID)
	if s.chatPaneID == paneID && s.chatConversationID != "" {
		conversationID = conversation.ConversationID(s.chatConversationID)
	}
	interaction.ID = fmt.Sprintf("ia-%s-%d", paneID, time.Now().UnixNano())
	interaction.ConversationID = conversationID
	interaction.AgentPaneID = paneID
	interaction.Fingerprint = s.liveFingerprintForPane(paneID)
	if err := s.interactionBroker.Publish(interaction); err != nil {
		slog.Warn("interaction publish rejected", "pane", paneID, "error", err)
		return
	}
	slog.Info("interaction published", "pane", paneID, "kind", string(interaction.Kind), "options", len(interaction.Options))
	s.publishChatBridge()
}
