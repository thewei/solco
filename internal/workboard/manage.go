package workboard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

/**
 * [INPUT]: 依赖 client.go 的 Exec 与 act/checkID，插件 CLI 的 archive/restore/clear（任务与 --repo 整个 Project）、`archive --done --repo`、`list --archived --json`、`coordinator --project R restart|new|stop`、`autopilot [--json]` / `autopilot on|off --project R`
 * [OUTPUT]: 对外提供 Client.AddProject（无任务登记文件夹为 Project：加一条只有“任务被合并”才会触发的事件自动化 → 从它回读插件起的名字 → 立即移除）、Client.Archived/Restore/Clear（归档视图）、ArchiveDone/ArchiveProject/RestoreProject（按 Project 整批）、CoordinatorControl（restart|new|stop）、Autopilots/SetAutopilot
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

// AddProject registers a folder as a Project without a task. The plugin has no
// verb for it: a folder is named the first time something is added there, and
// the one thing that adds nothing to the board is an automation. So this adds
// an event automation that only a merged task could fire, reads the name the
// plugin gave the Project back from it, and removes it again. It returns that
// name; the cleanup runs even when the read fails.
func (c *Client) AddProject(ctx context.Context, folder string) (string, error) {
	folder = strings.TrimSpace(folder)
	if !strings.HasPrefix(folder, "/") {
		return "", errors.New("workboard: add project: invalid folder")
	}
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	name := "solco-new-project-" + hex.EncodeToString(nonce[:])
	if err := c.AddAutomation(ctx, NewAutomation{Name: name, Prompt: "Registers this folder as a Project; removed right away.", Repo: folder, When: "merged"}); err != nil {
		return "", err
	}
	// The ctx may be what ran out: the registration automation must not stay.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = c.RemoveAutomation(cleanup, name)
	}()
	all, err := c.Automations(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range all {
		if a.Name == name && a.Repo != "" {
			return a.Repo, nil
		}
	}
	return "", errors.New("workboard: add project: the plugin did not name the Project")
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
