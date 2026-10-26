package workboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

/**
 * [INPUT]: 依赖 client.go 的 Exec/checkID 守卫，插件 CLI 的 `automations --json` 与 `automations on|off|run|remove <id>`（1.2.26 沙盒实测形状）
 * [OUTPUT]: 对外提供 Automation、Client.Automations（读）、SetAutomation/RunAutomation/RemoveAutomation（写，按插件的 id 定位）
 * [POS]: 自动化只做管理不做创建（创建要 prompt/触发器/条件，交给对协调者说）；run 会真起 Agent 并耗 token，确认文案由调用方写；整份清单一次读出，按 repo 归入对应 Project
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
