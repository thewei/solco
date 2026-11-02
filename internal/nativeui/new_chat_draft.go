package nativeui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: app.SessionStarter（SessionSpec.Prompt 随启动送达首条消息）、Chat WebView 桥（chatBridgeState / sendChatText）、conversation.PendingEcho 对账、Shell.openChat
 * [OUTPUT]: 提供 newChatDraft（「新建 Agent Tab 且默认视图是 Chat」时的空白会话草稿）、beginNewChatDraft / sendNewChatDraft / finishNewChatDraft / dropNewChatDraft、draftBridgeState、newChatDraftSurface
 * [POS]: 2026-11-02 用户拍板（design: designs/mobile-projection 议题 7）：点 + 选 Agent 且默认 Chat 时，先立即显示与 ChatView 同一页面的空白会话；首条消息发出即渲染（EchoPending），后台才真正起 Agent；pane 出现后原位交接给真实数据（PendingEcho 同 pane 保留）。草稿不是 pane——Herdr 仍是唯一运行时，没有第二套 Tab/进程状态；失败保留草稿并带原因
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// newChatDraft is the blank conversation shown before its Agent exists.
// Sent is empty while the user is still composing; Sending covers the launch.
type newChatDraft struct {
	Project   herdr.Project
	Kind      app.SessionKind
	Sent      string
	Sending   bool
	Outcome   string
	RequestID string
	// PaneID/HandoffAt: the launched pane, set once the Agent exists while the
	// page waits for its card (completeDraftHandoff).
	PaneID    string
	HandoffAt time.Time
}

// newChatDraftAvailable reports that the new-Tab menu may open a draft: Chat is
// the default Agent view and the WebView page exists (the native Chat has no
// pane-less state).
func (s *Shell) newChatDraftAvailable() bool {
	return s.settings.General.AgentChatFirst() && s.chatWebviewFor() != nil
}

// beginNewChatDraft shows the blank conversation for kind in project.
func (s *Shell) beginNewChatDraft(project herdr.Project, kind app.SessionKind) {
	s.newChatDraft = &newChatDraft{Project: project, Kind: kind}
	if s.router.Path() != routeWorkspace {
		s.router.Push(routeWorkspace)
	}
	s.publishChatBridge()
}

// dropNewChatDraft discards the draft (selection moved on, or the user closed it).
func (s *Shell) dropNewChatDraft() {
	if s.newChatDraft == nil {
		return
	}
	s.newChatDraft = nil
	s.publishChatBridge()
}

// draftBridgeState is the page projection while a draft is open: the
// invitation + composer before sending, the sent message and a Working line
// after.
func (s *Shell) draftBridgeState() ChatBridgeState {
	d := s.newChatDraft
	s.chatStateSeq++
	state := ChatBridgeState{
		Seq:          s.chatStateSeq,
		Disposition:  "ready",
		Outcome:      d.Outcome,
		Theme:        s.chatBridgeTheme(),
		HighContrast: s.settings.General.HighContrast,
		VoiceSource:  voiceSourceName(s.settings.Voice),
		SerifProse:   s.settings.General.ChatSerif,
		Outline:      s.settings.General.ChatOutline,
		Features:     ChatFeatures{Voice: true},
		Draft:        true,
		Provider:     d.Kind.Label,
		Title:        "New " + d.Kind.Label + " session",
	}
	if d.Sent == "" {
		state.Waiting = true
		return state
	}
	state.EchoPending = true
	state.Turns = chatsession.Turns([]conversation.TimelineTurn{{UserRow: true, Text: d.Sent}}, "", nil)
	return state
}

// sendNewChatDraft takes the first message: it renders at once and the Agent
// starts behind it with the message as its first prompt.
func (s *Shell) sendNewChatDraft(text string) ChatBridgeState {
	d := s.newChatDraft
	text = strings.TrimSpace(text)
	if text == "" || d.Sending {
		return s.chatBridgeState()
	}
	starter, instance := s.newTabLauncher(), s.activeInstance
	if starter == nil || instance == "" {
		d.Outcome = "Can't start " + d.Kind.Label + " right now."
		s.publishChatBridge()
		return s.chatBridgeState()
	}
	d.Sent, d.Sending, d.Outcome = text, true, ""
	d.RequestID = fmt.Sprintf("draft-%s-%d", d.Kind.ID, time.Now().UnixNano())
	spec := app.SessionSpec{
		RequestID:   d.RequestID,
		WorkspaceID: d.Project.ID,
		CWD:         d.Project.CWD,
		Agent:       history.AgentID(d.Kind.ID),
		Prompt:      text,
	}
	s.publishChatBridge()
	s.dispatch(func() {
		result, err := starter.StartSession(context.Background(), instance, spec)
		var projection *herdr.Projection
		if result.PaneID != "" && s.runtime != nil {
			if fresh, perr := s.runtime.Projection(instance); perr == nil {
				projection = &fresh
			}
		}
		s.applyOnWindow(func() { s.finishNewChatDraft(d, instance, result, err, projection) })
	})
	return s.chatBridgeState()
}

// finishNewChatDraft hands the draft over to the real pane, or puts the text
// back with the reason. The prompt itself never reaches a log.
func (s *Shell) finishNewChatDraft(d *newChatDraft, instance string, result app.SessionResult, err error, projection *herdr.Projection) {
	if err != nil {
		slog.Warn("new chat draft launch failed", "instance", instance, "agent", d.Kind.ID, "error", err)
	}
	current := s.newChatDraft == d && s.activeInstance == instance
	if result.PaneID != "" && s.activeInstance == instance && projection != nil {
		s.applyProjection(*projection, false)
	}
	if !current {
		// The user moved on while the Agent started: the new Tab still exists.
		if err != nil {
			s.pendingToast = "Could not start " + d.Kind.Label + ": " + err.Error()
		}
		return
	}
	if result.PaneID == "" {
		d.Sending, d.Sent = false, ""
		d.Outcome = "Could not start " + d.Kind.Label + ": " + fallbackText(errText(err), "unknown error")
		s.publishChatBridge()
		return
	}
	d.PaneID, d.HandoffAt = result.PaneID, time.Now()
	if err != nil {
		s.pendingToast = "Started " + d.Kind.Label + " with a problem: " + err.Error()
	}
	if result.PromptPending {
		d.Sent = ""
		s.pendingToast = d.Kind.Label + " is waiting on a question first — send your message again once it is answered."
	}
	s.completeDraftHandoff()
}

// draftHandoffPatience is how long the page keeps showing the sent message
// while the new pane's Agent card has not appeared in the projection.
const draftHandoffPatience = 10 * time.Second

// completeDraftHandoff moves a launched draft onto its real pane once the
// Agent card exists (called after the launch and after every projection
// apply): the sent text becomes the pane's pending echo, so the page keeps
// the message and its Working line and the provider's own rows replace it in
// place. An Agent that never shows up lands on the pane's Terminal instead.
func (s *Shell) completeDraftHandoff() {
	d := s.newChatDraft
	if d == nil || d.PaneID == "" {
		return
	}
	card, ok := s.agentCardForPane(d.PaneID)
	if !ok {
		if paneByID(s.projection, d.PaneID) != nil && time.Since(d.HandoffAt) > draftHandoffPatience {
			s.newChatDraft = nil
			s.showPane(d.PaneID)
		}
		return
	}
	s.newChatDraft = nil
	s.rememberPaneSurface(d.PaneID, WorkspaceSurfaceChat)
	s.selectPane(d.PaneID)
	if d.Sent != "" {
		s.chatPendingEcho = &conversation.PendingEcho{Text: d.Sent, Baseline: 0, PaneID: d.PaneID}
	}
	s.openChat(card)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// newChatDraftSurface is the centre of /workspace while a draft is open: a
// slim header (brand, title, discard) over the same Chat page.
func (s *Shell) newChatDraftSurface(c *ui.Context) {
	t := c.Theme()
	k := gorexColorsOf(t.Dark)
	d := s.newChatDraft
	ui.Column(c).Grow(1).MinWidth(0).Background(designTokens(t.Dark).Content).Children(func() {
		ui.Row(c).Height(gorexHeaderH).Padding(0, gorexGap, 0, 12).Gap(7).AlignItems(ui.Center).MinWidth(0).Children(func() {
			ui.Box(c).Size(15, 15).Shrink(0).Children(func() {
				markView(c, agentMark(history.AgentID(d.Kind.ID), t.Dark), 14.5, k.text)
			})
			ui.Text(c, "New "+d.Kind.Label+" session").FontSize(Typography().Body).FontWeight(WeightSemibold).
				TextColor(k.text).SingleLine().Ellipsis("…").Shrink(0).MaxWidthPercent(60)
			ui.Text(c, d.Project.Label).FontSize(Typography().BodySmall).TextColor(t.TextMuted).
				SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			ui.Spacer(c)
			if !d.Sending {
				close := gorexIconButton(c, k, iconClose, "Discard", "Discard new session", false, gorexRowBtn, gorexRowGlyph)
				if close.Clicked() {
					s.dropNewChatDraft()
				}
			}
		})
		s.chatWebviewBody(c)
	})
}
