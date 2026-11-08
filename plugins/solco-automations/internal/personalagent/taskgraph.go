package personalagent

import (
	"database/sql"
	"fmt"
	"sort"
)

// E01: the task graph. A task may depend on other tasks of the same Assistant
// and wait for them; a task may have a parent (a decomposition). The graph is
// kept in the same database as the tasks, so a restart loses none of it.

// checkDeps validates a dependency list for `taskID` ("" for a task not yet
// created): every id exists, belongs to the same Assistant, is not the task
// itself, and the new edges close no cycle. It returns the de-duplicated ids.
func checkDeps(tx queryer, assistantID, taskID string, deps []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, d := range deps {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		if d == taskID {
			return nil, fmt.Errorf("%w: a task cannot depend on itself", ErrInvalid)
		}
		t, err := getTaskTx(tx, d)
		if err != nil || t.AssistantID != assistantID {
			return nil, fmt.Errorf("%w: %q is not a task of this assistant", ErrInvalid, d)
		}
		out = append(out, d)
	}
	if taskID != "" && len(out) > 0 {
		// A cycle exists when some new dependency already depends (transitively) on taskID.
		for _, d := range out {
			reach, err := dependsTransitively(tx, d, taskID)
			if err != nil {
				return nil, err
			}
			if reach {
				return nil, fmt.Errorf("%w: that dependency would make a cycle", ErrInvalid)
			}
		}
	}
	return out, nil
}

// dependsTransitively says whether `from` depends, directly or not, on `target`.
func dependsTransitively(tx queryer, from, target string) (bool, error) {
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		rows, err := tx.Query(`SELECT depends_on FROM task_deps WHERE task_id=?`, cur)
		if err != nil {
			return false, wrapBusy(err)
		}
		var next []string
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err != nil {
				_ = rows.Close()
				return false, err
			}
			next = append(next, d)
		}
		_ = rows.Close()
		for _, d := range next {
			if d == target {
				return true, nil
			}
			if !seen[d] {
				seen[d] = true
				stack = append(stack, d)
			}
		}
	}
	return false, nil
}

func anyOpenDep(tx queryer, deps []string) (bool, error) {
	for _, d := range deps {
		t, err := getTaskTx(tx, d)
		if err != nil {
			return false, err
		}
		if t.State != TaskDone {
			return true, nil
		}
	}
	return false, nil
}

// Dependencies lists what a task waits for.
func (s *Store) Dependencies(taskID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT depends_on FROM task_deps WHERE task_id=? ORDER BY depends_on`, taskID)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, wrapBusy(rows.Err())
}

// AddDependencies adds edges to a task that has not started: a draft, queued
// or waiting task. A task a worker already claimed is never moved behind
// something new.
func (s *Store) AddDependencies(requestID, taskID string, deps []string, expectedRevision int) (Task, bool, error) {
	payload := map[string]any{"id": taskID, "deps": deps, "rev": expectedRevision}
	return execute(s, requestID, "task.deps", payload, func(tx *sql.Tx) (Task, error) {
		t, err := getTaskTx(tx, taskID)
		if err != nil {
			return t, err
		}
		if t.Revision != expectedRevision {
			return t, ErrStaleRevision
		}
		switch t.State {
		case TaskDraft, TaskQueued, TaskWaiting:
		default:
			return t, fmt.Errorf("%w: task is %s; dependencies can only be added before it starts", ErrBadState, t.State)
		}
		clean, err := checkDeps(tx, t.AssistantID, taskID, deps)
		if err != nil {
			return t, err
		}
		for _, d := range clean {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO task_deps(task_id,depends_on) VALUES(?,?)`, taskID, d); err != nil {
				return t, err
			}
		}
		all, err := depsTx(tx, taskID)
		if err != nil {
			return t, err
		}
		state := t.State
		if t.State != TaskDraft {
			if open, err := anyOpenDep(tx, all); err != nil {
				return t, err
			} else if open {
				state = TaskWaiting
			} else {
				state = TaskQueued
			}
		}
		if _, err := tx.Exec(`UPDATE tasks SET state=?, revision=revision+1, updated_at=? WHERE id=?`, state, s.nowMS(), taskID); err != nil {
			return t, err
		}
		return getTaskTx(tx, taskID)
	})
}

func depsTx(tx queryer, taskID string) ([]string, error) {
	rows, err := tx.Query(`SELECT depends_on FROM task_deps WHERE task_id=?`, taskID)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, wrapBusy(rows.Err())
}

// PublishTask moves a draft into the queue (or into waiting, when it has
// unfinished dependencies). Drafts never move by themselves.
func (s *Store) PublishTask(requestID, taskID string, expectedRevision int) (Task, bool, error) {
	payload := map[string]any{"id": taskID, "rev": expectedRevision}
	return execute(s, requestID, "task.publish", payload, func(tx *sql.Tx) (Task, error) {
		t, err := getTaskTx(tx, taskID)
		if err != nil {
			return t, err
		}
		if t.Revision != expectedRevision {
			return t, ErrStaleRevision
		}
		if t.State != TaskDraft {
			return t, fmt.Errorf("%w: task is %s, not a draft", ErrBadState, t.State)
		}
		a, err := getAssistantTx(tx, t.AssistantID)
		if err != nil {
			return t, err
		}
		if a.Status != StatusActive {
			return t, ErrAssistantDisabled
		}
		deps, err := depsTx(tx, taskID)
		if err != nil {
			return t, err
		}
		state := TaskQueued
		if open, err := anyOpenDep(tx, deps); err != nil {
			return t, err
		} else if open {
			state = TaskWaiting
		}
		if _, err := tx.Exec(`UPDATE tasks SET state=?, revision=revision+1, updated_at=? WHERE id=?`, state, s.nowMS(), taskID); err != nil {
			return t, err
		}
		return getTaskTx(tx, taskID)
	})
}

// Promote moves every waiting task whose dependencies are all done into the
// queue, and flags (needs you) the ones stuck behind a failed or cancelled
// dependency. It is a cheap, deterministic pass the dispatcher runs before it
// claims anything. It returns how many tasks it queued.
func (s *Store) Promote() (int, error) {
	n, _, err := execute(s, "", "task.promote", nil, func(tx *sql.Tx) (int, error) {
		rows, err := tx.Query(`SELECT `+taskCols+` FROM tasks WHERE state=?`, TaskWaiting)
		if err != nil {
			return 0, wrapBusy(err)
		}
		var waiting []Task
		for rows.Next() {
			t, err := scanTask(rows)
			if err != nil {
				_ = rows.Close()
				return 0, err
			}
			waiting = append(waiting, t)
		}
		_ = rows.Close()
		moved := 0
		now := s.nowMS()
		for _, t := range waiting {
			deps, err := depsTx(tx, t.ID)
			if err != nil {
				return 0, err
			}
			allDone, broken := true, false
			for _, d := range deps {
				dt, err := getTaskTx(tx, d)
				if err != nil {
					return 0, err
				}
				if dt.State != TaskDone {
					allDone = false
				}
				if dt.State == TaskFailed || dt.State == TaskCancelled {
					broken = true
				}
			}
			switch {
			case allDone:
				if _, err := tx.Exec(`UPDATE tasks SET state=?, needs_you=0, revision=revision+1, updated_at=? WHERE id=?`, TaskQueued, now, t.ID); err != nil {
					return 0, err
				}
				moved++
			case broken && !t.NeedsYou:
				if _, err := tx.Exec(`UPDATE tasks SET needs_you=1, revision=revision+1, updated_at=? WHERE id=?`, now, t.ID); err != nil {
					return 0, err
				}
				if err := emitTask(tx, t.ID, "task.blocked", now); err != nil {
					return 0, err
				}
			case !broken && t.NeedsYou:
				// The broken dependency was retried: the flag no longer holds.
				if _, err := tx.Exec(`UPDATE tasks SET needs_you=0, revision=revision+1, updated_at=? WHERE id=?`, now, t.ID); err != nil {
					return 0, err
				}
			}
		}
		return moved, nil
	})
	return n, err
}

// TaskNode is one task with its place in the graph.
type TaskNode struct {
	Task         Task     `json:"task"`
	DependsOn    []string `json:"dependsOn"`
	Children     []string `json:"children"`
	ChildrenDone int      `json:"childrenDone"`
}

// TaskGraph returns an Assistant's tasks with their edges, oldest first.
func (s *Store) TaskGraph(assistantID string) ([]TaskNode, error) {
	tasks, err := s.ListTasks(assistantID)
	if err != nil {
		return nil, err
	}
	nodes := map[string]*TaskNode{}
	order := []string{}
	for _, t := range tasks {
		nodes[t.ID] = &TaskNode{Task: t, DependsOn: []string{}, Children: []string{}}
		order = append(order, t.ID)
	}
	sort.Strings(order)
	for _, t := range tasks {
		deps, err := s.Dependencies(t.ID)
		if err != nil {
			return nil, err
		}
		nodes[t.ID].DependsOn = deps
		if p, ok := nodes[t.ParentTaskID]; ok && t.ParentTaskID != "" {
			p.Children = append(p.Children, t.ID)
			if t.State == TaskDone {
				p.ChildrenDone++
			}
		}
	}
	out := make([]TaskNode, 0, len(order))
	for _, id := range order {
		out = append(out, *nodes[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Task.CreatedAt < out[j].Task.CreatedAt })
	return out, nil
}
