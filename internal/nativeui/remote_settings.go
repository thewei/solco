package nativeui

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/app"
	"github.com/thewei/solco/internal/chatattach"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/hostd"
	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

/**
 * [INPUT]: 依赖 internal/remotehost（Server/Config/RelayConfig/RelayStatus/Candidates/PairingOffer）、settings.Remote、Shell.runtime（Herdr Manager）、controls.go 的字段/分段/按钮/开关、settingsGroup/row 共享设置组件、skip2/go-qrcode（只负责把配对 URL 编成模块矩阵）
 * [OUTPUT]: remoteSettingsPath、Shell.settingsRemote（/settings/remote，2026-11-01 重设计：Access 分段 → 一张状态卡（监听/Relay/P2P 汇成一句 + Pair a Phone…）→ 已配对手机与 Revoke → 折叠的 Advanced：Address、Port、Relay、P2P；Address 需要决定时 Advanced 自动展开）、Shell.remoteSummaryFor、Shell.remotePairDialog（二维码模态）、Shell.syncRemoteHost / stopRemoteHost、remoteConfigFrom、remoteRelayFrom
 * [POS]: 手机端 Remote Host（internal/remotehost）的唯一呈现与挂载点：默认关闭，用户选了网络才监听；Relay 是可选的自托管 alma-relay 公网路线（2026-11-01 用户确认），开启即随 Reconfigure 重拨，关闭即停；设置只存模式/端口/地址与 relay 开关/地址/注册钥匙，配对码、设备 token、TLS 私钥与 relaySecret 永远在 remotehost 自己的目录里；二维码用 Painter 画（黑白固定，与主题无关），relay 开启时 QR 追加 r/rt；撤销手机立即生效（remotehost 关它的 WS）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const remoteSettingsPath = "/settings/remote"

const (
	remoteCandidatesTTL = 3 * time.Second
)

// remoteOverlay resolves the tunnel factory for this Mac's RemoteHost directory. It is a package
// variable so tests can swap in a fake tunnel (see remote_settings_test.go useOverlay).
var remoteOverlay = hostd.TailcatOverlay

var (
	remoteModeLabels = []string{"Off", "This Mac only", "Local network", "Tailscale"}
	remoteModes      = []remotehost.Mode{remotehost.ModeOff, remotehost.ModeLoopback, remotehost.ModeLAN, remotehost.ModeTailscale}
)

// remoteState is the Remote Access page's presentation state. The Server is
// created on first need and lives until the shell closes.
type remoteState struct {
	srv       *remotehost.Server
	lease     *hostd.Lease // the right to listen on the Host port (the resident service may hold it while we are away)
	claiming  bool
	err       string
	portDraft string

	relayURLDraft string
	relayKeyDraft string

	p2pICEDraft  string
	p2pUserDraft string
	p2pPassDraft string

	pairOpen     bool // the pairing sheet is up
	pairErr      string
	pairedName   string
	ticking      atomic.Bool // one redraw a second while the page asks for it
	offer        remotehost.PairingOffer
	qr           [][]bool
	pairBaseline map[string]bool // per-device QR the sheet was drawn with; a redraw only when it changed

	cands     []remotehost.Candidate
	candsMode remotehost.Mode
	candsAt   time.Time

	advancedOpen bool

	tsAt  time.Time // the last Tailscale reachability probe
	tsHas bool
}

// remoteRelayFrom, remoteP2PFrom and remoteConfigFrom map the saved choice to the Host's configs; the mapping is
// shared with the resident service (internal/hostd) so both owners read the settings the same way.
func remoteRelayFrom(r settings.RemoteAccessSettings) remotehost.RelayConfig {
	return hostd.RelayFrom(r)
}
func remoteP2PFrom(r settings.RemoteAccessSettings) remotehost.P2PConfig { return hostd.P2PFrom(r) }
func remoteConfigFrom(r settings.RemoteAccessSettings) remotehost.Config { return hostd.ConfigFrom(r) }

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
	git := app.NewRemoteGit()
	opts := remotehost.Options{
		Config:           remoteConfigFrom(s.settings.Remote),
		Overlay:          remoteOverlay(s.userDataDir),
		Dir:              filepath.Join(s.userDataDir, "RemoteHost"),
		Backend:          remotehost.NewManagerBackend(s.runtime),
		Identity:         s.hostHookIdentity,
		AgentFacts:       s.hostAgentFacts,
		Usage:            s.hostUsage,
		Automations:      s.hostAutomations,
		AutomationsAdmin: s.hostAutomationsAdmin(),
		PersonalAgent:    s.hostPersonalAgent,
		AssistantAgents:  s.hostAssistantAgents,
		Sessions:         s.hostSessions(s.hist.service),
		HostName:         host,
		Version:          mygo.App.Version(),
		Git:              git,
		GitWrite:         git,
		Files:            true,
		Machines:         app.NewRemoteMachines(s.runtime),
		OnAddressLost:    s.remoteAddressLost,
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
		opts.Continuer = s.hostContinuer(s.hist.service)
	}
	opts.Trasher = s.hostTrasher(s.hist.service)
	opts.CoordinatorHistory = s.hostCoordinatorHistory(s.hist.service)
	opts.FilterTurns = hostFilterTurns
	opts.OnControlReleased = s.phoneReleasedTerminal
	opts.RemoteDesktop = s.remoteDesktopSeam()
	if voiceSeam := s.hostVoice(); voiceSeam != nil {
		opts.Voice = voiceSeam
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
	if remoteConfigFrom(s.settings.Remote).Mode == remotehost.ModeOff {
		s.remoteChatHost.Store(nil)
		s.remote.err = ""
		_ = srv.Reconfigure(remoteConfigFrom(s.settings.Remote), remoteRelayFrom(s.settings.Remote))
		s.releaseHostLease()
		return
	}
	if s.remote.lease == nil {
		s.claimHostLease() // the resident service may own the port; ask it to step aside, then come back here
		return
	}
	s.remote.err = ""
	srv.SetP2P(remoteP2PFrom(s.settings.Remote))
	if err := srv.Reconfigure(remoteConfigFrom(s.settings.Remote), remoteRelayFrom(s.settings.Remote)); err != nil {
		slog.Warn("remote host reconfigure failed", "error", err)
		s.remote.err = err.Error()
	}
	// Relay may still serve the phone even when the selected LAN interface
	// could not bind. OMP events remain safe to ingest on that fallback path.
	s.remoteChatHost.Store(srv)
}

// startRemoteHostAtLaunch brings the Host up only when the user left it on.
func (s *Shell) startRemoteHostAtLaunch() {
	if remoteConfigFrom(s.settings.Remote).Mode != remotehost.ModeOff {
		s.syncRemoteHost()
	}
}

// remoteAddressLost follows the Mac to its new LAN/Tailscale address after a
// DHCP or network change: the Host's watcher only reports, the fix rides the
// same user path (persist + Reconfigure), so the page, the QR and the next
// launch all agree. The watcher repeats until the saved address is healthy;
// once it is, later reports are no-ops here.
func (s *Shell) remoteAddressLost() {
	s.callOnWindow(func() {
		cfg := remoteConfigFrom(s.settings.Remote)
		if cfg.Mode != remotehost.ModeLAN && cfg.Mode != remotehost.ModeTailscale {
			return
		}
		recommended := s.pickRemoteAddress(cfg.Mode, cfg.Address)
		if recommended == "" || recommended == cfg.Address {
			return
		}
		slog.Info("remote host address lost; following the network", "mode", string(cfg.Mode))
		s.setRemote(func(r *settings.RemoteAccessSettings) { r.Address = recommended })
	})
}

func (s *Shell) stopRemoteHost() {
	s.remoteChatHost.Store(nil)
	if s.remote.srv != nil {
		s.remote.srv.Close()
	}
	s.releaseHostLease()
}

// remoteClaimGo runs the (possibly slow) lease claim off the window thread; tests make it synchronous.
var remoteClaimGo = func(f func()) { go f() }

// remoteClaimWait is how long the desktop waits for a resident service to hand the port over.
const remoteClaimWait = 6 * time.Second

// claimHostLease takes the Host port from the resident service (or finds it free) and then re-runs the sync. Two
// owners never listen at once: without the lease the desktop reports it instead of binding.
func (s *Shell) claimHostLease() {
	if s.remote.claiming || s.userDataDir == "" {
		return
	}
	s.remote.claiming = true
	dir := hostd.RemoteDir(s.userDataDir)
	remoteClaimGo(func() {
		lease, err := hostd.Claim(dir, remoteClaimWait)
		s.applyOnWindow(func() {
			s.remote.claiming = false
			if err != nil {
				slog.Warn("remote host port is owned elsewhere", "error", err)
				s.remote.err = "Remote access is unavailable: another Solco Host owns the port."
				return
			}
			s.remote.lease = lease
			// The resident Host releases the same apphook socket when it yields.
			// The GUI's first attach may have lost that race at startup; retry
			// just the direct listener now, without spawning duplicate spool readers.
			if s.hookServer == nil {
				s.ensureAppHookServer()
			}
			s.syncRemoteHost()
		})
	})
}

// releaseHostLease hands the port back so the resident service can serve while Solco is off or closed.
func (s *Shell) releaseHostLease() {
	s.remote.lease.Release()
	s.remote.lease = nil
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

// settingsRemote is the Remote Access page (2026-11-01 redesign): the one
// Access choice, a status card that says in one place whether phones can reach
// this Mac and by which routes (with the Pair a Phone action), the paired
// phones, and everything an ordinary setup never touches — address, port,
// relay, direct P2P — folded under Advanced.
func (s *Shell) remoteResidentRow(c *ui.Context) {
	exe, _ := os.Executable()
	bin, ok := hostd.Locate(exe)
	if !ok {
		return
	}
	installed := hostd.Installed()
	settingsGroup(c, "", row("Keep Running When Solco Quits", "Phones can still manage Assistants and watch Agents while this app is closed. Chat, files and Git need Solco open.", func() {
		if on, changed := settingSwitch(c, "Keep Running When Solco Quits", installed); changed {
			dir := s.userDataDir
			remoteClaimGo(func() {
				var err error
				if on {
					err = hostd.Install(bin, dir)
				} else {
					err = hostd.Uninstall()
				}
				s.applyOnWindow(func() {
					if err != nil {
						slog.Warn("resident service change failed", "error", err)
						s.remote.err = "The resident service could not be changed."
					}
				})
			})
		}
	}))
}

// remoteSummary is the status card's reading of the Host: a headline, the
// routes behind it, and whether phones can connect right now.

// remoteSummaryFor folds what used to be three Status rows (listener, relay,
// P2P) into one reading.
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

// --- pairing ----------------------------------------------------------------

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
