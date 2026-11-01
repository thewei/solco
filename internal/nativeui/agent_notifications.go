package nativeui

/**
 * [INPUT]: 依赖 herdr.Agent, agent.AgentNotification, mygo.NewNotification, window_actions.go 的 activateWindow, surface_header.go 的 panePrefersChat, chat_live.go 的 openChat, agent_quick_panel.go 的 openAgentTerminal
 * [OUTPUT]: 对外提供 agentNotificationID, agentNotificationGroup, parseAgentNotificationID, showAgentNotification, routeAgentNotification, RouteAgentNotification
 * [POS]: internal/nativeui 的系统桌面通知派发与点击路由，响应系统通知中心并路由到 Agent 窗格的偏好视图（Chat 或 Terminal）并激活窗口
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
)

// Desktop-notification identity for agent transitions (MyGo v0.2.15):
// a stable ID lets App.OnNotificationClick route a click back to the
// Agent's owning pane — even after the app quit and relaunched — and a
// per-workspace Group stacks each workspace's notifications together in
// Notification Center. The ID is presentation-only: it encodes the
// Herdr-owned (instance, terminal) pair and carries no runtime truth.

const agentNotificationIDPrefix = "solco-agent/"

// agentNotificationID is the stable notification ID of one Agent pane.
// It is pure text: MyGo hands it back verbatim, across runs.
func agentNotificationID(instanceID, terminalID string) string {
	return agentNotificationIDPrefix + instanceID + "/" + terminalID
}

// agentNotificationGroup groups one workspace's agent notifications in a
// single Notification Center stack.
func agentNotificationGroup(instanceID string) string {
	return "solco-workspace/" + instanceID
}

// parseAgentNotificationID decodes an ID this app issued. Anything else —
// foreign apps' IDs, empty segments, malformed shapes — fails closed.
func parseAgentNotificationID(id string) (agent.AgentKey, bool) {
	rest, ok := strings.CutPrefix(id, agentNotificationIDPrefix)
	if !ok {
		return agent.AgentKey{}, false
	}
	instance, terminal, ok := strings.Cut(rest, "/")
	if !ok || instance == "" || terminal == "" || strings.Contains(terminal, "/") {
		return agent.AgentKey{}, false
	}
	return agent.AgentKey{InstanceID: instance, TerminalID: terminal}, true
}

// showAgentNotification delivers one transition notification under the
// stable ID/group identity. Headless builds (unit tests) must not call
// it — MyGo's notification calls hop to the main thread and would block
// forever without an app loop.
func showAgentNotification(instanceID string, runtimeAgent herdr.Agent, notification *agent.AgentNotification) {
	obj := mygo.NewNotification(mygo.NotificationOptions{
		ID:     agentNotificationID(instanceID, runtimeAgent.TerminalID),
		Group:  agentNotificationGroup(instanceID),
		Title:  notification.Title,
		Body:   notification.Body,
		Silent: true,
	})
	if obj != nil {
		obj.Show()
	}
}

// routeAgentNotification handles a notification click by navigating
// client-locally to the Agent's owning pane — preferring Chat when configured
// or remembered, Terminal otherwise — and activating the application window.
// It revalidates the stale-prone ID against the live projection: an ended or
// unknown Agent surfaces a toast instead of a blind navigation. Pure state
// mutation, safe headless; the frame-local pendingToast is consumed by the
// next View pass.
func (s *Shell) routeAgentNotification(id string) {
	s.activateWindow()
	key, ok := parseAgentNotificationID(id)
	if !ok {
		return
	}
	for _, card := range s.workbenchCards() {
		if card.Key == key {
			if s.panePrefersChat(card.PaneID) && s.workspaceChatAvailable() {
				s.openChat(card)
			} else {
				s.openAgentTerminal(card)
			}
			return
		}
	}
	s.pendingToast = "Agent has ended"
}

// RouteAgentNotification is the main.go seam for MyGo's app-global
// notification-click listener.
func (s *Shell) RouteAgentNotification(id string) { s.routeAgentNotification(id) }
