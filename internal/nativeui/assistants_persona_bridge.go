package nativeui

/**
 * [INPUT]: 依赖 assistants_bridge 的 client/write/agentAllowed 与 internal/personalagent 的 Instructions/Runtime/MCP 客户端
 * [OUTPUT]: AssistantsBridge.Persona（工作方式文本 + 版本历史 + 各面启动时用的版本、Agent/Model/Effort 档案 + 真实在跑的 Agent、MCP 服务器四态）、
 *           SaveInstructions / RestoreInstructions / SaveRuntime / GrantMCP / VerifyMCP
 * [POS]: Assistants 3.1 U2 的桌面面：权威数据全在插件 SQLite 核心里，桥只转发并把拒绝原样（带稳定码）交给页面；
 *        Agent 选择与会话抽屉同一道闸（Settings → Agents 关掉的 Agent 不能成为档案默认）；
 *        VerifyMCP 起一个无头 Agent 只读它自报的 MCP 连接状态，不发模型请求；日志与快照都不带文本正文、MCP 参数或凭据
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"time"

	"github.com/thewei/solco/internal/personalagent"
)

// assistantsProbeTimeout bounds a MCP probe (a headless Agent start).
const assistantsProbeTimeout = 60 * time.Second

// PersonaView is the Work style tab: everything about how the Assistant works,
// read on open.
type PersonaView struct {
	Instructions personalagent.Instructions `json:"instructions"`
	Runtime      personalagent.Runtime      `json:"runtime"`
	MCP          personalagent.MCPView      `json:"mcp"`
}

// Persona reads the working style, the Agent profile and the MCP picture.
func (b *AssistantsBridge) Persona(ctx context.Context, id string) (PersonaView, error) {
	var out PersonaView
	c, err := b.client()
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsCallTimeout)
	defer cancel()
	if out.Instructions, err = c.Instructions(ctx, id); err != nil {
		return out, err
	}
	if out.Runtime, err = c.Runtime(ctx, id); err != nil {
		return out, err
	}
	if out.Runtime.Running == nil {
		out.Runtime.Running = []personalagent.RunningAgent{}
	}
	if out.MCP, err = c.MCP(ctx, id); err != nil {
		return out, err
	}
	return out, nil
}

// SaveInstructions stores a new revision (it reaches Agents started afterwards).
func (b *AssistantsBridge) SaveInstructions(ctx context.Context, requestID, id, body string, revision int) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.SetInstructions(ctx, requestID, id, body, revision)
		return err
	})
}

// RestoreInstructions makes an earlier revision's text current as a new revision.
func (b *AssistantsBridge) RestoreInstructions(ctx context.Context, requestID, id string, revision, expected int) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.RestoreInstructions(ctx, requestID, id, revision, expected)
		return err
	})
}

// SaveRuntime sets the Agent/Model/Effort new starts default to. An Agent the
// settings do not offer is refused; a running Agent is never restarted.
func (b *AssistantsBridge) SaveRuntime(ctx context.Context, requestID, id, agent, model, effort string, revision int) error {
	if err := b.agentAllowed(agent); err != nil {
		return err
	}
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.SetRuntime(ctx, requestID, id, agent, model, effort, revision)
		return err
	})
}

// GrantMCP allows or withdraws one server for the Assistant (next start).
func (b *AssistantsBridge) GrantMCP(ctx context.Context, requestID, id, serverID string, granted bool) error {
	return b.write(ctx, func(ctx context.Context, c *personalagent.Client) error {
		_, err := c.GrantMCP(ctx, requestID, id, serverID, granted)
		return err
	})
}

// VerifyMCP asks a headless Agent which of the granted servers it connects to.
func (b *AssistantsBridge) VerifyMCP(ctx context.Context, id string) error {
	c, err := b.client()
	if err != nil {
		return err
	}
	if id == "" {
		return errors.New("invalid: assistant id")
	}
	ctx, cancel := context.WithTimeout(ctx, assistantsProbeTimeout)
	defer cancel()
	_, err = c.VerifyMCP(ctx, id)
	return err
}
