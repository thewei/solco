package remotehost

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/thewei/solco/internal/personalagent"
)

/**
 * [INPUT]: 依赖 personal_agent.go 的 paSeam/paWrite/paBody/paID/paText 与 internal/personalagent 的 Group 客户端方法
 * [OUTPUT]: 对外提供 GET /api/v3/personal-agent/groups、groups/{id}?since=&limit=（群日志：成员、消息、投递、任务卡、真实 Agent 状态），
 *           POST …/groups（建，≥2 成员）、…/groups/{id}/messages（人的消息，只有精确 @ 的成员被唤醒）、…/rename、…/members/add|remove，
 *           bootstrap 的 capabilities.personalAgentGroups
 * [POS]: 手机真群聊的 Host 面（Assistants 3.1 U1）：群是 Go 核心里的权威会话，Host 只校验形状并转发同一个 CLI 客户端；
 *        手机不能代任何 Assistant 发言（没有 say 路由——发言只能来自真实在线的群 Agent），@ 的 id 必须出现在正文的 `@名字` 里由核心校验；
 *        日志只记设备、操作名与结果码，群名/消息/成员名永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	maxPAGroupMembers  = 8
	maxPAGroupMentions = 8
	maxPAGroupBody     = 8 << 10
	maxPAFeedLimit     = 200
)

func paIDs(w http.ResponseWriter, ids []string, min, max int) bool {
	if len(ids) < min || len(ids) > max {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !paIDRe.MatchString(id) || seen[id] {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return false
		}
		seen[id] = true
	}
	return true
}

func (s *Server) handlePAGroups(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	list, err := c.Groups(r.Context())
	if err != nil {
		s.paRefuse(w, dev, "groups", err)
		return
	}
	if list == nil {
		list = []personalagent.Group{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "groups": list})
}

func (s *Server) handlePAGroupFeed(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var since int64
	var limit int
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		since = n
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPAFeedLimit {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		limit = n
	}
	feed, err := c.GroupFeed(r.Context(), id, since, limit)
	if err != nil {
		s.paRefuse(w, dev, "group.feed", err)
		return
	}
	if feed.Messages == nil {
		feed.Messages = []personalagent.GroupMessage{}
	}
	if feed.Deliveries == nil {
		feed.Deliveries = []personalagent.GroupDelivery{}
	}
	if feed.Tasks == nil {
		feed.Tasks = []personalagent.GroupTaskCard{}
	}
	if feed.Agents == nil {
		feed.Agents = []personalagent.GroupAgentState{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "feed": feed})
}

func (s *Server) handlePACreateGroup(w http.ResponseWriter, r *http.Request, dev Device) {
	var req struct {
		RequestID string   `json:"requestId"`
		Title     string   `json:"title"`
		Members   []string `json:"members"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	title := strings.TrimSpace(req.Title)
	if len(title) > maxPAName || !paText(title, maxPAName) || strings.ContainsAny(title, "\r\n") {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if !paIDs(w, req.Members, 2, maxPAGroupMembers) {
		return
	}
	s.paWrite(w, r, dev, "group.create", func(ctx context.Context, c *personalagent.Client) (any, error) {
		g, err := c.CreateGroup(ctx, req.RequestID, title, req.Members)
		return map[string]any{"schema": 1, "group": g}, err
	})
}

func (s *Server) handlePAGroupMessage(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID string   `json:"requestId"`
		Body      string   `json:"body"`
		Mentions  []string `json:"mentions"`
		ReplyTo   string   `json:"replyTo"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" || !paText(body, maxPAGroupBody) || (req.ReplyTo != "" && !paIDRe.MatchString(req.ReplyTo)) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if len(req.Mentions) > 0 && !paIDs(w, req.Mentions, 1, maxPAGroupMentions) {
		return
	}
	s.paWrite(w, r, dev, "group.post", func(ctx context.Context, c *personalagent.Client) (any, error) {
		res, err := c.PostGroupMessage(ctx, req.RequestID, id, body, req.Mentions, req.ReplyTo)
		return map[string]any{"schema": 1, "result": res}, err
	})
}

func (s *Server) handlePAGroupRename(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID        string `json:"requestId"`
		Title            string `json:"title"`
		ExpectedRevision int    `json:"expectedRevision"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" || len(title) > maxPAName || !paText(title, maxPAName) || strings.ContainsAny(title, "\r\n") || req.ExpectedRevision < 1 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "group.rename", func(ctx context.Context, c *personalagent.Client) (any, error) {
		g, err := c.RenameGroup(ctx, req.RequestID, id, title, req.ExpectedRevision)
		return map[string]any{"schema": 1, "group": g}, err
	})
}

func (s *Server) handlePAGroupMember(add bool) func(http.ResponseWriter, *http.Request, Device) {
	op := "group.member.remove"
	if add {
		op = "group.member.add"
	}
	return func(w http.ResponseWriter, r *http.Request, dev Device) {
		id, ok := paID(w, r.PathValue("id"))
		if !ok {
			return
		}
		var req struct {
			RequestID   string `json:"requestId"`
			AssistantID string `json:"assistantId"`
		}
		if !paBody(w, r, &req, func() string { return req.RequestID }) {
			return
		}
		if !paIDRe.MatchString(req.AssistantID) {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		s.paWrite(w, r, dev, op, func(ctx context.Context, c *personalagent.Client) (any, error) {
			g, err := c.GroupMember(ctx, req.RequestID, id, req.AssistantID, add)
			return map[string]any{"schema": 1, "group": g}, err
		})
	}
}
