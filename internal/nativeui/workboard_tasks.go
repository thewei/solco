package nativeui

/**
 * [INPUT]: 依赖 internal/workboard 的 Client（list/details）、GroupOf 保守分组、Shell 的 projection 与 workboardAvailable 门
 * [OUTPUT]: workboardBoard（任务列表缓存）、refreshWorkboardTasks（异步刷新，限频）、projectForWorktree（details.worktree → Herdr Project 的连接）、workboardGroups（面板分组）
 * [POS]: Tasks 页签的数据层；任务权威仍归插件，这里只缓存最近一次读取，门关闭即清空；details 只为“可能 ready”与“已在执行”的任务读取，按 stage/state/msg 指纹缓存
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/workboard"
)

const (
	// workboardRefreshGap throttles projection-driven refreshes while the Tasks
	// tab is on screen; workboardBackgroundGap is the slower cadence that keeps
	// the Header aggregate and Project glyphs current with the panel closed.
	// Opening the panel or the tab forces a refresh.
	workboardRefreshGap    = 3 * time.Second
	workboardBackgroundGap = 15 * time.Second
	// workboardRefreshTimeout bounds one list plus its details reads.
	workboardRefreshTimeout = 20 * time.Second
	// workboardDetailCap bounds the details reads of one refresh.
	workboardDetailCap = 16
)

// newWorkboardClient and workboardGo are the seams tests replace: the client
// constructor, and how a board read is run in the background (tests run it
// inline so headless state is never touched from a second goroutine).
var (
	newWorkboardClient = workboard.NewClient
	workboardGo        = func(f func()) { go f() }
)

// workboardDetail is one cached details read with the fingerprint of the list
// row it was read for; a changed row invalidates it.
type workboardDetail struct {
	key    string
	detail workboard.Detail
}

// workboardBoard is the last board read: a cache, never an authority.
type workboardBoard struct {
	tasks    []workboard.Task
	details  map[string]workboardDetail
	loading  bool
	loadedAt time.Time
	// failed reports the last refresh could not read the board.
	failed bool
	// extras is what the plugin adds beside the list: ★ pins and each
	// Project's coordinator. Their absence never degrades the list itself.
	extras workboardExtras
}

// workboardExtras is the plugin's own ★ marks and coordinator status.
type workboardExtras struct {
	pinnedProjects map[string]bool
	pinnedTasks    map[string]bool
	coordinators   map[string]workboard.Coordinator
	autopilot      map[string]bool // project name -> its coordinator's autopilot is on
	automations    []workboard.Automation
}

// workboardCoordinatorCap bounds the coordinator status reads of one refresh.
const workboardCoordinatorCap = 16

// readWorkboardExtras reads the pinned set and the coordinator of every Project
// the board shows (and of every pinned one). A read that fails just leaves that
// piece out; the next refresh tries again.
func readWorkboardExtras(ctx context.Context, client *workboard.Client, tasks []workboard.Task) workboardExtras {
	extras := workboardExtras{
		pinnedProjects: map[string]bool{}, pinnedTasks: map[string]bool{},
		coordinators: map[string]workboard.Coordinator{},
		autopilot:    map[string]bool{},
	}
	if on, err := client.Autopilots(ctx); err == nil {
		extras.autopilot = on
	}
	if all, err := client.Automations(ctx); err == nil {
		extras.automations = all
	}
	if pinned, err := client.Pinned(ctx); err == nil {
		for _, name := range pinned.Projects {
			extras.pinnedProjects[name] = true
		}
		for _, id := range pinned.Tasks {
			extras.pinnedTasks[id] = true
		}
	}
	names := map[string]bool{}
	for name := range extras.pinnedProjects {
		names[name] = true
	}
	for _, task := range tasks {
		if name := task.Project(); name != "" {
			names[name] = true
		}
	}
	for name := range names {
		if len(extras.coordinators) >= workboardCoordinatorCap {
			break
		}
		if co, err := client.Coordinator(ctx, name); err == nil {
			extras.coordinators[name] = co
		}
	}
	return extras
}

func detailKey(t workboard.Task) string {
	return t.Stage + "|" + t.State + "|" + t.Kind + "|" + t.Msg
}

// wantsDetail reports whether a task's details are worth a CLI call: it may be
// ready to approve, or it runs somewhere we may need to point at.
func wantsDetail(t workboard.Task) bool {
	if t.ReadyCandidate() {
		return true
	}
	return t.Stage != "" && t.Stage != "todo" && t.Stage != "done"
}

// readWorkboard reads the list and the details that are stale in the cache.
func readWorkboard(ctx context.Context, client *workboard.Client, cache map[string]workboardDetail) ([]workboard.Task, map[string]workboardDetail, error) {
	tasks, err := client.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	details := make(map[string]workboardDetail, len(tasks))
	reads := 0
	for _, task := range tasks {
		if !wantsDetail(task) {
			continue
		}
		key := detailKey(task)
		if hit, ok := cache[task.ID]; ok && hit.key == key {
			details[task.ID] = hit
			continue
		}
		if reads >= workboardDetailCap {
			continue
		}
		reads++
		// A failed read leaves the task without details: the grouping then
		// reads it as "needs you", the safe direction.
		if detail, derr := client.Details(ctx, task.ID); derr == nil {
			details[task.ID] = workboardDetail{key: key, detail: detail}
		}
	}
	return tasks, details, nil
}

// refreshWorkboardTasks re-reads the board in the background. force skips the
// throttle; a refresh in flight is never doubled; a closed gate does nothing.
func (s *Shell) refreshWorkboardTasks(force bool) {
	board := &s.workboard.board
	if !s.workboardAvailable() || board.loading {
		return
	}
	now := timeNow()
	gap := workboardBackgroundGap
	if s.quickPanel != nil && s.quickPanelTabIndex() == quickPanelTasksTab {
		gap = workboardRefreshGap
	}
	if !force && now.Sub(board.loadedAt) < gap {
		return
	}
	board.loading = true
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	cache := make(map[string]workboardDetail, len(board.details))
	for id, d := range board.details {
		cache[id] = d
	}
	workboardGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), workboardRefreshTimeout)
		defer cancel()
		tasks, details, err := readWorkboard(ctx, client, cache)
		var extras workboardExtras
		if err == nil {
			extras = readWorkboardExtras(ctx, client, tasks)
		}
		s.applyOnWindow(func() {
			s.applyWorkboardTasks(tasks, details, err)
			if err == nil && s.workboardAvailable() {
				s.workboard.board.extras = extras
			}
		})
	})
}

// applyWorkboardTasks stores one refresh. A failed read keeps the previous
// rows and marks the board stale. A refresh that lands after the gate closed
// is dropped, so a removed plugin leaves nothing behind.
func (s *Shell) applyWorkboardTasks(tasks []workboard.Task, details map[string]workboardDetail, err error) {
	board := &s.workboard.board
	board.loading = false
	if !s.workboardAvailable() {
		*board = workboardBoard{}
		return
	}
	board.loadedAt = timeNow()
	board.failed = err != nil
	if err != nil {
		slog.Warn("work board refresh failed", "error", err)
	} else {
		board.tasks, board.details = tasks, details
	}
	if s.router.Path() == routeTask {
		s.loadWorkboardPage()
	}
	s.invalidateQuickPanel()
	if s.quickPanelTabIndex() == quickPanelTasksTab {
		s.fitQuickPanel()
	}
}

// workboardGroup is one section of the Tasks tab.
type workboardGroup struct {
	Group workboard.Group
	Tasks []workboard.Task
}

// workboardGroups splits the board into its sections in attention order,
// leaving out empty ones and the finished tasks (the panel is for what needs
// the human; Done lives in the plugin's own board).
func workboardGroups(board workboardBoard) []workboardGroup {
	buckets := map[workboard.Group][]workboard.Task{}
	for _, task := range board.tasks {
		var detail *workboard.Detail
		if d, ok := board.details[task.ID]; ok {
			detail = &d.detail
		}
		group := workboard.GroupOf(task, detail)
		if group == workboard.GroupDone {
			continue
		}
		buckets[group] = append(buckets[group], task)
	}
	var out []workboardGroup
	for _, group := range []workboard.Group{workboard.GroupNeedsYou, workboard.GroupReady, workboard.GroupWorking, workboard.GroupTodo} {
		if tasks := buckets[group]; len(tasks) > 0 {
			out = append(out, workboardGroup{Group: group, Tasks: tasks})
		}
	}
	return out
}

// projectForWorktree joins a task to its Herdr Project: the Project whose
// workspace or panes sit in the task's worktree (or inside it). "" when the
// task has no worktree yet or no Project is there — an unmatched task stays
// listed and opens the board instead.
func projectForWorktree(p herdr.Projection, worktree string) string {
	root := cleanPath(worktree)
	if root == "" {
		return ""
	}
	for _, project := range p.Projects {
		if pathWithin(root, cleanPath(project.CWD)) {
			return project.ID
		}
	}
	for _, pane := range p.Panes {
		if pathWithin(root, cleanPath(pane.CWD)) {
			return pane.ProjectID
		}
	}
	return ""
}

// cleanPath normalizes a path for the worktree join. macOS reaches /tmp, /var
// and /etc through /private (a shell's cwd reports the real path, a plugin may
// record the short one), so those prefixes are folded together without any
// filesystem access.
func cleanPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	path = filepath.Clean(path)
	for _, dir := range []string{"/tmp", "/var", "/etc"} {
		if path == "/private"+dir || strings.HasPrefix(path, "/private"+dir+"/") {
			return strings.TrimPrefix(path, "/private")
		}
	}
	return path
}

// pathWithin reports whether path is root or lies under it.
func pathWithin(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	if samePath(root, path) {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}

// actionableTask is a task that needs the human — Needs you or Ready to
// approve — with the Herdr Project its worktree joined to ("" when none).
type actionableTask struct {
	Group     workboard.Group
	ProjectID string
}

// actionableTasks lists the board's tasks that need the human, joined to their
// Projects. Nil while the plugin gate is closed: every surface built on it
// (Header aggregate, Project glyphs) then leaves no trace.
func (s *Shell) actionableTasks() []actionableTask {
	if !s.workboardAvailable() {
		return nil
	}
	return actionableTasksOf(s.workboard.board, s.projection)
}

func actionableTasksOf(board workboardBoard, p herdr.Projection) []actionableTask {
	var out []actionableTask
	for _, group := range workboardGroups(board) {
		if group.Group != workboard.GroupNeedsYou && group.Group != workboard.GroupReady {
			continue
		}
		for _, task := range group.Tasks {
			item := actionableTask{Group: group.Group}
			if d, ok := board.details[task.ID]; ok {
				item.ProjectID = projectForWorktree(p, d.detail.Worktree)
			}
			out = append(out, item)
		}
	}
	return out
}

// headerTaskCounts is what the Header Agent badge adds for tasks, as
// attention and review counts so the badge stays one aggregation. A task whose
// Project already has an Agent in the activity snapshot is skipped: its Agent
// is counted there, and the task would otherwise count twice.
func headerTaskCounts(tasks []actionableTask, entries []StatusCenterEntry) (attention, review int) {
	represented := make(map[string]bool, len(entries))
	for _, entry := range entries {
		represented[entry.Card.ProjectID] = true
	}
	for _, task := range tasks {
		if task.ProjectID != "" && represented[task.ProjectID] {
			continue
		}
		if task.Group == workboard.GroupNeedsYou {
			attention++
		} else {
			review++
		}
	}
	return attention, review
}

// mergeTaskActivity lights Project rows for tasks that need the human, on the
// same icon-corner dot the Agent and service rollups use: a Needs-you task
// outranks everything but an attention state already there; a Ready task only
// fills an empty or idle dot (it never hides working agents or services).
func mergeTaskActivity(index map[string]sidebarActivity, tasks []actionableTask) {
	needs, ready := map[string]int{}, map[string]int{}
	for _, task := range tasks {
		if task.ProjectID == "" {
			continue
		}
		if task.Group == workboard.GroupNeedsYou {
			needs[task.ProjectID]++
		} else {
			ready[task.ProjectID]++
		}
	}
	for projectID, n := range needs {
		act := index[projectID]
		detail := pluralUnits(n, "task") + " need you"
		if n == 1 {
			detail = "1 task needs you"
		}
		if act.On && act.Tone == ToneAttention {
			act.Detail = joinDetails(act.Detail, detail)
		} else {
			act = sidebarActivity{On: true, Tone: ToneAttention, Detail: joinDetails(act.Detail, detail)}
		}
		index[projectID] = act
	}
	for projectID, n := range ready {
		if needs[projectID] > 0 {
			continue
		}
		act := index[projectID]
		if act.On && act.Tone != ToneMuted {
			continue
		}
		index[projectID] = sidebarActivity{On: true, Tone: ToneSuccess, Detail: joinDetails(act.Detail, pluralUnits(n, "task")+" ready to approve")}
	}
}
