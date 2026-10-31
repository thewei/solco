package nativeui

/**
 * [INPUT]: 依赖 projects_view 的快照、workboard_actions 的 startWorkboardAction/setWorkboardPin、workboard_manage 的 startBoardManage/runBoardManage、workboard_library 的库操作、workboard_newtask 的 submitNewTaskDraft、saveTaskDescription、chat_live 的 bindLiveAgent/bindChatConversation
 * [OUTPUT]: ProjectsBridge 绑定服务（Snapshot / 导航 / 写命令）、Shell.bindProjectsChat
 * [POS]: Projects WebView 页（产品决策 2026-10-31）唯一表现层桥：页面只读快照，所有写命令回到既有 Shell 函数（确认弹窗、插件拒绝原文、toast 都沿用原生那一路，且原生弹窗/toast 画在 WebView 之上）；桥没有新增 Herdr 方法、不读插件私有存储；方法在自己的 goroutine 上跑，状态读写统一经 callOnWindow 回到 UI 车道；日志只记操作名与稳定 id，不含任务描述/对话正文
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/projectlibrary"
	"github.com/thewei/solco/internal/workboard"
)

// ProjectsBridge is the bound service of the Projects WebView page.
type ProjectsBridge struct {
	shell *Shell
}

// NewProjectsBridge builds the Projects page's bound service.
func NewProjectsBridge(shell *Shell) *ProjectsBridge { return &ProjectsBridge{shell: shell} }

// ComposeInput is the New Task sheet as the page filled it in.
type ComposeInput struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	After       string   `json:"after"`
	Kind        string   `json:"kind"` // task | research | chat
	Delegate    bool     `json:"delegate"`
	Attach      []string `json:"attach"` // Library item ids
}

// on runs fn on the window lane and waits.
func (b *ProjectsBridge) on(fn func()) { b.shell.callOnWindow(fn) }

// Snapshot is the page's first read (later changes arrive as events).
func (b *ProjectsBridge) Snapshot() ProjectsView {
	var v ProjectsView
	b.on(func() {
		v = b.shell.projectsView()
		b.shell.projectsSeq++
		v.Seq = b.shell.projectsSeq
	})
	return v
}

// OpenTask opens a task of the board.
func (b *ProjectsBridge) OpenTask(id string) { b.on(func() { b.shell.openWorkboardTaskPage(id) }) }

// OpenProject opens a Project's home.
func (b *ProjectsBridge) OpenProject(name string) {
	b.on(func() { b.shell.openWorkboardProjectPage(name) })
}

// Back leaves the page the way the title bar's Back does.
func (b *ProjectsBridge) Back() { b.on(func() { b.shell.router.Back() }) }

// Act runs one of the plugin's verbs on a task; the confirm dialogs, pickers
// and refusals are the native ones.
func (b *ProjectsBridge) Act(kind, id string) {
	b.on(func() {
		task, ok := b.shell.workboardTaskByID(id)
		if !ok {
			return
		}
		group := b.shell.workboardTaskGroup(task)
		for _, a := range workboardActionsForTask(task, group, b.shell.workboard.page.detail) {
			if a.Kind == kind {
				b.shell.startWorkboardAction(a, task)
				return
			}
		}
	})
}

// Pin stars or unstars a task or a Project.
func (b *ProjectsBridge) Pin(target string, pinned bool) {
	b.on(func() { b.shell.setWorkboardPin(target, pinned) })
}

// Manage runs a Project-level verb: confirms first where the verb is
// destructive or spends tokens.
func (b *ProjectsBridge) Manage(op, target string) {
	b.on(func() { b.shell.projectsManage(op, target) })
}

// projectsManage maps the page's verbs onto the one management path.
func (s *Shell) projectsManage(op, target string) {
	project, _ := s.workboardProjectByName(target)
	switch op {
	case "archive-done":
		s.startBoardManage(boardArchiveDoneConfirm, target, len(project.Done))
	case "archive-project":
		s.startBoardManage(boardArchiveProjectConfirm, target, project.Open+len(project.Done))
	case "coordinator-new":
		s.startBoardManage(boardCoordinatorNewConfirm, target, 0)
	case "autopilot-on":
		s.startBoardManage(boardAutopilotOnConfirm, target, 0)
	case "automation-run":
		s.startBoardManage(boardAutomationRunConfirm, target, 0)
	case "automation-remove":
		s.startBoardManage(boardAutomationRemoveConfirm, target, 0)
	case "stop-all":
		if n := len(s.projectWorkingTasks(project)); n > 0 {
			s.openConfirm(workboardStopAllConfirm, target, "Stop all "+itoa(n)+" working task"+pluralS(n)+"?",
				"Each is stopped on its own, one after another; it is not one atomic step. A task that finishes first is left alone, and the coordinator keeps running.")
		}
	case "automation-new":
		s.openWorkboardAutomation(target)
	case "coordinator-start", "coordinator-restart", "coordinator-stop", "autopilot-off", "automation-on", "automation-off":
		s.runBoardManage(op, target)
	}
}

// LoadTab reads what a tab needs once: "changes", "settings" (open task) or
// "reports" (open Project).
func (b *ProjectsBridge) LoadTab(tab string) {
	b.on(func() {
		s := b.shell
		switch tab {
		case "changes":
			if d := s.workboard.page.detail; d != nil {
				s.loadTaskChanges(d.Worktree)
			}
		case "settings":
			s.loadTaskSettings()
		case "reports":
			if project, ok := s.workboardProjectByName(s.projectsProject()); ok {
				s.loadProjectReports(project)
			}
		case "changes-refresh":
			if d := s.workboard.page.detail; d != nil {
				s.workboard.page.changesFor = ""
				s.loadTaskChanges(d.Worktree)
			}
		}
	})
}

// projectsProject is the Project the page is on: the open Project's name or
// the open task's Project.
func (s *Shell) projectsProject() string {
	if s.router.Path() == routeTask {
		if task, ok := s.workboardTaskByID(s.workboard.page.id); ok {
			return task.Project()
		}
		return ""
	}
	return s.workboard.project.name
}

// OpenCompose opens the New Task sheet for the page's Project.
func (b *ProjectsBridge) OpenCompose(delegate bool) {
	b.on(func() { b.shell.openWorkboardAddIn(b.shell.projectsProject(), delegate) })
}

// CloseCompose dismisses the New Task sheet.
func (b *ProjectsBridge) CloseCompose() {
	b.on(func() { b.shell.workboard.newTask = newTaskDraft{} })
}

// AddTask adds the task the sheet describes.
func (b *ProjectsBridge) AddTask(in ComposeInput) {
	b.on(func() {
		s := b.shell
		d := s.workboard.newTask
		kind := newTaskPlain
		switch in.Kind {
		case "research":
			kind = newTaskResearch
		case "chat":
			kind = newTaskChat
		}
		d.title, d.desc, d.after, d.kind, d.delegate = in.Title, in.Description, in.After, kind, in.Delegate
		d.attach = map[string]bool{}
		for _, id := range in.Attach {
			d.attach[id] = true
		}
		if strings.TrimSpace(d.title) == "" {
			return
		}
		s.workboard.newTask = newTaskDraft{}
		s.submitNewTaskDraft(d)
	})
}

// PickFiles asks for files and adds them to the page's Project Library under
// mode ("always", "reference" or "task"); it returns the ids added so a New
// Task sheet can tick them.
func (b *ProjectsBridge) PickFiles(mode string) []string {
	var project string
	b.on(func() { project = b.shell.projectsProject() })
	if project == "" || project == workboardOtherProject {
		return nil
	}
	paths, err := pickLibraryFiles()
	if err != nil || len(paths) == 0 {
		return nil
	}
	var ids []string
	b.on(func() {
		if b.shell.libraryStore() == nil {
			b.shell.pendingToast = "The library needs the app's data folder"
			return
		}
		for _, item := range b.shell.addLibraryPaths(project, projectlibrary.Mode(mode), paths) {
			ids = append(ids, item.ID)
		}
	})
	return ids
}

// SetLibraryMode changes how a library item reaches the agents.
func (b *ProjectsBridge) SetLibraryMode(id, mode string) {
	b.on(func() {
		if project := b.shell.projectsProject(); project != "" {
			b.shell.setLibraryMode(project, id, projectlibrary.Mode(mode))
		}
	})
}

// RemoveLibraryItem deletes a library item.
func (b *ProjectsBridge) RemoveLibraryItem(id string) {
	b.on(func() {
		if project := b.shell.projectsProject(); project != "" {
			b.shell.removeLibraryItem(project, id)
		}
	})
}

// SendLibraryItem hands one library file to the open task's running agent.
func (b *ProjectsBridge) SendLibraryItem(id string) {
	b.on(func() {
		s := b.shell
		project, task := s.projectsProject(), s.workboard.page.id
		store := s.libraryStore()
		if store == nil || project == "" || task == "" {
			return
		}
		if refs, err := store.TaskRefs(project, []string{id}); err == nil && refs != "" {
			s.sendTaskMessage(task, "Read this file too.\n\n"+refs)
		}
	})
}

// SaveDescription replaces the task's description through the plugin and
// optionally tells the agent already at work.
func (b *ProjectsBridge) SaveDescription(id, text string, tell bool) {
	b.on(func() { b.shell.saveTaskDescription(id, text, tell) })
}

// OpenReport opens a report link (served page in the browser, file in the
// system's opener).
func (b *ProjectsBridge) OpenReport(link string) { b.on(func() { b.shell.openBoardLink(link) }) }

// OpenInWorkspace goes to the Chat target's pane in the Workspace (Terminal
// and the rest of the pane's tools).
func (b *ProjectsBridge) OpenInWorkspace() {
	b.on(func() {
		s := b.shell
		project, found := s.workboardProjectByName(fallbackText(s.projectsProject(), workboardOtherProject))
		if pane, _, _ := s.projectsChatTarget(project, found); pane != "" {
			s.openPane(pane)
		}
	})
}

// OpenProjectWorkspace opens the task's worktree as a Workspace project.
func (b *ProjectsBridge) OpenProjectWorkspace() {
	b.on(func() {
		s := b.shell
		if d := s.workboard.page.detail; d != nil {
			if name := projectForWorktree(s.projection, d.Worktree); name != "" {
				s.selectProject(name)
				s.showViewTerminal()
			}
		}
	})
}

// OpenWorkBoard opens the plugin's own board Tab.
func (b *ProjectsBridge) OpenWorkBoard() { b.on(func() { b.shell.openWorkBoard() }) }

// saveTaskDescription writes the new description, then (when asked) tells the
// agent. Validation and refusals are the plugin's.
func (s *Shell) saveTaskDescription(id, text string, tell bool) {
	if !s.workboardAvailable() {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	_, canTell := s.taskAgentPane(id)
	canTell = canTell && s.workboardTaskLive(id)
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	slog.Info("work board write requested", "op", "describe", "task", id, "tell", tell && canTell)
	s.workboardWrite("describe", func(ctx context.Context) error {
		if err := client.Describe(ctx, id, text); err != nil {
			return err
		}
		if tell && canTell {
			return client.Tell(ctx, id, "The task description changed. It now reads:\n\n"+text)
		}
		return nil
	}, func() {
		s.pendingToast = "Description saved"
		// The cached details still carry the old words until the next read.
		delete(s.workboard.board.details, id)
		if s.workboard.page.id == id {
			s.workboard.page.detail = nil
		}
	})
}

// workboardTaskLive reports a task with an agent that could be told something.
func (s *Shell) workboardTaskLive(id string) bool {
	task, ok := s.workboardTaskByID(id)
	if !ok {
		return false
	}
	g := s.workboardTaskGroup(task)
	return g != workboard.GroupTodo && g != workboard.GroupDone
}

// bindProjectsChat binds the embedded Chat to the page's target (the Project's
// coordinator, or the task's agent) through the ordinary live/conversation
// paths. Called on the UI lane; false says nobody is there or the agent has
// no chat source yet.
func (s *Shell) bindProjectsChat() bool {
	project, found := s.workboardProjectByName(fallbackText(s.projectsProject(), workboardOtherProject))
	pane, _, _ := s.projectsChatTarget(project, found)
	if pane == "" {
		return false
	}
	card, ok := s.agentCardForPane(pane)
	if !ok {
		return false
	}
	return s.bindChatToCard(card)
}

// bindChatToCard is openChat's binding without its navigation: the page
// embeds the Chat, so the route stays where it is.
func (s *Shell) bindChatToCard(card agent.AgentCardModel) bool {
	s.chatPaneID = card.PaneID
	if card.PaneID != "" {
		s.selectPane(card.PaneID)
	}
	if s.bindLiveAgent(card.Key) {
		return true
	}
	if card.ConversationID != "" {
		s.bindChatConversation(card.ConversationID)
		return true
	}
	if s.liveSourcePending(card.Key) {
		s.rebindWorkspaceChat()
		return true
	}
	return false
}

// projectsChatSync binds the Chat to the page's target when it is not on it
// yet, from a follow-up update (never inside a frame build). A target whose
// bind failed is remembered so the page says so instead of retrying forever.
func (s *Shell) projectsChatSync() {
	if s.win == nil || s.projectsBinding {
		return
	}
	project, found := s.workboardProjectByName(fallbackText(s.projectsProject(), workboardOtherProject))
	pane, _, _ := s.projectsChatTarget(project, found)
	if pane == "" || s.chatPaneID == pane || s.projectsChatFailed == pane {
		return
	}
	s.projectsBinding = true
	s.win.Update(func() {
		s.projectsBinding = false
		if !s.bindProjectsChat() {
			s.projectsChatFailed = pane
			s.projectsPublish()
		} else if s.projectsChatFailed == pane {
			s.projectsChatFailed = ""
		}
	})
}

// sortedIDs is the ticked ids in a stable order.
func sortedIDs(set map[string]bool) []string {
	var ids []string
	for id, on := range set {
		if on {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
