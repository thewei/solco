package nativeui

import (
	"context"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
	"github.com/thewei/solco/internal/usage"
)

/**
 * [INPUT]: usage_page_test.go 的 usageTestShell/fakeUsageFetcher（假 Fetcher、内存 secrets）、Shell.hostUsage
 * [OUTPUT]: 对外提供 remote_usage.go 的行为回归：只读已开启的 provider、关掉的如实报 disabled、TTL 内不重读、线格式映射（窗口/时间/状态）
 * [POS]: 手机 Quota 缝的守卫测试——沿用桌面开关语义（同意之前绝不读）且从不触碰真实 Keychain/网络/子进程
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func hostUsageShell(t *testing.T) (*Shell, *fakeUsageFetcher, *fakeUsageFetcher) {
	t.Helper()
	codex := &fakeUsageFetcher{id: usage.Codex, snap: codexSnapshot()}
	claude := &fakeUsageFetcher{id: usage.Claude}
	s, _ := usageTestShell(t, codex, claude)
	s.applySettings(func(v *settings.Settings) error {
		v.Usage.Configured = true
		v.Usage.Enabled = map[string]bool{string(usage.Codex): true}
		return nil
	})
	return s, codex, claude
}

func TestHostUsageFollowsTheSavedSwitches(t *testing.T) {
	s, codex, claude := hostUsageShell(t)
	quota := s.hostUsage(context.Background(), false)

	if got := len(quota.Providers); got != len(usage.All) {
		t.Fatalf("providers = %d, want %d (every provider answers, even off ones)", got, len(usage.All))
	}
	var codexState, claudeState string
	for _, p := range quota.Providers {
		switch p.ID {
		case "codex":
			codexState = p.State
		case "claude":
			claudeState = p.State
		}
	}
	if codexState != "live" {
		t.Fatalf("codex state %q, want live", codexState)
	}
	if claudeState != "disabled" {
		t.Fatalf("claude state %q, want disabled", claudeState)
	}
	if codex.calls.Load() != 1 || claude.calls.Load() != 0 {
		t.Fatalf("reads: codex %d claude %d, want exactly the enabled one", codex.calls.Load(), claude.calls.Load())
	}
}

func TestHostUsageReusesTheCacheWithinTheWindow(t *testing.T) {
	s, codex, _ := hostUsageShell(t)
	s.hostUsage(context.Background(), false)
	s.hostUsage(context.Background(), false)
	if codex.calls.Load() != 1 {
		t.Fatalf("codex read %d times within the reuse window, want 1", codex.calls.Load())
	}
}

func TestHostUsageMapsTheWire(t *testing.T) {
	s, _, _ := hostUsageShell(t)
	quota := s.hostUsage(context.Background(), false)
	var codex *remotehost.QuotaProvider
	for i := range quota.Providers {
		if quota.Providers[i].ID == "codex" {
			codex = &quota.Providers[i]
		}
	}
	if codex == nil {
		t.Fatal("no codex provider")
	}
	if codex.Plan != "Plus" || len(codex.Windows) != 1 || codex.AtMs == 0 || codex.CheckedMs == 0 {
		t.Fatalf("codex provider %+v", codex)
	}
	w := codex.Windows[0]
	if w.Name != "5-hour" || w.UsedPct != 62 || w.SpanMs != int64((5*time.Hour).Milliseconds()) || w.ResetsAtMs == 0 {
		t.Fatalf("window %+v", w)
	}
}
