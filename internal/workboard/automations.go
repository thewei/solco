package workboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

/**
 * [INPUT]: 依赖 client.go 的 Exec/checkID 守卫，插件 CLI 的 `automations --json` 与 `automations on|off|run|remove <id>`（1.2.26 沙盒实测形状）
 * [OUTPUT]: 对外提供 Automation、Client.Automations（读）、SetAutomation/RunAutomation/RemoveAutomation（写，按插件的 id 定位）、NewAutomation/AutomationEvents/Client.AddAutomation（`automations add "<name>" --prompt … (--schedule … | --when … [--every-repo] [--if …])`）\n * [POS]: 自动化的创建与管理；schedule 与 if 的语法和校验全归插件（原文拒绝透传，不创建任何东西），客户端只保证 name/prompt 非空、触发器二选一、不把值当成选项；run 会真起 Agent 并耗 token，确认文案由调用方写；整份清单一次读出，按 repo 归入对应 Project
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Automation is one recurring job the plugin runs for a repo.
type Automation struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	On       bool   `json:"on"`
	Trigger  string `json:"trigger"` // in plain words: "daily 09:00", "when a task is merged"
	Repo     string `json:"repo"`
	Research bool   `json:"research"`
	Chat     bool   `json:"chat"`
	Next     int64  `json:"next"`   // epoch ms of the next scheduled firing, 0 when none
	OffWhy   string `json:"offWhy"` // why it is off, when the plugin turned it off
}

// Automations lists every automation (`automations --json`).
func (c *Client) Automations(ctx context.Context) ([]Automation, error) {
	out, err := c.exec(ctx, "automations", "--json")
	if err != nil {
		return nil, err
	}
	var all []Automation
	if err := json.Unmarshal([]byte(out), &all); err != nil {
		return nil, fmt.Errorf("workboard: automations: %w", err)
	}
	live := all[:0]
	for _, a := range all {
		if a.ID != "" {
			live = append(live, a)
		}
	}
	return live, nil
}

func (c *Client) automation(ctx context.Context, verb, id string) error {
	if strings.TrimSpace(id) == "" || strings.HasPrefix(id, "-") {
		return fmt.Errorf("workboard: automations %s: invalid automation", verb)
	}
	_, err := c.exec(ctx, "automations", verb, id)
	return err
}

// SetAutomation turns one on or off; off, it never fires and keeps everything.
func (c *Client) SetAutomation(ctx context.Context, id string, on bool) error {
	if on {
		return c.automation(ctx, "on", id)
	}
	return c.automation(ctx, "off", id)
}

// RunAutomation fires one now: it adds a task and puts an agent on it.
func (c *Client) RunAutomation(ctx context.Context, id string) error {
	return c.automation(ctx, "run", id)
}

// RemoveAutomation removes one; the tasks it made stay.
func (c *Client) RemoveAutomation(ctx context.Context, id string) error {
	return c.automation(ctx, "remove", id)
}

// AutomationEvents are the events `--when` takes in the pinned 1.2.x plugin,
// in the plugin's order. A renamed one is refused by the plugin, not here.
var AutomationEvents = []string{"added", "approval", "review-failed", "failed", "needs-you", "merged"}

// NewAutomation is what `automations add` takes: a name, the prompt each
// firing's task gets, and one trigger — a Schedule in the plugin's own words
// ("every 2h", "daily 09:00", "mon 09:00", cron "0 9 * * 1") or a When event
// (EveryRepo: of any repo; If: only for a task like 'branch = feature/*').
type NewAutomation struct {
	Name, Prompt, Repo string
	Schedule, When, If string
	EveryRepo          bool
	Research, Chat     bool
}

// AddAutomation creates an automation. The plugin parses the trigger and the
// condition; its refusal comes back as the error and nothing is created.
func (c *Client) AddAutomation(ctx context.Context, a NewAutomation) error {
	name, prompt, repo := strings.TrimSpace(a.Name), strings.TrimSpace(a.Prompt), strings.TrimSpace(a.Repo)
	schedule, when, cond := strings.TrimSpace(a.Schedule), strings.TrimSpace(a.When), strings.TrimSpace(a.If)
	for _, v := range []string{name, prompt, repo, schedule, when, cond} {
		if strings.HasPrefix(v, "-") {
			return errors.New("workboard: automations add: invalid text")
		}
	}
	switch {
	case name == "" || prompt == "":
		return errors.New("workboard: automations add: a name and a prompt are required")
	case (schedule == "") == (when == ""):
		return errors.New("workboard: automations add: give a schedule or an event, not both")
	case schedule != "" && (cond != "" || a.EveryRepo):
		return errors.New("workboard: automations add: a condition goes with an event")
	}
	args := []string{"automations", "add", name, "--prompt", prompt}
	if schedule != "" {
		args = append(args, "--schedule", schedule)
	} else {
		args = append(args, "--when", when)
		if a.EveryRepo {
			args = append(args, "--every-repo")
		}
		if cond != "" {
			args = append(args, "--if", cond)
		}
	}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	if a.Chat {
		args = append(args, "--chat")
	} else if a.Research {
		args = append(args, "--research")
	}
	_, err := c.exec(ctx, args...)
	return err
}
