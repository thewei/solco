package nativeui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	qrcode "github.com/skip2/go-qrcode"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

/**
 * [INPUT]: 依赖 internal/remotehost（Server/Config/Candidates/PairingOffer）、settings.Remote、Shell.runtime（Herdr Manager）、controls.go 的字段/分段/按钮、settingsGroup/row 共享设置组件、skip2/go-qrcode（只负责把配对 URL 编成模块矩阵）
 * [OUTPUT]: remoteSettingsPath、Shell.settingsRemote（/settings/remote：Access 分段、Address 选择、Port、Status、Pair a phone、已配对手机与 Revoke）、Shell.remotePairDialog（二维码模态）、Shell.syncRemoteHost / stopRemoteHost、remoteConfigFrom
 * [POS]: 手机端 Remote Host（internal/remotehost）的唯一呈现与挂载点：默认关闭，用户选了网络才监听；设置只存模式/端口/地址，配对码、设备 token、TLS 私钥永远在 remotehost 自己的目录里；二维码用 Painter 画（黑白固定，与主题无关）；撤销手机立即生效（remotehost 关它的 WS）
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

	cands     []remotehost.Candidate
	candsMode remotehost.Mode
	candsAt   time.Time

	pairOpen bool
	offer    remotehost.PairingOffer
	qr       [][]bool
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
	srv, err := remotehost.New(remotehost.Options{
		Config:   remoteConfigFrom(s.settings.Remote),
		Dir:      filepath.Join(s.userDataDir, "RemoteHost"),
		Backend:  remotehost.NewManagerBackend(s.runtime),
		Identity: s.hostHookIdentity,
		HostName: host,
		Version:  mygo.App.Version(),
	})
	if err != nil {
		slog.Warn("remote host unavailable", "error", err)
		s.remote.err = "Remote access is unavailable: " + err.Error()
		return nil
	}
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
	if err := srv.Reconfigure(remoteConfigFrom(s.settings.Remote)); err != nil {
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
		return "Only this Mac can connect — for development and USB tunnelling."
	case remotehost.ModeLAN:
		return "Phones on the same Wi-Fi or Ethernet network. Traffic is encrypted and pinned to this Mac's certificate."
	case remotehost.ModeTailscale:
		return "Phones on your tailnet and nothing else. Tailscale must be running on this Mac."
	}
	return "Nothing listens. The phone app cannot reach this Mac."
}

func (s *Shell) settingsRemote(c *ui.Context) {
	t := c.Theme()
	typ := Typography()
	cfg := remoteConfigFrom(s.settings.Remote)
	srv := s.remoteServer()
	ui.Text(c, "Lets the Solco phone app browse your Workspaces and type into a terminal. It is off until you choose a network; there is no account and no relay, and terminal contents are never logged.").
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

	if srv == nil || srv.ListenAddress() == "" {
		return
	}
	settingsGroup(c, "Phones", row("Pair a phone", "Shows a one-time code that is good for 6 minutes.", func() {
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
		list = append(list, row(d.Name, remoteDeviceNote(d), func() {
			if compactButton(cc, "Revoke").Clicked() {
				if err := srv.Devices().Revoke(d.ID); err != nil {
					s.remote.err = "Could not revoke " + d.Name + ": " + err.Error()
				}
			}
		}))
	}
	settingsGroup(c, "Paired phones", list...)
}

func remoteDeviceNote(d remotehost.Device) string {
	note := "Paired " + d.CreatedAt.Format("2 Jan 2006")
	if !d.LastSeenAt.IsZero() {
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
			ui.Text(c, "Open Solco on your phone and scan this code. It works once.").
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
