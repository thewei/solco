package remotehost

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/personalagent"
)

/**
 * [INPUT]: 依赖 Options.CoordinatorHistory（桌面 history 目录里该 Assistant 的 Coordinator 最新转录）、PersonalAgent 客户端（只读 Coordinator 是否跑过）、internal/chatsession（Reader/State）
 * [OUTPUT]: Server.visibleTurns（Options.FilterTurns：Solco 自己键入的行——Coordinator 的 standing brief——以及 Agent 对它的回复，不进手机的任何 Chat 状态，与桌面 Chat 一致）；对外提供 GET /api/v3/personal-agent/assistants/{id}/coordinator/chat[?older=N]（scope view，bootstrap capabilities.personalAgentCoordinatorChat）：Coordinator 没在线时它上一段对话的只读 State，与桌面 `/assistants` 的「同一 session」预览同源；`older=N` 把窗口再往上翻 N 页（无状态：每次请求新开 Reader，翻页数由手机带来，上限同渲染窗口上界），`hasOlder` 照 State 给出
 * [POS]: 手机 Assistant 页也是一个聊天窗口（2026-11-07）：在线走窗格 chat，离线读这条；没有转录 = 409 no_conversation（手机显示开场邀请）；不写任何文件、不启动 Agent，常驻 Host（无目录接缝）不宣告该能力
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// CoordinatorTranscript is where an Assistant's previous Coordinator
// conversation lives: the provider and the transcript file the catalog found.
type CoordinatorTranscript struct {
	Provider history.AgentID
	Path     string
}

// CoordinatorHistoryFunc finds the newest transcript of an Assistant's
// Coordinator. ran says the Coordinator has been started before, so a catalog
// that does not know the file yet may be asked to look again. ok=false: none.
type CoordinatorHistoryFunc func(ctx context.Context, assistantID string, ran bool) (CoordinatorTranscript, bool)

// coordinatorChatBody is the previous conversation, read-only: the same State a
// pane chat carries, addressed by Assistant (there is no pane to name).
type coordinatorChatBody struct {
	AssistantID string `json:"assistantId"`
	chatsession.State
}

// visibleTurns leaves out what the desktop's Chat leaves out (Options.FilterTurns:
// Solco's own typing into an Agent, the Coordinator's standing brief, and the
// Agent's greeting of it). Every phone chat state goes through it, so a
// Coordinator's history and its live pane read alike and a handover does not
// make a brief appear.
func (s *Server) visibleTurns(turns []chatsession.ChatTurn) []chatsession.ChatTurn {
	if s.opts.FilterTurns == nil {
		return turns
	}
	return s.opts.FilterTurns(turns)
}

// coordinatorOlderMax caps ?older=: the window stops offering earlier pages at
// chatsession.MaxAccumulated anyway, so a larger ask only repeats the same page.
const coordinatorOlderMax = chatsession.MaxAccumulated / history.WindowLimit

func (s *Server) handlePACoordinatorChat(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if s.opts.CoordinatorHistory == nil {
		writeError(w, http.StatusNotFound, "personal_agent_unavailable", "")
		return
	}
	older := 0
	if raw := r.URL.Query().Get("older"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "older must be a non-negative integer")
			return
		}
		older = min(n, coordinatorOlderMax)
	}
	ctx := r.Context()
	co, err := c.Coordinator(ctx, id)
	if err != nil {
		s.paRefuse(w, dev, "coordinator.chat", err)
		return
	}
	ran := co.TaskID != "" && co.State != personalagent.CoordinatorNone
	found, ok := s.opts.CoordinatorHistory(ctx, id, ran)
	if !ok {
		writeError(w, http.StatusConflict, "no_conversation", "")
		return
	}
	reader, err := chatsession.Open(found.Provider, found.Path)
	if err == nil {
		_, err = reader.Sync()
	}
	if err != nil {
		slog.Warn("remote host coordinator chat: read failed", "device", dev.ID, "error", err)
		writeError(w, http.StatusConflict, "no_conversation", "")
		return
	}
	for range older {
		if !reader.Older() {
			break
		}
	}
	state := reader.State(time.Now())
	state.Turns = s.visibleTurns(state.Turns)
	writeJSON(w, http.StatusOK, coordinatorChatBody{AssistantID: id, State: state})
}
