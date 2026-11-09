/**
 * [INPUT]: 依赖 time, mygo, internal/agent, internal/herdr
 * [OUTPUT]: 对外提供 Shell.reconcileWorkbench, notifyAgentTransition, markAgentReviewed, markAgentReviewedIfPending, markAgentReviewedForPane, workbenchState
 * [POS]: nativeui 桌面工作台状态中枢，Agent 目录与运行时事件分发及状态调和
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
)

// workbenchState is the Shell's 0.5 Agent workbench presentation state: the
// client-owned markers, the reconciled directory, per-agent previous phases,
// and the MRU visit order. Runtime authority stays with Herdr.
type workbenchState struct {
	markers   *agent.MarkerStore
	directory *agent.AgentDirectory
	previous  map[agent.AgentKey]agent.AgentRuntimePhase
	mru       []agent.AgentKey
	// recentWorking anchors the directory sort hysteresis: the instant each
	// Agent's Working status may stop ordering it near the top. Agent hooks
	// report idle between every short task loop, and without the anchor the
	// honest bucket flip would keep reshuffling the list.
	recentWorking map[agent.AgentKey]time.Time
	// lastActive is the last instant this client saw the Agent work or
	// change phase; the sidebar's time tag takes the later of it and the
	// session file's modification time (Shell.agentSessionMod).
	lastActive map[agent.AgentKey]time.Time

	// Header popover Agents chip filter (2026-10-31): filterAll lists every
	// status group; otherwise filter is a quickPanel group index
	// (quickPanelNeedsYouGroup…).
	filterAll  bool
	filter     int
	headerOpen bool

	// switcher is the MRU overlay state (0.5 §13).
	switcherOpen   bool
	switcherIndex  int
	switcherOrigin agent.AgentKey
	switcherHad    bool
	// switcherFresh is true until the overlay has built once; switcherPointer
	// is where the pointer rests over its rows (see hoverSwitcherRow).
	switcherFresh   bool
	switcherPointer switcherPointer
}

// reconcileWorkbench rebuilds the Agent directory from the just-applied
// Herdr projection: one classified transition per agent, one marker store
// update, one directory replacement per reconciled snapshot (0.5 §21: one
// event burst → one projection update; no render-time IO).
func (s *Shell) reconcileWorkbench() {
	if s.workbench.markers == nil {
		s.workbench.markers = agent.NewMarkerStore()
	}
	if s.workbench.directory == nil {
		s.workbench.directory = agent.NewAgentDirectory()
	}
	if s.workbench.previous == nil {
		s.workbench.previous = make(map[agent.AgentKey]agent.AgentRuntimePhase)
	}
	if s.workbench.recentWorking == nil {
		s.workbench.recentWorking = make(map[agent.AgentKey]time.Time)
	}
	if s.workbench.lastActive == nil {
		s.workbench.lastActive = make(map[agent.AgentKey]time.Time)
	}

	projectName := make(map[string]string, len(s.projection.Projects))
	for _, project := range s.projection.Projects {
		projectName[project.ID] = project.Label
	}

	cards := make([]agent.AgentCardModel, 0, len(s.projection.Agents))
	live := make(map[agent.AgentKey]bool, len(s.projection.Agents))
	now := time.Now()
	for _, runtimeAgent := range s.projection.Agents {
		if s.paneShellIdle[runtimeAgent.PaneID] {
			// Measured at its bare shell prompt: Herdr's binding outlives
			// the agent process, so this entry is a dead leftover, not a
			// running agent card (2026-10-30 sidebar stale-agent defect).
			continue
		}
		key := agent.AgentKey{InstanceID: s.activeInstance, TerminalID: runtimeAgent.TerminalID}
		phase := agent.ParseRuntimePhase(runtimeAgent.Status, false)
		previous := s.workbench.previous[key]
		transition := agent.ClassifyTransition(previous, phase)
		s.workbench.previous[key] = phase
		if phase == agent.PhaseWorking {
			s.workbench.recentWorking[key] = now.Add(agent.WorkingHysteresis)
		}
		if phase == agent.PhaseWorking || (transition != agent.TransitionNone && transition != agent.TransitionFirstObservation) {
			s.workbench.lastActive[key] = now
		}

		selected := runtimeAgent.PaneID != "" && runtimeAgent.PaneID == s.selectedPaneID
		markers := s.workbench.markers.Observe(key, transition, selected)
		s.notifyAgentTransition(runtimeAgent, transition)
		if transition == agent.TransitionReadyForReview {
			// The final session title usually lands with the last transcript
			// writes: re-resolve now instead of waiting out the History-meta
			// lane's one-minute throttle.
			s.refreshUsageProjection(true)
		}

		provider := agentProvider(&runtimeAgent)
		card := agent.ProjectAgentCard(agent.AgentCardInputs{
			InstanceID:  key.InstanceID,
			TerminalID:  key.TerminalID,
			PaneID:      runtimeAgent.PaneID,
			TabID:       runtimeAgent.TabID,
			ProjectID:   runtimeAgent.ProjectID,
			ProjectName: projectName[runtimeAgent.ProjectID],
			Provider:    provider,
			Title:       s.agentTitleFrom(runtimeAgent),
			AgentStatus: runtimeAgent.Status,
			Revision:    int64(runtimeAgent.Revision),
			Attention:   agent.ParseOperationalState(runtimeAgent.Status),
			Markers:     markers,
		})
		card.ActiveUntil = s.workbench.recentWorking[key]
		card.LastActive = s.workbench.lastActive[key]
		card.Subagents = s.agentSubagents[key]
		if mod := s.agentSessionMod[key]; mod.After(card.LastActive) {
			card.LastActive = mod
		}
		cards = append(cards, card)
		live[key] = true
	}
	s.workbench.markers.Retain(live)
	for key := range s.workbench.recentWorking {
		if !live[key] {
			delete(s.workbench.recentWorking, key)
			delete(s.workbench.lastActive, key)
		}
	}
	s.workbench.directory.Replace(cards)
	s.publishRemoteAgentFacts()
	s.pruneAgentMRU(live)
	// A launched new-session draft waits here for its Agent card (new_chat_draft.go).
	s.completeAgentOpen()
}

// agentTitleFrom applies the title fallback: History session title (the
// agent's own session name, cached by the History-meta lane) → agent name
// → provider display name → "Agent".
func (s *Shell) agentTitleFrom(runtimeAgent herdr.Agent) string {
	if title := s.sessionTitleFor(agent.AgentKey{InstanceID: s.activeInstance, TerminalID: runtimeAgent.TerminalID}); title != "" {
		return title
	}
	if runtimeAgent.Name != "" {
		return runtimeAgent.Name
	}
	return ""
}

// agentCardTitleForPane resolves a pane's Agent title the way the Agents
// section shows it (session title → agent name → provider name), gated on
// a live agent. It is the ONE title utility for pane-bearing labels —
// tree rows, crumb rows, the pane card header and the chat header all
// route through it. A dead binding, an untitled agent, or a Solco-minted
// plumbing name (kind-<6hex>) reports false so callers keep their own
// label fallbacks.
func (s *Shell) agentCardTitleForPane(paneID string) (string, bool) {
	if s.paneAgent(paneID) == nil {
		return "", false
	}
	card, has := s.agentCardForPane(paneID)
	if !has || card.Title == "" || agent.IsGeneratedAgentName(card.Title, string(card.Provider)) {
		return "", false
	}
	return card.Title, true
}

// paneRuntimeAgent finds the runtime agent attached to one pane.
func (s *Shell) paneRuntimeAgent(paneID string) (herdr.Agent, bool) {
	for i := range s.projection.Agents {
		if s.projection.Agents[i].PaneID == paneID {
			return s.projection.Agents[i], true
		}
	}
	return herdr.Agent{}, false
}

// canReleaseAgent gates the release action: only an agent that no longer
// works (done or idle on a live pane) may be released; a working or
// blocked agent still owns its pane.
func canReleaseAgent(runtimeAgent herdr.Agent) bool {
	switch agent.ParseRuntimePhase(runtimeAgent.Status, false) {
	case agent.PhaseDone, agent.PhaseIdle:
		return true
	default:
		return false
	}
}

// notifyAgentTransition delivers the narrow transition notification through
// MyGo when the platform supports it. Markers stay the in-app source of
// truth even when OS delivery is unavailable (0.5 §15).
func (s *Shell) notifyAgentTransition(runtimeAgent herdr.Agent, transition agent.AgentTransition) {
	notification := agent.NotificationFor(transition, s.agentTitleFrom(runtimeAgent))
	if notification == nil {
		return
	}
	if s.remote.srv != nil {
		s.remote.srv.NotifyAgent(notification.Title, notification.Body) // the user's webhook channels, when no phone is there
	}
	// Headless builds (unit tests) have no window and no app loop: MyGo's
	// notification calls hop to the main thread and would block forever.
	if s.win == nil {
		return
	}
	if !mygo.NotificationsSupported() {
		return
	}
	showAgentNotification(s.activeInstance, runtimeAgent, notification)
}

// workbenchCards returns the reconciled cards in actionable-first order.
func (s *Shell) workbenchCards() []agent.AgentCardModel {
	if s.workbench.directory == nil {
		return nil
	}
	return s.workbench.directory.All()
}

// openAgentCard navigates client-locally to the Agent's owning pane (which
// lands blocked agents on their Terminal), records the MRU visit and clears
// unread/review markers. It never emits a Herdr focus RPC.
func (s *Shell) openAgentCard(card agent.AgentCardModel) {
	if s.holdCommitNavigation() {
		return
	}
	s.workbench.markers.Visit(card.Key)
	s.markAgentReviewedIfPending(card.Key)
	s.recordAgentVisit(card.Key)
	if card.PaneID != "" {
		s.selectPane(card.PaneID)
	}
	if s.router.Path() != routeWorkspace {
		s.router.Push(routeWorkspace)
	}
}

// markAgentReviewed is the explicit review action (0.5 §12): it clears
// review-pending and unread markers and updates directory facts.
func (s *Shell) markAgentReviewed(key agent.AgentKey) {
	if s.workbench.markers == nil {
		return
	}
	s.workbench.markers.MarkReviewed(key)
	s.reconcileWorkbenchMarkersOnly()
}

// markAgentReviewedIfPending clears review-pending for the agent matching key
// if it is currently waiting for review, reporting true when state changed.
func (s *Shell) markAgentReviewedIfPending(key agent.AgentKey) bool {
	if s.workbench.markers == nil {
		return false
	}
	if !s.workbench.markers.Get(key).ReviewPending {
		return false
	}
	s.markAgentReviewed(key)
	return true
}

// markAgentReviewedForPane clears review-pending for the agent running on targetPaneID
// when the pane or its tree row gains focus.
func (s *Shell) markAgentReviewedForPane(targetPaneID string) bool {
	if targetPaneID == "" {
		return false
	}
	if card, ok := s.agentCardForPane(targetPaneID); ok {
		return s.markAgentReviewedIfPending(card.Key)
	}
	return false
}

// reconcileWorkbenchMarkersOnly refreshes marker facts on the existing cards
// without a new transition classification pass.
func (s *Shell) reconcileWorkbenchMarkersOnly() {
	cards := s.workbenchCards()
	updated := make([]agent.AgentCardModel, 0, len(cards))
	for _, card := range cards {
		card.Unread = s.workbench.markers.Get(card.Key).Unread
		card.ReviewPending = s.workbench.markers.Get(card.Key).ReviewPending
		updated = append(updated, card)
	}
	s.workbench.directory.Replace(updated)
	// Marker facts render in the floating panel too; it repaints only when
	// invalidated (it gets no input between user interactions).
	s.invalidateQuickPanel()
}

// recordAgentVisit maintains the MRU order (0.5 §13): most recent first,
// bounded to 10 live Agent identities.
func (s *Shell) recordAgentVisit(key agent.AgentKey) {
	next := make([]agent.AgentKey, 0, len(s.workbench.mru)+1)
	next = append(next, key)
	for _, existing := range s.workbench.mru {
		if existing != key {
			next = append(next, existing)
		}
	}
	if len(next) > 10 {
		next = next[:10]
	}
	s.workbench.mru = next
}

func (s *Shell) pruneAgentMRU(live map[agent.AgentKey]bool) {
	pruned := make([]agent.AgentKey, 0, len(s.workbench.mru))
	for _, key := range s.workbench.mru {
		if live[key] {
			pruned = append(pruned, key)
		}
	}
	if len(pruned) > 10 {
		pruned = pruned[:10]
	}
	s.workbench.mru = pruned
}

// AgentMRUList returns the current MRU order for the switcher overlay.
func (s *Shell) AgentMRUList() []agent.AgentCardModel {
	cards := make([]agent.AgentCardModel, 0, len(s.workbench.mru))
	for _, key := range s.workbench.mru {
		if card, ok := s.workbench.directory.Get(key); ok {
			cards = append(cards, card)
		}
	}
	return cards
}
