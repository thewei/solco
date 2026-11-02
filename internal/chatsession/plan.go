package chatsession

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
 * [OUTPUT]: 提供 PlanOf：把会话里最近一次任务清单工具调用折成页面的 Tasks 面板数据（只读投影）
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

// ChatPlanItem is one row of the Tasks panel.
type ChatPlanItem struct {
	Text   string `json:"text"`
	Status string `json:"status"`
	// Phase is the provider's own group label (omp phases); "" when flat.
	Phase string `json:"phase,omitempty"`
}

// ChatPlan is the latest task list the session published.
type ChatPlan struct {
	Items []ChatPlanItem `json:"items"`
	// Turn is the timeline index of the turn that last wrote the list; the
	// page retires a finished list once a later user turn exists.
	Turn int `json:"turn"`
}

var (
	ompPlanPhase = regexp.MustCompile(`^  (\S.*):$`)
	ompPlanTask  = regexp.MustCompile(`^    - \[( |[xX])\] (.*)$`)
	ompPlanNote  = regexp.MustCompile(`\s+\((in progress|dropped|blocked(?::[^)]*)?)\)$`)
)

// PlanOf scans turns newest-first for a task-list tool call whose payload
// parses; the first hit is the current list. nil when there is none.
func PlanOf(turns []conversation.TimelineTurn) *ChatPlan {
	whole := latestPlan(turns)
	// Cursor's TodoWrite can send only the items that changed (merge:true),
	// so its list is the replay of every call, not the newest one.
	if merged := mergedTodoPlan(turns); merged != nil && (whole == nil || merged.Turn >= whole.Turn) {
		whole = merged
	}
	// Claude Code's task tools build the list call by call, so they replay
	// the whole window; the newer of the two writers wins.
	if tasks := claudeTaskPlan(turns); tasks != nil && (whole == nil || tasks.Turn >= whole.Turn) {
		return tasks
	}
	return whole
}

// latestPlan is the newest single call that carries a whole list.
func latestPlan(turns []conversation.TimelineTurn) *ChatPlan {
	for index := len(turns) - 1; index >= 0; index-- {
		steps := turns[index].Steps
		for at := len(steps) - 1; at >= 0; at-- {
			tool := steps[at].Tool
			if tool == nil {
				continue
			}
			var items []ChatPlanItem
			switch strings.ToLower(tool.Name) {
			case "todowrite", "todo_write":
				items = claudePlanItems(deref(tool.Input))
			case "update_plan":
				items = codexPlanItems(deref(tool.Input))
			case "todo":
				items = ompPlanItems(deref(tool.Output))
				if tool.Details != nil && len(items) == 0 {
					// Pi's snapshot is the whole list as of this call: an
					// empty one (clear, everything deleted) retires the tray
					// instead of resurrecting an older call's list.
					if items = piPlanItems(*tool.Details); len(items) == 0 {
						return nil
					}
				}
			}
			if len(items) > 0 {
				return &ChatPlan{Items: items, Turn: index}
			}
		}
	}
	return nil
}

// mergedTodoPlan replays TodoWrite calls in order when any of them is a
// partial update (`"merge": true`, Cursor's shape): a plain call replaces the
// list, a merge call updates the items it names by id (content and status
// when given) and appends the unknown ones. nil when no call merges — the
// whole-list providers keep the newest-call rule.
func mergedTodoPlan(turns []conversation.TimelineTurn) *ChatPlan {
	type todo struct{ id, text, status string }
	var items []todo
	merging, last := false, -1
	for index, turn := range turns {
		for _, step := range turn.Steps {
			tool := step.Tool
			if tool == nil {
				continue
			}
			if name := strings.ToLower(tool.Name); name != "todowrite" && name != "todo_write" {
				continue
			}
			var input struct {
				Merge bool `json:"merge"`
				Todos []struct {
					ID      string `json:"id"`
					Content string `json:"content"`
					Status  string `json:"status"`
				} `json:"todos"`
			}
			if json.Unmarshal([]byte(deref(tool.Input)), &input) != nil || len(input.Todos) == 0 {
				continue
			}
			merging = merging || input.Merge
			if !input.Merge {
				items = items[:0]
			}
			for _, entry := range input.Todos {
				at := -1
				for i := range items {
					if entry.ID != "" && items[i].id == entry.ID {
						at = i
						break
					}
				}
				if at < 0 {
					if text := strings.TrimSpace(entry.Content); text != "" {
						items = append(items, todo{entry.ID, text, normalizePlanStatus(entry.Status)})
					}
					continue
				}
				if text := strings.TrimSpace(entry.Content); text != "" {
					items[at].text = text
				}
				if entry.Status != "" {
					items[at].status = normalizePlanStatus(entry.Status)
				}
			}
			last = index
		}
	}
	if !merging || len(items) == 0 {
		return nil
	}
	out := make([]ChatPlanItem, len(items))
	for i, item := range items {
		out[i] = ChatPlanItem{Text: item.text, Status: item.status}
	}
	return &ChatPlan{Items: out, Turn: last}
}

var claudeTaskID = regexp.MustCompile(`#(\d+)`)

// claudeTaskPlan replays Claude Code's TaskCreate / TaskUpdate calls (the
// interactive task tools; TodoWrite is the one-shot list): a create appends
// a pending task whose id the result text names ("Task #3 created …", the
// next number otherwise); an update moves one task's status or subject, and
// "deleted" removes it. Calls whose result reports a failure are skipped.
func claudeTaskPlan(turns []conversation.TimelineTurn) *ChatPlan {
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
	items := make([]ChatPlanItem, len(tasks))
	for at, t := range tasks {
		items[at] = ChatPlanItem{Text: t.text, Status: t.status}
	}
	return &ChatPlan{Items: items, Turn: last}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func claudePlanItems(input string) []ChatPlanItem {
	var payload struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if json.Unmarshal([]byte(input), &payload) != nil {
		return nil
	}
	items := make([]ChatPlanItem, 0, len(payload.Todos))
	for _, todo := range payload.Todos {
		if text := strings.TrimSpace(todo.Content); text != "" {
			items = append(items, ChatPlanItem{Text: text, Status: normalizePlanStatus(todo.Status)})
		}
	}
	return items
}

func codexPlanItems(input string) []ChatPlanItem {
	var payload struct {
		Plan []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if json.Unmarshal([]byte(input), &payload) != nil {
		return nil
	}
	items := make([]ChatPlanItem, 0, len(payload.Plan))
	for _, step := range payload.Plan {
		if text := strings.TrimSpace(step.Step); text != "" {
			items = append(items, ChatPlanItem{Text: text, Status: normalizePlanStatus(step.Status)})
		}
	}
	return items
}

// ompPlanItems reads the full list omp prints after every op: "Overall: …"
// then `  Phase:` headers over `    - [X] task (note)` rows. A result without
// the Overall line (an error, a refusal) carries no list.
func ompPlanItems(output string) []ChatPlanItem {
	at := strings.Index(output, "\nOverall:")
	if at < 0 {
		return nil
	}
	phase := ""
	var items []ChatPlanItem
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
		items = append(items, ChatPlanItem{Text: text, Status: status, Phase: phase})
	}
	return items
}

// piPlanItems reads the whole list Pi's todo extension attaches to each
// result (`{"tasks":[{subject,status,activeForm}]}`): its text output is only
// an increment. Deleted tasks are gone from the list, as the extension shows.
func piPlanItems(snapshot string) []ChatPlanItem {
	if snapshot == "" {
		return nil
	}
	var payload struct {
		Tasks []struct {
			Subject string `json:"subject"`
			Status  string `json:"status"`
		} `json:"tasks"`
	}
	if json.Unmarshal([]byte(snapshot), &payload) != nil {
		return nil
	}
	var items []ChatPlanItem
	for _, task := range payload.Tasks {
		text := strings.TrimSpace(task.Subject)
		if text == "" || task.Status == "deleted" {
			continue
		}
		items = append(items, ChatPlanItem{Text: text, Status: normalizePlanStatus(task.Status)})
	}
	return items
}

func normalizePlanStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "done", "complete":
		return planDone
	case "in_progress", "active", "running":
		return planActive
	case "cancelled", "canceled":
		return planDropped
	default:
		return planPending
	}
}
