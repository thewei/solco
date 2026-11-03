package nativeui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/thewei/solco/internal/automations"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: automationsState.target（窗口线程写的插件目录与 Herdr session）、newAutomationsClient 测试缝、internal/automations.Client（插件 CLI 的唯一调用点）、remote_automations.go 的 remoteAutomationsOf
 * [OUTPUT]: 对外提供 Shell.hostAutomationsAdmin（remotehost.AutomationsAdmin）：手机对桌面 Automations 的管理——详情（含提示词/理由与最近 10 次运行）、新建草稿、保存编辑、批准/立即运行/暂停/恢复/删除、起调度器、标已读；每个写动作应答新读出的列表并让桌面页跟着重读
 * [POS]: 与桌面页同一个 automations.Client、同一套插件语义：校验与拒绝全是插件的，插件的拒绝原文（有界）作为 automation_refused 的 message 回给手机，其余只出稳定码；
 *        Host goroutine 只碰原子目标与每次新建的 Client，不碰任何 UI 状态；窗口侧用 applyOnWindow 重读列表（steps 通知与快照由此自然跟上）；
 *        门关闭时目标为空 → Open() 为 false → 能力位消失、端点 404；日志只记操作名与结果码，名字/提示词/理由/错误原文永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// automationsTarget is what a management call needs to reach the plugin.
type automationsTarget struct {
	root    string
	session string
	agents  []remotehost.AutomationAgent
}

const (
	remoteAutomationRuns    = 10
	remoteAutomationTimeout = 30 * time.Second
)

type remoteAutomationsAdmin struct{ s *Shell }

// hostAutomationsAdmin is the Host's management seam for the phone.
func (s *Shell) hostAutomationsAdmin() remotehost.AutomationsAdmin {
	return remoteAutomationsAdmin{s: s}
}

func (a remoteAutomationsAdmin) Open() bool { return a.s.automations.target.Load() != nil }

func (a remoteAutomationsAdmin) client() (*automations.Client, error) {
	t := a.s.automations.target.Load()
	if t == nil {
		return nil, &remotehost.AutomationError{Code: "automation_unavailable", Status: http.StatusNotFound}
	}
	return newAutomationsClient(t.root, t.session), nil
}

// refuse translates a plugin CLI failure: the plugin's own words (bounded) for
// a refusal, a bare stable code when the call could not even run.
func refuseAutomation(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &remotehost.AutomationError{Code: "automation_failed", Status: http.StatusBadGateway}
	}
	text := strings.TrimSpace(strings.TrimPrefix(err.Error(), "automations: "))
	if i := strings.Index(text, "\n"); i > 0 {
		text = text[:i]
	}
	return &remotehost.AutomationError{Code: "automation_refused", Status: http.StatusConflict, Detail: text}
}

// fresh re-reads the list for the answer and asks the window to do the same,
// so the desktop page, its notifications and the phone snapshot follow.
func (a remoteAutomationsAdmin) fresh(ctx context.Context, c *automations.Client) (remotehost.Automations, error) {
	var agents []remotehost.AutomationAgent
	if t := a.s.automations.target.Load(); t != nil {
		agents = t.agents
	}
	a.s.applyOnWindow(func() { a.s.refreshAutomations(true) })
	res, err := c.List(ctx)
	if err != nil {
		return remotehost.Automations{}, refuseAutomation(err)
	}
	return remoteAutomationsOf(res, agents), nil
}

func (a remoteAutomationsAdmin) Detail(ctx context.Context, name string) (remotehost.AutomationDetail, error) {
	c, err := a.client()
	if err != nil {
		return remotehost.AutomationDetail{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, remoteAutomationTimeout)
	defer cancel()
	list, err := c.List(ctx)
	if err != nil {
		return remotehost.AutomationDetail{}, refuseAutomation(err)
	}
	var found *automations.Automation
	for i := range list.Automations {
		if list.Automations[i].Name == name {
			found = &list.Automations[i]
		}
	}
	if found == nil {
		return remotehost.AutomationDetail{}, &remotehost.AutomationError{Code: "unknown_automation", Status: http.StatusNotFound}
	}
	hist, err := c.History(ctx, name)
	if err != nil {
		return remotehost.AutomationDetail{}, refuseAutomation(err)
	}
	return remoteAutomationDetailOf(*found, hist.Runs), nil
}

// remoteAutomationDetailOf maps one automation and its runs to the wire.
func remoteAutomationDetailOf(a automations.Automation, runs []automations.Run) remotehost.AutomationDetail {
	item := remoteAutomationsOf(automations.ListResult{Automations: []automations.Automation{a}}, nil).Automations[0]
	d := remotehost.AutomationDetail{
		Name: a.Name, State: item.State, Schedule: item.Schedule, NextRunMs: item.NextRunMs,
		AutomationFields: remotehost.AutomationFields{
			Cron: a.Cron, Repo: a.Repo, Workspace: a.WorkspaceMode, Agent: a.Agent, Model: a.Model,
			Prompt: a.Prompt, Rationale: a.Rationale, OnOverlap: a.OnOverlap, Headless: a.Headless,
			TimeoutMinutes: a.TimeoutMinutes, CatchUpMinutes: a.CatchUpMinutes, CleanupTTLHours: a.CleanupTTLHours,
		},
		Runs: make([]remotehost.AutomationRunInfo, 0, remoteAutomationRuns),
	}
	for _, r := range runs {
		if len(d.Runs) == remoteAutomationRuns {
			break
		}
		info := remotehost.AutomationRunInfo{
			ID: r.RunID, Status: r.Status, Trigger: r.Trigger, DurationSec: r.DurationSeconds,
			Agent: r.Agent, WorkspaceID: r.WorkspaceID, PaneID: r.PaneID,
			Unread: !r.Seen && (r.Status == "done" || r.Status == "failed"),
		}
		if t, err := time.Parse(time.RFC3339, r.StartedAt); err == nil {
			info.AtMs = t.UnixMilli()
		}
		d.Runs = append(d.Runs, info)
	}
	return d
}

func fieldsOf(f remotehost.AutomationFields) automations.Fields {
	return automations.Fields{
		Cron: f.Cron, Repo: f.Repo, Workspace: f.Workspace, Agent: f.Agent, Model: f.Model,
		Prompt: f.Prompt, Rationale: f.Rationale, OnOverlap: f.OnOverlap, Headless: f.Headless,
		TimeoutMinutes: f.TimeoutMinutes, CatchUpMinutes: f.CatchUpMinutes, CleanupTTLHours: f.CleanupTTLHours,
	}
}

func (a remoteAutomationsAdmin) Create(ctx context.Context, name string, f remotehost.AutomationFields) (remotehost.Automations, error) {
	return a.write(ctx, "create", func(ctx context.Context, c *automations.Client) error {
		return c.Add(ctx, name, fieldsOf(f))
	})
}

func (a remoteAutomationsAdmin) Update(ctx context.Context, name string, f remotehost.AutomationFields) (remotehost.Automations, error) {
	return a.write(ctx, "update", func(ctx context.Context, c *automations.Client) error {
		return c.Put(ctx, name, fieldsOf(f))
	})
}

func (a remoteAutomationsAdmin) Do(ctx context.Context, name string, op remotehost.AutomationOp) (remotehost.Automations, error) {
	return a.write(ctx, string(op), func(ctx context.Context, c *automations.Client) error {
		switch op {
		case remotehost.OpApproveAutomation:
			return c.Approve(ctx, name)
		case remotehost.OpRunAutomation:
			return c.Run(ctx, name)
		case remotehost.OpPauseAutomation:
			return c.Pause(ctx, name)
		case remotehost.OpResumeAutomation:
			return c.Resume(ctx, name)
		case remotehost.OpDeleteAutomation:
			return c.Remove(ctx, name)
		}
		return fmt.Errorf("automations: unknown operation")
	})
}

func (a remoteAutomationsAdmin) MarkSeen(ctx context.Context, runID string, all bool) (remotehost.Automations, error) {
	return a.write(ctx, "seen", func(ctx context.Context, c *automations.Client) error {
		if all {
			return c.MarkAllSeen(ctx)
		}
		return c.MarkSeen(ctx, runID)
	})
}

func (a remoteAutomationsAdmin) StartScheduler(ctx context.Context) (remotehost.Automations, error) {
	t := a.s.automations.target.Load()
	if t == nil {
		return remotehost.Automations{}, &remotehost.AutomationError{Code: "automation_unavailable", Status: http.StatusNotFound}
	}
	c := newAutomationsClient(t.root, t.session)
	logPath := ""
	if state := a.s.automations.remoteState.Load(); state != nil && *state != "" {
		logPath = *state + "/daemon.log"
	}
	if err := c.StartScheduler(logPath); err != nil {
		slog.Warn("remote automations: scheduler start failed", "error", err)
		return remotehost.Automations{}, refuseAutomation(err)
	}
	time.Sleep(automationsStartSettle)
	return a.fresh(ctx, c)
}

// write runs one plugin call, then answers the fresh list.
func (a remoteAutomationsAdmin) write(ctx context.Context, op string, do func(context.Context, *automations.Client) error) (remotehost.Automations, error) {
	c, err := a.client()
	if err != nil {
		return remotehost.Automations{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, remoteAutomationTimeout)
	defer cancel()
	if err := do(ctx, c); err != nil {
		slog.Warn("remote automations: refused", "op", op)
		return remotehost.Automations{}, refuseAutomation(err)
	}
	return a.fresh(ctx, c)
}
