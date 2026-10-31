package remotehost

import (
	"log/slog"
	"net/http"

	"github.com/thewei/solco/internal/chatsession"
)

/**
 * [INPUT]: 依赖 Backend.Projection（窗格的 cwd）、chatsession.ListFiles（与桌面 `@` 补全同一份规则）、server.go 的 guard/resolveInstance
 * [OUTPUT]: 对外提供 GET /api/v3/panes/{paneID}/chat/files?token=（scope view）——手机输入框 `@` 补全：窗格项目目录里匹配 token 的条目名（浅层、子目录前缀下钻、最多 20 条、目录以 `/` 结尾、点文件仅在 token 以点开头时）
 * [POS]: 只读且只列名字：不读内容、不写；路径被限制在窗格 cwd 内（`..` 与指向目录外的符号链接一律列空）；未知窗格 404 unknown_pane；token/目录/条目名不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type filesBody struct {
	Files []string `json:"files"`
}

func (s *Server) handleChatFiles(w http.ResponseWriter, r *http.Request, _ Device) {
	instance, ok := s.resolveInstance(w, r)
	if !ok {
		return
	}
	paneID := r.PathValue("paneID")
	proj, err := s.opts.Backend.Projection(instance)
	if err != nil {
		slog.Warn("remote host files: projection failed", "instance", instance, "error", err)
		writeError(w, http.StatusBadGateway, "backend_unavailable", "")
		return
	}
	var cwd string
	found := false
	for _, p := range proj.Panes {
		if p.ID == paneID {
			cwd, found = p.CWD, true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "unknown_pane", "")
		return
	}
	files := chatsession.ListFiles(cwd, r.URL.Query().Get("token"), true)
	if files == nil {
		files = []string{}
	}
	writeJSON(w, http.StatusOK, filesBody{Files: files})
}
