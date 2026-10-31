package workboard

/**
 * [INPUT]: 依赖 task.go 的 Task（stage/state）；stage 词表取自插件 store/files.rs 的 STAGES（2026-10-31 实测 1.2.26）
 * [OUTPUT]: RunStrip——任务运行条的六段（Work/Check/PR/Review/Approve/Merge）及其状态
 * [POS]: 纯映射，不起进程、不读文件；只回答“任务现在走到流水线的哪一段”，从不推断插件没说的事（failed 与未知 stage 不点亮任何一段）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// StepState is how one segment of the run strip is drawn.
type StepState uint8

const (
	// StepPending is a segment the task has not reached.
	StepPending StepState = iota
	// StepDone is a segment the task has passed.
	StepDone
	// StepCurrent is the segment the task is in, moving on its own.
	StepCurrent
	// StepWaiting is the segment the task is in, held for the human.
	StepWaiting
)

// Step is one named segment of the run strip.
type Step struct {
	Label string
	State StepState
}

// stripLabels are the pipeline's segments in order: the finish line (commit,
// sync, repo check), the pull request, the second agent's review, the human's
// yes, the merge.
var stripLabels = [...]string{"Work", "Check", "PR", "Review", "Approve", "Merge"}

// stripIndex maps a plugin stage to its segment: the index, or -1 when the
// stage lights nothing (not started, failed, or a stage this client does not
// know), and done for a finished task.
func stripIndex(stage string) (idx int, done bool) {
	switch stage {
	case "starting", "setup", "working", "progress", "stopped", "nosummary", "asks",
		"attempts", "pick", "tried", "plan":
		return 0, false
	case "checking":
		return 1, false
	case "pushing":
		return 2, false
	case "reviewing", "reviewneeds":
		return 3, false
	case "approval":
		return 4, false
	case "merging":
		return 5, false
	case "done":
		return len(stripLabels), true
	}
	// todo, after, queued, failed, "" and anything new.
	return -1, false
}

// RunStrip places a task on the pipeline. A finished task is all done; a task
// that is blocked holds its segment as Waiting.
func RunStrip(t Task) []Step {
	idx, done := stripIndex(t.Stage)
	if t.State == "done" {
		idx, done = len(stripLabels), true
	}
	steps := make([]Step, len(stripLabels))
	for i, label := range stripLabels {
		steps[i].Label = label
		switch {
		case done || (idx >= 0 && i < idx):
			steps[i].State = StepDone
		case i == idx && t.State == "blocked":
			steps[i].State = StepWaiting
		case i == idx:
			steps[i].State = StepCurrent
		}
	}
	return steps
}
