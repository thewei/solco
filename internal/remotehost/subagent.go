package remotehost

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/thewei/solco/internal/chatsession"
)

/**
 * [INPUT]: 依赖 resolveChat（窗格 → Provider 转录）、chatsession.Reader.Subagent（子代理自己的文件，只读）、server.go 的 guard/resolveInstance
 * [OUTPUT]: 对外提供 GET /api/v3/panes/{paneID}/chat/subagents/{subID}（scope view）：抽屉里一个子代理自己的只读对话（`subagent` 卡片 + `turns` + 可选 `error`）
 * [POS]: 手机的子 Agent 抽屉；subID 必须是该会话 State.subagents 已列出的 id（路径永远来自 Host 自己的读取，客户端不传路径）；与桌面同一份 chatsession.OpenSubagent；不写 Provider 文件，子代理转录正文不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func (s *Server) handleChatSubagent(w http.ResponseWriter, r *http.Request, _ Device) {
	instance, ok := s.resolveInstance(w, r)
	if !ok {
		return
	}
	paneID, subID := r.PathValue("paneID"), r.PathValue("subID")
	target := s.resolveChat(instance, paneID)
	if target.Code != "" {
		writeError(w, chatErrorStatus[target.Code], target.Code, "")
		return
	}
	reader, err := chatsession.Open(target.Provider, target.Path)
	if err == nil {
		_, err = reader.Sync()
	}
	if err != nil {
		slog.Warn("remote host subagent: read failed", "instance", instance, "pane", paneID, "error", err)
		writeError(w, http.StatusConflict, "no_conversation", "")
		return
	}
	reader.State(s.opts.Now()) // lists the conversation's sub-agents and links them to their spawning calls
	view, err := reader.Subagent(subID, s.opts.Now())
	if errors.Is(err, chatsession.ErrUnknownSubagent) {
		writeError(w, http.StatusNotFound, "unknown_subagent", "")
		return
	}
	if err != nil {
		slog.Warn("remote host subagent: open failed", "instance", instance, "pane", paneID, "error", err)
		writeError(w, http.StatusBadGateway, "subagent_unavailable", "")
		return
	}
	writeJSON(w, http.StatusOK, view)
}
