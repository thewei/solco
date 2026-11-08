package remotehost

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
)

/**
 * [INPUT]: server_test.go 的 harness、quota.go 的 handler 与 Options.Usage 缝
 * [OUTPUT]: 对外提供 /api/v3/quota 的行为回归：无缝 404、需配对、refresh=1 传 force、providers 永不为 null、bootstrap 能力位
 * [POS]: quota.go 的守卫测试；缝由测试注入，绝不读真实 usage
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type fakeUsage struct {
	mu    sync.Mutex
	force []bool
	quota Quota
}

func (f *fakeUsage) serve(_ context.Context, force bool) Quota {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.force = append(f.force, force)
	return f.quota
}

func (f *fakeUsage) forces() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.force...)
}

func usageHarness(t *testing.T) (*harness, *fakeUsage) {
	t.Helper()
	h := newHarness(t)
	f := &fakeUsage{}
	h.srv.opts.Usage = f.serve
	return h, f
}

func TestQuotaAbsentWithoutSource(t *testing.T) {
	h := newHarness(t)
	if resp := h.get("/api/v3/quota", h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "quota_unavailable" {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestQuotaNeedsAuth(t *testing.T) {
	h, f := usageHarness(t)
	if resp := h.get("/api/v3/quota", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if len(f.forces()) != 0 {
		t.Fatal("an unauthenticated read reached Usage")
	}
}

func TestQuotaForwardsForceAndNeverNullProviders(t *testing.T) {
	h, f := usageHarness(t)
	if resp := h.get("/api/v3/quota", h.token, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("plain status %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/quota?refresh=1", h.token, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status %d", resp.StatusCode)
	}
	if forces := f.forces(); len(forces) != 2 || forces[0] || !forces[1] {
		t.Fatalf("forces %v, want [false true]", forces)
	}
	var out struct {
		Providers []QuotaProvider `json:"providers"`
	}
	// the last answer (empty seam quota) must still carry a JSON array, not null
	if err := json.NewDecoder(h.get("/api/v3/quota", h.token, nil).Body).Decode(&out); err != nil || out.Providers == nil {
		t.Fatalf("providers decode %v, body %#v", err, out.Providers)
	}
}

func TestQuotaBootstrapAdvertisesCapability(t *testing.T) {
	h, _ := usageHarness(t)
	var body struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Capabilities["quota"] {
		t.Fatalf("capabilities %v", body.Capabilities)
	}
}

// sampleQuota is the golden file's desktop: one live reading with plain and
// counted windows, one rate-limited provider still carrying its old reading,
// one provider switched off on the Mac.
func sampleQuota() Quota {
	return Quota{Providers: []QuotaProvider{
		{
			ID: "codex", State: "live", Plan: "Plus",
			Windows: []QuotaWindow{
				{Name: "5-hour", UsedPct: 0, SpanMs: 5 * 60 * 60 * 1000, ResetsAtMs: 1785549600000},
				{Name: "Weekly", UsedPct: 19, SpanMs: 7 * 24 * 60 * 60 * 1000, ResetsAtMs: 1786068000000},
			},
			Notes:     []string{"Credits $0.00", "1 rate-limit reset available"},
			AtMs:      1785546001000,
			CheckedMs: 1785546001000,
		},
		{
			ID: "claude", State: "rate_limited", Detail: "rate limited, retry in 15m0s", Plan: "Max 5×",
			Windows:   []QuotaWindow{{Name: "Weekly", UsedPct: 87, SpanMs: 7 * 24 * 60 * 60 * 1000, ResetsAtMs: 1786068000000}},
			AtMs:      1785542400000,
			CheckedMs: 1785546001000,
			RetryAtMs: 1785546900000,
		},
		{ID: "cursor", State: "disabled", Windows: []QuotaWindow{}},
	}}
}
