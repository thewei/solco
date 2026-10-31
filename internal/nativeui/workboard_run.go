package nativeui

/**
 * [INPUT]: 依赖 projects_section 的 taskAgentPane、workspace 的 agentCardForPane
 * [OUTPUT]: taskAgentCard（任务 → 它的 Agent 卡，经 projects_section 的 taskAgentPane）、projectCoordinatorCard（Project 协调者的 Agent 卡）
 * [POS]: Projects WebView 页的聊天目标解析：把任务/协调者接到 Herdr 已有的 Agent pane（任务→Agent 的连接沿用侧栏任务行的 taskAgentPane：pane cwd 在任务 worktree 内），本身不画 UI、不新增 Herdr 方法、不读插件文件
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"github.com/thewei/solco/internal/agent"
)

// taskAgentCard is the task's Agent card, found through the same pane the
// sidebar's task row opens (taskAgentPane). A plain shell has no card, so there
// is no Chat to open.
func (s *Shell) taskAgentCard(id string) (agent.AgentCardModel, bool) {
	pane, ok := s.taskAgentPane(id)
	if !ok {
		return agent.AgentCardModel{}, false
	}
	return s.agentCardForPane(pane)
}

// projectCoordinatorCard is the Agent card of the Project's running
// coordinator, when this session has its pane open.
func (s *Shell) projectCoordinatorCard(project workboardProject) (agent.AgentCardModel, bool) {
	pane, ok := s.projectCoordinatorPane(project)
	if !ok {
		return agent.AgentCardModel{}, false
	}
	return s.agentCardForPane(pane)
}
