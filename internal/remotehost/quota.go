package remotehost

import (
	"log/slog"
	"net/http"
)

/**
 * [INPUT]: 依赖 Options.Usage（桌面 Quota 页的数据缝，nativeui 提供）、server.go 的 guard/writeJSON/writeError
 * [OUTPUT]: 对外提供 Quota/QuotaProvider/QuotaWindow 与 GET /api/v3/quota?refresh=1、bootstrap 的 capabilities.quota
 * [POS]: 手机「Quota」只读视图（桌面 /quota 的同一份数据）：Host 只转发快照，读谁、是否已同意全部沿用桌面已保存的
 *        开关，手机没有任何凭据路径；?refresh=1 是手机的 pull-to-refresh（TTL/底线/限速语义在缝的实现里不变）；
 *        每家 provider 自带失败态，端点本身不报错；日志只记设备、provider 数与 force，来源细节永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// QuotaWindow is one limit: a share of an allowance spent over a span, with
// the moment it resets. Times are epoch milliseconds; absent means unknown.
type QuotaWindow struct {
	Name       string  `json:"name"`
	UsedPct    float64 `json:"usedPct"` // 0..100
	SpanMs     int64   `json:"spanMs,omitempty"`
	ResetsAtMs int64   `json:"resetsAtMs,omitempty"`
	Amount     string  `json:"amount,omitempty"` // "$11.60 of $20" / "186 of 300"
	Used       float64 `json:"used,omitempty"`   // a counted allowance ("124 of 500 requests")
	Max        float64 `json:"max,omitempty"`
	Unit       string  `json:"unit,omitempty"`
	Group      string  `json:"group,omitempty"` // windows of one model set render as one block
	Minor      bool    `json:"minor,omitempty"` // a sub-limit of the main allowance
}

// QuotaProvider is one provider's standing: its last good reading (if any)
// and why the latest attempt did or did not refresh it. State is the wire's
// own word: disabled|idle|live|setup|expired|rate_limited|unavailable|
// custom_endpoint.
type QuotaProvider struct {
	ID        string        `json:"id"` // claude|codex|cursor|zhipu|copilot|antigravity
	State     string        `json:"state"`
	Detail    string        `json:"detail,omitempty"` // short, secret-free reason
	Plan      string        `json:"plan,omitempty"`
	Windows   []QuotaWindow `json:"windows"`
	Notes     []string      `json:"notes,omitempty"`
	AtMs      int64         `json:"atMs,omitempty"`      // the reading's own moment
	CheckedMs int64         `json:"checkedMs,omitempty"` // the latest attempt
	RetryAtMs int64         `json:"retryAtMs,omitempty"` // rate_limited: when to try again
}

// Quota is the desktop's quota page as the phone draws it: every provider in
// the page's order, each with its own standing.
type Quota struct {
	Providers []QuotaProvider `json:"providers"`
}

// handleQuota answers GET /api/v3/quota. The read happens inside the seam
// (one TTL-cached pass over the enabled providers, ~25 s worst case per
// provider), so the handler adds no timeout of its own.
func (s *Server) handleQuota(w http.ResponseWriter, r *http.Request, dev Device) {
	if s.opts.Usage == nil {
		writeError(w, http.StatusNotFound, "quota_unavailable", "")
		return
	}
	force := r.URL.Query().Get("refresh") == "1"
	quota := s.opts.Usage(r.Context(), force)
	if quota.Providers == nil {
		quota.Providers = []QuotaProvider{}
	}
	slog.Info("remote host quota", "device", dev.ID, "providers", len(quota.Providers), "force", force)
	writeJSON(w, http.StatusOK, quota)
}
