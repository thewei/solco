package remotehost

/**
 * [INPUT]: personal_agent.go 的缝（paSeam/paWrite/paDecode/paID/paText）与 personalagent.Client 的 memory/proactivity 命令
 * [OUTPUT]: Assistants 记忆路由——两个 Markdown 文件（GET …/assistants/{id}/memory 读 USER.md + ASSISTANT.md 与各自 rev、
 *           POST …/assistants/{id}/memory/write 整文件覆盖其一，rev 过期即 stale_revision）与主动性规则
 *           （GET|POST …/assistants/{id}/proactivity）；capability personalAgentMemory / personalAgentProactivity
 * [POS]: 与桌面同一个 CLI 客户端；权威仍是插件 daemon 与它的记忆文件，Host 只校验请求形状；日志只记设备/操作名/结果码，记忆正文永不进日志
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
	notes, err := c.ReadNotes(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "memory.read", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "notes": notes})
}

// handlePAMemoryWrite replaces one memory file. There is no approval step; the
// guard against losing someone's edit is the rev of the read it was made from.
func (s *Server) handlePAMemoryWrite(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		File        string `json:"file"`
		Text        string `json:"text"`
		ExpectedRev string `json:"expectedRev"`
	}
	if !paDecode(w, r, &req) {
		return
	}
	if (req.File != personalagent.NotesUser && req.File != personalagent.NotesAssistant) ||
		!paText(req.Text, maxPAMemoryText) || !paText(req.ExpectedRev, 32) || strings.ContainsAny(req.ExpectedRev, "\r\n") {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "memory.write", func(ctx context.Context, c *personalagent.Client) (any, error) {
		n, err := c.WriteNotes(ctx, id, req.File, req.Text, req.ExpectedRev)
		return map[string]any{"schema": 1, "notes": n}, err
	})
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
