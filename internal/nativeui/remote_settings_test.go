package nativeui

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

// [INPUT]: 依赖 remote_settings.go 与 internal/remotehost 的真实 Server（loopback 空闲端口）
// [OUTPUT]: Remote Access 设置页的守卫：默认关闭且无监听、选「This Mac only」才监听并显示地址、二维码对话框画出矩阵并在关闭时作废配对码、撤销手机、坏模式回落为关、设置文件不含配对码/指纹；Relay 区——坏地址被拒且不保存、离线地址拨号进退避、QR 携带 r/rt、关闭后回落且 QR 不再携带、relaySecret 不进设置文件
// [POS]: 2026-10-31 Remote Host 呈现层回归；2026-11-01 追加 relay 接入（自托管 alma-relay）的页面守卫；真机二维码扫描与手机连接在 P1 任务 10
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

func remoteFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// fakeOverlay stands in for the Tailcat tunnel: a loopback listener nobody dials. Nothing here reaches the network.
type fakeOverlay struct{ ln net.Listener }

func (f *fakeOverlay) Listener() net.Listener        { return f.ln }
func (f *fakeOverlay) Addr() string                  { return "tc-fake-address" }
func (f *fakeOverlay) PeerKey(string) (string, bool) { return "", false }
func (f *fakeOverlay) Disconnect(string) bool        { return false }
func (f *fakeOverlay) Close() error                  { return f.ln.Close() }

// useOverlay swaps the tunnel factory for the test: ready at once, failing, or blocked until release is closed.
func useOverlay(t *testing.T, fail error, release <-chan struct{}) {
	t.Helper()
	old := remoteOverlay
	remoteOverlay = func(string) remotehost.OverlayFactory {
		return func(ctx context.Context, allow func(string) bool) (remotehost.Overlay, error) {
			if release != nil {
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if fail != nil {
				return nil, fail
			}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			return &fakeOverlay{ln: ln}, nil
		}
	}
	t.Cleanup(func() { remoteOverlay = old })
}

// waitForText settles frames until the page shows text, up to five seconds.
func waitForText(t *testing.T, tester *ui.Tester, text string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		settleFrames(tester, 1)
		if tester.HasText(text) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never saw %q; texts=%q", text, tester.Texts())
}

func enableRemote(s *Shell) {
	on := true
	s.setRemote(func(r *settings.RemoteAccessSettings) { r.Enabled = &on })
}

func remoteShell(t *testing.T) *Shell {
	t.Helper()
	useOverlay(t, nil, nil)
	s := NewShell(WithUserDataDir(t.TempDir()))
	s.loading = false
	t.Cleanup(s.stopRemoteHost)
	s.router.Replace(remoteSettingsPath)
	return s
}

func TestRemoteConfigFromNeverWidens(t *testing.T) {
	cases := []struct {
		in   settings.RemoteAccessSettings
		mode remotehost.Mode
		port int
	}{
		{settings.RemoteAccessSettings{}, remotehost.ModeOff, remotehost.DefaultPort},
		{settings.RemoteAccessSettings{Mode: "wide-open"}, remotehost.ModeOff, remotehost.DefaultPort},
		{settings.RemoteAccessSettings{Mode: "lan", Port: 9000, Address: "192.168.1.5"}, remotehost.ModeLAN, 9000},
	}
	for _, tc := range cases {
		got := remoteConfigFrom(tc.in)
		if got.Mode != tc.mode || got.Port != tc.port {
			t.Errorf("%+v → %+v", tc.in, got)
		}
	}
}

func TestRemoteAccessIsOffUntilSwitchedOn(t *testing.T) {
	s := remoteShell(t)
	tester := ui.NewTester(s.View, 1200, 800)
	settleFrames(tester, 3)
	for _, want := range []string{"Remote Access", "Remote Access is off", "Turn on to pair a phone.", "Advanced"} {
		if !tester.HasText(want) {
			t.Fatalf("Remote Access page lacks %q; texts=%q", want, tester.Texts())
		}
	}
	if tester.HasText("Pair a Phone…") {
		t.Fatalf("an off Host offers pairing; texts=%q", tester.Texts())
	}
	if srv := s.remote.srv; srv != nil && (srv.ListenAddress() != "" || srv.OverlayStatus().State != remotehost.OverlayStopped) {
		t.Fatalf("serving while off: %q %v", srv.ListenAddress(), srv.OverlayStatus())
	}
}

func TestRemoteSwitchReachesReadyPairsAndRevokes(t *testing.T) {
	s := remoteShell(t)
	tester := ui.NewTester(s.View, 1200, 1100)
	settleFrames(tester, 3)
	enableRemote(s)
	waitForText(t, tester, "Secure connection ready")
	if !tester.HasText("Pair a Phone…") {
		t.Fatalf("a ready Host offers no pairing; texts=%q", tester.Texts())
	}
	srv := s.remote.srv
	if srv.ListenAddress() != "" {
		t.Fatalf("the switch alone opened a direct listener on %s", srv.ListenAddress())
	}

	// the pairing dialog draws a v2 code that names the tunnel, counts down, and never prints it
	if err := tester.Click("Pair a Phone…"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 3)
	r := &s.remote
	if !r.pairOpen || len(r.qr) < 21 || !tester.HasText("Pair a Phone") {
		t.Fatalf("pairing dialog not drawn: open=%v n=%d texts=%q", r.pairOpen, len(r.qr), tester.Texts())
	}
	u, err := url.Parse(r.offer.URL)
	if err != nil || u.Query().Get("v") != "2" || u.Query().Get("tc") != "tc-fake-address" {
		t.Fatalf("offer is not a v2 tunnel offer: %v %v", r.offer.URL, err)
	}
	foundCountdown := false
	for _, text := range tester.Texts() {
		if strings.HasPrefix(text, "Valid for about ") && strings.HasSuffix(text, " minutes") { // the tester reads the spoken label; the face is mm:ss
			foundCountdown = true
		}
		if strings.Contains(text, "tc-fake-address") || strings.Contains(text, r.offer.Code) {
			t.Fatalf("the dialog prints a secret: %q", text)
		}
	}
	if !foundCountdown {
		t.Fatalf("no mm:ss countdown; texts=%q", tester.Texts())
	}
	raw, _ := json.Marshal(s.settings)
	if strings.Contains(string(raw), r.offer.Code) || strings.Contains(string(raw), r.offer.Fingerprint) || strings.Contains(string(raw), "tc-fake-address") {
		t.Fatal("a pairing secret leaked into settings")
	}

	// success is the real binding of a new device, not the handshake
	if _, _, err := srv.Devices().Add("Test Phone"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 2)
	if !tester.HasText("Test Phone paired") {
		t.Fatalf("a bound device is not reported; texts=%q", tester.Texts())
	}
	if err := tester.Click("Done"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 3)
	if r.pairOpen || r.offer.Code != "" || r.qr != nil || r.pairedName != "" {
		t.Fatalf("pairing state survived Done: %+v", r.offer)
	}
	if !tester.HasText("Test Phone") {
		t.Fatalf("paired phone not listed; texts=%q", tester.Texts())
	}

	// Revoke is confirmed first; the other phone stays
	other, _, err := srv.Devices().Add("Other Phone")
	if err != nil {
		t.Fatal(err)
	}
	var target string
	for _, d := range srv.Devices().List() {
		if d.Name == "Test Phone" {
			target = d.ID
		}
	}
	s.openConfirm("revoke-phone", target, "Revoke access for Test Phone?", "This phone will need to pair again to connect.")
	settleFrames(tester, 2)
	if !tester.HasText("Revoke access for Test Phone?") || !tester.HasText("This phone will need to pair again to connect.") {
		t.Fatalf("revoke is not confirmed; texts=%q", tester.Texts())
	}
	if len(srv.Devices().List()) != 2 {
		t.Fatal("revoked before the confirm")
	}
	if err := tester.Click("Revoke Access"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 2)
	list := srv.Devices().List()
	if len(list) != 1 || list[0].ID != other.ID {
		t.Fatalf("devices after Revoke = %v", list)
	}

	// Off closes the tunnel and the codes but keeps the phone list, still revocable
	off := false
	s.setRemote(func(rr *settings.RemoteAccessSettings) { rr.Enabled = &off })
	settleFrames(tester, 3)
	if st := srv.OverlayStatus().State; st != remotehost.OverlayStopped {
		t.Fatalf("tunnel still %q after Off", st)
	}
	if !tester.HasText("Remote Access is off") || !tester.HasText("Other Phone") {
		t.Fatalf("off page lost the card or the phone list; texts=%q", tester.Texts())
	}
}

func TestRemoteStatusMatrix(t *testing.T) {
	// Starting: the tunnel is still coming up
	release := make(chan struct{})
	useOverlay(t, nil, release)
	s := NewShell(WithUserDataDir(t.TempDir()))
	s.loading = false
	t.Cleanup(s.stopRemoteHost)
	enableRemote(s)
	srv := s.remote.srv
	sum := s.remoteSummaryFor(srv, remoteConfigFrom(s.settings.Remote))
	if sum.Title != "Starting secure connection…" || !sum.Busy || sum.Pairable {
		t.Fatalf("starting = %+v", sum)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for srv.OverlayStatus().State != remotehost.OverlayReady && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	sum = s.remoteSummaryFor(srv, remoteConfigFrom(s.settings.Remote))
	if sum.Title != "Secure connection ready" || !sum.Pairable || sum.Tone != ToneSuccess {
		t.Fatalf("ready = %+v", sum)
	}

	// Off
	if sum := s.remoteSummaryFor(srv, remotehost.Config{}); sum.Title != "Remote Access is off" || sum.Pairable {
		t.Fatalf("off = %+v", sum)
	}
	// Recovering: the resident service is handing the port back
	s.remote.claiming = true
	if sum := s.remoteSummaryFor(srv, remoteConfigFrom(s.settings.Remote)); sum.Title != "Reconnecting…" || sum.Pairable || !sum.Busy {
		t.Fatalf("recovering = %+v", sum)
	}
	s.remote.claiming = false
}

func TestRemoteTunnelFailureFallsBackOrReports(t *testing.T) {
	useOverlay(t, errors.New("derp unreachable"), nil)
	s := NewShell(WithUserDataDir(t.TempDir()))
	s.loading = false
	t.Cleanup(s.stopRemoteHost)
	s.router.Replace(remoteSettingsPath)
	tester := ui.NewTester(s.View, 1200, 1100)
	settleFrames(tester, 3)

	// no other route: nothing real can serve, so no pairing, but Retry and Details
	enableRemote(s)
	waitForText(t, tester, "Can't start remote access")
	if !tester.HasText("Retry") || !tester.HasText("Details") || tester.HasText("Pair a Phone…") {
		t.Fatalf("error card actions wrong; texts=%q", tester.Texts())
	}
	s.openPairing()
	if s.remote.pairErr == "" || !s.remote.pairOpen {
		t.Fatal("pairing with no route must say it is unavailable")
	}
	settleFrames(tester, 2)
	if !tester.HasText("Pairing is unavailable") || !tester.HasText("Retry") {
		t.Fatalf("no unavailable state; texts=%q", tester.Texts())
	}
	s.closePairing()

	// an enabled direct fallback keeps pairing possible, and the card says so
	s.setRemote(func(r *settings.RemoteAccessSettings) { r.Mode = "loopback"; r.Port = remoteFreePort(t) })
	waitForText(t, tester, "Connection available")
	if !tester.HasText("Pair a Phone…") {
		t.Fatalf("fallback leaves no pairing; texts=%q", tester.Texts())
	}
}

func TestRemotePairingCodeExpiresAndRenews(t *testing.T) {
	s := remoteShell(t)
	tester := ui.NewTester(s.View, 1200, 1100)
	settleFrames(tester, 3)
	enableRemote(s)
	waitForText(t, tester, "Secure connection ready")
	s.openPairing()
	settleFrames(tester, 2)
	old := s.remote.offer.Code
	s.remote.offer.ExpiresAt = time.Now().Add(-time.Second)
	settleFrames(tester, 2)
	if !tester.HasText("This code expired.") || !tester.HasText("New Code") || tester.HasText("Copy Pairing Link") {
		t.Fatalf("expiry state wrong; texts=%q", tester.Texts())
	}
	if err := tester.Click("New Code"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 2)
	if s.remote.offer.Code == "" || s.remote.offer.Code == old || tester.HasText("This code expired.") {
		t.Fatalf("New Code did not renew: %q", s.remote.offer.Code)
	}
}

func TestRemoteAddressModesWithoutACandidateStayStopped(t *testing.T) {
	s := remoteShell(t)
	// The switch is explicitly off (a derived-on lan file would serve through the tunnel regardless of
	// candidates); without a candidate and without the tunnel, the Host stays stopped and says why.
	off := false
	s.setRemote(func(r *settings.RemoteAccessSettings) { r.Mode = "lan"; r.Address = ""; r.Enabled = &off })
	// A switched-off Host serves nothing and is not an error state (the card says off); the old
	// "lan without a candidate stays stopped with a reason" path now belongs to the switch-on case,
	// where the tunnel covers a missing direct address.
	if srv := s.remote.srv; srv != nil && srv.ListenAddress() != "" {
		t.Fatalf("a switched-off Host listens: %v", srv)
	}
	if s.remote.err != "" {
		t.Fatalf("a switched-off Host is not an error state: %q", s.remote.err)
	}
}

func TestRemoteAddressStaleWhenTheModeNoLongerOffersIt(t *testing.T) {
	cands := []remotehost.Candidate{{Address: "192.168.0.59", Interface: "en0"}}
	if remoteAddressStale("192.168.0.59", cands) {
		t.Fatal("an offered address is not stale")
	}
	if !remoteAddressStale("192.168.64.3", cands) {
		t.Fatal("a saved bridge address that is no longer offered must be flagged")
	}
	if remoteAddressStale("", cands) {
		t.Fatal("no saved address is not stale, it is unchosen")
	}
}

func TestRemoteDeviceNoteSaysOnlineOrLastSeen(t *testing.T) {
	d := remotehost.Device{Name: "P", CreatedAt: time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC), LastSeenAt: time.Now().Add(-5 * time.Minute)}
	if got := remoteDeviceNote(d, true); got != "Online · Paired 31 Oct 2026" {
		t.Fatalf("online note = %q", got)
	}
	if got := remoteDeviceNote(d, false); !strings.HasPrefix(got, "Paired 31 Oct 2026 · last seen ") {
		t.Fatalf("offline note = %q", got)
	}
}

// relayOfflineURL refuses instantly and offline: nothing here may dial the
// real internet.
const relayOfflineURL = "https://127.0.0.1:1"

func TestRemoteRelayPageSavesAndDrivesTheTunnel(t *testing.T) {
	s := remoteShell(t)
	tester := ui.NewTester(s.View, 1200, 800)
	settleFrames(tester, 3)
	if err := tester.Click("Advanced"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 3)
	if !tester.HasText("Alma Relay") {
		t.Fatalf("the Relay group is missing; texts=%q", tester.Texts())
	}

	// the listener comes first: the relay only runs on a started Host
	s.setRemote(func(r *settings.RemoteAccessSettings) {
		r.Mode = "loopback"
		r.Port = remoteFreePort(t)
	})

	// a malformed address keeps the Host serving and puts the relay in the
	// error state, naming the rule
	s.setRemote(func(r *settings.RemoteAccessSettings) {
		r.RelayEnabled = true
		r.RelayURL = "https://relay.example/base"
	})
	settleFrames(tester, 3)
	if !strings.Contains(s.remoteDiagnostics(s.remote.srv, remoteConfigFrom(s.settings.Remote)), "must be a bare base address") {
		t.Fatalf("the rejected relay address is not explained; %q", s.remoteDiagnostics(s.remote.srv, remoteConfigFrom(s.settings.Remote)))
	}
	if st := s.remote.srv.RelayStatus(); st.State != remotehost.RelayError || !strings.Contains(st.Message, "bare base address") {
		t.Fatalf("relay status = %+v", st)
	}

	// a syntactically valid but unreachable address dials into the backoff,
	// entirely offline
	s.setRemote(func(r *settings.RemoteAccessSettings) { r.RelayURL = relayOfflineURL })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.remote.srv.RelayStatus(); st.State == remotehost.RelayBackoff {
			break
		}
		settleFrames(tester, 1)
	}
	if st := s.remote.srv.RelayStatus(); st.State != remotehost.RelayBackoff {
		t.Fatalf("relay state = %s, want backoff", st.State)
	}

	// the pairing QR carries the relay params while the relay is on
	offer, err := s.remote.srv.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(offer.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("r") != relayOfflineURL {
		t.Fatalf("QR r = %q", q.Get("r"))
	}
	secret, err := os.ReadFile(filepath.Join(s.userDataDir, "RemoteHost", "relay-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("rt") != strings.TrimSpace(string(secret)) {
		t.Fatal("QR rt must be the stored relay secret")
	}
	s.remote.srv.CancelPairing()

	// switching the relay off returns to idle and drops r/rt from new offers
	s.setRemote(func(r *settings.RemoteAccessSettings) { r.RelayEnabled = false })
	if st := s.remote.srv.RelayStatus(); st.State != remotehost.RelayIdle {
		t.Fatalf("relay state after disable = %s", st.State)
	}
	offer, err = s.remote.srv.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(offer.URL)
	if u.Query().Has("r") || u.Query().Has("rt") {
		t.Fatalf("a plain offer carries relay params: %s", offer.URL)
	}
	s.remote.srv.CancelPairing()

	// the relay secret never leaks into the settings file
	raw, _ := json.Marshal(s.settings)
	if strings.Contains(string(raw), strings.TrimSpace(string(secret))) || strings.Contains(string(raw), "relay-secret") {
		t.Fatal("the relay connect code leaked into settings")
	}
	if s.settings.Remote.RelayEnabled || s.settings.Remote.RelayURL != relayOfflineURL {
		t.Fatalf("relay settings = %+v", s.settings.Remote)
	}
}

func TestRemoteRelayFromMapsTheSavedChoice(t *testing.T) {
	got := remoteRelayFrom(settings.RemoteAccessSettings{RelayEnabled: true, RelayURL: " https://relay.test ", RelayKey: "k"})
	if !got.Enabled || got.URL != "https://relay.test" || got.RegistrationKey != "k" {
		t.Fatalf("remoteRelayFrom = %+v", got)
	}
}

func TestRemoteP2PFromMapsTheSavedChoice(t *testing.T) {
	if got := remoteP2PFrom(settings.RemoteAccessSettings{}); !got.Enabled || len(got.ICEServers) != 0 {
		t.Fatalf("zero settings = %+v, want P2P on with the built-in STUN list", got)
	}
	got := remoteP2PFrom(settings.RemoteAccessSettings{P2PDisabled: true, P2PICE: "turn:t.test:3478", P2PTurnUser: "u", P2PTurnPass: "p"})
	if got.Enabled || len(got.ICEServers) != 1 || got.ICEServers[0].Username != "u" {
		t.Fatalf("saved choice = %+v", got)
	}
}

// The page keeps the fallbacks, relay, P2P and diagnostics behind Advanced: a default page is the switch, the
// status card and the phones; opening Advanced reveals the folded rows in the spec's order.
func TestRemoteAdvancedIsFoldedByDefault(t *testing.T) {
	s := remoteShell(t)
	tester := ui.NewTester(s.View, 1200, 1400)
	settleFrames(tester, 3)
	enableRemote(s)
	waitForText(t, tester, "Secure connection ready")
	folded := []string{"Built-in Secure Connection", "Allow Local Network Fallback", "Alma Relay", "WebRTC P2P", "Connection Diagnostics"}
	for _, text := range folded {
		if tester.HasText(text) {
			t.Errorf("%q is drawn before Advanced is opened", text)
		}
	}
	if err := tester.Click("Advanced"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 3)
	for _, text := range folded {
		if !tester.HasText(text) {
			t.Errorf("%q missing once Advanced is open; texts=%q", text, tester.Texts())
		}
	}
	for _, gone := range []string{"Port"} {
		if tester.HasText(gone) {
			t.Errorf("%q shows although no direct fallback is chosen", gone)
		}
	}
}

func TestRemoteDiagnosticsAreRedacted(t *testing.T) {
	s := remoteShell(t)
	enableRemote(s)
	deadline := time.Now().Add(5 * time.Second)
	for s.remote.srv.OverlayStatus().State != remotehost.OverlayReady && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	offer, err := s.remote.srv.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	text := s.remoteDiagnostics(s.remote.srv, remoteConfigFrom(s.settings.Remote))
	for _, secret := range []string{"tc-fake-address", offer.Code, offer.Fingerprint, "solco://"} {
		if strings.Contains(text, secret) {
			t.Fatalf("diagnostics leak %q:\n%s", secret, text)
		}
	}
	if !strings.Contains(text, "Secure connection: Ready") {
		t.Fatalf("diagnostics lack the route health:\n%s", text)
	}
}

// The page opens like its siblings — a labelled first group — and the
// Advanced strip leads from the column's left edge like every other row.
func TestRemoteAccessPageSharesTheSettingsRhythm(t *testing.T) {
	s := remoteShell(t)
	tester := ui.NewTester(s.View, 1200, 800)
	settleFrames(tester, 3)
	title, ok := tester.Find("Phone access")
	if !ok {
		t.Fatalf("the first group has no label; texts=%q", tester.Texts())
	}
	advanced, ok := tester.Find("Advanced")
	if !ok {
		t.Fatal("no Advanced strip")
	}
	if advanced.X-title.X > 40 {
		t.Fatalf("Advanced starts %.0fpt right of the group labels: it is centered, not leading", advanced.X-title.X)
	}
}
