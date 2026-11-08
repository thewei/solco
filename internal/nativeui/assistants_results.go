package nativeui

/**
 * [INPUT]: 依赖 internal/personalagent 的 Results（插件 `assistant results`：一天内已结束的任务 + 最新 run 的窗格）与 Snapshot 借来的窗格集合、Herdr session
 * [OUTPUT]: AssistantsResult（一张「任务结果」系统卡：标题、完成/失败/待评审、时间、可打开的窗格）、AssistantsBridge.results（Snapshot 用；窗格只在本窗口 Herdr 树里才给出）
 * [POS]: Assistants 页聊天上方的「任务结果」卡片源（2026-11-07）：卡片完全由插件已提交的任务状态生成（确定性，不经过模型改写），页面只负责展示与已读水位；轻量读（一次有界查询，不读完整任务列表）；不读对话、任务简报/结果正文，不入日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/personalagent"
)

// resultLimit bounds one read; the page shows the newest two and counts the rest.
const resultLimit = 4

// AssistantsResult is one settled task as a card above the composer.
type AssistantsResult struct {
	TaskID string `json:"taskId"`
	Title  string `json:"title"`
	// State is the task's own: done | failed | review_required.
	State string `json:"state"`
	At    int64  `json:"at"`
	// PaneID is the newest run's pane while it is in this window's Herdr tree
	// ("" = nothing to open).
	PaneID string `json:"paneId,omitempty"`
}

// results reads the Assistant's result cards. A failed read is "no cards", not
// an error: the page's other surfaces must not depend on it (an older plugin
// has no such command).
func results(ctx context.Context, c *personalagent.Client, id, session string, panes map[string]bool) []AssistantsResult {
	out := []AssistantsResult{}
	list, err := c.Results(ctx, id, resultLimit)
	if err != nil {
		return out
	}
	for _, r := range list {
		card := AssistantsResult{TaskID: r.TaskID, Title: r.Title, State: r.State, At: r.At}
		same := r.HerdrSession == "" || herdr.ParseRef(r.HerdrSession).Session == session || r.HerdrSession == session
		if r.PaneID != "" && same && panes[r.PaneID] {
			card.PaneID = r.PaneID
		}
		out = append(out, card)
	}
	return out
}
