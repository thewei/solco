package nativeui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	qrcode "github.com/skip2/go-qrcode"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/chatattach"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

/**
 * [INPUT]: 依赖 internal/remotehost（Server/Config/RelayConfig/RelayStatus/Candidates/PairingOffer）、settings.Remote、Shell.runtime（Herdr Manager）、controls.go 的字段/分段/按钮/开关、settingsGroup/row 共享设置组件、skip2/go-qrcode（只负责把配对 URL 编成模块矩阵）
 * [OUTPUT]: remoteSettingsPath、Shell.settingsRemote（/settings/remote：Access 分段、Address 选择、Port、Status、Relay 区（开关、Relay Address、Registration Key、Relay Status）、Pair a phone、已配对手机与 Revoke）、Shell.remotePairDialog（二维码模态）、Shell.syncRemoteHost / stopRemoteHost、remoteConfigFrom、remoteRelayFrom
 * [POS]: 手机端 Remote Host（internal/remotehost）的唯一呈现与挂载点：默认关闭，用户选了网络才监听；Relay 是可选的自托管 alma-relay 公网路线（2026-11-01 用户确认），开启即随 Reconfigure 重拨，关闭即停；设置只存模式/端口/地址与 relay 开关/地址/注册钥匙，配对码、设备 token、TLS 私钥与 relaySecret 永远在 remotehost 自己的目录里；二维码用 Painter 画（黑白固定，与主题无关），relay 开启时 QR 追加 r/rt；撤销手机立即生效（remotehost 关它的 WS）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const remoteSettingsPath = "/settings/remote"

const (
	remoteCandidatesTTL = 3 * time.Second
	remoteQRSize        = 232
)

var (
	remoteModeLabels = []string{"Off", "This Mac only", "Local network", "Tailscale"}
	remoteModes      = []remotehost.Mode{remotehost.ModeOff, remotehost.ModeLoopback, remotehost.ModeLAN, remotehost.ModeTailscale}
)

// remoteState is the Remote Access page's presentation state. The Server is
// created on first need and lives until the shell closes.
type remoteState struct {
	srv       *remotehost.Server
	err       string
	portDraft string

	relayURLDraft string
	relayKeyDraft string

	p2pICEDraft  string
	p2pUserDraft string
	p2pPassDraft string

	cands     []remotehost.Candidate
	candsMode remotehost.Mode
	candsAt   time.Time

	pairOpen bool
	offer    remotehost.PairingOffer
	qr       [][]bool
}

// remoteRelayFrom maps the saved choice to the Host's relay config.
func remoteRelayFrom(r settings.RemoteAccessSettings) remotehost.RelayConfig {
	return remotehost.RelayConfig{Enabled: r.RelayEnabled, URL: strings.TrimSpace(r.RelayURL), RegistrationKey: r.RelayKey}
}

// remoteP2PFrom maps the saved choice to the Host's P2P config (on unless the
// user switched it off; empty ICE text = the built-in STUN list).
func remoteP2PFrom(r settings.RemoteAccessSettings) remotehost.P2PConfig {
	return remotehost.P2PConfig{
		Enabled:    !r.P2PDisabled,
		ICEServers: remotehost.ParseICEServers(r.P2PICE, r.P2PTurnUser, r.P2PTurnPass),
	}
}

// remoteConfigFrom maps the saved choice to the Host's listener config; an
// unreadable mode is off, never wider.
func remoteConfigFrom(r settings.RemoteAccessSettings) remotehost.Config {
	mode, err := remotehost.ParseMode(r.Mode)
	if err != nil {
		mode = remotehost.ModeOff
	}
	port := r.Port
	if port == 0 {
		port = remotehost.DefaultPort
	}
	return remotehost.Config{Mode: mode, Port: port, Address: r.Address}
}

func remoteModeIndex(m remotehost.Mode) int {
	for i, mm := range remoteModes {
		if mm == m {
			return i
		}
	}
	return 0
}

// remoteServer returns the Host, building it on first use. Without a user-data
// directory (headless tests) there is none.
func (s *Shell) remoteServer() *remotehost.Server {
	if s.remote.srv != nil {
		return s.remote.srv
	}
	if s.userDataDir == "" {
		return nil
	}
	host, _ := os.Hostname()
	userDataDir := s.userDataDir // fixed at startup; the Host's goroutines must not touch the Shell
	opts := remotehost.Options{
		Config:     remoteConfigFrom(s.settings.Remote),
		Dir:        filepath.Join(s.userDataDir, "RemoteHost"),
		Backend:    remotehost.NewManagerBackend(s.runtime),
		Identity:   s.hostHookIdentity,
		AgentFacts: s.hostAgentFacts,
		Usage:      s.hostUsage,
		HostName:   host,
		Version:    mygo.App.Version(),
		Git:        app.NewRemoteGit(),
		Attach: func(data []byte, mime, name string) (string, error) {
			dir, err := chatattach.Dir(userDataDir)
			if err != nil {
				return "", err
			}
			return chatattach.Save(dir, data, mime, name, time.Now())
		},
	}
	if s.launch != nil {
		opts.Launcher = app.NewRemoteLauncher(s.launch)
		opts.Queue = app.NewRemoteFollowUps(s.launch)
	}
	srv, err := remotehost.New(opts)
	if err != nil {
		slog.Warn("remote host unavailable", "error", err)
		s.remote.err = "Remote access is unavailable: " + err.Error()
		return nil
	}
	srv.SetP2P(remoteP2PFrom(s.settings.Remote))
	s.remote.srv = srv
	return srv
}

// syncRemoteHost makes the listener match the saved choice (launch, and every
// change on the page). A failure leaves the Host stopped and says why.
func (s *Shell) syncRemoteHost() {
	s.closePairing()
	srv := s.remoteServer()
	if srv == nil {
		return
	}
	s.remote.err = ""
	srv.SetP2P(remoteP2PFrom(s.settings.Remote))
	if err := srv.Reconfigure(remoteConfigFrom(s.settings.Remote), remoteRelayFrom(s.settings.Remote)); err != nil {
		slog.Warn("remote host reconfigure failed", "error", err)
		s.remote.err = err.Error()
	}
}

// startRemoteHostAtLaunch brings the Host up only when the user left it on.
func (s *Shell) startRemoteHostAtLaunch() {
	if remoteConfigFrom(s.settings.Remote).Mode != remotehost.ModeOff {
		s.syncRemoteHost()
	}
}

func (s *Shell) stopRemoteHost() {
	if s.remote.srv != nil {
		s.remote.srv.Close()
	}
}

// setRemote saves a change to the page's choice and rebinds the listener.
func (s *Shell) setRemote(mutate func(*settings.RemoteAccessSettings)) {
	s.applySettings(func(v *settings.Settings) error {
		mutate(&v.Remote)
		return nil
	})
	s.syncRemoteHost()
}

// remoteCandidates lists the addresses the chosen mode may bind, re-read at
// most every few seconds.
func (s *Shell) remoteCandidates(mode remotehost.Mode) []remotehost.Candidate {
	r := &s.remote
	if mode != r.candsMode || time.Since(r.candsAt) > remoteCandidatesTTL {
		r.cands, _ = remotehost.Candidates(mode)
		r.candsMode, r.candsAt = mode, time.Now()
	}
	return r.cands
}

func remoteCandidateLabel(c remotehost.Candidate) string { return c.Address + " · " + c.Interface }

func remoteModeNote(m remotehost.Mode) string {
	switch m {
	case remotehost.ModeLoopback:
		return "This Mac only (development, USB tunnelling)."
	case remotehost.ModeLAN:
		return "Phones on the same Wi-Fi or Ethernet."
	case remotehost.ModeTailscale:
		return "Phones on your tailnet. Tailscale must be running."
	}
	return "Off."
}

func (s *Shell) settingsRemote(c *ui.Context) {
	t := c.Theme()
	typ := Typography()
	cfg := remoteConfigFrom(s.settings.Remote)
	srv := s.remoteServer()
	ui.Text(c, "Browse Workspaces and drive agents from the Solco phone app. Off until you pick a network.").
		FontSize(typ.BodySmall).TextColor(t.TextMuted)

	rows := []settingsRow{
		row("Access", remoteModeNote(cfg.Mode), func() {
			chosen := remoteModeIndex(cfg.Mode)
			if segmentedControl(c, &chosen, remoteModeLabels...).Label("Access").Changed() {
				mode := remoteModes[chosen]
				s.setRemote(func(r *settings.RemoteAccessSettings) {
					r.Mode = string(mode)
					if mode == remotehost.ModeLAN || mode == remotehost.ModeTailscale {
						r.Address = s.pickRemoteAddress(mode, r.Address)
					}
				})
			}
		}),
	}
	if cfg.Mode == remotehost.ModeLAN || cfg.Mode == remotehost.ModeTailscale {
		rows = append(rows, row("Address", "The one interface address Solco listens on.", func() { s.remoteAddressPicker(c, cfg) }))
	}
	if cfg.Mode != remotehost.ModeOff {
		rows = append(rows,
			row("Port", "", func() { s.remotePortField(c, cfg) }),
			row("Status", "", func() { s.remoteStatus(c, srv, cfg) }),
		)
	}
	settingsGroup(c, "Listener", rows...)

	relay := remoteRelayFrom(s.settings.Remote)
	relayRows := []settingsRow{
		row("Relay", "Reach this Mac through your own relay (alma-relay) when no direct path exists. The relay sees requests in plaintext, and its registration key can replay a paired phone's credentials.", func() {
			if on, changed := settingSwitch(c, "Relay", relay.Enabled); changed {
				s.setRemote(func(r *settings.RemoteAccessSettings) { r.RelayEnabled = on })
			}
		}),
	}
	if relay.Enabled {
		relayRows = append(relayRows,
			row("Relay Address", "The bare https:// or wss:// address of your self-hosted relay.", func() { s.remoteRelayAddressField(c, relay) }),
			row("Registration Key", "The relay's shared registration key, when it asks for one.", func() { s.remoteRelayKeyField(c, relay) }),
		)
	}
	relayRows = append(relayRows, row("Relay Status", "", func() { s.remoteRelayStatus(c, srv) }))
	settingsGroup(c, "Relay", relayRows...)

	p2p := remoteP2PFrom(s.settings.Remote)
	p2pRows := []settingsRow{
		row("P2P", "A phone may open a direct WebRTC link over LAN, Tailscale or the relay. STUN servers are contacted only when a phone opens one.", func() {
			if on, changed := settingSwitch(c, "P2P", p2p.Enabled); changed {
				s.setRemote(func(r *settings.RemoteAccessSettings) { r.P2PDisabled = !on })
			}
		}),
	}
	if p2p.Enabled {
		p2pRows = append(p2pRows,
			row("ICE Servers", "STUN/TURN URLs, comma-separated. Empty uses the built-in STUN servers.", func() { s.remoteICEField(c) }),
			row("TURN Username", "For the turn: URLs above.", func() {
				s.remoteTurnField(c, &s.remote.p2pUserDraft, "Username", s.settings.Remote.P2PTurnUser, false, func(r *settings.RemoteAccessSettings, v string) { r.P2PTurnUser = v })
			}),
			row("TURN Credential", "", func() {
				s.remoteTurnField(c, &s.remote.p2pPassDraft, "Credential", s.settings.Remote.P2PTurnPass, true, func(r *settings.RemoteAccessSettings, v string) { r.P2PTurnPass = v })
			}),
			row("P2P Status", "", func() { s.remoteP2PStatus(c, srv) }),
		)
	}
	settingsGroup(c, "Direct P2P", p2pRows...)

	if srv == nil || srv.ListenAddress() == "" {
		return
	}
	settingsGroup(c, "Phones", row("Pair a phone", "One-time code, valid for 6 minutes.", func() {
		if regularButton(c, "Show QR Code…").Clicked() {
			s.openPairing()
		}
	}))
	devices := srv.Devices().List()
	if len(devices) == 0 {
		return
	}
	var list []settingsRow
	for _, d := range devices {
		d := d
		cc := c.Key("remote-device:" + d.ID)
		list = append(list, row(d.Name, remoteDeviceNote(d, srv.DeviceOnline(d.ID)), func() {
			if compactButton(cc, "Revoke").Clicked() {
				if err := srv.Devices().Revoke(d.ID); err != nil {
					s.remote.err = "Could not revoke " + d.Name + ": " + err.Error()
				}
			}
		}))
	}
	settingsGroup(c, "Paired phones", list...)
}

func remoteDeviceNote(d remotehost.Device, online bool) string {
	note := "Paired " + d.CreatedAt.Format("2 Jan 2006")
	switch {
	case online:
		note = "Online · " + note
	case !d.LastSeenAt.IsZero():
		note += " · last seen " + usageAgo(time.Since(d.LastSeenAt))
	}
	return note
}

// pickRemoteAddress keeps the saved address when the mode still offers it,
// else takes the first one on offer (empty when none).
func (s *Shell) pickRemoteAddress(mode remotehost.Mode, current string) string {
	cands := s.remoteCandidates(mode)
	for _, cand := range cands {
		if cand.Address == current {
			return current
		}
	}
	if len(cands) > 0 {
		return cands[0].Address
	}
	return ""
}

// remoteAddressStale reports a saved address the mode no longer offers: the
// Host may still be bound to it, but a phone is not told to use it.
func remoteAddressStale(address string, cands []remotehost.Candidate) bool {
	return address != "" && !slices.ContainsFunc(cands, func(c remotehost.Candidate) bool { return c.Address == address })
}

func (s *Shell) remoteAddressPicker(c *ui.Context, cfg remotehost.Config) {
	t := c.Theme()
	cands := s.remoteCandidates(cfg.Mode)
	if len(cands) == 0 {
		msg := "No local-network address found."
		if cfg.Mode == remotehost.ModeTailscale {
			msg = "No Tailscale address found — is Tailscale running?"
		}
		ui.Row(c).Gap(Spacing().M).AlignItems(ui.Center).Children(func() {
			ui.Text(c, msg).FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			if compactButton(c, "Check Again").Clicked() {
				s.remote.candsAt = time.Time{}
				s.setRemote(func(r *settings.RemoteAccessSettings) { r.Address = s.pickRemoteAddress(cfg.Mode, r.Address) })
			}
		})
		return
	}
	if remoteAddressStale(cfg.Address, cands) {
		best := cands[0].Address
		ui.Row(c).Gap(Spacing().M).AlignItems(ui.Center).Children(func() {
			ui.Text(c, cfg.Address+" is not an address phones can use any more (the network changed, or it is a VM or tunnel interface).").
				FontSize(Typography().BodySmall).TextColor(designTokens(t.Dark).StatusColor(ToneWarning, t.Dark)).MaxWidth(360)
			if compactButton(c, "Use "+best).Clicked() {
				s.setRemote(func(r *settings.RemoteAccessSettings) { r.Address = best })
			}
		})
	}
	labels := make([]string, len(cands))
	selected := ""
	for i, cand := range cands {
		labels[i] = remoteCandidateLabel(cand)
		if cand.Address == cfg.Address {
			selected = labels[i]
		}
	}
	if selectField(c, &selected, labels).Label("Address").Changed() {
		for i, l := range labels {
			if l == selected {
				address := cands[i].Address
				s.setRemote(func(r *settings.RemoteAccessSettings) { r.Address = address })
			}
		}
	}
}

func (s *Shell) remotePortField(c *ui.Context, cfg remotehost.Config) {
	r := &s.remote
	field := textField(c, &r.portDraft).Width(120).Label("Port").Placeholder(strconv.Itoa(remotehost.DefaultPort))
	if field.Submitted() {
		n, err := strconv.Atoi(strings.TrimSpace(r.portDraft))
		if err != nil || n < 1024 || n > 65535 {
			r.err = "Port must be a number from 1024 to 65535."
		} else if n != cfg.Port {
			s.setRemote(func(rr *settings.RemoteAccessSettings) { rr.Port = n })
		}
	}
	if !field.Focused() {
		r.portDraft = strconv.Itoa(cfg.Port)
	}
}

func (s *Shell) remoteStatus(c *ui.Context, srv *remotehost.Server, cfg remotehost.Config) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	text, color := "Not listening.", t.TextMuted
	switch {
	case s.remote.err != "":
		text, color = s.remote.err, tokens.StatusColor(ToneError, t.Dark)
	case srv != nil && srv.ListenAddress() != "":
		text, color = "Listening on "+srv.ListenAddress()+" over HTTPS.", t.Success
	case cfg.Mode == remotehost.ModeLAN || cfg.Mode == remotehost.ModeTailscale:
		text = "Choose an address to start listening."
	}
	ui.Text(c, text).FontSize(Typography().BodySmall).TextColor(color).MaxWidth(360)
}

// remoteRelayAddressField commits the relay base on Return, refusing anything
// that is not a bare https:// or wss:// address (the error names the rule).
func (s *Shell) remoteRelayAddressField(c *ui.Context, relay remotehost.RelayConfig) {
	r := &s.remote
	field := textField(c, &r.relayURLDraft).Width(240).Label("Address").Placeholder("https://relay.example.com")
	if field.Submitted() {
		text := strings.TrimSuffix(strings.TrimSpace(r.relayURLDraft), "/")
		cfg := remotehost.RelayConfig{Enabled: true, URL: text, RegistrationKey: relay.RegistrationKey}
		if err := cfg.Validate(); err != nil {
			r.err = err.Error()
		} else {
			s.setRemote(func(rr *settings.RemoteAccessSettings) { rr.RelayURL = text })
		}
	}
	if !field.Focused() {
		r.relayURLDraft = relay.URL
	}
}

// remoteRelayKeyField stores the relay's shared registration key; it is a
// relay credential, so it is masked like the other secrets on this page.
func (s *Shell) remoteRelayKeyField(c *ui.Context, relay remotehost.RelayConfig) {
	r := &s.remote
	field := textField(c, &r.relayKeyDraft).Width(240).Label("Key").Placeholder("unset on open relays").Password()
	if field.Submitted() {
		text := strings.TrimSpace(r.relayKeyDraft)
		s.setRemote(func(rr *settings.RemoteAccessSettings) { rr.RelayKey = text })
	}
	if !field.Focused() {
		r.relayKeyDraft = relay.RegistrationKey
	}
}

// remoteICEField commits the STUN/TURN list on Return.
func (s *Shell) remoteICEField(c *ui.Context) {
	r := &s.remote
	field := textField(c, &r.p2pICEDraft).Width(240).Label("ICE Servers").Placeholder("stun:stun.example.com:3478")
	if field.Submitted() {
		text := strings.TrimSpace(r.p2pICEDraft)
		s.setRemote(func(rr *settings.RemoteAccessSettings) { rr.P2PICE = text })
	}
	if !field.Focused() {
		r.p2pICEDraft = s.settings.Remote.P2PICE
	}
}

// remoteTurnField is one of the two TURN credential fields (the password one is masked).
func (s *Shell) remoteTurnField(c *ui.Context, draft *string, label, saved string, secret bool, set func(*settings.RemoteAccessSettings, string)) {
	field := textField(c, draft).Width(240).Label(label)
	if secret {
		field = field.Password()
	}
	if field.Submitted() {
		text := strings.TrimSpace(*draft)
		s.setRemote(func(rr *settings.RemoteAccessSettings) { set(rr, text) })
	}
	if !field.Focused() {
		*draft = saved
	}
}

// remoteP2PStatus counts the phones currently linked directly.
func (s *Shell) remoteP2PStatus(c *ui.Context, srv *remotehost.Server) {
	t := c.Theme()
	text, color := "No phone is linked directly.", t.TextMuted
	if srv != nil {
		if n := srv.P2PSessions(); n == 1 {
			text, color = "1 phone is linked directly.", t.Success
		} else if n > 1 {
			text, color = strconv.Itoa(n)+" phones are linked directly.", t.Success
		}
	}
	ui.Text(c, text).FontSize(Typography().BodySmall).TextColor(color).MaxWidth(360)
}

// remoteRelayStatus is the Relay group's live state, driven by the Host.
func (s *Shell) remoteRelayStatus(c *ui.Context, srv *remotehost.Server) {
	t := c.Theme()
	tokens := designTokens(t.Dark)
	text, color := "Off. The phone connects over the network directly.", t.TextMuted
	if srv != nil {
		switch st := srv.RelayStatus(); st.State {
		case remotehost.RelayConnecting:
			text, color = "Connecting…", t.TextMuted
		case remotehost.RelayOnline:
			text, color = "Connected through the relay.", t.Success
		case remotehost.RelayBackoff:
			text, color = "Reconnecting: "+st.Message, tokens.StatusColor(ToneWarning, t.Dark)
		case remotehost.RelayError:
			text, color = st.Message, tokens.StatusColor(ToneError, t.Dark)
		}
	}
	if s.remote.err != "" {
		text, color = s.remote.err, tokens.StatusColor(ToneError, t.Dark)
	}
	ui.Text(c, text).FontSize(Typography().BodySmall).TextColor(color).MaxWidth(360)
}

// --- pairing ----------------------------------------------------------------

func (s *Shell) openPairing() {
	srv := s.remoteServer()
	if srv == nil {
		return
	}
	offer, err := srv.StartPairing()
	if err != nil {
		s.remote.err = err.Error()
		return
	}
	code, err := qrcode.New(offer.URL, qrcode.Medium)
	if err != nil {
		srv.CancelPairing()
		s.remote.err = "Could not draw the pairing code: " + err.Error()
		return
	}
	s.remote.offer, s.remote.qr, s.remote.pairOpen = offer, code.Bitmap(), true
}

// closePairing drops the code and its picture (the sheet closed, or the
// address it names is gone).
func (s *Shell) closePairing() {
	r := &s.remote
	if r.offer.Code == "" && !r.pairOpen {
		return
	}
	if r.srv != nil {
		r.srv.CancelPairing()
	}
	r.pairOpen, r.offer, r.qr = false, remotehost.PairingOffer{}, nil
}

func (s *Shell) remotePairDialog(c *ui.Context) {
	r := &s.remote
	if !r.pairOpen {
		if r.offer.Code != "" {
			s.closePairing()
		}
		return
	}
	ui.Modal(c, &r.pairOpen, func() {
		t := c.Theme()
		sp := Spacing()
		typ := Typography()
		expired := time.Now().After(r.offer.ExpiresAt)
		ui.Column(c).Width(360).Gap(sp.L).AlignItems(ui.Center).Children(func() {
			modalTitle(c, "Pair a Phone")
			ui.Text(c, "Scan with Solco on your phone. Works once.").
				FontSize(typ.BodySmall).TextColor(t.TextMuted)
			s.remoteQRView(c, expired)
			if expired {
				ui.Text(c, "This code expired.").FontSize(typ.BodySmall).TextColor(designTokens(t.Dark).StatusColor(ToneError, t.Dark))
			} else {
				ui.Text(c, fmt.Sprintf("Valid until %s", r.offer.ExpiresAt.Format("15:04"))).FontSize(typ.Caption).TextColor(t.TextMuted)
			}
			ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
				if expired {
					if regularButton(c, "New Code").Clicked() {
						s.openPairing()
					}
				} else if regularButton(c, "Copy Link").Clicked() {
					s.copyToClipboard(r.offer.URL)
				}
				if regularPrimaryButton(c, "Done").Clicked() {
					r.pairOpen = false
				}
			})
		})
	})
}

// remoteQRView draws the code in fixed black on white: a QR must keep its
// contrast in every theme, and the bitmap already carries its quiet zone.
func (s *Shell) remoteQRView(c *ui.Context, dim bool) {
	grid := s.remote.qr
	n := len(grid)
	if n == 0 {
		return
	}
	ink := ui.RGB(0, 0, 0)
	if dim {
		ink = ui.RGB(200, 200, 200)
	}
	ui.Box(c).Width(remoteQRSize).Height(remoteQRSize).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, ui.RGB(255, 255, 255), 8)
		cell := r.W / float32(n)
		for y, line := range grid {
			for x, dark := range line {
				if dark {
					p.Fill(ui.Rect{X: r.X + float32(x)*cell, Y: r.Y + float32(y)*cell, W: cell + 0.5, H: cell + 0.5}, ink, 0)
				}
			}
		}
	})
}

// hostHookIdentity lends the Remote Host the session identities this app's
// hooks learned (Herdr's agent list has none for Claude Code/Codex). It runs
// on the Host's goroutines, so it reads Shell state on the window lane, and it
// applies the same provider/terminal rule as restampHookIdentities.
func (s *Shell) hostHookIdentity(instance, paneID, terminalID, kind string) *herdr.AgentSessionIdentity {
	var out *herdr.AgentSessionIdentity
	s.callOnWindow(func() {
		entry, found := s.hookIdentities[instance+"/"+paneID]
		if !found {
			return
		}
		if entry.terminalID != "" && terminalID != "" && entry.terminalID != terminalID {
			return
		}
		have, ok := history.ParseAgentID(kind)
		want, wantOK := history.ParseAgentID(entry.identity.Agent)
		if ok && wantOK && have == want {
			identity := entry.identity
			out = &identity
		}
	})
	return out
}
