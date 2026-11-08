/**
 * [INPUT]: 依赖 mygo/ui, internal/agent, WorkspacePrimarySurface 状态
 * [OUTPUT]: 对外提供 workspacePage（中心 workspaceCenter + 右侧面板槽 side_panel.go sidePanelHost）, contentGround（内容列底色：浅色窗口有终端卡片时 = paneGround 灰，标题栏/卡片间隙/面板旁同色；其余沿用画布）, agentCardForPane, workspaceHomeCenter, submitHomePrompt
 * [POS]: Terminal/Chat/Detail/Commit 唯一主表面切换中心（Diff 审阅随 Git 整页于 2026-11-08 G5 退役；改动与文件在右侧面板里列、点开后以 Detail 卡片在中心看，一次只显示一个，绝不分屏）；选中回跳按 paneSurfaceMemo 记忆回 Chat/Terminal（2026-10-31 窗格表面记忆）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package nativeui

import (
	"log/slog"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
)

// workspacePage is the /workspace row: the center (workspaceCenter), then the
// right panel's slot (side_panel.go) — which is empty unless the user opened the
// panel beside the Terminal or Chat.
func (s *Shell) workspacePage(c *ui.Context) {
	ui.Row(c).Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		s.workspaceCenter(c)
		s.sidePanelHost(c)
	})
}

// contentGround is the fill of the content column that carries the title bar
// and the route. While cards (Terminal or Detail) are on screen in a light window it is
// the cards' own grey ground, so the title bar, the gaps between the cards and
// the strip beside the right panel are one colour (2026-11-08); every other
// route and surface keeps the canvas.
func (s *Shell) contentGround(dark bool) ui.Color {
	if s.router.Path() == routeWorkspace && (s.surface.current() == WorkspaceSurfaceDetail || s.surface.current() == WorkspaceSurfaceTerminal && len(s.terminals) > 0) {
		if ground, ok := paneGround(dark); ok {
			return ground
		}
	}
	return s.tokens(dark).Content
}

// workspaceCenter renders the /workspace center through the single
// WorkspacePrimarySurface owner: Terminal, Chat, Detail, or Commit state. Commit
// displays the selected file's diff under its native modal editor; no
// Terminal frame or second Git transaction can receive input while the modal
// is active.
func (s *Shell) workspaceCenter(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		if s.errText != "" {
			s.workspaceErrorBanner(c, t)
		}

		switch s.surface.current() {
		case WorkspaceSurfaceCommit:
			// Commit is a modal above the diff it is about, never a second editor.
			gorexContentCard(c, gorexColorsOf(t.Dark), func() { s.gdDiffSurface(c) })
		case WorkspaceSurfaceChat:
			gorexContentCard(c, gorexColorsOf(t.Dark), func() { s.workspaceChatSurface(c) })
		case WorkspaceSurfaceDetail:
			// The panel's picked item as a card, in place of the Terminal or
			// Chat it was opened from (detail_surface.go).
			s.detailCardHost(c)
		default:
			// The Terminal find bar must mount above the canvas: ⌘F only
			// flips terminalFind.open, without this row the bar never
			// rendered (2026-10-06 F19).
			s.terminalFindBar(c)
			s.terminalSurface(c)
		}
	})
}

// workspaceErrorBanner is the shared reconnect banner above any surface.
func (s *Shell) workspaceErrorBanner(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Padding(7, 12).Background(t.Danger.Alpha(0.12)).BorderWidth(0, 0, 1, 0).BorderColor(t.Danger.Alpha(0.35)).Children(func() {
		ui.Text(c, s.errText).TextColor(t.Danger).FontSize(Typography().BodySmall).Grow(1).SingleLine()
		if regularButton(c, "Retry").Clicked() {
			s.reloadInstances(false)
		}
	})
}

// workspaceHomeCenter is the prompt-first empty state of the workspace
// (Cursor-3 home, 2026-10-29): one quiet context line of the real open
// facts, the shared prompt dock when the work board gate is open, and a
// row of real quick actions. It never fakes a pull-down or a send target:
// the header keeps the switchers, and the dock submits into the plugin's
// own New Task dialog.
func (s *Shell) workspaceHomeCenter(c *ui.Context) {
	t := c.Theme()
	sp := Spacing()
	project, branch := s.sidebarProjectTitle(), s.currentBranchName()
	ui.Column(c).Grow(1).MinWidth(0).Center().Gap(sp.L).Padding(sp.XL).Children(func() {
		ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconFolder).FontSize(14).TextColor(t.TextMuted)
			ui.Text(c, fallbackText(project, "No project")).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			if branch != "" {
				ui.Text(c, "·").FontSize(Typography().BodySmall).TextColor(t.TextMuted)
				ui.Icon(c, iconBranch).FontSize(14).TextColor(t.TextMuted)
				ui.Text(c, branch).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			}
			if s.activeInstance != "" {
				ui.Text(c, "·").FontSize(Typography().BodySmall).TextColor(t.TextMuted)
				ui.Icon(c, iconServer).FontSize(14).TextColor(t.TextMuted)
				ui.Text(c, s.activeInstance).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			}
		})
		ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
			// The sidebar header's hover "+" was the only way to a Workspace
			// (device E2E 2026-11-03): the empty center names it too.
			ghostPill(c, "home-new-workspace", "New Workspace from Folder…", s.createWorkspaceFromFolder)
			ghostPill(c, "home-sessions", "Sessions", s.showViewHistory)
			ghostPill(c, "home-hosts", "Hosts", s.showViewHosts)
		})
	})
}

// terminalSurface renders the Terminal primary surface. The terminal canvas
// exists ONLY while this surface is visible (GWB-040); attachments stay
// alive underneath (GWB-041) and receive no input while hidden (GWB-039).
func (s *Shell) terminalSurface(c *ui.Context) {
	t := c.Theme()
	if len(s.terminals) == 0 {
		if s.shellState() != stateLoading && s.status == "" {
			// The quiet empty state becomes the Cursor-3 home: what is
			// open, and the real quick actions. Loading, offline and
			// bootstrap states keep their own faces.
			s.workspaceHomeCenter(c)
			return
		}
		ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
			if s.shellState() == stateLoading {
				ui.Spinner(c).Size(24, 24)
			}
			if s.status != "" || s.shellState() != stateLoading {
				ui.Text(c, fallbackText(s.status, "No terminal open")).TextColor(t.TextMuted)
			}
		})
		return
	}
	s.terminalCanvas(c)
}

// showSurface switches the primary surface with the §6/§34 focus rules:
// leaving Terminal explicitly removes terminal focus; entering it restores
// pane focus and resyncs geometry (GWB-042).
func (s *Shell) showSurface(kind WorkspaceSurfaceKind) {
	if s.surface.current() == WorkspaceSurfaceCommit && kind != WorkspaceSurfaceCommit && s.holdCommitNavigation() {
		return
	}
	if s.surface.current() == kind {
		return
	}
	switch kind {
	case WorkspaceSurfaceTerminal:
		s.enterTerminalSurface()
		// The pane is on its Terminal now; navigation back to it must
		// return here (2026-10-31 pane-surface memory).
		s.rememberPaneSurface(s.selectedPaneID, WorkspaceSurfaceTerminal)
	case WorkspaceSurfaceChat:
		if s.openWorkspaceChat() {
			s.rememberPaneSurface(s.selectedPaneID, WorkspaceSurfaceChat)
		}
	case WorkspaceSurfaceDetail:
		s.showDetailSurface()
	case WorkspaceSurfaceCommit:
		s.openCommitSurface()
	}
}

// enterTerminalSurface is the Terminal entry shared by showSurface and the
// selection sync: reset the surface state machine onto the current context,
// re-attach the Tab's terminals and refresh Git facts.
func (s *Shell) enterTerminalSurface() {
	s.surface.resetToTerminal(s.workspaceContext())
	if err := s.syncTerminals(s.projection); err != nil {
		s.errText = err.Error()
	}
	s.syncGitContext()
}

// showPaneSurface lands paneID on the surface its memory names — Chat when
// it prefers Chat and Chat is bindable, Terminal otherwise — and never
// rewrites the pane-surface memory: arriving at a pane is navigation, and
// only an explicit surface choice (the card-header switch, an Open
// Terminal action, openChat) records memory. The one answer every "go to
// this pane" path shares (2026-11-03 surface-owner unification).
func (s *Shell) showPaneSurface(paneID string) {
	if s.panePrefersChat(paneID) && s.workspaceChatAvailable() {
		s.showSurface(WorkspaceSurfaceChat)
		return
	}
	if s.surface.current() == WorkspaceSurfaceTerminal {
		return
	}
	s.enterTerminalSurface()
}

// landSelectedPane is the one owner of "return to the workspace home": the
// header home tab, ⌘1 / Go-menu Workspace, the palette Workspace action and
// the view-tab exits all land here. It routes to the workspace and shows the
// selected pane's remembered surface (showPaneSurface) — a Chat-pinned pane
// stays Chat — instead of hardwiring Terminal and rewriting the memory the
// way the old showViewTerminal landing did (2026-11-03 unification; sandbox
// repro in docs/debug/2026-11-03-chat-view-revert-on-return.md).
func (s *Shell) landSelectedPane() {
	if s.holdCommitNavigation() {
		return
	}
	if s.router.Path() != routeWorkspace {
		s.router.Push(routeWorkspace)
	}
	s.showPaneSurface(s.selectedPaneID)
}

// workspaceChatAvailable fences user-triggered Chat navigation to an
// actual Agent pane. A previously bound conversation for another pane does
// not qualify the currently selected plain shell as a Chat destination.
func (s *Shell) workspaceChatAvailable() bool {
	_, ok := s.agentCardForSelectedPane()
	return ok
}

func (s *Shell) chatUnavailableNotice() {
	s.pendingToast = "Chat needs an Agent pane. Select an Agent from the sidebar."
}

// openWorkspaceChat activates Chat only for an Agent-backed pane. The
// ordinary shell remains on its current surface; no stale Chat draft or
// conversation may be projected as though it belongs to that shell. It
// reports whether Chat opened, so the pane-surface memory only records
// panes that really showed Chat.
func (s *Shell) openWorkspaceChat() bool {
	if !s.workspaceChatAvailable() {
		s.chatUnavailableNotice()
		return false
	}
	s.surface.openChat(s.workspaceContext())
	s.rebindWorkspaceChat()
	return true
}

// rebindChatForCard reconciles the active chat session with one Agent card
// whatever the Workspace selection is: the Workspace Chat passes the selected
// pane's card, an embedding page (Assistants) passes its own, so binding a
// Chat never has to move the Mac user's navigation.
func (s *Shell) rebindChatForCard(card agent.AgentCardModel) {
	if s.chatPaneID == card.PaneID && s.chatConversationID != "" {
		// The bound live transcript yields when the pane's occupant has
		// moved on: another agent took the pane over (the provider
		// changed) or the same agent restarted into a new session (the
		// measured fingerprint changed). Without this, switching agents
		// in place kept the previous agent's view on the Chat surface
		// (2026-10-31). A pane whose occupant is gone or has not
		// reported an identity yet keeps the binding; a history-bound
		// view (no live fingerprint) only obeys the linked-yield rule.
		current := s.liveFingerprintForPane(card.PaneID)
		occupant := agentProvider(s.paneAgent(card.PaneID))
		stale := s.chatLiveFingerprint != "" &&
			((current != "" && current != s.chatLiveFingerprint) ||
				(occupant != "" && occupant != s.chatLiveProvider))
		if !stale {
			// A hand-linked session (chat_link.go) yields once the
			// agent's measured identity arrives; anything else stays
			// bound.
			if provider, _ := s.agentSessionIdentity(card.Key); !(s.chatSessionLinked() && provider != "") {
				// A later direct live bind (sidebar path) supersedes
				// the sourceless latch.
				s.chatSourcelessPaneID = ""
				return
			}
		}
	}
	if s.chatPaneID == card.PaneID && s.chatSourcelessPaneID == card.PaneID {
		// The latch holds only while the pane still has no chat source:
		// once the agent establishes a session (card carries the
		// conversation identity), rebind for real.
		// An identity whose transcript is not on disk yet (a fresh
		// session) is no source either.
		if provider, _ := s.agentSessionIdentity(card.Key); card.ConversationID == "" && (provider == "" || s.liveSourcePending(card.Key)) {
			return
		}
		s.chatSourcelessPaneID = ""
	}
	s.chatPaneID = card.PaneID
	s.chatSourcelessPaneID = ""
	if card.Key.TerminalID != "" && s.bindLiveAgent(card.Key) {
		return
	}
	if card.ConversationID != "" {
		s.bindChatConversation(card.ConversationID)
		return
	}
	// An Agent without any chat source must not inherit the previous
	// pane's conversation: drop to the empty state. The render path
	// re-enters while the pane stays sourceless, so the notice is
	// latched per pane (the guard above exits early).
	s.stopLiveTail()
	s.chatConversationID = ""
	s.chatTurns = nil
	s.chatWindowMessages = nil
	s.chatWindowStart = 0
	s.chatHasOlder = false
	s.chatContextTokens = nil
	s.chatModel = ""
	s.chatSourcelessPaneID = card.PaneID
	slog.Info("chat sourceless", "pane", card.PaneID, "reason", s.chatSourcelessReason(card))
	// The page shows its own invitation (type the first prompt below),
	// so no toast; a prompt already sent from here keeps its echo.
	s.retainPendingEchoForPane(card.PaneID)
	s.appendPendingEchoTurn()
	s.publishChatBridge()
	return
}

// chatSourcelessReason names why a card landed without a chat source, for the
// log line only: ids and a fixed word, never a path or any conversation text.
func (s *Shell) chatSourcelessReason(card agent.AgentCardModel) string {
	provider, identity := s.agentSessionIdentity(card.Key)
	switch {
	case card.Key.TerminalID == "":
		return "no_terminal"
	case provider == "" || identity == nil:
		return "no_identity"
	case s.liveSourcePending(card.Key):
		return "transcript_not_written"
	default:
		return "source_unresolved"
	}
}

// rebindWorkspaceChat reconciles the active chat session with the selected pane.
func (s *Shell) rebindWorkspaceChat() {
	if card, ok := s.agentCardForSelectedPane(); ok {
		s.rebindChatForCard(card)
	} else if s.selectedPaneID != "" && s.selectedPaneID != s.chatPaneID {
		s.chatPaneID = s.selectedPaneID
		s.chatSourcelessPaneID = ""
		s.retainPendingEchoForPane(s.selectedPaneID)
		s.stopLiveTail()
		s.chatConversationID = ""
		s.chatTurns = nil
		s.chatContextTokens = nil
		s.chatModel = ""
		s.publishChatBridge()
	}
}

// toggleTerminalChat toggles between Terminal and Chat on the active workspace.
func (s *Shell) toggleTerminalChat() {
	if s.surface.current() != WorkspaceSurfaceChat && !s.workspaceChatAvailable() {
		s.chatUnavailableNotice()
		return
	}
	if s.router.Path() != routeWorkspace {
		s.router.Push(routeWorkspace)
	}
	if s.surface.current() == WorkspaceSurfaceChat {
		s.showSurface(WorkspaceSurfaceTerminal)
	} else {
		s.showSurface(WorkspaceSurfaceChat)
	}
}

// agentCardForSelectedPane finds the agent card associated with the selected pane.
func (s *Shell) agentCardForSelectedPane() (agent.AgentCardModel, bool) {
	targetPaneID := s.selectedPaneID
	if targetPaneID == "" {
		targetPaneID = s.projection.FocusedPaneID
	}
	return s.agentCardForPane(targetPaneID)
}

// agentCardForPane projects one pane's Agent card from the workbench
// directory, falling back to the raw projection.
func (s *Shell) agentCardForPane(targetPaneID string) (agent.AgentCardModel, bool) {
	if targetPaneID == "" {
		return agent.AgentCardModel{}, false
	}
	if s.workbench.directory != nil {
		for _, card := range s.workbench.directory.All() {
			if card.PaneID == targetPaneID {
				return card, true
			}
		}
	}
	for _, runtimeAgent := range s.projection.Agents {
		if runtimeAgent.PaneID == targetPaneID {
			provider := agentProvider(&runtimeAgent)
			key := agent.AgentKey{InstanceID: s.activeInstance, TerminalID: runtimeAgent.TerminalID}
			return agent.AgentCardModel{
				Key:           key,
				PaneID:        runtimeAgent.PaneID,
				TabID:         runtimeAgent.TabID,
				ProjectID:     runtimeAgent.ProjectID,
				Provider:      provider,
				ProviderLabel: provider.DisplayName(),
				Title:         s.agentTitleFrom(runtimeAgent),
				RuntimePhase:  agent.ParseRuntimePhase(runtimeAgent.Status, false),
			}, true
		}
	}
	return agent.AgentCardModel{}, false
}

// syncWorkspaceForSelection rebinds everything that follows the selected
// Tab: Git context, terminal sync and surface validity (GWB-031/033). The
// surface follows the pane-surface memory (2026-10-31): navigation back to
// a pane lands on the surface it was last used on — Chat when remembered
// and still bindable, Terminal otherwise. Commit is workspace-level and is
// never flipped here (TestSidebarWayHomeDoesNotRestoreChat).
func (s *Shell) syncWorkspaceForSelection() {
	ctx := s.workspaceContext()
	if !s.surface.isValid(ctx) {
		// Context changed (project/tab/instance): the new pane's remembered
		// surface, else Terminal (§6 with the pane-surface memory). No
		// terminal sync here — applyProjection and syncSelectionPresentation
		// own it around every selection change.
		if s.launchHold() {
			// The launch holds the surface (chat_launch.go): bind the pane's
			// Chat once it is available, never fall back to Terminal for a
			// pane that is still booting.
			if s.workspaceChatAvailable() {
				s.openWorkspaceChat()
			}
		} else if s.panePrefersChat(s.selectedPaneID) && s.workspaceChatAvailable() {
			s.openWorkspaceChat()
		} else {
			s.surface.resetToTerminal(ctx)
		}
	} else {
		s.reconcilePaneSurface()
	}
	s.syncGitContext()
	if s.surface.current() == WorkspaceSurfaceCommit {
		s.ensureGitSnapshot(false)
	}
	if s.surface.current() == WorkspaceSurfaceChat {
		s.rebindWorkspaceChat()
	}
}

// reconcilePaneSurface keeps an unchanged context honest with the selected
// pane's remembered view: switching panes inside a Tab lands each pane
// where it was last used (2026-10-31 pane-surface memory).
func (s *Shell) reconcilePaneSurface() {
	if s.launchHold() {
		return
	}
	switch s.surface.current() {
	case WorkspaceSurfaceChat:
		if !s.panePrefersChat(s.selectedPaneID) {
			s.showSurface(WorkspaceSurfaceTerminal)
		}
	case WorkspaceSurfaceTerminal:
		if s.panePrefersChat(s.selectedPaneID) && s.workspaceChatAvailable() {
			s.showSurface(WorkspaceSurfaceChat)
		}
	case WorkspaceSurfaceDetail:
		// Another pane being selected is navigation away from the card (it
		// stays open behind); a projection refresh that keeps the selection
		// must not pull the reader out of it.
		if s.selectedPaneID != s.surface.detailPaneID() {
			s.showPaneSurface(s.selectedPaneID)
		}
	}
}
