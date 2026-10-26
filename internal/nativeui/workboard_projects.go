package nativeui

/**
 * [INPUT]: 依赖 workboardBoard 缓存、workboardGroups（TUI 同款状态分组）、workboard.Task.Project（任务 id 的 repo 前缀）
 * [OUTPUT]: workboardProject / workboardProjects：把看板按 Project（repo）名分组，项目内任务按 needs you → ready → working → to do 排序，项目按“最需要你的任务”排序
 * [POS]: 侧栏 Projects 区的纯数据层；Project 清单只能由 `list --json` 的任务 id 推导（插件不提供 projects JSON，其数据目录与此同集合，不读私有存储）；全部派生、无注册表
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"sort"
	"strings"

	"github.com/wh-studio/solco/internal/workboard"
)

// workboardOtherProject names tasks whose id carries no repo part.
const workboardOtherProject = "Other"

// workboardProject is one Project (repo) on the board with its open tasks in
// the board's status order. Finished tasks keep the Project listed but are not
// part of Groups; they are Done.
type workboardProject struct {
	Name   string
	Groups []workboardGroup
	Open   int
	Done   []workboard.Task
	// Pinned is the plugin's ★ on the Project; PinnedTasks the ids of its
	// starred tasks; Coord its coordinator as last read (zero when unread).
	Pinned      bool
	PinnedTasks map[string]bool
	Coord       workboard.Coordinator
}

// Dot is the Project row's state: its most urgent open task, raised to
// "needs you" while its coordinator waits on the human, and "working" when only
// the coordinator is busy.
func (p workboardProject) Dot() operationalState {
	dot := opUnknown
	if top, ok := p.Top(); ok {
		dot = groupOperationalState(top)
	}
	switch p.Coord.State {
	case "blocked":
		return opNeedsAttention
	case "working", "starting":
		if dot == opUnknown || dot == opIdle {
			return opWorking
		}
	}
	return dot
}

// Top is the group that needs the human most; ok is false when nothing is open.
func (p workboardProject) Top() (workboard.Group, bool) {
	if len(p.Groups) == 0 {
		return 0, false
	}
	return p.Groups[0].Group, true
}

// workboardProjects splits the board by Project. Projects order by their most
// urgent open task, then by name; a Project with only finished tasks comes last.
// Pinned (★) Projects lead, as the plugin's own list does.
func workboardProjects(board workboardBoard) []workboardProject {
	byName := map[string][]workboard.Task{}
	var names []string
	for _, task := range board.tasks {
		name := task.Project()
		if name == "" {
			name = workboardOtherProject
		}
		if _, seen := byName[name]; !seen {
			names = append(names, name)
		}
		byName[name] = append(byName[name], task)
	}
	// A pinned Project stays listed even when no task of it is on the board.
	pinnedNames := make([]string, 0, len(board.extras.pinnedProjects))
	for name := range board.extras.pinnedProjects {
		if _, seen := byName[name]; !seen {
			pinnedNames = append(pinnedNames, name)
		}
	}
	sort.Strings(pinnedNames)
	names = append(names, pinnedNames...)
	out := make([]workboardProject, 0, len(names))
	for _, name := range names {
		groups := workboardGroups(workboardBoard{tasks: byName[name], details: board.details})
		open := 0
		for _, group := range groups {
			open += len(group.Tasks)
		}
		var done []workboard.Task
		for _, task := range byName[name] {
			var detail *workboard.Detail
			if d, ok := board.details[task.ID]; ok {
				detail = &d.detail
			}
			if workboard.GroupOf(task, detail) == workboard.GroupDone {
				done = append(done, task)
			}
		}
		pinnedTasks := map[string]bool{}
		for _, task := range byName[name] {
			if board.extras.pinnedTasks[task.ID] {
				pinnedTasks[task.ID] = true
			}
		}
		out = append(out, workboardProject{
			Name: name, Groups: groups, Open: open, Done: done,
			Pinned: board.extras.pinnedProjects[name], PinnedTasks: pinnedTasks, Coord: board.extras.coordinators[name],
		})
	}
	rank := func(p workboardProject) int {
		if top, ok := p.Top(); ok {
			return int(top)
		}
		return 1 << 20
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		if ri, rj := rank(out[i]), rank(out[j]); ri != rj {
			return ri < rj
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
