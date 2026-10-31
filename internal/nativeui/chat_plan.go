package nativeui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/thewei/solco/internal/conversation"
)

/**
 * [INPUT]: 依赖 conversation.TimelineTurn（工具调用的完整 Input / Output 文本）
 * [OUTPUT]: 提供 chatPlan：把会话里最近一次任务清单工具调用折成页面的 Tasks 面板数据（只读投影）
 * [POS]: Chat 任务清单的唯一数据口：Claude TodoWrite / Codex update_plan 读 Input，Claude TaskCreate/TaskUpdate 整窗重放（未经真机转录验证），omp todo 读自己的结果文本；
 *        不回放操作、不持有第二份状态，找不到可解析的调用就没有面板
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Plan item states the page draws (one glyph each).
const (
	planPending = "pending"
	planActive  = "active"
	planDone    = "done"
	planBlocked = "blocked"
	planDropped = "dropped"
)

// ChatBridgePlanItem is one row of the Tasks panel.
type ChatBridgePlanItem struct {
	Text   string `json:"text"`
	Status string `json:"status"`
	// Phase is the provider's own group label (omp phases); "" when flat.
	Phase string `json:"phase,omitempty"`
}

// ChatBridgePlan is the latest task list the session published.
type ChatBridgePlan struct {
	Items []ChatBridgePlanItem `json:"items"`
	// Turn is the timeline index of the turn that last wrote the list; the
	// page retires a finished list once a later user turn exists.
	Turn int `json:"turn"`
}

var (
	ompPlanPhase = regexp.MustCompile(`^  (\S.*):$`)
	ompPlanTask  = regexp.MustCompile(`^    - \[( |[xX])\] (.*)$`)
	ompPlanNote  = regexp.MustCompile(`\s+\((in progress|dropped|blocked(?::[^)]*)?)\)$`)
)

// chatPlan scans turns newest-first for a task-list tool call whose payload
// parses; the first hit is the current list. nil when there is none.
func chatPlan(turns []conversation.TimelineTurn) *ChatBridgePlan {
	whole := latestPlan(turns)
	// Claude Code's task tools build the list call by call, so they replay
	// the whole window; the newer of the two writers wins.
	if tasks := claudeTaskPlan(turns); tasks != nil && (whole == nil || tasks.Turn >= whole.Turn) {
		return tasks
	}
	return whole
}

// latestPlan is the newest single call that carries a whole list.
func latestPlan(turns []conversation.TimelineTurn) *ChatBridgePlan {
	for index := len(turns) - 1; index >= 0; index-- {
		steps := turns[index].Steps
		for at := len(steps) - 1; at >= 0; at-- {
			tool := steps[at].Tool
			if tool == nil {
				continue
			}
			var items []ChatBridgePlanItem
			switch strings.ToLower(tool.Name) {
			case "todowrite", "todo_write":
				items = claudePlanItems(deref(tool.Input))
			case "update_plan":
				items = codexPlanItems(deref(tool.Input))
			case "todo":
				items = ompPlanItems(deref(tool.Output))
			}
			if len(items) > 0 {
				return &ChatBridgePlan{Items: items, Turn: index}
			}
		}
	}
	return nil
}

var claudeTaskID = regexp.MustCompile(`#(\d+)`)

// claudeTaskPlan replays Claude Code's TaskCreate / TaskUpdate calls (the
// interactive task tools; TodoWrite is the one-shot list): a create appends
// a pending task whose id the result text names ("Task #3 created …", the
// next number otherwise); an update moves one task's status or subject, and
// "deleted" removes it. Calls whose result reports a failure are skipped.
func claudeTaskPlan(turns []conversation.TimelineTurn) *ChatBridgePlan {
	type task struct {
		id, text, status string
	}
	var tasks []task
	last := -1
	for index, turn := range turns {
		for _, step := range turn.Steps {
			tool := step.Tool
			if tool == nil {
				continue
			}
			name := strings.ToLower(tool.Name)
			if name != "taskcreate" && name != "taskupdate" {
				continue
			}
			output := deref(tool.Output)
			if lower := strings.ToLower(output); strings.HasPrefix(lower, "error") || strings.Contains(lower, "not found") {
				continue
			}
			var input struct {
				Subject string `json:"subject"`
				TaskID  any    `json:"taskId"`
				Status  string `json:"status"`
			}
			if json.Unmarshal([]byte(deref(tool.Input)), &input) != nil {
				continue
			}
			if name == "taskcreate" {
				subject := strings.TrimSpace(input.Subject)
				if subject == "" {
					continue
				}
				id := strconv.Itoa(len(tasks) + 1)
				if match := claudeTaskID.FindStringSubmatch(output); match != nil {
					id = match[1]
				}
				tasks = append(tasks, task{id: id, text: subject, status: planPending})
				last = index
				continue
			}
			id := strings.TrimSpace(fmt.Sprint(input.TaskID))
			for at := range tasks {
				if tasks[at].id != id {
					continue
				}
				if strings.EqualFold(input.Status, "deleted") {
					tasks = append(tasks[:at], tasks[at+1:]...)
				} else {
					if input.Status != "" {
						tasks[at].status = normalizePlanStatus(input.Status)
					}
					if subject := strings.TrimSpace(input.Subject); subject != "" {
						tasks[at].text = subject
					}
				}
				last = index
				break
			}
		}
	}
	if len(tasks) == 0 {
		return nil
	}
	items := make([]ChatBridgePlanItem, len(tasks))
	for at, t := range tasks {
		items[at] = ChatBridgePlanItem{Text: t.text, Status: t.status}
	}
	return &ChatBridgePlan{Items: items, Turn: last}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func claudePlanItems(input string) []ChatBridgePlanItem {
	var payload struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if json.Unmarshal([]byte(input), &payload) != nil {
		return nil
	}
	items := make([]ChatBridgePlanItem, 0, len(payload.Todos))
	for _, todo := range payload.Todos {
		if text := strings.TrimSpace(todo.Content); text != "" {
			items = append(items, ChatBridgePlanItem{Text: text, Status: normalizePlanStatus(todo.Status)})
		}
	}
	return items
}

func codexPlanItems(input string) []ChatBridgePlanItem {
	var payload struct {
		Plan []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if json.Unmarshal([]byte(input), &payload) != nil {
		return nil
	}
	items := make([]ChatBridgePlanItem, 0, len(payload.Plan))
	for _, step := range payload.Plan {
		if text := strings.TrimSpace(step.Step); text != "" {
			items = append(items, ChatBridgePlanItem{Text: text, Status: normalizePlanStatus(step.Status)})
		}
	}
	return items
}

// ompPlanItems reads the full list omp prints after every op: "Overall: …"
// then `  Phase:` headers over `    - [X] task (note)` rows. A result without
// the Overall line (an error, a refusal) carries no list.
func ompPlanItems(output string) []ChatBridgePlanItem {
	at := strings.Index(output, "\nOverall:")
	if at < 0 {
		return nil
	}
	phase := ""
	var items []ChatBridgePlanItem
	for _, line := range strings.Split(output[at:], "\n") {
		if match := ompPlanPhase.FindStringSubmatch(line); match != nil {
			phase = match[1]
			continue
		}
		match := ompPlanTask.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		text, status := match[2], planPending
		if match[1] != " " {
			status = planDone
		}
		if note := ompPlanNote.FindStringSubmatch(text); note != nil {
			text = strings.TrimSpace(strings.TrimSuffix(text, note[0]))
			switch {
			case note[1] == "in progress":
				status = planActive
			case note[1] == "dropped":
				status = planDropped
			default:
				status = planBlocked
			}
		}
		items = append(items, ChatBridgePlanItem{Text: text, Status: status, Phase: phase})
	}
	return items
}

func normalizePlanStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "done", "complete":
		return planDone
	case "in_progress", "active", "running":
		return planActive
	default:
		return planPending
	}
}
