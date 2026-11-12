package nativeui

/**
 * [INPUT]: 依赖 internal/automations 的 Watcher/Event（相邻两次 list 的差）、mygo.NewNotification、remotehost.Server.NotifyAutomation、automations_ui.go 的 goAutomations/openAutomationsDrawer、window_actions.go 的 activateWindow
 * [OUTPUT]: 对外提供 Shell.notifyAutomationSteps（桌面系统通知 + 手机事件帧）、automationNotificationID / parseAutomationNotificationID、Shell.routeAutomationNotification
 * [POS]: 自动化步骤变化（Agent 起草了草稿、run 开始/完成/失败）的唯一通知出口：判定全在 internal/automations.Watcher，状态仍归插件；通知只带自动化名与步骤，不带提示词/理由/错误文本；点击回到该自动化的抽屉
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/thewei/solco/internal/automations"
)

const (
	automationNotificationIDPrefix = "solco-automation/"
	automationNotificationGroup    = "solco-automations"
)

// automationNotificationID is the stable ID of one automation's notification:
// a later step replaces the earlier one instead of stacking, and a click
// carries the name back across relaunches.
func automationNotificationID(name string) string { return automationNotificationIDPrefix + name }

// parseAutomationNotificationID decodes an ID this app issued; anything else
// fails closed.
func parseAutomationNotificationID(id string) (string, bool) {
	name, ok := strings.CutPrefix(id, automationNotificationIDPrefix)
	return name, ok && name != ""
}

// automationNotificationText words one step. Title and body carry the
// automation's name and the step only.
func automationNotificationText(ev automations.Event) (title, body string) {
	switch ev.Kind {
	case automations.EventDraft:
		return "Draft needs approval", ev.Name
	case automations.EventStarted:
		return ev.Name, "Started"
	case automations.EventDone:
		return ev.Name, "Finished"
	default:
		return ev.Name, "Failed"
	}
}

// notifyAutomationSteps announces what changed since the previous list read:
// a system notification on this Mac and a frame to every paired phone's open
// events socket. The first read after launch, or after the gate reopens,
// announces nothing.
func (s *Shell) notifyAutomationSteps(list []automations.Automation) {
	for _, ev := range s.automations.watcher.Observe(list) {
		if s.remote.srv != nil {
			s.remote.srv.NotifyAutomation(string(ev.Kind), ev.Name, ev.RunID)
		}
		title, body := automationNotificationText(ev)
		if ev.Kind == automations.EventFailed {
			s.islandNotify(islandAlert{ID: automationNotificationID(ev.Name), Kind: islandAutomation, Title: title, Body: body})
		}
		// Headless builds (unit tests) have no window and no app loop: MyGo's
		// notification calls hop to the main thread and would block forever.
		if s.win == nil || !mygo.NotificationsSupported() {
			continue
		}
		if ev.Kind == automations.EventFailed && s.islandQuiets() {
			continue // the island has it
		}
		if n := mygo.NewNotification(mygo.NotificationOptions{
			ID:     automationNotificationID(ev.Name),
			Group:  automationNotificationGroup,
			Title:  title,
			Body:   body,
			Silent: true,
		}); n != nil {
			n.Show()
		}
	}
}

// routeAutomationNotification handles a click: the window comes forward on the
// automation's drawer, or on the page alone when the automation is gone.
func (s *Shell) routeAutomationNotification(name string) {
	s.activateWindow()
	if !s.automationsAvailable() {
		return
	}
	s.goAutomations()
	if s.automations.list.automation(name) != nil {
		s.openAutomationsDrawer(name)
	}
}
