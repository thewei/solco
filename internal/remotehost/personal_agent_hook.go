package remotehost

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"unicode/utf8"

	"github.com/thewei/solco/internal/personalagent"
)

/**
 * [INPUT]: personal_agent.go 的 paSeam（桌面 Assistants 核心客户端缝）、internal/personalagent.Client.HookIngest
 * [OUTPUT]: POST /api/v3/hooks/assistants/{id}/wake —— 外部服务请 ONE Assistant 做一次检查（check.requested）的入站口；202 = 已持久记录（不代表有人看过）
 * [POS]: 2026-11-05 Webhook 入站（docs/assistant-webhook-sleep-wake-design.md W2）：不走设备 Bearer，认证只有核心验的逐 Assistant、可撤销 HMAC 密钥
 *        （X-Solco-Hook-Key / -Timestamp / -Delivery / -Signature，签名覆盖 method+path+时间戳+delivery+body 哈希）；Host 只做体积/UTF-8 校验后原样转发，
 *        验签、5 分钟时间窗、资源归属、delivery 去重、每密钥每小时限流、事件落库全在 Go 核心事务里；认证失败一律同一个 401，
 *        不泄露密钥是否存在；插件没有开放门（无密钥即等于关闭）时 404；日志只记操作名与结果码，永不记 body/密钥/签名/reason
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const maxHookBody = 16 << 10

func (s *Server) handleHookWake(w http.ResponseWriter, r *http.Request) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	if len(r.Header.Values("X-Solco-Hook-Key")) != 1 || len(r.Header.Values("X-Solco-Signature")) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHookBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "")
		return
	}
	if !utf8.Valid(raw) {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	ctx, cancel := durablePAContext(r.Context(), maxPAWriteDuration)
	defer cancel()
	rec, err := c.HookIngest(ctx, personalagent.HookDelivery{
		KeyID: r.Header.Get("X-Solco-Hook-Key"), Method: r.Method, Path: r.URL.Path,
		Timestamp: r.Header.Get("X-Solco-Timestamp"), Delivery: r.Header.Get("X-Solco-Delivery"),
		Signature: r.Header.Get("X-Solco-Signature"), Body: string(raw),
	})
	if err != nil {
		var pe *personalagent.Error
		if !errors.As(err, &pe) {
			slog.Warn("remote host webhook failed", "op", "hook.ingest")
			writeError(w, http.StatusBadGateway, "personal_agent_failed", "")
			return
		}
		status := http.StatusConflict
		switch pe.Code {
		case "unauthorized":
			writeError(w, http.StatusUnauthorized, "unauthorized", "")
			return
		case "rate_limited":
			w.Header().Set("Retry-After", "3600")
			status = http.StatusTooManyRequests
		case "invalid":
			status = http.StatusBadRequest
		case "not_found", "assistant_disabled":
			// A valid key whose Assistant is gone or paused: say nothing more.
			status = http.StatusNotFound
		case "internal":
			status = http.StatusBadGateway
		}
		slog.Warn("remote host webhook refused", "op", "hook.ingest", "code", pe.Code)
		writeError(w, status, pe.Code, truncateUTF8(pe.Message, 240))
		return
	}
	slog.Info("remote host webhook accepted", "op", "hook.ingest", "routing", rec.Routing, "replayed", rec.Replayed)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"schema": 1, "eventId": rec.EventID, "assistantId": rec.AssistantID,
		"receipt": rec.Receipt, "routing": rec.Routing, "replayed": rec.Replayed,
	})
}
