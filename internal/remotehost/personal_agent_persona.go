package remotehost

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/thewei/solco/internal/personalagent"
)

/**
 * [INPUT]: 依赖 personal_agent.go 的 paSeam/paWrite/paBody/paID/paText 与 internal/personalagent 的 Instructions/Runtime/MCP 客户端方法
 * [OUTPUT]: 对外提供 GET/POST /api/v3/personal-agent/assistants/{id}/instructions（工作方式文本，版本化；POST 存新版本，/restore 放回旧版本）、
 *           GET/POST …/runtime（Agent/Model/Effort 档案 + 真实在跑的 Agent）、GET …/mcp、POST …/mcp/grant、POST …/mcp/verify（Agent 自报的真实连接状态），
 *           bootstrap 的 capabilities.personalAgentPersona
 * [POS]: Assistants 3.1 U2 的 Host 面：人格/运行档案/MCP 授权都是 Go 核心里的权威数据，Host 只校验形状并转发同一个 CLI 客户端；
 *        MCP 注册表只含名字、传输与程序/主机名——参数、环境、头、URL 路径（凭据所在）永不上线；
 *        绑定是否生效由核心如实分四态（configured/granted/bound/verified）给出；日志只记设备、操作名与结果码，文本永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const maxPAInstructions = 8 << 10

var paMCPServerRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func (s *Server) handlePAInstructions(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	out, err := c.Instructions(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "persona.instructions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "instructions": out})
}

func (s *Server) handlePASetInstructions(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID        string `json:"requestId"`
		Body             string `json:"body"`
		ExpectedRevision int    `json:"expectedRevision"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" || !paText(body, maxPAInstructions) || req.ExpectedRevision < 1 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "persona.instructions.set", func(ctx context.Context, c *personalagent.Client) (any, error) {
		out, err := c.SetInstructions(ctx, req.RequestID, id, body, req.ExpectedRevision)
		return map[string]any{"schema": 1, "instructions": out}, err
	})
}

func (s *Server) handlePARestoreInstructions(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID        string `json:"requestId"`
		Revision         int    `json:"revision"`
		ExpectedRevision int    `json:"expectedRevision"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	if req.Revision < 1 || req.ExpectedRevision < 1 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "persona.instructions.restore", func(ctx context.Context, c *personalagent.Client) (any, error) {
		out, err := c.RestoreInstructions(ctx, req.RequestID, id, req.Revision, req.ExpectedRevision)
		return map[string]any{"schema": 1, "instructions": out}, err
	})
}

func (s *Server) handlePARuntime(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	out, err := c.Runtime(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "persona.runtime", err)
		return
	}
	if out.Running == nil {
		out.Running = []personalagent.RunningAgent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "runtime": out})
}

// validIdleSleepMin is the quiet minutes a phone may choose (0 = never sleep).
func validIdleSleepMin(n int) bool { return n == 0 || n == 15 || n == 30 || n == 60 }

func (s *Server) handlePASetRuntime(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID        string `json:"requestId"`
		Agent            string `json:"agent"`
		Model            string `json:"model"`
		Effort           string `json:"effort"`
		ExpectedRevision int    `json:"expectedRevision"`
		// IdleSleepMin is optional (0, 15, 30 or 60); left out, the earlier choice stays.
		IdleSleepMin *int `json:"idleSleepMin"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	if !paText(req.Agent, 32) || !paText(req.Model, 120) || !paText(req.Effort, 24) || req.ExpectedRevision < 0 ||
		strings.ContainsAny(req.Agent+req.Effort, "\r\n ") || strings.ContainsAny(req.Model, "\r\n") ||
		(req.IdleSleepMin != nil && !validIdleSleepMin(*req.IdleSleepMin)) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "persona.runtime.set", func(ctx context.Context, c *personalagent.Client) (any, error) {
		out, err := c.SetRuntime(ctx, req.RequestID, id, req.Agent, req.Model, req.Effort, req.ExpectedRevision, req.IdleSleepMin)
		if out.Running == nil {
			out.Running = []personalagent.RunningAgent{}
		}
		return map[string]any{"schema": 1, "runtime": out}, err
	})
}

func (s *Server) handlePAMCP(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	out, err := c.MCP(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "persona.mcp", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "mcp": paMCP(out)})
}

// paMCP keeps every list a list on the wire.
func paMCP(v personalagent.MCPView) personalagent.MCPView {
	if v.Servers == nil {
		v.Servers = []personalagent.MCPServerState{}
	}
	if v.Bindings == nil {
		v.Bindings = []personalagent.MCPBinding{}
	}
	return v
}

func (s *Server) handlePAMCPGrant(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID string `json:"requestId"`
		ServerID  string `json:"serverId"`
		Granted   bool   `json:"granted"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	if req.ServerID == "" || len(req.ServerID) > 64 || !paMCPServerRe.MatchString(req.ServerID) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	s.paWrite(w, r, dev, "persona.mcp.grant", func(ctx context.Context, c *personalagent.Client) (any, error) {
		out, err := c.GrantMCP(ctx, req.RequestID, id, req.ServerID, req.Granted)
		return map[string]any{"schema": 1, "mcp": paMCP(out)}, err
	})
}

func (s *Server) handlePAMCPVerify(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	s.paWrite(w, r, dev, "persona.mcp.verify", func(ctx context.Context, c *personalagent.Client) (any, error) {
		out, err := c.VerifyMCP(ctx, id)
		return map[string]any{"schema": 1, "mcp": paMCP(out)}, err
	})
}
