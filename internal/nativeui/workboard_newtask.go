package nativeui

/**
 * [INPUT]: 依赖 internal/workboard 的 NewTask/Client.AddTask（`add <title> [--repo] [--desc] [--research|--chat] [--after] [--delegate]`）、workboardWrite 单一写通道、s.git.root（当前 Project 的 Git 仓库）
 * [OUTPUT]: newTaskDraft、openWorkboardAdd（当前 Project 的仓库）/openWorkboardAddIn（按 Project 名）、newTaskDialog（模态：标题、描述、Task·Research·Chat 三选一、After、立即启动 Agent）、submitNewTask
 * [POS]: New Task 的唯一入口模态，取代原来只问标题的文本对话框；Research 与 Chat 互斥（插件里 chat 优先，故不让用户同时选）；描述/After 的校验与拒绝全归插件（`--after` 先于创建检查，拒绝原文透传，不创建任何东西）；按仓库根目录创建时，提交瞬间仓库已变则拒绝
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

// Kinds of new task, in the segmented control's order.
const (
	newTaskPlain = iota
	newTaskResearch
	newTaskChat
)

// newTaskDraft is the open New Task dialog. repo is a Project name, or (with
// byRoot) the root of the Git repository the dialog opened for.
type newTaskDraft struct {
	open        bool
	repo        string
	byRoot      bool
	title, desc string
	after       string
	kind        int
	delegate    bool
}

// openWorkboardAdd opens New Task for the current Project's Git repository;
// without one there is nowhere to put the task. delegate presets "Start an
// agent now".
func (s *Shell) openWorkboardAdd(delegate bool) {
	if s.git == nil || s.git.root == "" {
		s.pendingToast = workboardNoRepoToast
		return
	}
	s.hideQuickPanel()
	s.workboard.newTask = newTaskDraft{open: true, repo: s.git.root, byRoot: true, delegate: delegate}
}

// openWorkboardAddIn opens New Task inside a Project the user is looking at;
// the plugin resolves the name, so no Git repository is needed.
func (s *Shell) openWorkboardAddIn(project string, delegate bool) {
	s.hideQuickPanel()
	s.workboard.newTask = newTaskDraft{open: true, repo: project, delegate: delegate}
}

// newTaskDialog renders the dialog while it is open.
func (s *Shell) newTaskDialog(c *ui.Context) {
	d := &s.workboard.newTask
	if !d.open {
		return
	}
	ui.Modal(c, &d.open, func() {
		sp := Spacing()
		t := c.Theme()
		ui.Column(c).Width(420).MaxWidth(480).Gap(sp.L).Children(func() {
			modalTitle(c, "New Task")
			ui.TextInput(c, &d.title).Label("Title").Placeholder("Title").AutoFocus().FillWidth()
			ui.TextArea(c, &d.desc).Label("Description").Placeholder("Description (optional)").FillWidth().Height(72)
			ui.Segmented(c, &d.kind, "Task", "Research only", "Chat only").Label("Kind")
			ui.TextInput(c, &d.after).Label("After").Placeholder("Wait for tasks, e.g. r/12, r/13 (optional)").FillWidth()
			ui.Checkbox(c, &d.delegate, "Start an agent now")
			switch d.kind {
			case newTaskResearch:
				ui.Text(c, "Changes nothing in the repo; the agent writes a report.").FontSize(Typography().Caption).TextColor(t.TextMuted)
			case newTaskChat:
				ui.Text(c, "A conversation that ends when you say.").FontSize(Typography().Caption).TextColor(t.TextMuted)
			}
			ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					d.open = false
				}
				if ui.PrimaryButton(c, "Add").Clicked() && strings.TrimSpace(d.title) != "" {
					s.submitNewTask()
				}
			})
		})
	})
}

// submitNewTask closes the dialog and adds the task it describes.
func (s *Shell) submitNewTask() {
	d := s.workboard.newTask
	s.workboard.newTask = newTaskDraft{}
	if !s.workboardAvailable() {
		return
	}
	if d.byRoot && (s.git == nil || s.git.root != d.repo) {
		s.pendingToast = "Repository changed. Reopen the dialog to continue."
		return
	}
	var after []string
	for _, ref := range strings.FieldsFunc(d.after, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		after = append(after, ref)
	}
	task := workboard.NewTask{
		Title: d.title, Repo: d.repo, Description: d.desc,
		Research: d.kind == newTaskResearch, Chat: d.kind == newTaskChat,
		After: after, Delegate: d.delegate,
	}
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("work board write requested", "op", "add", "delegate", d.delegate, "research", task.Research, "chat", task.Chat, "after", len(after))
	s.workboardWrite("add", func(ctx context.Context) error { return client.AddTask(ctx, task) }, nil)
}
