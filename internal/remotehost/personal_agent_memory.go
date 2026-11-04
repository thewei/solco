package remotehost

/**
 * [INPUT]: personal_agent.go 的缝（paSeam/paWrite/paBody/paID/paText）与 personalagent.Client 的 memory/proactivity 命令
 * [OUTPUT]: Assistants 3.1 U5 路由——记忆（GET …/assistants/{id}/memory 活记忆+整理发现、GET …/memory/{id}/history、
 *           POST …/assistants/{id}/memory/add、POST …/memory/{id}/edit|approve|forget）与主动性规则
 *           （GET|POST …/assistants/{id}/proactivity）；capability personalAgentMemory / personalAgentProactivity
 * [POS]: 与桌面同一个 CLI 客户端；权威仍是插件 daemon 的 SQLite，Host 只校验请求形状；日志只记设备/操作名/结果码，记忆正文永不进日志
 */

import (
	"context"
	"net/http"
	"strings"

	"github.com/thewei/solco/internal/personalagent"
)

func (s *Server) handlePAMemory(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	review, err := c.ReviewMemory(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "memory.review", err)
		return
	}
	for _, p := range []*[]string{&review.StaleCandidates, &review.Conflicts} {
		if *p == nil {
			*p = []string{}
		}
	}
	if review.Duplicates == nil {
		review.Duplicates = [][]string{}
	}
	if review.Items == nil {
		review.Items = []personalagent.MemoryItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "review": review})
}

func (s *Server) handlePAMemoryHistory(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	versions, err := c.MemoryHistory(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "memory.history", err)
		return
	}
	if versions == nil {
		versions = []personalagent.MemoryVersion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "versions": versions})
}

func (s *Server) handlePAMemoryAdd(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID string `json:"requestId"`
		Content   string `json:"content"`
		Kind      string `json:"kind"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" || !paText(content, maxPAText) || !paText(req.Kind, 32) || strings.ContainsAny(req.Kind, "\r\n") {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "memory.add", func(ctx context.Context, c *personalagent.Client) (any, error) {
		m, err := c.AddMemory(ctx, req.RequestID, id, content, req.Kind)
		return map[string]any{"schema": 1, "memory": m}, err
	})
}

// handlePAMemoryEdit serves edit (new text), approve (a candidate) and forget.
func (s *Server) handlePAMemoryEdit(action string) deviceHandler {
	core := map[string]string{"edit": "correct", "approve": "approve", "forget": "forget"}[action]
	return func(w http.ResponseWriter, r *http.Request, dev Device) {
		id, ok := paID(w, r.PathValue("id"))
		if !ok {
			return
		}
		var req struct {
			RequestID        string `json:"requestId"`
			ExpectedRevision int    `json:"expectedRevision"`
			Content          string `json:"content"`
		}
		if !paBody(w, r, &req, func() string { return req.RequestID }) {
			return
		}
		content := strings.TrimSpace(req.Content)
		if req.ExpectedRevision < 1 || (action == "edit") != (content != "") || !paText(content, maxPAText) {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		s.paWrite(w, r, dev, "memory."+action, func(ctx context.Context, c *personalagent.Client) (any, error) {
			m, err := c.EditMemory(ctx, req.RequestID, core, id, req.ExpectedRevision, content)
			return map[string]any{"schema": 1, "memory": m}, err
		})
	}
}

func (s *Server) handlePAProactivity(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	p, err := c.Proactivity(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "proactivity.show", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "proactivity": p})
}

func (s *Server) handlePASetProactivity(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID        string `json:"requestId"`
		ExpectedRevision int    `json:"expectedRevision"`
		personalagent.ProactivityInput
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	in := req.ProactivityInput
	if req.ExpectedRevision < 0 || !paText(in.Level, 16) || !paText(in.QuietStart, 5) || !paText(in.QuietEnd, 5) || !paText(in.TZ, 64) ||
		strings.ContainsAny(in.Level+in.QuietStart+in.QuietEnd+in.TZ, "\r\n") {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "proactivity.set", func(ctx context.Context, c *personalagent.Client) (any, error) {
		p, err := c.SetProactivity(ctx, req.RequestID, id, in, req.ExpectedRevision)
		return map[string]any{"schema": 1, "proactivity": p}, err
	})
}
