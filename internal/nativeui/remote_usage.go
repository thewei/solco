package nativeui

import (
	"context"

	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/usage"
)

/**
 * [INPUT]: 依赖 settings.Usage（开关与同意）、internal/usage.Service（TTL 缓存、底线与限速）、usageState 的 site/agySignIn 原子镜像、applyOnWindow/invalidateQuickPanel
 * [OUTPUT]: 对外提供 Shell.hostUsage（Host goroutine 取手机 Quota 视图数据）与 quotaProviderOf/quotaStateOf/quotaWindowOf（usage.Result → 线格式）
 * [POS]: 手机端 Quota 与桌面 /quota 同源同语义：读哪些 provider、是否已同意，完全沿用桌面设置（手机没有任何凭据路径）；
 *        手机打开视图 = 桌面打开页面的同一次「读一次」，force = 手动 Refresh（Service 的底线与限速语义不变）；
 *        只碰线程安全面（settingsService.Current 的锁内快照、Service 自己的锁、fetcher 读的原子镜像），从不触 UI 状态；
 *        映射是纯函数：intenum → 线上单词，窗口原样搬运（时间变 epoch 毫秒，零值省略）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// hostUsage serves the phone's quota view (GET /quota). It runs on the Host's
// goroutines and touches only thread-safe state: the settings service's
// locked snapshot, the usage Service's own lock, and the atomics the fetchers
// read. Opening the view reads once — the same trigger as the desktop page —
// and force is the phone's pull-to-refresh; the reuse window's floor and a
// vendor rate limit still hold.
func (s *Shell) hostUsage(ctx context.Context, force bool) remotehost.Quota {
	cfg := s.settingsService.Current().Usage
	site := cfg.ZhipuSite
	s.usage.site.Store(&site)
	s.usage.agySignIn.Store(cfg.SignInAllowed[string(usage.Antigravity)])

	var ids []usage.ID
	for _, id := range usage.All {
		if cfg.Enabled[string(id)] {
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		s.usage.svc.Refresh(ctx, ids, force, func() {
			s.applyOnWindow(func() { s.invalidateQuickPanel() })
		})
	}

	providers := make([]remotehost.QuotaProvider, 0, len(usage.All))
	for _, id := range usage.All {
		providers = append(providers, quotaProviderOf(id, cfg.Enabled[string(id)], s.usage.svc.Result(id)))
	}
	return remotehost.Quota{Providers: providers}
}

// quotaProviderOf maps one provider's standing to the wire.
func quotaProviderOf(id usage.ID, enabled bool, r usage.Result) remotehost.QuotaProvider {
	p := remotehost.QuotaProvider{
		ID:      string(id),
		State:   quotaStateOf(enabled, r),
		Detail:  r.Detail,
		Windows: []remotehost.QuotaWindow{},
	}
	if !r.Has {
		return p
	}
	p.Plan = r.Snapshot.Plan
	if !r.Snapshot.At.IsZero() {
		p.AtMs = r.Snapshot.At.UnixMilli()
	}
	for _, w := range r.Snapshot.Windows {
		p.Windows = append(p.Windows, quotaWindowOf(w))
	}
	if len(r.Snapshot.Notes) > 0 {
		p.Notes = r.Snapshot.Notes
	}
	if !r.Checked.IsZero() {
		p.CheckedMs = r.Checked.UnixMilli()
	}
	if !r.RetryAt.IsZero() {
		p.RetryAtMs = r.RetryAt.UnixMilli()
	}
	return p
}

// quotaStateOf names a provider's standing in the wire's own words. A
// provider the user switched off never hides why: it simply says disabled.
func quotaStateOf(enabled bool, r usage.Result) string {
	if !enabled {
		return "disabled"
	}
	switch r.State {
	case usage.StateLive:
		return "live"
	case usage.StateSetup:
		return "setup"
	case usage.StateExpired:
		return "expired"
	case usage.StateRateLimited:
		return "rate_limited"
	case usage.StateUnavailable:
		return "unavailable"
	case usage.StateCustomEndpoint:
		return "custom_endpoint"
	}
	return "idle"
}

func quotaWindowOf(w usage.Window) remotehost.QuotaWindow {
	q := remotehost.QuotaWindow{
		Name:    w.Name,
		UsedPct: w.UsedPct,
		Amount:  w.Amount,
		Used:    w.Used,
		Max:     w.Max,
		Unit:    w.Unit,
		Group:   w.Group,
		Minor:   w.Minor,
	}
	if w.Span > 0 {
		q.SpanMs = w.Span.Milliseconds()
	}
	if !w.Resets.IsZero() {
		q.ResetsAtMs = w.Resets.UnixMilli()
	}
	return q
}
