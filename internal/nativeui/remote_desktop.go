package nativeui

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/remotedesktop"
	"github.com/thewei/solco/internal/remotedesktop/platform/auto"
	"github.com/thewei/solco/internal/remotedesktop/platform/macos"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
	"github.com/thewei/solco/internal/remotedesktop/rtc"
	"github.com/thewei/solco/internal/remotedesktop/session"
	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

/**
 * [INPUT]: 依赖 internal/remotedesktop（Service/Authorizer/Settings）与其 platform/auto + macos 后端、protocol 线格式、remotehost.IsVirtualInterface、settings.RemoteDesktop、controls.go 的开关/分段/按钮、settingsGroup/row/statusPill 共享设置组件、activateWindow、mygo.NewNotification
 * [OUTPUT]: remoteDesktopSettingsPath、Shell.settingsRemoteDesktop（/settings/remote-desktop：开关 → 状态卡 → 权限（Screen Recording / Accessibility 分开列、系统设置直达、自动复检）→ 连接（每次询问、画质）→ 当前会话与一键断开）、Shell.remoteDesktopDialog（手机请求连接的批准模态：拒绝 / 仅查看 / 允许控制）、Shell.remoteDesktopService、Shell.syncRemoteDesktop、Shell.stopRemoteDesktop、remoteDesktopSeam
 * [POS]: Remote Desktop（手机看 Mac 整个桌面并操控，用户 2026-11-03 批准）在桌面端的唯一呈现与挂载点：默认关闭；开启后手机经 Remote Host 的 /remote-desktop 信令请求连接，Service 先查权限与忙闲，再由本文件弹出批准模态（默认每次询问，窗口被拉到最前并发系统通知），
 *        用户可降级为“仅查看”；会话只在 Mac 用户一键断开、撤销手机、关闭开关或退出时结束；屏幕内容、剪贴板与输入文本永不进日志与设置；Swift helper 由 Service 的 macOS 后端按需拉起，Solco 自己不抓屏、不注入输入
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	remoteDesktopSettingsPath = "/settings/remote-desktop"
	rdPollEvery               = 2 * time.Second
	rdPageSeenWindow          = 5 * time.Second
	rdPermissionTimeout       = 8 * time.Second
)

var (
	rdTierLabels = []string{"Smooth", "Standard", "High"}
	rdTiers      = []protocol.Tier{protocol.TierSmooth, protocol.TierStandard, protocol.TierHigh}
)

// rdApproval is one phone waiting for the Mac user's answer.
type rdApproval struct {
	req   remotedesktop.AuthRequest
	reply chan remotedesktop.Decision
}

// remoteDesktopState is presentation state; every field is touched on the window lane only.
type remoteDesktopState struct {
	svc *remotedesktop.Service

	perms      protocol.PermissionStatus
	permsKnown bool
	permsErr   string
	pageSeen   time.Time
	polling    bool
	sessions   []session.Info

	pending     *rdApproval
	approveOpen bool
	err         string

	unlockDraft string      // the password being typed; cleared on Save
	autoUnlock  atomic.Bool // mirror of settings.RemoteDesktop.AutoUnlock for the session goroutines
}

// rdUnlockKey is the credential key of the Mac login password in Solco's encrypted store.
const rdUnlockKey = "remotedesktop.unlock"

// remoteDesktopService builds the Service on first use. It costs nothing until a phone connects:
// the macOS backend launches the helper per session.
func (s *Shell) remoteDesktopService() *remotedesktop.Service {
	r := &s.rdesk
	if r.svc != nil {
		return r.svc
	}
	backend := auto.New(macos.Options{Logger: slog.Default()})
	r.svc = remotedesktop.NewService(remotedesktop.Config{
		Backend:    backend,
		Authorizer: shellAuthorizer{s},
		Trust:      shellTrust{s},
		// One ICE list for the whole remote stack: the Host's own (static STUN/TURN + the relay's short-lived
		// TURN credentials). It is used only for a phone that arrives off the LAN (srflx/relay in its offer);
		// LAN and Tailscale phones connect on host candidates alone. Tunnels stay in (a Tailscale phone arrives
		// on one); VM bridges and system links do not.
		ICEServers: s.remoteICEServers,
		// The Mac login password lives only in the encrypted credential file; the session reads it for the
		// instant of typing and the phone can never ask for it.
		UnlockSecret: func() (string, bool) {
			pw, ok := appSecrets.Get(rdUnlockKey)
			return pw, ok && pw != ""
		},
		AutoUnlock: r.autoUnlock.Load,
		InterfaceFilter: func(name string) bool {
			return !remotehost.IsVirtualInterface(name) || strings.HasPrefix(name, "utun")
		},
		OnChange: func() { s.applyOnWindow(func() { s.rdRefreshSessions() }) },
		Log:      slog.Default(),
	})
	r.svc.SetSettings(remoteDesktopSettingsFrom(s.settings.RemoteDesktop))
	return r.svc
}

// remoteICEServers is the Host's STUN/TURN list in the media transport's shape (none while the Host is not built).
func (s *Shell) remoteICEServers(ctx context.Context) []rtc.ICEServer {
	srv := s.remote.srv
	if srv == nil {
		return nil
	}
	var out []rtc.ICEServer
	for _, sv := range srv.ICEServers(ctx) {
		out = append(out, rtc.ICEServer{URLs: sv.URLs, Username: sv.Username, Credential: sv.Credential})
	}
	return out
}

func remoteDesktopSettingsFrom(v settings.RemoteDesktopSettings) remotedesktop.Settings {
	tier := protocol.Tier(v.Tier)
	if !tier.Valid() {
		tier = protocol.DefaultTier
	}
	return remotedesktop.Settings{Enabled: v.Enabled, RequireApproval: !v.SkipApproval, DefaultTier: tier}
}

// remoteDesktopSeam is the Host's view of the Service (nil in headless runs without a user-data dir).
func (s *Shell) remoteDesktopSeam() remotehost.RemoteDesktop {
	if svc := s.remoteDesktopService(); svc != nil {
		return svc
	}
	return nil
}

// syncRemoteDesktop pushes the saved switches into the Service (launch and every change).
func (s *Shell) syncRemoteDesktop() {
	s.rdesk.autoUnlock.Store(s.settings.RemoteDesktop.AutoUnlock && s.settings.RemoteDesktop.UnlockSaved)
	s.remoteDesktopService().SetSettings(remoteDesktopSettingsFrom(s.settings.RemoteDesktop))
}

func (s *Shell) stopRemoteDesktop() {
	if s.rdesk.svc != nil {
		s.rdesk.svc.Close()
	}
}

func (s *Shell) setRemoteDesktop(mutate func(*settings.RemoteDesktopSettings)) {
	s.applySettings(func(v *settings.Settings) error {
		mutate(&v.RemoteDesktop)
		return nil
	})
	s.syncRemoteDesktop()
}

func (s *Shell) rdRefreshSessions() {
	if s.rdesk.svc != nil {
		s.rdesk.sessions = s.rdesk.svc.Sessions()
	}
	s.updateTray()
	if s.quickPanel != nil {
		s.fitQuickPanel()
		s.invalidateQuickPanel()
	}
}

// remoteDesktopTrayLabel names the phone on this Mac's screen for the menu bar ("" = no session): the
// device's name and whether it only watches. Never anything about what is on the screen.
func (s *Shell) remoteDesktopTrayLabel() string {
	if len(s.rdesk.sessions) == 0 {
		return ""
	}
	info := s.rdesk.sessions[0]
	name := strings.TrimSpace(info.Device.Name)
	if name == "" {
		name = "a phone"
	}
	if !info.Controlling {
		return name + " (viewing)"
	}
	return name + " (controlling)"
}

// quickPanelRemoteHeight is the banner's height in the quick panel (one row, plus its rule).
const quickPanelRemoteHeight = 44

// quickPanelRemoteDesktop is the quick panel's banner while a phone is on the screen: who, and a Disconnect
// that ends the session on the spot (the Mac user's own switch, the same call as Settings).
func (s *Shell) quickPanelRemoteDesktop(c *ui.Context) {
	label := s.remoteDesktopTrayLabel()
	if label == "" || s.rdesk.svc == nil {
		return
	}
	sp := Spacing()
	t := c.Theme()
	id := s.rdesk.sessions[0].ID
	ui.Row(c.Key("qp-rd")).FillWidth().Padding(sp.XS, sp.M, sp.XS).Gap(sp.S).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Remote Desktop · "+label).FontSize(Typography().Caption).Grow(1).SingleLine().TextColor(t.TextMuted)
		if compactButton(c, "Disconnect").Clicked() {
			s.rdesk.svc.Disconnect(id)
		}
	})
}

// --- approval ---------------------------------------------------------------

// shellAuthorizer asks the person at the Mac: the window comes forward, a notification goes out,
// and the modal blocks the phone's request until Allow, View Only, Deny, dismissal or the timeout.
type shellAuthorizer struct{ s *Shell }

func (a shellAuthorizer) Authorize(ctx context.Context, req remotedesktop.AuthRequest) (remotedesktop.Decision, error) {
	s := a.s
	ap := &rdApproval{req: req, reply: make(chan remotedesktop.Decision, 1)}
	s.applyOnWindow(func() {
		s.rdesk.pending, s.rdesk.approveOpen = ap, true
		s.activateWindow()
		if s.win != nil { // headless tests have no app loop to hop to
			if n := mygo.NewNotification(mygo.NotificationOptions{
				ID: "solco-remote-desktop", Title: "Remote Desktop request",
				Body: fmt.Sprintf("%s wants to see and control this Mac.", rdDeviceName(req.Device)),
			}); n != nil {
				n.Show()
			}
		}
	})
	select {
	case d := <-ap.reply:
		return d, nil
	case <-ctx.Done():
		s.applyOnWindow(func() {
			if s.rdesk.pending == ap {
				s.rdesk.pending, s.rdesk.approveOpen = nil, false
			}
		})
		return remotedesktop.Decision{}, ctx.Err()
	}
}

// shellTrust keeps the remembered approvals in the saved settings. The Host calls it from request
// goroutines, so every touch hops onto the window lane where settings are owned.
type shellTrust struct{ s *Shell }

func (t shellTrust) Granted(deviceID string) (protocol.Mode, bool) {
	var mode protocol.Mode
	var ok bool
	t.s.callOnWindow(func() {
		for _, g := range t.s.settings.RemoteDesktop.Trusted {
			if g.DeviceID == deviceID {
				mode, ok = protocol.Mode(g.Mode), protocol.Mode(g.Mode).Valid()
				return
			}
		}
	})
	return mode, ok
}

func (t shellTrust) Remember(device protocol.DeviceInfo, mode protocol.Mode) {
	if device.ID == "" || !mode.Valid() {
		return
	}
	t.s.applyOnWindow(func() {
		t.s.setRemoteDesktop(func(v *settings.RemoteDesktopSettings) {
			g := settings.RemoteDesktopTrust{DeviceID: device.ID, Name: strings.TrimSpace(device.Name), Mode: string(mode), Since: time.Now().Unix()}
			for i := range v.Trusted {
				if v.Trusted[i].DeviceID == device.ID {
					v.Trusted[i] = g
					return
				}
			}
			v.Trusted = append(v.Trusted, g)
		})
	})
}

func (t shellTrust) Forget(deviceID string) {
	t.s.applyOnWindow(func() { t.s.rdForgetTrust(deviceID) })
}

// rdForgetTrust drops one remembered approval (window lane).
func (s *Shell) rdForgetTrust(deviceID string) {
	s.setRemoteDesktop(func(v *settings.RemoteDesktopSettings) {
		kept := v.Trusted[:0:0]
		for _, g := range v.Trusted {
			if g.DeviceID != deviceID {
				kept = append(kept, g)
			}
		}
		v.Trusted = kept
	})
}

func rdDeviceName(d protocol.DeviceInfo) string {
	if n := strings.TrimSpace(d.Name); n != "" {
		return n
	}
	return "A phone"
}

func (s *Shell) rdDecide(d remotedesktop.Decision) {
	r := &s.rdesk
	if r.pending != nil {
		r.pending.reply <- d // buffered: the Authorizer may already have given up
	}
	r.pending, r.approveOpen = nil, false
}

// remoteDesktopDialog is the approval modal. Dismissing it (Esc, outside click) is a refusal.
func (s *Shell) remoteDesktopDialog(c *ui.Context) {
	r := &s.rdesk
	if r.pending == nil {
		r.approveOpen = false
		return
	}
	if !r.approveOpen {
		s.rdDecide(remotedesktop.Decision{})
		return
	}
	req := r.pending.req
	canControl := r.perms.Accessibility || !r.permsKnown
	ui.Modal(c, &r.approveOpen, func() {
		t := c.Theme()
		sp := Spacing()
		typ := Typography()
		ui.Column(c).Width(400).Gap(sp.L).Children(func() {
			modalTitle(c, fmt.Sprintf("Allow “%s” to use this Mac?", rdDeviceName(req.Device)))
			ui.Text(c, "It will see your whole screen. With Allow Control it can also click and type as you.").
				FontSize(typ.BodySmall).TextColor(t.TextMuted)
			ui.Text(c, "Solco remembers your answer for this phone. Forget it any time in Settings → Remote Desktop.").
				FontSize(typ.BodySmall).TextColor(t.TextMuted)
			if !canControl {
				ui.Text(c, "Accessibility is off, so this connection can only watch.").
					FontSize(typ.BodySmall).TextColor(t.TextMuted)
			}
			ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
				if regularButton(c, "Deny").Clicked() {
					s.rdDecide(remotedesktop.Decision{})
				}
				if regularButton(c, "View Only").Clicked() {
					s.rdDecide(remotedesktop.Decision{Allow: true, Mode: protocol.ModeViewOnly})
				}
				if canControl && regularPrimaryButton(c, "Allow Control").Clicked() {
					s.rdDecide(remotedesktop.Decision{Allow: true, Mode: protocol.ModeFullControl})
				}
			})
		})
	})
}

// --- permissions polling ----------------------------------------------------

// rdPollWhileVisible keeps the permission and session readings fresh while the page is open: one
// goroutine, every couple of seconds, ending a few seconds after the page was last drawn.
func (s *Shell) rdPollWhileVisible() {
	r := &s.rdesk
	r.pageSeen = time.Now()
	if r.polling {
		return
	}
	r.polling = true
	svc := s.remoteDesktopService()
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), rdPermissionTimeout)
			perms, err := svc.Permissions(ctx)
			cancel()
			sessions := svc.Sessions()
			s.applyOnWindow(func() {
				r.perms, r.permsKnown, r.sessions = perms, err == nil, sessions
				r.permsErr = ""
				if err != nil {
					r.permsErr = "Could not read permissions."
				}
			})
			time.Sleep(rdPollEvery)
			seen := false
			s.callOnWindow(func() {
				seen = time.Since(r.pageSeen) < rdPageSeenWindow
				if !seen {
					r.polling = false
				}
			})
			if !seen {
				return
			}
		}
	}()
}

func (s *Shell) rdRequestPermission(kind protocol.PermissionKind, pane string) {
	svc := s.remoteDesktopService()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rdPermissionTimeout)
		defer cancel()
		perms, err := svc.RequestPermission(ctx, kind)
		s.applyOnWindow(func() {
			if err == nil {
				s.rdesk.perms, s.rdesk.permsKnown = perms, true
			}
		})
		// The OS prompts once; after that only System Settings can change the answer.
		_ = exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?"+pane).Start()
	}()
}

// --- page -------------------------------------------------------------------

func (s *Shell) settingsRemoteDesktop(c *ui.Context) {
	svc := s.remoteDesktopService()
	s.rdPollWhileVisible()
	cfg := s.settings.RemoteDesktop
	remoteOn := remoteConfigFrom(s.settings.Remote).Mode != remotehost.ModeOff

	settingsGroup(c, "Phone control", row("Allow remote desktop", "Your paired phones can ask to see and control this Mac. Off until you turn it on.", func() {
		if on, changed := settingSwitch(c, "Allow remote desktop", cfg.Enabled); changed {
			s.setRemoteDesktop(func(v *settings.RemoteDesktopSettings) { v.Enabled = on })
		}
	}))
	if !cfg.Enabled {
		return
	}
	s.rdStatusCard(c, svc, remoteOn)

	s.rdPermissionRows(c)

	tierIdx := 1
	for i, tier := range rdTiers {
		if string(tier) == cfg.Tier {
			tierIdx = i
		}
	}
	settingsGroup(c, "Connection",
		row("Ask before connecting", "A new phone waits until you allow it here; Solco then remembers that phone. Turn off only on a Mac you trust every paired phone with.", func() {
			if on, changed := settingSwitch(c, "Ask before connecting", !cfg.SkipApproval); changed {
				s.setRemoteDesktop(func(v *settings.RemoteDesktopSettings) { v.SkipApproval = !on })
			}
		}),
		row("Quality", "Smooth 720p · Standard 1080p · High 1440p, 30 fps. The phone can change it while connected.", func() {
			if segmentedControl(c, &tierIdx, rdTierLabels...).Label("Quality").Changed() {
				tier := string(rdTiers[tierIdx])
				s.setRemoteDesktop(func(v *settings.RemoteDesktopSettings) { v.Tier = tier })
			}
		}),
	)
	s.rdUnlockRows(c)
	s.rdTrustedRows(c, svc)
	s.rdSessionRows(c, svc)
}

// rdUnlockRows is the opt-in saved login password: typed once here, stored encrypted on this Mac, typed into
// the lock screen only during a Control session you approved. The page never shows it again.
func (s *Shell) rdUnlockRows(c *ui.Context) {
	cfg := s.settings.RemoteDesktop
	sp := Spacing()
	settingsGroup(c, "Lock screen",
		row("Mac login password", "Optional. Stored encrypted on this Mac only and never sent to a phone. Solco types it into the lock screen only while a phone you approved has Control.", func() {
			ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
				if cfg.UnlockSaved {
					statusPill(c, "Saved", ToneSuccess)
				}
				textField(c, &s.rdesk.unlockDraft).Width(220).Label("Mac login password").Placeholder("Mac login password").Password()
				if regularButton(c, "Save").Label("Save password").Clicked() {
					pw := s.rdesk.unlockDraft
					if pw == "" || len(pw) > 512 {
						s.pendingToast = "Password must contain 1–512 characters."
						return
					}
					if err := appSecrets.Set(rdUnlockKey, pw); err != nil {
						s.pendingToast = "Could not save the password: " + firstErrLine(err)
						return
					}
					s.rdesk.unlockDraft = ""
					s.setRemoteDesktop(func(v *settings.RemoteDesktopSettings) { v.UnlockSaved = true })
					s.pendingToast = "Password saved."
				}
				remove := regularButton(c, "Remove").Label("Remove password")
				remove.Disabled(!cfg.UnlockSaved)
				if remove.Clicked() {
					if err := appSecrets.Delete(rdUnlockKey); err != nil {
						s.pendingToast = "Could not remove the password: " + firstErrLine(err)
						return
					}
					s.setRemoteDesktop(func(v *settings.RemoteDesktopSettings) { v.UnlockSaved, v.AutoUnlock = false, false })
					s.pendingToast = "Password removed."
				}
			})
		}),
		row("Unlock automatically", "When the Mac is locked during a Control session, Solco types the saved password (two tries). Off: the phone shows an Unlock button instead.", func() {
			if !cfg.UnlockSaved {
				ui.Text(c, "Save a password first").FontSize(Typography().BodySmall).TextColor(c.Theme().TextMuted)
				return
			}
			if on, changed := settingSwitch(c, "Unlock automatically", cfg.AutoUnlock); changed {
				s.setRemoteDesktop(func(v *settings.RemoteDesktopSettings) { v.AutoUnlock = on })
			}
		}),
	)
}

// rdTrustedRows lists the phones that connect without asking; Forget ends their session and asks again next time.
func (s *Shell) rdTrustedRows(c *ui.Context, svc *remotedesktop.Service) {
	trusted := s.settings.RemoteDesktop.Trusted
	if len(trusted) == 0 || s.settings.RemoteDesktop.SkipApproval {
		return
	}
	var rows []settingsRow
	for _, g := range trusted {
		g := g
		mode := "Full control"
		if g.Mode == string(protocol.ModeViewOnly) {
			mode = "View only"
		}
		name := rdDeviceName(protocol.DeviceInfo{Name: g.Name})
		rows = append(rows, row(name, mode+" · connects without asking", func() {
			if compactButton(c.Key("rd-trust:"+g.DeviceID), "Forget").Clicked() {
				svc.ForgetDevice(g.DeviceID)
			}
		}))
	}
	settingsGroup(c, "Remembered phones", rows...)
}

func (s *Shell) rdStatusCard(c *ui.Context, svc *remotedesktop.Service, remoteOn bool) {
	r := &s.rdesk
	t := c.Theme()
	tokens := designTokens(t.Dark)
	typ := Typography()
	sp := Spacing()
	title, detail, tone := "Ready", "Paired phones can request this Mac.", ToneSuccess
	switch {
	case len(r.sessions) > 0:
		title, detail = "Connected · "+rdDeviceName(r.sessions[0].Device), rdSessionLine(r.sessions[0])
	case !remoteOn:
		title, detail, tone = "Remote Access is off", "Phones reach this Mac through Remote Access. Turn it on first.", ToneWarning
	case r.permsKnown && !r.perms.ScreenRecording:
		title, detail, tone = "Screen Recording needed", "Allow it below so a phone can see this Mac.", ToneWarning
	case r.permsErr != "":
		title, detail, tone = r.permsErr, "", ToneError
	}
	ui.Row(c).FillWidth().Gap(sp.L).AlignItems(ui.Center).Padding(sp.L).
		Radius(Radius().Card).Background(tokens.Panel).Border(1, tokens.BorderSubtle).Children(func() {
		ui.Box(c).Size(8, 8).Shrink(0).Radius(4).Background(tokens.StatusColor(tone, t.Dark))
		ui.Column(c).Grow(1).MinWidth(0).Gap(sp.XXS).Children(func() {
			ui.Text(c, title).FontSize(typ.Section).FontWeight(WeightSemibold).TextColor(t.Text)
			if detail != "" {
				ui.Text(c, detail).FontSize(typ.BodySmall).TextColor(t.TextMuted)
			}
		})
		switch {
		case len(r.sessions) > 0:
			if regularButton(c, "Disconnect").Shrink(0).Clicked() {
				svc.Disconnect(r.sessions[0].ID)
			}
		case !remoteOn:
			if regularPrimaryButton(c, "Open Remote Access").Shrink(0).Clicked() {
				s.router.Push(remoteSettingsPath)
			}
		}
	})
}

func (s *Shell) rdPermissionRows(c *ui.Context) {
	r := &s.rdesk
	perm := func(label, note string, granted bool, kind protocol.PermissionKind, pane string) settingsRow {
		return row(label, note, func() {
			switch {
			case !r.permsKnown:
				statusPill(c, "Checking…", ToneMuted)
			case granted:
				statusPill(c, "Allowed", ToneSuccess)
			default:
				ui.Row(c).Gap(Spacing().S).AlignItems(ui.Center).Children(func() {
					statusPill(c, "Not allowed", ToneWarning)
					if regularButton(c, "Allow…").Shrink(0).Clicked() {
						s.rdRequestPermission(kind, pane)
					}
				})
			}
		})
	}
	settingsGroup(c, "Permissions",
		perm("Screen Recording", "Lets your phone see this Mac. Required.", r.perms.ScreenRecording, protocol.PermissionScreenRecording, "Privacy_ScreenCapture"),
		perm("Accessibility", "Lets your phone click and type. Without it a phone can only watch.", r.perms.Accessibility, protocol.PermissionAccessibility, "Privacy_Accessibility"),
	)
}

func (s *Shell) rdSessionRows(c *ui.Context, svc *remotedesktop.Service) {
	r := &s.rdesk
	if len(r.sessions) == 0 {
		return
	}
	var rows []settingsRow
	for _, info := range r.sessions {
		info := info
		mode := "Full control"
		if !info.Controlling {
			mode = "View only"
		}
		rows = append(rows, row(rdDeviceName(info.Device), mode+" · "+rdSessionLine(info), func() {
			if compactButton(c.Key("rd-session:"+info.ID), "Disconnect").Clicked() {
				svc.Disconnect(info.ID)
			}
		}))
	}
	settingsGroup(c, "Active session", rows...)
}

// rdSessionLine is the one-line reading of a live session: size, rate, latency, path.
func rdSessionLine(info session.Info) string {
	st := info.State
	parts := []string{fmt.Sprintf("%d×%d", st.Width, st.Height)}
	if info.Stats.FPS > 0 {
		parts = append(parts, fmt.Sprintf("%.0f fps", info.Stats.FPS))
	}
	if info.Stats.BitrateKbps > 0 {
		parts = append(parts, fmt.Sprintf("%.1f Mbps", float64(info.Stats.BitrateKbps)/1000))
	}
	if info.Stats.RTTMillis > 0 {
		parts = append(parts, fmt.Sprintf("%.0f ms", info.Stats.RTTMillis))
	}
	if info.Stats.PathType != "" {
		parts = append(parts, info.Stats.PathType)
	}
	return strings.Join(parts, " · ")
}
