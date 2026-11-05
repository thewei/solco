package personalagent

import "strings"

// C06: the parent/child chain of one piece of work, read from the records —
// Goal → milestone → Task (→ parent Task, dependencies) → Runs → ledger facts.
// It is how a Coordinator (or a person) answers "where did this come from and
// what happened to it" without trusting a model's memory.

// TraceRun is one attempt, with only what proves it happened.
type TraceRun struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	HasPane   bool   `json:"hasPane"`
	HasResult bool   `json:"hasResult"`
}

// TraceFact is one ledger line about the chain.
type TraceFact struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// TaskStub is a related task, light.
type TaskStub struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// Trace is the chain for one task or one goal.
type Trace struct {
	GoalID         string      `json:"goalId,omitempty"`
	GoalTitle      string      `json:"goalTitle,omitempty"`
	GoalState      string      `json:"goalState,omitempty"`
	Milestone      string      `json:"milestone,omitempty"`
	MilestoneState string      `json:"milestoneState,omitempty"`
	TaskID         string      `json:"taskId,omitempty"`
	TaskTitle      string      `json:"taskTitle,omitempty"`
	TaskState      string      `json:"taskState,omitempty"`
	ParentTaskID   string      `json:"parentTaskId,omitempty"`
	DependsOn      []string    `json:"dependsOn,omitempty"`
	Tasks          []TaskStub  `json:"tasks,omitempty"` // for a goal: the tasks it filed
	Runs           []TraceRun  `json:"runs,omitempty"`
	Facts          []TraceFact `json:"facts,omitempty"`
}

// Trace follows a task id or a goal id.
func (s *Store) Trace(id string) (Trace, error) {
	var tr Trace
	if t, err := s.GetTask(id); err == nil {
		tr.TaskID, tr.TaskTitle, tr.TaskState, tr.ParentTaskID = t.ID, clip(t.Title, 100), t.State, t.ParentTaskID
		tr.DependsOn, _ = s.Dependencies(t.ID)
		runs, _ := s.ListRuns(t.ID)
		for _, r := range runs {
			tr.Runs = append(tr.Runs, TraceRun{ID: r.ID, Status: r.Status, HasPane: r.PaneID != "", HasResult: strings.TrimSpace(r.Result) != ""})
		}
		if t.GoalID != "" {
			tr.fillGoal(s, t.GoalID, t.ID)
		}
		tr.facts(s, t.AssistantID, "", t.ID)
		return tr, nil
	}
	g, err := s.GetGoal(id)
	if err != nil {
		return Trace{}, err
	}
	tr.fillGoal(s, g.ID, "")
	tasks, _ := s.ListTasks(g.AssistantID)
	for _, t := range tasks {
		if t.GoalID == g.ID {
			tr.Tasks = append(tr.Tasks, TaskStub{ID: t.ID, Title: clip(t.Title, 100), State: t.State})
		}
	}
	tr.facts(s, g.AssistantID, g.ID, "")
	return tr, nil
}

func (tr *Trace) fillGoal(s *Store, goalID, taskID string) {
	g, err := s.GetGoal(goalID)
	if err != nil {
		return
	}
	tr.GoalID, tr.GoalTitle, tr.GoalState = g.ID, clip(g.Title, 100), g.State
	if taskID == "" {
		return
	}
	if plan, err := s.GetGoalPlan(goalID); err == nil {
		for _, m := range plan.Milestones {
			if m.TaskID == taskID {
				tr.Milestone, tr.MilestoneState = m.ID, m.State
			}
		}
	}
}

func (tr *Trace) facts(s *Store, assistantID, goalID, taskID string) {
	fs, err := s.Facts(assistantID, goalID, 100)
	if err != nil {
		return
	}
	for _, f := range fs {
		if goalID == "" && f.TaskID != taskID {
			continue
		}
		if len(tr.Facts) >= 6 {
			break
		}
		tr.Facts = append(tr.Facts, TraceFact{Kind: f.Kind, Text: clip(f.Text, 160)})
	}
}

// openDuplicate finds an unfinished task of the Assistant that covers the same
// work: the same title (ignoring case and spacing) under the same goal.
func (s *Store) openDuplicate(assistantID, goalID, title string) (Task, bool) {
	want := normalizeTitle(title)
	tasks, err := s.ListTasks(assistantID)
	if err != nil || want == "" {
		return Task{}, false
	}
	for _, t := range tasks {
		switch t.State {
		case TaskDone, TaskCancelled, TaskFailed:
			continue
		}
		if t.Kind == KindCoordinator || t.Kind == KindGroupAgent {
			continue
		}
		if t.GoalID == goalID && normalizeTitle(t.Title) == want {
			return t, true
		}
	}
	return Task{}, false
}

func normalizeTitle(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
