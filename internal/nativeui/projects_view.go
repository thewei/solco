package nativeui

/**
 * [INPUT]: 依赖 workboard_projects 的 workboardProject（board 缓存分组）、workboard_page 的 detail/changes/settings 缓存、workboard_library 的库与报告缓存、workboard_actions 的 workboardActionsForTask、workboard_automations 的清单、projects_section 的 taskAgentPane
 * [OUTPUT]: ProjectsView 及其子结构（Project 首页 / 任务页 / 新建任务面板 / 聊天目标的纯数据快照）、Shell.projectsView
 * [POS]: Projects WebView 页（2026-10-31 决策）的唯一数据口：全部由 Shell 已有缓存派生，不做 IO、不持有第二份状态；页面只读它画图，写操作走 projects_bridge 回到既有 Shell 函数；日志与快照都不带对话正文
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/projectlibrary"
	"github.com/thewei/solco/internal/workboard"
)

// ProjectsView is everything the Projects page draws, one value per publish.
type ProjectsView struct {
	// Seq numbers the published snapshots (0 in a build used for change detection).
	Seq   int64  `json:"seq"`
	Theme string `json:"theme"`
	// Mode is "project", "task" or "gone" (the page's subject left the board).
	Mode      string        `json:"mode"`
	Available bool          `json:"available"`
	Project   *ProjectPane  `json:"project,omitempty"`
	Task      *TaskPane     `json:"task,omitempty"`
	Compose   *ComposePane  `json:"compose,omitempty"`
	Chat      ProjectsChat  `json:"chat"`
	Counts    ProjectCounts `json:"counts"`
}

// ProjectsChat names who the embedded Chat talks to. Target is the pane id
// (empty when nobody is there to talk to) and changes when the page must ask
// the bridge to bind the Chat again.
type ProjectsChat struct {
	Target string `json:"target"`
	Label  string `json:"label"`
	// Why says, when Target is empty, what the page should tell the user.
	Why string `json:"why"`
	// Waiting: the agent is stopped on a prompt before it has a conversation
	// (a trust-this-folder question at its first start), so the Chat has
	// nothing to read; the page offers the terminal instead of a spinner.
	Waiting bool `json:"waiting"`
}

// ProjectCounts feed the header chips.
type ProjectCounts struct {
	NeedsYou int `json:"needsYou"`
	Ready    int `json:"ready"`
	Working  int `json:"working"`
}

// ProjectPane is the Project home.
type ProjectPane struct {
	Name        string          `json:"name"`
	Other       bool            `json:"other"`
	Pinned      bool            `json:"pinned"`
	Open        int             `json:"open"`
	DoneCount   int             `json:"doneCount"`
	Autopilot   bool            `json:"autopilot"`
	Coordinator CoordinatorPane `json:"coordinator"`
	Groups      []TaskGroupPane `json:"groups"`
	Done        []TaskRow       `json:"done"`
	Library     LibraryPane     `json:"library"`
	Reports     ReportsPane     `json:"reports"`
	Automations []AutomationRow `json:"automations"`
	// Working is the number of tasks "Stop All Tasks…" would stop.
	Working int `json:"working"`
}

// CoordinatorPane is the Project's coordinator as the plugin last reported.
type CoordinatorPane struct {
	State   string `json:"state"`
	Agent   string `json:"agent"`
	Running bool   `json:"running"`
	Said    string `json:"said"`
	Queued  int    `json:"queued"`
	Error   string `json:"error"`
}

// TaskGroupPane is one section of the Tasks list.
type TaskGroupPane struct {
	Key   string    `json:"key"`
	Label string    `json:"label"`
	Tone  string    `json:"tone"`
	Tasks []TaskRow `json:"tasks"`
}

// TaskRow is one task line.
type TaskRow struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Msg      string `json:"msg"`
	Group    string `json:"group"`
	Tone     string `json:"tone"`
	App      string `json:"app"`
	PR       string `json:"pr"`
	Round    int    `json:"round"`
	Pinned   bool   `json:"pinned"`
	Research bool   `json:"research"`
	Chat     bool   `json:"chat"`
	Stage    string `json:"stage"`
}

// LibraryPane is the Project's Library (Solco-owned; see internal/projectlibrary).
type LibraryPane struct {
	Available bool              `json:"available"`
	Items     []LibraryItemPane `json:"items"`
	// Block is what the instructions carry for this Project right now.
	Block string `json:"block"`
	Note  string `json:"note"`
	// AlwaysUsed / AlwaysCap measure the always-read budget in bytes.
	AlwaysUsed int    `json:"alwaysUsed"`
	AlwaysCap  int    `json:"alwaysCap"`
	TotalSize  int64  `json:"totalSize"`
	TotalText  string `json:"totalText"`
}

// LibraryItemPane is one library file.
type LibraryItemPane struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
	SizeText string `json:"sizeText"`
}

// ReportsPane lists tasks with a report.
type ReportsPane struct {
	Loading bool        `json:"loading"`
	Rows    []ReportRow `json:"rows"`
}

// ReportRow is one report.
type ReportRow struct {
	TaskID string `json:"taskId"`
	Title  string `json:"title"`
	Link   string `json:"link"`
	Local  bool   `json:"local"`
}

// AutomationRow is one automation of the Project.
type AutomationRow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Caption string `json:"caption"`
	On      bool   `json:"on"`
}

// TaskPane is the task page.
type TaskPane struct {
	TaskRow
	Project    string          `json:"project"`
	GroupLabel string          `json:"groupLabel"`
	Strip      []StripStepPane `json:"strip"`
	Actions    []ActionPane    `json:"actions"`
	Pinned     bool            `json:"pinned"`
	Loading    bool            `json:"loading"`
	Facts      []FactPane      `json:"facts"`
	Narrative  NarrativePane   `json:"narrative"`
	Review     *ReviewPane     `json:"review,omitempty"`
	ReportLink string          `json:"reportLink"`
	Worktree   string          `json:"worktree"`
	Changes    ChangesPane     `json:"changes"`
	Settings   SettingsPane    `json:"settings"`
	Context    ContextPane     `json:"context"`
	HasAgent   bool            `json:"hasAgent"`
	CanOpenPrj bool            `json:"canOpenProject"`
	DockTitle  string          `json:"dockTitle"`
	DockNote   string          `json:"dockNote"`
}

// StripStepPane is one segment of the run strip; State is pending, done,
// current or waiting.
type StripStepPane struct {
	Label string `json:"label"`
	State string `json:"state"`
}

// ActionPane is one of the plugin's verbs for the task. Dock marks the
// decisions that move the task forward; Primary the one that does.
type ActionPane struct {
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Primary bool   `json:"primary"`
	Dock    bool   `json:"dock"`
}

// FactPane is a label/value row of the Run tab.
type FactPane struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// NarrativePane is the plugin's free text on the task.
type NarrativePane struct {
	Description   string        `json:"description"`
	FailureTitle  string        `json:"failureTitle"`
	FailureDetail string        `json:"failureDetail"`
	FailureNext   string        `json:"failureNext"`
	Plan          string        `json:"plan"`
	LimitStopped  string        `json:"limitStopped"`
	Handovers     []string      `json:"handovers"`
	Attempts      []AttemptPane `json:"attempts"`
}

// AttemptPane is one parallel attempt.
type AttemptPane struct {
	Letter   string `json:"letter"`
	Agent    string `json:"agent"`
	Standing string `json:"standing"`
	Stat     string `json:"stat"`
}

// ReviewPane is the Review tab.
type ReviewPane struct {
	Verdict  string        `json:"verdict"`
	Failed   bool          `json:"failed"`
	By       string        `json:"by"`
	Text     string        `json:"text"`
	Findings []FindingPane `json:"findings"`
	Rounds   []string      `json:"rounds"`
}

// FindingPane is one reviewer finding.
type FindingPane struct {
	Severity string `json:"severity"`
	Subject  string `json:"subject"`
	Where    string `json:"where"`
	Proof    string `json:"proof"`
}

// ChangesPane is the Changes tab (uncommitted work in the worktree).
type ChangesPane struct {
	Loaded    bool             `json:"loaded"`
	Loading   bool             `json:"loading"`
	Files     []ChangeFilePane `json:"files"`
	More      int              `json:"more"`
	Additions int              `json:"additions"`
	Deletions int              `json:"deletions"`
}

// ChangeFilePane is one changed file.
type ChangeFilePane struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// SettingsPane is the Settings tab: what the task runs on beyond defaults.
type SettingsPane struct {
	Loaded bool          `json:"loaded"`
	Rows   []SettingPane `json:"rows"`
}

// SettingPane is one non-default setting.
type SettingPane struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	From  string `json:"from"`
}

// ContextPane is the Context tab.
type ContextPane struct {
	Description string `json:"description"`
	// Full is the whole description, for the Edit sheet (Description is clipped).
	Full     string            `json:"full"`
	CanEdit  bool              `json:"canEdit"`
	Tellable bool              `json:"tellable"`
	Live     bool              `json:"live"`
	Items    []ContextItemPane `json:"items"`
}

// ContextItemPane is a library file next to a task: whether the description
// already carries it, and whether a running agent can still be sent it.
type ContextItemPane struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	InBrief bool   `json:"inBrief"`
}

// ComposePane opens the New Task sheet.
type ComposePane struct {
	Project  string            `json:"project"`
	Delegate bool              `json:"delegate"`
	Items    []LibraryItemPane `json:"items"`
	// CanAttach is false when there is no library to attach from.
	CanAttach bool `json:"canAttach"`
}

// groupKey names a group for the page.
func groupKey(g workboard.Group) string {
	switch g {
	case workboard.GroupNeedsYou:
		return "needs"
	case workboard.GroupReady:
		return "ready"
	case workboard.GroupWorking:
		return "working"
	case workboard.GroupTodo:
		return "todo"
	}
	return "done"
}

// groupTone is the status colour word of a group: shape says the kind, colour
// the urgency (the page owns the palette).
func groupTone(g workboard.Group) string {
	switch g {
	case workboard.GroupNeedsYou:
		return "attention"
	case workboard.GroupReady:
		return "ready"
	case workboard.GroupWorking:
		return "working"
	}
	return "idle"
}

// taskRow is one task line with the group it sits in.
func (s *Shell) taskRow(task workboard.Task, g workboard.Group) TaskRow {
	return TaskRow{
		ID: task.ID, Title: fallbackText(task.Title, task.ID), Msg: task.Msg,
		Group: groupKey(g), Tone: groupTone(g), App: task.App, PR: task.PRNumber(),
		Round: task.ReviewRound, Pinned: s.workboard.board.extras.pinnedTasks[task.ID],
		Research: task.Research, Chat: task.Chat, Stage: task.Stage,
	}
}

// projectsView builds the page's snapshot on the UI lane from cached state.
func (s *Shell) projectsView() ProjectsView {
	v := ProjectsView{Theme: s.chatBridgeTheme(), Available: s.workboardAvailable()}
	if !v.Available {
		v.Mode = "gone"
		return v
	}
	if s.router.Path() == routeTask {
		v.Mode = "task"
	} else {
		v.Mode = "project"
	}
	var project workboardProject
	var found bool
	switch v.Mode {
	case "task":
		task, ok := s.workboardTaskByID(s.workboard.page.id)
		if !ok {
			v.Mode = "gone"
			return v
		}
		project, found = s.workboardProjectByName(fallbackText(task.Project(), workboardOtherProject))
		v.Task = s.taskPane(task)
	default:
		project, found = s.workboardProjectByName(s.workboard.project.name)
		if !found {
			v.Mode = "gone"
			return v
		}
	}
	if found {
		v.Project = s.projectPane(project)
		for _, g := range project.Groups {
			switch g.Group {
			case workboard.GroupNeedsYou:
				v.Counts.NeedsYou = len(g.Tasks)
			case workboard.GroupReady:
				v.Counts.Ready = len(g.Tasks)
			case workboard.GroupWorking:
				v.Counts.Working = len(g.Tasks)
			}
		}
	}
	v.Chat = s.projectsChat(project, found, v)
	if d := s.workboard.newTask; d.open && d.inPage {
		v.Compose = s.composePane(d)
	}
	return v
}

// projectsChatTarget is who the Chat should talk to: the task's agent on a
// task page, the Project's coordinator on its home.
func (s *Shell) projectsChatTarget(project workboardProject, found bool) (pane, label, why string) {
	if s.router.Path() == routeTask {
		id := s.workboard.page.id
		task, _ := s.workboardTaskByID(id)
		if card, ok := s.taskAgentCard(id); ok {
			return card.PaneID, id + "'s agent", ""
		}
		group := s.workboardTaskGroup(task)
		if group == workboard.GroupTodo {
			return "", id, "Nobody is on this task yet. Delegate it to start an agent."
		}
		return "", id, "This task's agent isn't open in this session."
	}
	if !found {
		return "", "", ""
	}
	if card, ok := s.projectCoordinatorCard(project); ok {
		return card.PaneID, project.Name + " coordinator", ""
	}
	if project.Name == workboardOtherProject {
		return "", "", "Tasks without a repository have no coordinator."
	}
	return "", project.Name + " coordinator", "The coordinator isn't running. Start it to plan and delegate by talking."
}

func (s *Shell) projectsChat(project workboardProject, found bool, v ProjectsView) ProjectsChat {
	pane, label, why := s.projectsChatTarget(project, found)
	chat := ProjectsChat{Target: pane, Label: label, Why: why}
	if card, ok := s.agentCardForPane(pane); ok && pane != "" {
		chat.Waiting = card.ConversationID == "" && (card.Attention == agent.OpNeedsAttention || card.RuntimePhase == agent.PhaseBlocked)
	}
	return chat
}

// projectPane projects one Project.
func (s *Shell) projectPane(project workboardProject) *ProjectPane {
	p := &ProjectPane{
		Name: project.Name, Other: project.Name == workboardOtherProject, Pinned: project.Pinned,
		Open: project.Open, DoneCount: len(project.Done),
		Autopilot: s.workboard.board.extras.autopilot[project.Name],
		Coordinator: CoordinatorPane{
			State: project.Coord.State, Agent: project.Coord.Agent, Running: project.Coord.Running(),
			Said: clipPreview(strings.TrimSpace(string(project.Coord.Said)), 800, 6), Queued: project.Coord.Queued, Error: project.Coord.Error,
		},
		Groups: []TaskGroupPane{}, Done: []TaskRow{},
	}
	for _, group := range project.Groups {
		var queued, rest []TaskRow
		for _, task := range group.Tasks {
			row := s.taskRow(task, group.Group)
			if group.Group == workboard.GroupTodo && task.Stage == "after" {
				queued = append(queued, row)
				continue
			}
			rest = append(rest, row)
		}
		if group.Group == workboard.GroupWorking {
			p.Working = len(rest)
		}
		if len(rest) > 0 {
			p.Groups = append(p.Groups, TaskGroupPane{Key: groupKey(group.Group), Label: group.Group.Label(), Tone: groupTone(group.Group), Tasks: rest})
		}
		if len(queued) > 0 {
			p.Groups = append(p.Groups, TaskGroupPane{Key: "queued", Label: "Queued", Tone: "idle", Tasks: queued})
		}
	}
	for _, task := range project.Done {
		p.Done = append(p.Done, s.taskRow(task, workboard.GroupDone))
	}
	p.Library = s.libraryPane(project.Name)
	p.Reports = s.reportsPane(project)
	p.Automations = []AutomationRow{}
	for _, a := range s.projectAutomationList(project.Name) {
		p.Automations = append(p.Automations, AutomationRow{ID: a.ID, Name: a.Name, Caption: automationCaption(a), On: a.On})
	}
	return p
}

// libraryItemPane maps one stored item.
func libraryItemPane(item projectlibrary.Item) LibraryItemPane {
	return LibraryItemPane{ID: item.ID, Name: item.Name, Mode: string(item.Mode), Kind: item.Kind, Size: item.Size, SizeText: sizeText(item.Size)}
}

// libraryPane is the Library view of a Project; its items are the cache read
// when the Project page opened.
func (s *Shell) libraryPane(project string) LibraryPane {
	lib := &s.workboard.library
	out := LibraryPane{Items: []LibraryItemPane{}, AlwaysCap: projectlibrary.MaxAlwaysBytes}
	if s.libraryStore() == nil || project == workboardOtherProject {
		return out
	}
	out.Available = true
	if lib.project != project {
		return out
	}
	out.Block, out.Note = lib.block, lib.note
	items := append([]projectlibrary.Item(nil), lib.items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Added.After(items[j].Added) })
	for _, item := range items {
		out.Items = append(out.Items, libraryItemPane(item))
		out.TotalSize += item.Size
		if item.Mode == projectlibrary.Always {
			out.AlwaysUsed += int(item.Size)
		}
	}
	out.TotalText = sizeText(out.TotalSize)
	return out
}

// reportsPane lists the reports the cached details carry.
func (s *Shell) reportsPane(project workboardProject) ReportsPane {
	out := ReportsPane{Loading: s.workboard.library.loading, Rows: []ReportRow{}}
	for _, r := range s.projectReports(project) {
		out.Rows = append(out.Rows, ReportRow{
			TaskID: r.Task.ID, Title: fallbackText(r.Task.Title, r.Task.ID), Link: r.Link,
			Local: strings.HasPrefix(r.Link, "/"),
		})
	}
	return out
}

func stepState(st workboard.StepState) string {
	switch st {
	case workboard.StepDone:
		return "done"
	case workboard.StepCurrent:
		return "current"
	case workboard.StepWaiting:
		return "waiting"
	}
	return "pending"
}

// taskPane projects the open task from the board row plus the freshest details.
func (s *Shell) taskPane(task workboard.Task) *TaskPane {
	group := s.workboardTaskGroup(task)
	page := &s.workboard.page
	detail := page.detail
	if detail == nil {
		if d, ok := s.workboard.board.details[task.ID]; ok {
			cp := d.detail
			detail = &cp
		}
	}
	t := &TaskPane{
		TaskRow: s.taskRow(task, group), Project: task.Project(), GroupLabel: group.Label(),
		Pinned: s.workboard.board.extras.pinnedTasks[task.ID], Loading: page.loading && detail == nil,
		Strip: []StripStepPane{}, Actions: []ActionPane{}, Facts: []FactPane{},
	}
	if !task.Chat && !task.Research {
		for _, step := range workboard.RunStrip(task) {
			t.Strip = append(t.Strip, StripStepPane{Label: step.Label, State: stepState(step.State)})
		}
	}
	dock, rest := splitTaskDock(workboardActionsForTask(task, group, detail))
	for _, a := range dock {
		t.Actions = append(t.Actions, ActionPane{Kind: a.Kind, Label: a.Label, Primary: isPrimaryWrite(a.Kind), Dock: true})
	}
	for _, a := range rest {
		t.Actions = append(t.Actions, ActionPane{Kind: a.Kind, Label: a.Label, Primary: isPrimaryWrite(a.Kind)})
	}
	t.DockTitle, t.DockNote = group.Label(), task.Msg
	switch {
	case task.PlanReady():
		t.DockTitle, t.DockNote = "Plan ready", "Read the plan under Run, then approve it or send it back with words."
	case task.NeedsPick():
		t.DockTitle, t.DockNote = "Attempts to compare", "Several attempts passed. Keep one."
	}
	t.DockNote = clipPreview(strings.TrimSpace(t.DockNote), 400, 3)
	_, t.HasAgent = s.taskAgentPane(task.ID)
	t.Facts = s.taskFacts(task, group, detail)
	if detail != nil {
		t.Worktree = detail.Worktree
		t.ReportLink = detail.ReportLink
		t.CanOpenPrj = projectForWorktree(s.projection, detail.Worktree) != ""
		t.Narrative = taskNarrative(detail, group)
		t.Review = taskReview(detail)
	}
	t.Narrative.Handovers = nonNil(t.Narrative.Handovers)
	if t.Narrative.Attempts == nil {
		t.Narrative.Attempts = []AttemptPane{}
	}
	t.Changes = s.changesPane(detail)
	t.Settings = s.settingsPane()
	t.Context = s.contextPane(task, group, detail)
	return t
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// taskFacts is the Run tab's state group: only what the plugin reported.
func (s *Shell) taskFacts(task workboard.Task, group workboard.Group, d *workboard.Detail) []FactPane {
	out := []FactPane{{Label: "Status", Value: group.Label()}}
	add := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			out = append(out, FactPane{Label: label, Value: value})
		}
	}
	add("Note", task.Msg)
	add("Stage", task.Stage)
	if d != nil {
		add("Agent", d.Agent)
		add("Branch", d.Branch)
		add("Pull request", d.PR)
		add("Queue", d.QueueWords)
		add("Time limit", d.LimitTime)
		add("Token limit", d.LimitTokens)
	} else {
		add("Pull request", task.PR)
	}
	return out
}

// taskNarrative is the free text, clipped as the native page did; the failure
// record is hidden for a task nobody is on (to do) or that is finished.
func taskNarrative(d *workboard.Detail, g workboard.Group) NarrativePane {
	n := NarrativePane{
		Description: clipPreview(d.Description, 1600, 14), Plan: clipPreview(d.Plan, 1600, 40),
		LimitStopped: clipPreview(d.LimitStopped, 1600, 4), Handovers: d.Handovers,
	}
	if d.Failure && g != workboard.GroupTodo && g != workboard.GroupDone {
		n.FailureTitle, n.FailureDetail, n.FailureNext = d.FailureTitle, clipPreview(d.FailureDetail, 1600, 14), d.FailureNext
	}
	for _, a := range d.Attempts {
		n.Attempts = append(n.Attempts, AttemptPane{Letter: strings.ToUpper(a.Letter), Agent: a.Agent, Standing: a.Standing, Stat: a.Stat})
	}
	return n
}

// taskReview is the Review tab, nil when the plugin reported no review.
func taskReview(d *workboard.Detail) *ReviewPane {
	if d.ReviewText == "" && d.ReviewBy == "" && !d.ReviewFailed && len(d.Findings) == 0 && len(d.Reviews) == 0 {
		return nil
	}
	verdict := "Passed"
	if d.ReviewFailed {
		verdict = "Did not pass"
	}
	if d.ReviewFindings > 0 {
		verdict += " · " + strconv.Itoa(d.ReviewFindings) + " finding" + pluralS(d.ReviewFindings)
	}
	r := &ReviewPane{Verdict: verdict, Failed: d.ReviewFailed, By: d.ReviewBy, Text: clipPreview(d.ReviewText, 1600, 12), Findings: []FindingPane{}, Rounds: []string{}}
	for _, f := range d.Findings {
		if f.Subject == "" {
			continue
		}
		r.Findings = append(r.Findings, FindingPane{
			Severity: strings.ToUpper(fallbackText(f.Severity, "major")), Subject: clipPreview(f.Subject, 400, 4),
			Where: clipPreview(f.Where, 400, 3), Proof: clipPreview(f.Proof, 400, 3),
		})
	}
	for i := 0; i+1 < len(d.Reviews); i++ {
		rv := d.Reviews[i]
		line := "Round " + strconv.Itoa(i+1) + " · " + map[bool]string{true: "Did not pass", false: "Passed"}[rv.Failed]
		if n := len(rv.Findings); n > 0 {
			line += " · " + strconv.Itoa(n) + " finding" + pluralS(n)
		}
		r.Rounds = append(r.Rounds, line)
	}
	return r
}

// changesFilesCap bounds the file lines the page draws.
const changesFilesCap = 30

func (s *Shell) changesPane(d *workboard.Detail) ChangesPane {
	page := &s.workboard.page
	out := ChangesPane{Files: []ChangeFilePane{}, Loading: page.changesLoading}
	if d == nil || d.Worktree == "" || page.changes == nil || page.changesFor != d.Worktree {
		return out
	}
	out.Loaded = true
	out.Additions, out.Deletions = page.changes.TotalAdditions, page.changes.TotalDeletions
	for i, f := range page.changes.Files {
		if i == changesFilesCap {
			out.More = len(page.changes.Files) - changesFilesCap
			break
		}
		out.Files = append(out.Files, ChangeFilePane{Path: f.Path, Additions: f.Additions, Deletions: f.Deletions})
	}
	return out
}

func (s *Shell) settingsPane() SettingsPane {
	page := &s.workboard.page
	out := SettingsPane{Rows: []SettingPane{}}
	if page.settings == nil {
		return out
	}
	out.Loaded = true
	for _, setting := range page.settings.Settings {
		if setting.Place == "builtin" {
			continue
		}
		out.Rows = append(out.Rows, SettingPane{Name: setting.Name, Value: clipPreview(setting.ValueText(), 120, 2), From: setting.From})
	}
	return out
}

// contextPane is the Context tab: the description and the library files that
// could still be handed to the agent.
func (s *Shell) contextPane(task workboard.Task, group workboard.Group, d *workboard.Detail) ContextPane {
	live := group != workboard.GroupTodo && group != workboard.GroupDone
	out := ContextPane{Items: []ContextItemPane{}, Live: live}
	description := ""
	if d != nil {
		description = d.Description
		out.CanEdit = group != workboard.GroupDone
	}
	out.Description = clipPreview(strings.TrimSpace(description), 1600, 14)
	out.Full = description
	if _, ok := s.taskAgentPane(task.ID); ok {
		out.Tellable = live
	}
	project := task.Project()
	store := s.libraryStore()
	lib := &s.workboard.library
	if store == nil || project == "" || lib.project != project {
		return out
	}
	for _, item := range lib.items {
		path, err := store.Path(project, item)
		out.Items = append(out.Items, ContextItemPane{ID: item.ID, Name: item.Name, Mode: string(item.Mode), InBrief: err == nil && strings.Contains(description, path)})
	}
	return out
}

// composePane is the New Task sheet's data: the Project's library to attach.
func (s *Shell) composePane(d newTaskDraft) *ComposePane {
	project := s.projectOfDraft(d)
	out := &ComposePane{Project: project, Delegate: d.delegate, Items: []LibraryItemPane{}}
	if project == "" {
		out.Project = filepath.Base(d.repo)
		return out
	}
	lib := s.libraryPane(project)
	out.CanAttach = lib.Available
	out.Items = lib.Items
	return out
}
