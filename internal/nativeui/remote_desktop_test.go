package nativeui

import (
	"context"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotedesktop"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
	"github.com/thewei/solco/internal/settings"
)

// [INPUT]: 依赖 remote_desktop.go（批准模态的状态机、设置映射）与 internal/remotedesktop 的 Authorizer 契约
// [OUTPUT]: Remote Desktop 呈现层守卫：默认关闭且每次询问、设置映射不放宽、批准三态（允许控制 / 仅查看 / 拒绝）原样回到 Authorizer、关闭模态即拒绝、手机放弃（ctx 取消）清掉待批准项、设置页文案不暴露内部细节
// [POS]: 2026-11-03 Remote Desktop 桌面端守卫；真机批准与权限流程在 P1 真机验收
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

func rdShell(t *testing.T) *Shell {
	t.Helper()
	s := NewShell(WithUserDataDir(t.TempDir()))
	s.loading = false
	t.Cleanup(s.stopRemoteDesktop)
	return s
}

func TestRemoteDesktopSettingsDefaultOffAndAsk(t *testing.T) {
	got := remoteDesktopSettingsFrom(settings.RemoteDesktopSettings{})
	if got.Enabled || !got.RequireApproval || got.DefaultTier != protocol.DefaultTier {
		t.Fatalf("zero value must be off, asking, standard: %+v", got)
	}
	got = remoteDesktopSettingsFrom(settings.RemoteDesktopSettings{Enabled: true, SkipApproval: true, Tier: "high"})
	if !got.Enabled || got.RequireApproval || got.DefaultTier != protocol.TierHigh {
		t.Fatalf("saved choice not carried: %+v", got)
	}
	if got := remoteDesktopSettingsFrom(settings.RemoteDesktopSettings{Tier: "ultra"}); got.DefaultTier != protocol.DefaultTier {
		t.Fatalf("bad tier must fall back: %+v", got)
	}
}

func authorize(s *Shell, ctx context.Context) <-chan struct {
	d   remotedesktop.Decision
	err error
} {
	out := make(chan struct {
		d   remotedesktop.Decision
		err error
	}, 1)
	go func() {
		d, err := shellAuthorizer{s}.Authorize(ctx, remotedesktop.AuthRequest{Device: protocol.DeviceInfo{ID: "d", Name: "Pixel"}, RequestedMode: protocol.ModeFullControl})
		out <- struct {
			d   remotedesktop.Decision
			err error
		}{d, err}
	}()
	return out
}

func waitRD(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestRemoteDesktopApprovalDecisions(t *testing.T) {
	for name, want := range map[string]remotedesktop.Decision{
		"control": {Allow: true, Mode: protocol.ModeFullControl},
		"view":    {Allow: true, Mode: protocol.ModeViewOnly},
		"deny":    {},
	} {
		t.Run(name, func(t *testing.T) {
			s := rdShell(t)
			res := authorize(s, context.Background())
			waitRD(t, func() bool { return s.rdesk.pending != nil && s.rdesk.approveOpen })
			if s.rdesk.pending.req.Device.Name != "Pixel" {
				t.Fatal("request lost its device")
			}
			s.rdDecide(want)
			r := <-res
			if r.err != nil || r.d != want {
				t.Fatalf("got %+v %v, want %+v", r.d, r.err, want)
			}
			if s.rdesk.pending != nil || s.rdesk.approveOpen {
				t.Fatal("approval state not cleared")
			}
		})
	}
}

func TestRemoteDesktopPhoneGivingUpClearsThePrompt(t *testing.T) {
	s := rdShell(t)
	ctx, cancel := context.WithCancel(context.Background())
	res := authorize(s, ctx)
	waitRD(t, func() bool { return s.rdesk.pending != nil })
	cancel()
	if r := <-res; r.err == nil {
		t.Fatal("a cancelled request must error")
	}
	waitRD(t, func() bool { return s.rdesk.pending == nil && !s.rdesk.approveOpen })
}

func TestRemoteDesktopDeviceNameFallback(t *testing.T) {
	if got := rdDeviceName(protocol.DeviceInfo{Name: "  "}); got != "A phone" {
		t.Fatalf("got %q", got)
	}
}
