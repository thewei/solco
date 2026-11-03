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
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/projectlibrary"
	"github.com/thewei/solco/internal/workboard"
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
	attach      map[string]bool // Library item ids sent with this task
	// inPage: the Projects page draws the sheet (the dialog below is the
	// fallback for a Project that has no page, or the Board view).
	inPage bool
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
	s.openCompose(newTaskDraft{open: true, repo: s.git.root, byRoot: true, delegate: delegate})
}

// openWorkboardAddIn opens New Task inside a Project the user is looking at;
// the plugin resolves the name, so no Git repository is needed.
func (s *Shell) openWorkboardAddIn(project string, delegate bool) {
	s.hideQuickPanel()
	s.openCompose(newTaskDraft{open: true, repo: project, delegate: delegate})
}

// openCompose opens New Task. A Project the board knows gets the sheet on its
// Projects page (the page is opened first when another page is showing); one it
// does not know yet, and the Board view, keep the dialog.
func (s *Shell) openCompose(d newTaskDraft) {
	s.workboard.newTask = d
	project := s.projectOfDraft(d)
	if project != "" && !s.workboardBoardMode() {
		if !s.projectsHosts(project) {
			s.openWorkboardProjectPage(project)
		}
		s.workboard.newTask.inPage = true
	}
	s.reloadLibrary(s.newTaskProject())
}

// newTaskProject is the Project whose library the open dialog offers: its
// name, or for a Git root the board Project of the same folder name. Empty
// when the board has no such Project (nothing to attach to yet).
func (s *Shell) newTaskProject() string { return s.projectOfDraft(s.workboard.newTask) }

func (s *Shell) projectOfDraft(d newTaskDraft) string {
	name := d.repo
	if d.byRoot {
		name = filepath.Base(d.repo)
	}
	if _, ok := s.workboardProjectByName(name); !ok || name == workboardOtherProject {
		return ""
	}
	return name
}

// newTaskContext is the dialog's Context section: tick the Library files this
// task should read, add new ones (kept as Task items), and see what the agent
// will be handed.
func (s *Shell) newTaskContext(c *ui.Context, d *newTaskDraft, project string) {
	t := c.Theme()
	sp := Spacing()
	lib := &s.workboard.library
	if d.attach == nil {
		d.attach = map[string]bool{}
	}
	ui.Column(c).FillWidth().Gap(sp.S).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			groupLabel(c, "Context")
			ui.Box(c).Grow(1)
			if regularButton(c, "Add Files…").Clicked() {
				s.pickIntoNewTask(project)
			}
		})
		for _, item := range lib.items {
			on := d.attach[item.ID]
			label := item.Name + " · " + string(item.Mode)
			if ui.Checkbox(c, &on, label).Changed() {
				d.attach[item.ID] = on
			}
		}
		brief := libraryBrief(lib.items, d.attach)
		ui.Column(c).FillWidth().Gap(2).Children(func() {
			ui.Text(c, "What the agent will get").FontSize(Typography().Caption).FontWeight(WeightMedium)
			line := func(text string) {
				ui.Text(c, text).FontSize(Typography().Caption).TextColor(t.TextMuted)
			}
			line("This task: " + fallbackText(strings.TrimSpace(d.title), "(untitled)") + briefDescSuffix(d.desc))
			if len(brief.Attached) > 0 {
				line("Attached: " + strings.Join(brief.Attached, ", "))
			}
			if len(brief.Always) > 0 {
				line("Always in its instructions: " + strings.Join(brief.Always, ", "))
			}
			if brief.OnDemand > 0 {
				line(itoa(brief.OnDemand) + " more file" + pluralS(brief.OnDemand) + " it can open from INDEX.md")
			}
			line("Its access follows the plugin's worker access setting (Settings → Work Board).")
		})
	})
}

func briefDescSuffix(desc string) string {
	if strings.TrimSpace(desc) == "" {
		return ""
	}
	return " + your description"
}

// pickIntoNewTask adds files to the Project's library as Task items and ticks
// them for the open dialog.
func (s *Shell) pickIntoNewTask(project string) {
	if s.libraryStore() == nil {
		s.pendingToast = "The library needs the app's data folder"
		return
	}
	go func() {
		paths, err := pickLibraryFiles()
		s.applyOnUI(func() {
			if err != nil || len(paths) == 0 {
				return
			}
			added := s.addLibraryPaths(project, projectlibrary.Task, paths)
			d := &s.workboard.newTask
			if d.attach == nil {
				d.attach = map[string]bool{}
			}
			for _, item := range added {
				d.attach[item.ID] = true
			}
		})
	}()
}

// newTaskDialog renders the dialog while it is open.
func (s *Shell) newTaskDialog(c *ui.Context) {
	d := &s.workboard.newTask
	if !d.open || d.inPage {
		return
	}
	ui.Modal(c, &d.open, func() {
		sp := Spacing()
		t := c.Theme()
		ui.Column(c).Width(420).MaxWidth(480).Gap(sp.L).Children(func() {
			modalTitle(c, "New Task")
			captionedField(c, "Title", "", func() {
				textField(c, &d.title).Label("Title").Placeholder("Title").AutoFocus().FillWidth()
			})
			captionedField(c, "Description", "", func() {
				textArea(c, &d.desc).Label("Description").Placeholder("Description (optional)").FillWidth().Height(72)
			})
			captionedField(c, "Kind", "", func() {
				segmentedControl(c, &d.kind, "Task", "Research only", "Chat only").Label("Kind")
			})
			captionedField(c, "After", "", func() {
				textField(c, &d.after).Label("After").Placeholder("Wait for tasks, e.g. r/12, r/13 (optional)").FillWidth()
			})
			if project := s.newTaskProject(); project != "" && s.libraryStore() != nil && d.kind != newTaskChat {
				s.newTaskContext(c, d, project)
			}
			ui.Checkbox(c, &d.delegate, "Start an agent now")
			switch d.kind {
			case newTaskResearch:
				ui.Text(c, "Changes nothing in the repo; the agent writes a report.").FontSize(Typography().Caption).TextColor(t.TextMuted)
			case newTaskChat:
				ui.Text(c, "A conversation that ends when you say.").FontSize(Typography().Caption).TextColor(t.TextMuted)
			}
			ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
				if regularButton(c, "Cancel").Clicked() {
					d.open = false
				}
				if regularPrimaryButton(c, "Add").Clicked() && strings.TrimSpace(d.title) != "" {
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
	s.submitNewTaskDraft(d)
}

// submitNewTaskDraft adds the task d describes (the dialog's and the page's
// shared path).
func (s *Shell) submitNewTaskDraft(d newTaskDraft) {
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
	// Attached Library files travel as @path lines in the description; the
	// store owns the paths, the agent reads them itself.
	desc := d.desc
	if project := s.projectOfDraft(d); project != "" && len(d.attach) > 0 {
		if store := s.libraryStore(); store != nil {
			ids := sortedIDs(d.attach)
			if refs, err := store.TaskRefs(project, ids); err == nil && refs != "" {
				desc = strings.TrimSpace(desc + "\n\n" + refs)
			}
		}
	}
	task := workboard.NewTask{
		Title: d.title, Repo: d.repo, Description: desc,
		Research: d.kind == newTaskResearch, Chat: d.kind == newTaskChat,
		After: after, Delegate: d.delegate,
	}
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("work board write requested", "op", "add", "delegate", d.delegate, "research", task.Research, "chat", task.Chat, "after", len(after))
	s.workboardWrite("add", func(ctx context.Context) error { return client.AddTask(ctx, task) }, nil)
}
