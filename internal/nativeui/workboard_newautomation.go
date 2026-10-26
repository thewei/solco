package nativeui

/**
 * [INPUT]: 依赖 internal/workboard 的 NewAutomation/AutomationEvents/Client.AddAutomation、workboardWrite 单一写通道、官方 ui.Modal/Segmented/Select/TextInput/TextArea
 * [OUTPUT]: newAutomationDraft、openWorkboardAutomation（按 Project 名）、newAutomationDialog（模态：名称、提示词、Schedule·Event 二选一；Schedule 为插件自己的写法文本，Event 为下拉 + 全部项目 + 可选条件 --if；Task·Research·Chat 三选一）、submitNewAutomation
 * [POS]: Project 页 Automations 组与项目菜单的创建入口；触发器语法（every/daily/mon/cron）与 --if 条件全归插件解析，拒绝原文透传、什么都不创建；事件名取钉住的 1.2.x 清单（改名由插件拒绝）；创建后板刷新，新自动化出现在该组
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/solco/internal/herdr"
	"github.com/wh-studio/solco/internal/workboard"
)

// Triggers of a new automation, in the segmented control's order.
const (
	automationOnSchedule = iota
	automationOnEvent
)

// newAutomationDraft is the open New Automation dialog for one Project.
type newAutomationDraft struct {
	open         bool
	project      string
	name, prompt string
	trigger      int
	schedule     string
	event        string // one of workboard.AutomationEvents
	everyRepo    bool
	cond         string
	kind         int // newTaskPlain / newTaskResearch / newTaskChat
}

// openWorkboardAutomation opens New Automation for a Project.
func (s *Shell) openWorkboardAutomation(project string) {
	s.hideQuickPanel()
	s.workboard.newAutomation = newAutomationDraft{open: true, project: project, event: workboard.AutomationEvents[0]}
}

// newAutomationDialog renders the dialog while it is open.
func (s *Shell) newAutomationDialog(c *ui.Context) {
	d := &s.workboard.newAutomation
	if !d.open {
		return
	}
	ui.Modal(c, &d.open, func() {
		sp := Spacing()
		t := c.Theme()
		ui.Column(c).Width(440).MaxWidth(500).Gap(sp.L).Children(func() {
			modalTitle(c, "New Automation")
			ui.TextInput(c, &d.name).Label("Name").Placeholder("Name").AutoFocus().FillWidth()
			ui.TextArea(c, &d.prompt).Label("Prompt").Placeholder("What each firing's task is to do").FillWidth().Height(88)
			ui.Segmented(c, &d.trigger, "Schedule", "Event").Label("Trigger")
			if d.trigger == automationOnSchedule {
				ui.TextInput(c, &d.schedule).Label("Schedule").Placeholder("every 2h · daily 09:00 · mon 09:00 · cron \"0 9 * * 1\"").FillWidth()
			} else {
				ui.Select(c, &d.event, workboard.AutomationEvents).Label("Event")
				ui.Checkbox(c, &d.everyRepo, "A task of any project")
				ui.TextInput(c, &d.cond).Label("Only if").Placeholder("only if, e.g. branch = feature/* (optional)").FillWidth()
			}
			ui.Segmented(c, &d.kind, "Task", "Research only", "Chat only").Label("Kind")
			ui.Text(c, "Each firing adds a task in "+d.project+" and puts an agent on it.").FontSize(Typography().Caption).TextColor(t.TextMuted)
			ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					d.open = false
				}
				if ui.PrimaryButton(c, "Add").Clicked() && s.newAutomationReady() {
					s.submitNewAutomation()
				}
			})
		})
	})
}

// newAutomationReady is whether the dialog has what the plugin needs; what
// the words mean stays the plugin's to judge.
func (s *Shell) newAutomationReady() bool {
	d := s.workboard.newAutomation
	if strings.TrimSpace(d.name) == "" || strings.TrimSpace(d.prompt) == "" {
		return false
	}
	return d.trigger == automationOnEvent || strings.TrimSpace(d.schedule) != ""
}

// submitNewAutomation closes the dialog and adds the automation it describes.
func (s *Shell) submitNewAutomation() {
	d := s.workboard.newAutomation
	s.workboard.newAutomation = newAutomationDraft{}
	if !s.workboardAvailable() {
		return
	}
	a := workboard.NewAutomation{
		Name: d.name, Prompt: d.prompt, Repo: d.project,
		Research: d.kind == newTaskResearch, Chat: d.kind == newTaskChat,
	}
	if d.trigger == automationOnSchedule {
		a.Schedule = d.schedule
	} else {
		a.When, a.EveryRepo, a.If = d.event, d.everyRepo, d.cond
	}
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("work board write requested", "op", "automation-add", "project", d.project, "event", a.When != "")
	s.workboardWrite("automation-add", func(ctx context.Context) error { return client.AddAutomation(ctx, a) }, nil)
}
