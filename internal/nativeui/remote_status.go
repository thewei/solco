package nativeui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

/**
 * [INPUT]: 依赖 remote_settings.go 的 remoteState/remoteServer/setRemote/各字段控件、internal/remotehost 的 Server.OverlayStatus/ListenAddress/RelayStatus/Devices/DeviceOnline、settingsGroup/row/rowMenuSlot/openConfirm 共享组件
 * [OUTPUT]: Shell.settingsRemote（/settings/remote：一个 Remote Access 开关 → 一张状态卡 → 常驻服务行 → Paired Phones（每台一个 … 菜单 + 确认式 Revoke Access）→ 折叠 Advanced）、Shell.remoteSummaryFor（状态矩阵）、Shell.revokePhone、Shell.remoteTick、Shell.remoteDiagnostics
 * [POS]: Remote Access 页的状态与设备呈现（2026-11-05 Tailcat-first 重设计，规格 docs/unified-remote-access-ui-ux-spec-2026-11-05.md §3）：状态只读自 Server 的真实端点（隧道 ready / 直连监听 / relay 在线），绝不从配置推导；开关映射 settings.Remote.Enabled，Advanced 里的 LAN/Tailscale 回退只是 Mode 的另一种读法；诊断只给可验证的路线健康与脱敏文本，永不出现 Tailcat 地址、IP 列表、公钥或 PSK
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// settingsRemote is the Remote Access page: the one switch, one status card,
// the paired phones, and everything an ordinary setup never touches folded
// under Advanced.
func (s *Shell) settingsRemote(c *ui.Context) {
	cfg := remoteConfigFrom(s.settings.Remote)
	srv := s.remoteServer()
	on := cfg.Tailcat

	settingsGroup(c, "Phone access", row("Remote Access", "Reach this Mac from your paired phones. No VPN app is needed.", func() {
		if v, changed := settingSwitch(c, "Remote Access", on); changed {
			s.setRemote(func(r *settings.RemoteAccessSettings) { r.Enabled = &v })
		}
	}))
	sum := s.remoteSummaryFor(srv, cfg)
	s.remoteStatusCard(c, sum)
	if on {
		s.remoteResidentRow(c)
	}
	s.remotePairedPhones(c, srv, on)
	s.remoteAdvanced(c, srv, cfg)
	if sum.Busy || s.remote.pairOpen || (on && srv != nil) {
		s.remoteTick() // the card follows the tunnel and the phones; one redraw a second, only while this page is on screen
	}
}

// remoteTick schedules one redraw a second. The chain lives only as long as a render keeps asking for it, so it
// stops by itself when the page is left.
func (s *Shell) remoteTick() {
	if !s.remote.ticking.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(time.Second, func() {
		s.remote.ticking.Store(false)
		if s.win != nil {
			s.win.Update(func() {})
		}
	})
}

// remoteSummary is the status card's reading of the Host (spec §3.2).
type remoteSummary struct {
	Title, Detail string
	Tone          StatusTone
	Pairable      bool // Pair a Phone… can be pressed
	Busy          bool // a spinner stands in for the dot
	Retry         bool // the card offers Retry and Details
	Off           bool // nothing to show beside the headline
}

// remoteSummaryFor reads what really serves — the tunnel when it is ready, the direct listener when bound, the
// relay when online — and never infers readiness from the saved choice.
func (s *Shell) remoteSummaryFor(srv *remotehost.Server, cfg remotehost.Config) remoteSummary {
	if !cfg.Tailcat {
		// Without the switch the only thing that can still serve is the developer loopback listener.
		if srv != nil && srv.ListenAddress() != "" {
			return remoteSummary{Title: "Development listener ready", Detail: "Listening on this Mac only.", Tone: ToneSuccess, Pairable: true}
		}
		return remoteSummary{Title: "Remote Access is off", Detail: "Turn on to pair a phone.", Tone: ToneMuted, Off: true}
	}
	if s.remote.claiming {
		return remoteSummary{Title: "Reconnecting…", Detail: "Restoring remote access.", Tone: ToneWarning, Busy: true}
	}
	if srv == nil {
		detail := s.remote.err
		if detail == "" {
			detail = "Open connection details to find the cause."
		}
		return remoteSummary{Title: "Can't start remote access", Detail: detail, Tone: ToneError, Retry: true}
	}
	fallback := srv.ListenAddress() != "" || (remoteRelayFrom(s.settings.Remote).Enabled && srv.RelayStatus().State == remotehost.RelayOnline)
	ov := srv.OverlayStatus()
	switch {
	case ov.State == remotehost.OverlayReady:
		if n := s.remoteConnected(srv); n > 0 {
			detail := "One paired phone is connected."
			if n > 1 {
				detail = strconv.Itoa(n) + " paired phones are connected."
			}
			return remoteSummary{Title: "Phone connected", Detail: detail, Tone: ToneSuccess, Pairable: true}
		}
		return remoteSummary{Title: "Secure connection ready", Detail: "Pair a phone to connect to this Mac.", Tone: ToneSuccess, Pairable: true}
	case ov.State == remotehost.OverlayError || s.remote.err != "":
		if fallback {
			return remoteSummary{Title: "Connection available", Detail: "Secure connection unavailable; using a fallback.", Tone: ToneWarning, Pairable: true}
		}
		detail := s.remote.err
		if detail == "" {
			detail = "Open connection details to find the cause."
		}
		return remoteSummary{Title: "Can't start remote access", Detail: detail, Tone: ToneError, Retry: true}
	}
	return remoteSummary{Title: "Starting secure connection…", Detail: "Preparing a connection for your devices.", Tone: ToneWarning, Busy: true}
}

func (s *Shell) remoteConnected(srv *remotehost.Server) int {
	n := 0
	for _, d := range srv.Devices().List() {
		if srv.DeviceOnline(d.ID) {
			n++
		}
	}
	return n
}

// remoteRelayPart is the relay's line: ok = the relay is enabled and has something to say, warn = it is not healthy.
func (s *Shell) remoteRelayPart(srv *remotehost.Server) (part string, ok, warn bool) {
	if !remoteRelayFrom(s.settings.Remote).Enabled {
		return "", false, false
	}
	switch st := srv.RelayStatus(); st.State {
	case remotehost.RelayOnline:
		return "Relay connected", true, false
	case remotehost.RelayConnecting:
		return "Relay connecting…", true, false
	case remotehost.RelayBackoff:
		return "Relay reconnecting: " + st.Message, true, true
	case remotehost.RelayError:
		return "Relay: " + st.Message, true, true
	}
	return "", false, false
}

// remoteStatusCard is the page's one status block: dot (or a spinner while the tunnel starts), headline, one line
// of detail, and the action the state allows.
func (s *Shell) remoteStatusCard(c *ui.Context, sum remoteSummary) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	typ := Typography()
	sp := Spacing()
	ui.Row(c).FillWidth().Gap(sp.L).AlignItems(ui.Center).Padding(sp.L).
		Radius(Radius().Card).Background(tokens.Panel).Border(1, tokens.BorderSubtle).Children(func() {
		if sum.Busy {
			ui.Spinner(c).Size(14, 14).Shrink(0)
		} else {
			ui.Box(c).Size(8, 8).Shrink(0).Radius(4).Background(tokens.StatusColor(sum.Tone, t.Dark))
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(sp.XXS).Children(func() {
			ui.Text(c, sum.Title).FontSize(typ.Section).FontWeight(WeightSemibold).TextColor(t.Text)
			if sum.Detail != "" {
				ui.Text(c, sum.Detail).FontSize(typ.BodySmall).TextColor(t.TextMuted)
			}
		})
		switch {
		case sum.Off:
		case sum.Retry:
			if regularButton(c, "Details").Shrink(0).Clicked() {
				s.remote.advancedOpen = true
			}
			if regularPrimaryButton(c, "Retry").Shrink(0).Clicked() {
				s.syncRemoteHost()
			}
		default:
			if regularPrimaryButton(c, "Pair a Phone…").Shrink(0).Disabled(!sum.Pairable).Clicked() {
				s.openPairing()
			}
		}
	})
}

// remotePairedPhones lists the paired phones, one row each with a ⋯ menu. The device store outlives the switch,
// so a phone can still be revoked while Remote Access is off.
func (s *Shell) remotePairedPhones(c *ui.Context, srv *remotehost.Server, on bool) {
	if srv == nil {
		return
	}
	devices := srv.Devices().List()
	if len(devices) == 0 {
		if on {
			settingsGroup(c, "Paired phones", row("No phones paired yet", "Add a phone with the button above.", func() {}))
		}
		return
	}
	var list []settingsRow
	for _, d := range devices {
		d := d
		cc := c.Key("remote-device:" + d.ID)
		list = append(list, row(d.Name, remoteDeviceNote(d, srv.DeviceOnline(d.ID)), func() {
			rowMenuSlot(cc, "remote-device-menu:"+d.ID, "Actions for "+d.Name, iconActions, true, gorexRowBtn, gorexRowGlyph, func(m *ui.Menu) {
				if m.Item("Revoke Access…").Chosen() {
					s.openConfirm("revoke-phone", d.ID, "Revoke access for "+d.Name+"?", "This phone will need to pair again to connect.")
				}
			})
		}))
	}
	settingsGroup(c, "Paired phones", list...)
}

// revokePhone removes one phone: its token stops working, its sockets close and its tunnel peer is dropped
// (remotehost does all three from the store's revoke callbacks). Other phones are untouched.
func (s *Shell) revokePhone(id string) {
	srv := s.remoteServer()
	if srv == nil {
		return
	}
	if err := srv.Devices().Revoke(id); err != nil {
		s.remote.err = "Could not revoke the phone: " + err.Error()
		return
	}
	s.pendingToast = "Access revoked"
}

// remoteTailscaleOffered reports whether a Tailscale address exists on this Mac, probed at most every few seconds
// (the Advanced row for an existing Tailscale install only shows then, or while it is the saved choice).
func (s *Shell) remoteTailscaleOffered() bool {
	return len(s.remoteCandidates(remotehost.ModeTailscale)) > 0
}

// remoteAdvanced is the folded rest, in the spec's order: the built-in connection's state, the optional direct
// fallbacks (local network, an existing Tailscale address), the relay, the WebRTC path and the diagnostics.
func (s *Shell) remoteAdvanced(c *ui.Context, srv *remotehost.Server, cfg remotehost.Config) {
	t := c.Theme()
	sp := Spacing()
	tokens := designTokens(t.Dark)
	saved, _ := remotehost.ParseMode(s.settings.Remote.Mode)
	lanOn := saved == remotehost.ModeLAN
	tsOn := s.settings.Remote.TailscaleEnabled || saved == remotehost.ModeTailscale
	tsAddr := strings.TrimSpace(s.settings.Remote.TailscaleAddress)
	if tsAddr == "" && saved == remotehost.ModeTailscale {
		tsAddr = strings.TrimSpace(s.settings.Remote.Address)
	}
	forced := false
	if cfg.Tailcat && lanOn {
		cands := s.remoteCandidates(remotehost.ModeLAN)
		forced = len(cands) == 0 || cfg.Address == "" || remoteAddressStale(cfg.Address, cands)
	}
	if !forced && cfg.Tailcat && tsOn {
		cands := s.remoteCandidates(remotehost.ModeTailscale)
		forced = len(cands) == 0 || tsAddr == "" || remoteAddressStale(tsAddr, cands)
	}
	open := s.remote.advancedOpen || forced
	strip := ui.ButtonBase(c.Key("remote-advanced")).FillWidth().Padding(sp.S, sp.XS).Gap(sp.S).AlignItems(ui.Center).
		Justify(ui.Start).Radius(Radius().Row).FocusRing(false).Label("Advanced")
	if strip.Hovered() {
		strip.Background(tokens.PanelHover)
	}
	strip.Children(func() {
		treeChevron(c, true, open, t.TextMuted)
		ui.Text(c, "Advanced").FontSize(Typography().BodySmall).FontWeight(WeightMedium).TextColor(t.TextMuted)
		ui.Text(c, "fallbacks, relay, diagnostics").FontSize(Typography().Caption).TextColor(t.TextMuted)
	})
	if strip.Clicked() && !forced {
		s.remote.advancedOpen = !s.remote.advancedOpen
	}
	if !open {
		return
	}

	var rows []settingsRow
	rows = append(rows, row("Built-in Secure Connection", "The preferred route; it needs no open port and no VPN app.", func() {
		text, tone := s.remoteOverlayLabel(srv, cfg)
		statusPill(c, text, tone)
	}))
	rows = append(rows, row("Allow Local Network Fallback", "Phones on the same Wi-Fi or Ethernet may also connect straight to this Mac.", func() {
		if on, changed := settingSwitch(c, "Allow Local Network Fallback", lanOn); changed {
			s.setLANFallback(on)
		}
	}))
	if tsOn || s.remoteTailscaleOffered() {
		rows = append(rows, row("Existing Tailscale Address", "Also listen on this Mac's Tailscale address. Tailscale must be running.", func() {
			if on, changed := settingSwitch(c, "Existing Tailscale Address", tsOn); changed {
				s.setTailscaleFallback(on)
			}
		}))
	}
	if lanOn {
		rows = append(rows, row("Address", "The local-network interface address Solco listens on.", func() {
			s.remoteAddressPickerFor(c, remotehost.ModeLAN, s.settings.Remote.Address, "Address", func(r *settings.RemoteAccessSettings, addr string) { r.Address = addr })
		}))
	}
	if tsOn {
		rows = append(rows, row("Tailscale Address", "The Tailscale interface address Solco listens on.", func() {
			s.remoteAddressPickerFor(c, remotehost.ModeTailscale, tsAddr, "Tailscale Address", func(r *settings.RemoteAccessSettings, addr string) {
				if curMode, _ := remotehost.ParseMode(r.Mode); curMode == remotehost.ModeTailscale {
					r.Mode = string(remotehost.ModeOff)
					r.Address = ""
				}
				r.TailscaleEnabled = true
				r.TailscaleAddress = addr
			})
		}))
	}
	if lanOn || tsOn {
		rows = append(rows, row("Port", "", func() { s.remotePortField(c, cfg) }))
	}
	relay := remoteRelayFrom(s.settings.Remote)
	rows = append(rows, row("Alma Relay", "Your own relay (alma-relay) for when no direct path exists. Unlike the built-in connection it sees requests in plaintext.", func() {
		if on, changed := settingSwitch(c, "Alma Relay", relay.Enabled); changed {
			s.setRemote(func(r *settings.RemoteAccessSettings) { r.RelayEnabled = on })
		}
	}))
	if relay.Enabled {
		rows = append(rows,
			row("Relay Address", "The bare https:// or wss:// address of your relay.", func() { s.remoteRelayAddressField(c, relay) }),
			row("Registration Key", "The relay's shared key, when it asks for one.", func() { s.remoteRelayKeyField(c, relay) }),
		)
	}
	p2p := remoteP2PFrom(s.settings.Remote)
	rows = append(rows, row("WebRTC P2P", "A direct data link between phone and Mac through whichever route already works. STUN is contacted only when a phone opens one.", func() {
		if on, changed := settingSwitch(c, "WebRTC P2P", p2p.Enabled); changed {
			s.setRemote(func(r *settings.RemoteAccessSettings) { r.P2PDisabled = !on })
		}
	}))
	if p2p.Enabled {
		rows = append(rows,
			row("ICE Servers", "STUN/TURN URLs, comma-separated. Empty uses the built-in STUN servers.", func() { s.remoteICEField(c) }),
			row("TURN Username", "For the turn: URLs above.", func() {
				s.remoteTurnField(c, &s.remote.p2pUserDraft, "Username", s.settings.Remote.P2PTurnUser, false, func(r *settings.RemoteAccessSettings, v string) { r.P2PTurnUser = v })
			}),
			row("TURN Credential", "", func() {
				s.remoteTurnField(c, &s.remote.p2pPassDraft, "Credential", s.settings.Remote.P2PTurnPass, true, func(r *settings.RemoteAccessSettings, v string) { r.P2PTurnPass = v })
			}),
		)
	}
	rows = append(rows, row("Connection Diagnostics", s.remoteDiagnosticsLine(srv, cfg), func() {
		ui.Row(c).Gap(Spacing().M).Children(func() {
			if compactButton(c, "Retry Connection").Clicked() {
				s.syncRemoteHost()
			}
			if compactButton(c, "Copy Diagnostics").Clicked() {
				s.copyTextToClipboard(s.remoteDiagnostics(srv, cfg))
				s.pendingToast = "Diagnostics copied"
			}
		})
	}))
	settingsGroup(c, "", rows...).Transition(disclosureMotion)
}

// setLANFallback turns the local-network listener on or off without disturbing Tailscale.
func (s *Shell) setLANFallback(on bool) {
	s.setRemote(func(r *settings.RemoteAccessSettings) {
		if cur, _ := remotehost.ParseMode(r.Mode); cur == remotehost.ModeTailscale {
			// Migrate a legacy ModeTailscale choice before switching Mode to lan/off.
			r.TailscaleEnabled = true
			if strings.TrimSpace(r.TailscaleAddress) == "" {
				r.TailscaleAddress = r.Address
			}
			r.Address = ""
		}
		if !on {
			r.Mode = string(remotehost.ModeOff)
			return
		}
		r.Mode = string(remotehost.ModeLAN)
		r.Address = s.pickRemoteAddress(remotehost.ModeLAN, r.Address)
	})
}

// setTailscaleFallback turns the Tailscale listener on or off independently of LAN.
func (s *Shell) setTailscaleFallback(on bool) {
	s.setRemote(func(r *settings.RemoteAccessSettings) {
		if cur, _ := remotehost.ParseMode(r.Mode); cur == remotehost.ModeTailscale {
			r.Mode = string(remotehost.ModeOff)
			if strings.TrimSpace(r.TailscaleAddress) == "" {
				r.TailscaleAddress = r.Address
			}
			r.Address = ""
		}
		r.TailscaleEnabled = on
		if on {
			r.TailscaleAddress = s.pickRemoteAddress(remotehost.ModeTailscale, r.TailscaleAddress)
		}
	})
}

func (s *Shell) remoteOverlayLabel(srv *remotehost.Server, cfg remotehost.Config) (string, StatusTone) {
	if !cfg.Tailcat {
		return "Off", ToneMuted
	}
	if srv == nil {
		return "Unavailable", ToneError
	}
	switch srv.OverlayStatus().State {
	case remotehost.OverlayReady:
		return "Ready", ToneSuccess
	case remotehost.OverlayError:
		return "Unavailable", ToneError
	}
	return "Starting", ToneWarning
}

// remoteDiagnosticsLine is the one-line reading shown under Connection Diagnostics.
func (s *Shell) remoteDiagnosticsLine(srv *remotehost.Server, cfg remotehost.Config) string {
	text, _ := s.remoteOverlayLabel(srv, cfg)
	parts := []string{"Secure connection: " + text}
	if srv != nil && srv.ListenAddress() != "" {
		parts = append(parts, "Direct listener: on")
	}
	if srv != nil {
		if part, ok, _ := s.remoteRelayPart(srv); ok {
			parts = append(parts, part)
		}
		if msg := srv.OverlayStatus().Message; msg != "" {
			parts = append(parts, msg)
		}
	}
	return strings.Join(parts, " · ")
}

// remoteDiagnostics is the redacted text Copy Diagnostics puts on the clipboard: route health and error
// categories only — never the tunnel address, a key, an IP, a token or a pairing code.
func (s *Shell) remoteDiagnostics(srv *remotehost.Server, cfg remotehost.Config) string {
	var b strings.Builder
	overlay, _ := s.remoteOverlayLabel(srv, cfg)
	fmt.Fprintf(&b, "Remote Access: %s\n", map[bool]string{true: "on", false: "off"}[cfg.Tailcat])
	fmt.Fprintf(&b, "Secure connection: %s\n", overlay)
	if srv != nil {
		if msg := srv.OverlayStatus().Message; msg != "" {
			fmt.Fprintf(&b, "Secure connection note: %s\n", msg)
		}
		fmt.Fprintf(&b, "Direct listener: %s\n", map[bool]string{true: "on", false: "off"}[srv.ListenAddress() != ""])
		if part, ok, _ := s.remoteRelayPart(srv); ok {
			fmt.Fprintf(&b, "%s\n", part)
		}
		devices := srv.Devices().List()
		fmt.Fprintf(&b, "Paired phones: %d (%d connected)\n", len(devices), s.remoteConnected(srv))
	}
	if s.remote.err != "" {
		fmt.Fprintf(&b, "Last error: %s\n", s.remote.err)
	}
	return b.String()
}
