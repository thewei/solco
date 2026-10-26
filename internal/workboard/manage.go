package workboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

/**
 * [INPUT]: 依赖 client.go 的 Exec 与 act/checkID，插件 CLI 的 archive/restore/clear（任务与 --repo 整个 Project）、`archive --done --repo`、`list --archived --json`、`coordinator --project R restart|new|stop`、`autopilot [--json]` / `autopilot on|off --project R`
 * [OUTPUT]: 对外提供 Client.Archived/Restore/Clear（归档视图）、ArchiveDone/ArchiveProject/RestoreProject（按 Project 整批）、CoordinatorControl（restart|new|stop）、Autopilots/SetAutopilot
 * [POS]: 管理面（P6）：每个动词的含义与拒绝都归插件（archive --repo 会停掉在跑的 Agent、协调者并关闭其自动化；clear 永久移除任务记录），本包只做参数守卫与形状解析，确认文案由调用方写；从不带 --anyway/--takeover，不开 autopilot 的 goal/设置通道
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Archived returns the archived tasks (`list --archived --json`).
func (c *Client) Archived(ctx context.Context) ([]Task, error) {
	out, err := c.exec(ctx, "list", "--archived", "--json")
	if err != nil {
		return nil, err
	}
	return ParseTasks(out)
}

// Restore brings one archived task back.
func (c *Client) Restore(ctx context.Context, id string) error { return c.act(ctx, "restore", id) }

// Clear removes one archived task's record for good (the plugin keeps work
// that is not saved elsewhere and says so).
func (c *Client) Clear(ctx context.Context, id string) error { return c.act(ctx, "clear", id) }

func checkRepo(op, repo string) error {
	if strings.TrimSpace(repo) == "" || strings.HasPrefix(repo, "-") {
		return fmt.Errorf("workboard: %s: invalid project", op)
	}
	return nil
}

// Swept is what `archive --done` reports.
type Swept struct {
	Archived []string          `json:"archived"`
	Failed   []json.RawMessage `json:"failed"`
}

// ArchiveDone archives every done task of one Project. It never touches the
// tasks that are not done: the plugin lists those as left.
func (c *Client) ArchiveDone(ctx context.Context, repo string) (Swept, error) {
	if err := checkRepo("archive", repo); err != nil {
		return Swept{}, err
	}
	out, err := c.exec(ctx, "archive", "--done", "--repo", repo, "--json")
	if err != nil {
		return Swept{}, err
	}
	var s Swept
	if json.Unmarshal([]byte(out), &s) != nil {
		return Swept{}, errors.New("workboard: archive: unreadable result")
	}
	return s, nil
}

// ArchiveProject puts a whole Project away. The plugin stops what is running
// in it, stops its coordinator and turns its automations off; RestoreProject
// brings the tasks and automations back.
func (c *Client) ArchiveProject(ctx context.Context, repo string) error {
	if err := checkRepo("archive", repo); err != nil {
		return err
	}
	_, err := c.exec(ctx, "archive", "--repo", repo)
	return err
}

// RestoreProject brings an archived Project back as it was.
func (c *Client) RestoreProject(ctx context.Context, repo string) error {
	if err := checkRepo("restore", repo); err != nil {
		return err
	}
	_, err := c.exec(ctx, "restore", "--repo", repo)
	return err
}

// CoordinatorControl restarts a Project's coordinator in the same
// conversation (restart), starts a fresh one (new) or stops it (stop).
func (c *Client) CoordinatorControl(ctx context.Context, project, verb string) error {
	if err := checkRepo("coordinator", project); err != nil {
		return err
	}
	switch verb {
	case "restart", "new", "stop":
	default:
		return fmt.Errorf("workboard: coordinator: unknown verb %q", verb)
	}
	_, err := c.exec(ctx, "coordinator", "--project", project, verb)
	return err
}

// Autopilots reads which coordinators have autopilot on, keyed by Project
// name. Only coordinators the plugin lists appear; the main and machine
// coordinators are not Projects and are left out.
func (c *Client) Autopilots(ctx context.Context) (map[string]bool, error) {
	out, err := c.exec(ctx, "autopilot", "--json")
	if err != nil {
		return nil, err
	}
	var rows []struct {
		On          bool   `json:"on"`
		Coordinator string `json:"coordinator"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, fmt.Errorf("workboard: autopilot: %w", err)
	}
	on := map[string]bool{}
	for _, r := range rows {
		if name, ok := strings.CutPrefix(r.Coordinator, "project:"); ok && name != "" {
			on[name] = r.On
		}
	}
	return on, nil
}

// SetAutopilot turns one Project coordinator's autopilot on (with its saved
// goal and settings; the plugin refuses when it has no goal) or off.
func (c *Client) SetAutopilot(ctx context.Context, project string, on bool) error {
	if err := checkRepo("autopilot", project); err != nil {
		return err
	}
	verb := "off"
	if on {
		verb = "on"
	}
	_, err := c.exec(ctx, "autopilot", verb, "--project", project)
	return err
}
