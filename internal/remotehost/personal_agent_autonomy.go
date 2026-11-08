package remotehost

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/thewei/solco/internal/personalagent"
)

/**
 * [INPUT]: personal_agent.go 的 paSeam/paWrite/paBody/paID/paText、internal/personalagent 的 HookKeys/HookRecords/CreateHookKey/RevokeHookKey/Triggers/TriggerAction
 * [OUTPUT]: GET …/assistants/{id}/autonomy（该 Assistant 的 webhook 密钥（无密钥正文）、最近收据、事件触发器与 webhook 入口路径）、
 *           POST …/hooks/keys（建密钥，secret 只在这一次响应里出现）、POST …/hooks/keys/{keyId}/revoke、POST …/triggers/{triggerId}/pause|resume|cancel，bootstrap 的 capabilities.personalAgentAutonomy
 * [POS]: 2026-11-05 主动性管理面（Gaps A05/A06/A09）：密钥、收据与触发器都是 Go 核心里的权威数据，Host 只校验形状并转发同一个 CLI 客户端；
 *        触发器的批准仍只走审批卡（approvals/{id}/decision），这里只有暂停/恢复/撤回；密钥与触发器必须属于路径里的 Assistant（先列后核对，别人的 id 一律 404）；
 *        收据只含来源、资源、已清洗的 reason 与路由结果，发送方的 body 永不上线；日志只记设备、操作名与结果码，secret/reason/brief 永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

var paHookKeyRe = regexp.MustCompile(`^hk_[0-9a-f]{16}$`)

func paAutonomyPath(assistantID string) string {
	return "/api/v3/hooks/assistants/" + assistantID + "/wake"
}

func (s *Server) handlePAAutonomy(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	keys, err := c.HookKeys(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "autonomy.keys", err)
		return
	}
	events, err := c.HookRecords(r.Context(), id, 20)
	if err != nil {
		s.paRefuse(w, dev, "autonomy.events", err)
		return
	}
	triggers, err := c.Triggers(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "autonomy.triggers", err)
		return
	}
	if keys == nil {
		keys = []personalagent.HookKey{}
	}
	if events == nil {
		events = []personalagent.HookRecord{}
	}
	if triggers == nil {
		triggers = []personalagent.Trigger{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "autonomy": map[string]any{
		"hookPath": paAutonomyPath(id), "keys": keys, "events": events, "triggers": triggers,
	}})
}

func (s *Server) handlePACreateHookKey(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		RequestID string   `json:"requestId"`
		Sources   []string `json:"sources"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	if len(req.Sources) < 1 || len(req.Sources) > 8 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	for _, src := range req.Sources {
		if !paText(src, 32) || strings.ContainsAny(src, "\r\n ") {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
	}
	s.paWrite(w, r, dev, "autonomy.key.create", func(ctx context.Context, c *personalagent.Client) (any, error) {
		key, secret, path, err := c.CreateHookKey(ctx, req.RequestID, id, req.Sources)
		return map[string]any{"schema": 1, "key": key, "secret": secret, "path": path}, err
	})
}

// paOwnsKey is true when the path's Assistant really holds that key.
func paOwnsKey(ctx context.Context, c *personalagent.Client, assistantID, keyID string) (bool, error) {
	keys, err := c.HookKeys(ctx, assistantID)
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		if k.ID == keyID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) handlePARevokeHookKey(w http.ResponseWriter, r *http.Request, dev Device) {
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	keyID := r.PathValue("keyId")
	if !paHookKeyRe.MatchString(keyID) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	var req struct {
		RequestID string `json:"requestId"`
	}
	if !paBody(w, r, &req, func() string { return req.RequestID }) {
		return
	}
	s.paWrite(w, r, dev, "autonomy.key.revoke", func(ctx context.Context, c *personalagent.Client) (any, error) {
		if owns, err := paOwnsKey(ctx, c, id, keyID); err != nil || !owns {
			if err == nil {
				err = &personalagent.Error{Code: "not_found", Message: "no such key"}
			}
			return nil, err
		}
		key, err := c.RevokeHookKey(ctx, req.RequestID, keyID)
		return map[string]any{"schema": 1, "key": key}, err
	})
}

func (s *Server) handlePATriggerAction(action string) deviceHandler {
	return func(w http.ResponseWriter, r *http.Request, dev Device) {
		id, ok := paID(w, r.PathValue("id"))
		if !ok {
			return
		}
		triggerID, ok := paID(w, r.PathValue("triggerId"))
		if !ok {
			return
		}
		var req struct {
			RequestID        string `json:"requestId"`
			ExpectedRevision int    `json:"expectedRevision"`
		}
		if !paBody(w, r, &req, func() string { return req.RequestID }) {
			return
		}
		if req.ExpectedRevision < 1 {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		s.paWrite(w, r, dev, "autonomy.trigger."+action, func(ctx context.Context, c *personalagent.Client) (any, error) {
			list, err := c.Triggers(ctx, id)
			if err != nil {
				return nil, err
			}
			owned := false
			for _, t := range list {
				owned = owned || t.ID == triggerID
			}
			if !owned {
				return nil, &personalagent.Error{Code: "not_found", Message: "no such trigger"}
			}
			t, err := c.TriggerAction(ctx, req.RequestID, action, triggerID, req.ExpectedRevision)
			return map[string]any{"schema": 1, "trigger": t}, err
		})
	}
}
