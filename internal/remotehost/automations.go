package remotehost

import (
	"context"
	"log/slog"
	"net/http"
)

/**
 * [INPUT]: 依赖 Options.Automations（桌面 /automations 页缓存的数据缝，nativeui 提供）、server.go 的 guard/writeJSON/writeError
 * [OUTPUT]: 对外提供 AutomationRun/AutomationItem/Automations 与 GET /api/v3/automations、bootstrap 的 capabilities.automations
 * [POS]: 手机「Automations」只读列表（桌面 Automations 页的同一份数据，插件才是权威）：Host 只转发快照；
 *        不带提示词、理由、错误文本、仓库路径——手机只看「叫什么、何时跑、上次怎样」；缝在插件门关闭时回 ok=false，
 *        此时没有能力位、端点 404；日志只记设备与条数
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// AutomationRun is the newest run of one automation. Status is the plugin's
// own word (running|done|failed|queued|skipped|missed|cancelled|…); times are
// epoch milliseconds, absent when unknown.
type AutomationRun struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	AtMs        int64  `json:"atMs,omitempty"`
	DurationSec int64  `json:"durationSec,omitempty"`
}

// AutomationItem is one row of the Mac's Automations page. State is draft
// (waiting for a person's approval), paused, or active.
type AutomationItem struct {
	Name      string         `json:"name"`
	State     string         `json:"state"`
	Schedule  string         `json:"schedule,omitempty"` // the plugin's own words: "Daily 09:00"
	Agent     string         `json:"agent,omitempty"`
	Headless  bool           `json:"headless,omitempty"`
	NextRunMs int64          `json:"nextRunMs,omitempty"`
	LastRun   *AutomationRun `json:"lastRun,omitempty"`
	Unread    bool           `json:"unread,omitempty"` // the newest run finished and nobody opened it
}

// Automations is the page as the phone draws it. Scheduler is whether the
// plugin's daemon is running (nothing fires on schedule without it).
type Automations struct {
	Scheduler   bool             `json:"scheduler"`
	Automations []AutomationItem `json:"automations"`
}

// AutomationsFunc serves the list; ok=false means the plugin gate is closed
// (not installed, disabled, or outside the verified version window).
type AutomationsFunc func(ctx context.Context) (Automations, bool)

// handleAutomations answers GET /api/v3/automations.
func (s *Server) handleAutomations(w http.ResponseWriter, r *http.Request, dev Device) {
	if s.opts.Automations == nil {
		writeError(w, http.StatusNotFound, "automations_unavailable", "")
		return
	}
	out, ok := s.opts.Automations(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, "automations_unavailable", "")
		return
	}
	if out.Automations == nil {
		out.Automations = []AutomationItem{}
	}
	slog.Info("remote host automations", "device", dev.ID, "automations", len(out.Automations))
	writeJSON(w, http.StatusOK, out)
}
