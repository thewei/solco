package workboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

/**
 * [INPUT]: 依赖 herdr-projects 1.2.x 的 `list --pinned --json`（{machine, projects[], tasks[]}）与 `coordinator --project <repo> status --json`（{name,state,agent,pane,queued,error,said}），以及 Client 的 exec 缝
 * [OUTPUT]: 对外提供 Pinned/ParsePinned/Client.Pinned、Pin/Unpin（任务 id 或 Project 名，插件原生的 ★）、Coordinator/ParseCoordinator/Client.Coordinator（Project 协调者的只读状态）
 * [POS]: work board 的增强读面：全部来自插件自己的 JSON，无任何 tab 名/目录推断；读失败只让增强信息缺席，不影响看板主体
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Pinned is what the plugin marks with its ★: Projects by name, tasks by id.
type Pinned struct {
	Projects []string
	Tasks    []string
}

// ParsePinned reads `list --pinned --json`.
func ParsePinned(raw string) (Pinned, error) {
	var env struct {
		Projects []string `json:"projects"`
		Tasks    []Task   `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return Pinned{}, err
	}
	out := Pinned{Projects: env.Projects}
	for _, t := range env.Tasks {
		out.Tasks = append(out.Tasks, t.ID)
	}
	return out, nil
}

// Pinned returns the pinned Projects and tasks.
func (c *Client) Pinned(ctx context.Context) (Pinned, error) {
	out, err := c.exec(ctx, "list", "--pinned", "--json")
	if err != nil {
		return Pinned{}, err
	}
	return ParsePinned(out)
}

// Pin marks a task (by id) or a Project (by name) with the plugin's ★.
func (c *Client) Pin(ctx context.Context, target string) error { return c.act(ctx, "pin", target) }

// Unpin removes that mark.
func (c *Client) Unpin(ctx context.Context, target string) error { return c.act(ctx, "unpin", target) }

// Coordinator is one Project coordinator as the plugin reports it. State is
// one of none, starting, working, blocked, idle, exited; Pane is the Herdr
// pane it runs in, empty when it is not running.
type Coordinator struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Agent  string `json:"agent"`
	Pane   string `json:"pane"`
	Queued int    `json:"queued"`
	Error  string `json:"error"`
	// Said is the coordinator's latest message to the human, as the plugin
	// reports it (empty when it has said nothing).
	Said looseText `json:"said"`
}

// looseText is a string that reads as empty when the plugin sends another
// shape, so a display-only field can never fail the whole status.
type looseText string

func (l *looseText) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) != nil {
		s = ""
	}
	*l = looseText(s)
	return nil
}

// Running reports a coordinator with a pane to go into.
func (c Coordinator) Running() bool { return c.Pane != "" && c.State != "none" && c.State != "exited" }

// ParseCoordinator reads `coordinator status --json`.
func ParseCoordinator(raw string) (Coordinator, error) {
	var c Coordinator
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Coordinator{}, err
	}
	return c, nil
}

// Coordinator reads one Project's coordinator status. It never starts it.
func (c *Client) Coordinator(ctx context.Context, project string) (Coordinator, error) {
	project = strings.TrimSpace(project)
	if project == "" || strings.HasPrefix(project, "-") {
		return Coordinator{}, errors.New("workboard: coordinator: invalid project")
	}
	out, err := c.exec(ctx, "coordinator", "--project", project, "status", "--json")
	if err != nil {
		return Coordinator{}, err
	}
	return ParseCoordinator(out)
}
