package nativeui

import (
	"encoding/json"
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

func remoteShell(t *testing.T) *Shell {
	t.Helper()
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

func TestRemoteAccessIsOffUntilChosen(t *testing.T) {
	s := remoteShell(t)
	tester := ui.NewTester(s.View, 1200, 800)
	settleFrames(tester, 3)
	for _, want := range []string{"Access", "Off.", "Advanced"} {
		if !tester.HasText(want) {
			t.Fatalf("Remote Access page lacks %q; texts=%q", want, tester.Texts())
		}
	}
	if tester.HasText("Pair a Phone…") || tester.HasText("Listening · This Mac only") {
		t.Fatalf("an off Host shows listener controls; texts=%q", tester.Texts())
	}
	if srv := s.remote.srv; srv != nil && srv.ListenAddress() != "" {
		t.Fatalf("listening while off on %s", srv.ListenAddress())
	}
}

func TestRemoteLoopbackListensPairsAndRevokes(t *testing.T) {
	s := remoteShell(t)
	port := remoteFreePort(t)
	s.applySettings(func(v *settings.Settings) error { v.Remote.Port = port; return nil })
	tester := ui.NewTester(s.View, 1200, 1100) // tall: the status card, the phones and the folded Advanced share the page
	settleFrames(tester, 3)

	if err := tester.Click("This Mac only"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 3)
	srv := s.remote.srv
	if srv == nil || !strings.HasSuffix(srv.ListenAddress(), ":"+itoa(port)) || !strings.HasPrefix(srv.ListenAddress(), "127.0.0.1:") {
		t.Fatalf("listen address = %v", srv)
	}
	if s.settings.Remote.Mode != "loopback" {
		t.Fatalf("saved mode = %q", s.settings.Remote.Mode)
	}
	if !tester.HasText("Listening · This Mac only") || !tester.HasText("Pair a Phone…") {
		t.Fatalf("status or pairing entry missing; texts=%q", tester.Texts())
	}

	// pairing sheet: a matrix is drawn and closing voids the code
	if err := tester.Click("Pair a Phone…"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 3)
	if !s.remote.pairOpen || len(s.remote.qr) < 21 || !tester.HasText("Pair a Phone") {
		t.Fatalf("pairing sheet not drawn: open=%v n=%d texts=%q", s.remote.pairOpen, len(s.remote.qr), tester.Texts())
	}
	offer := s.remote.offer
	if !strings.HasPrefix(offer.URL, "solco://pair?") {
		t.Fatalf("offer URL = %q", offer.URL)
	}
	raw, _ := json.Marshal(s.settings)
	if strings.Contains(string(raw), offer.Code) || strings.Contains(string(raw), offer.Fingerprint) {
		t.Fatal("pairing code or fingerprint leaked into settings")
	}
	if err := tester.Click("Done"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 3)
	if s.remote.pairOpen || s.remote.offer.Code != "" || s.remote.qr != nil {
		t.Fatalf("pairing state survived Done: %+v", s.remote.offer)
	}

	// a paired phone is listed and Revoke removes it
	if _, _, err := srv.Devices().Add("Test Phone"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 3)
	if !tester.HasText("Test Phone") {
		t.Fatalf("paired phone not listed; texts=%q", tester.Texts())
	}
	if err := tester.Click("Revoke"); err != nil {
		t.Fatal(err)
	}
	if got := srv.Devices().List(); len(got) != 0 {
		t.Fatalf("devices after Revoke = %v", got)
	}

	// back to Off closes the port
	if err := tester.Click("Off"); err != nil {
		t.Fatal(err)
	}
	if srv.ListenAddress() != "" {
		t.Fatalf("still listening on %s after Off", srv.ListenAddress())
	}
}

func TestRemoteAddressModesWithoutACandidateStayStopped(t *testing.T) {
	s := remoteShell(t)
	s.setRemote(func(r *settings.RemoteAccessSettings) { r.Mode = "lan"; r.Address = "" })
	if srv := s.remote.srv; srv == nil || srv.ListenAddress() != "" {
		t.Fatalf("lan without an address listens: %v", srv)
	}
	if s.remote.err == "" {
		t.Fatal("no reason shown for the stopped Host")
	}
}

func TestRemoteAddressStaleWhenTheModeNoLongerOffersIt(t *testing.T) {
	cands := []remotehost.Candidate{{Address: "192.168.5.59", Interface: "en0"}}
	if remoteAddressStale("192.168.5.59", cands) {
		t.Fatal("an offered address is not stale")
	}
	if !remoteAddressStale("192.168.139.3", cands) {
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
	if !tester.HasText("Relay") {
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
	if !tester.HasText("must be a bare base address") {
		t.Fatalf("the rejected relay address is not explained; texts=%q", tester.Texts())
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

// The redesigned page keeps relay, P2P, address and port behind Advanced: a
// default page is the Access choice, the status reading and nothing else;
// opening Advanced reveals the folded rows, and the status card carries the
// pairing action (the old Status rows are gone).
func TestRemoteAdvancedIsFoldedByDefault(t *testing.T) {
	s := remoteShell(t)
	port := remoteFreePort(t)
	s.applySettings(func(v *settings.Settings) error { v.Remote.Port = port; return nil })
	tester := ui.NewTester(s.View, 1200, 1100)
	settleFrames(tester, 3)
	if err := tester.Click("This Mac only"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 3)
	for _, folded := range []string{"Relay", "P2P", "Port", "Relay Status", "P2P Status", "Status"} {
		if tester.HasText(folded) {
			t.Errorf("%q is drawn before Advanced is opened; texts=%q", folded, tester.Texts())
		}
	}
	if err := tester.Click("Advanced"); err != nil {
		t.Fatal(err)
	}
	settleFrames(tester, 3)
	for _, shown := range []string{"Port", "Relay", "P2P"} {
		if !tester.HasText(shown) {
			t.Errorf("%q missing once Advanced is open; texts=%q", shown, tester.Texts())
		}
	}
	for _, gone := range []string{"Relay Status", "P2P Status"} {
		if tester.HasText(gone) {
			t.Errorf("the redundant %q row came back", gone)
		}
	}
}

func TestRemoteSummaryFoldsListenerRelayAndP2P(t *testing.T) {
	s := remoteShell(t)
	cfg := remotehost.Config{Mode: remotehost.ModeLAN}
	if sum := s.remoteSummaryFor(nil, cfg); sum.Listening || !strings.Contains(sum.Detail, "Advanced") {
		t.Fatalf("LAN without an address = %+v, want a pointer to Advanced", sum)
	}
	if sum := s.remoteSummaryFor(nil, remotehost.Config{Mode: remotehost.ModeLoopback}); sum.Listening || sum.Title != "Not listening" {
		t.Fatalf("loopback not started = %+v", sum)
	}
	s.remote.err = "port in use"
	if sum := s.remoteSummaryFor(nil, cfg); sum.Title != "port in use" || sum.Tone != ToneError {
		t.Fatalf("an error must lead the card: %+v", sum)
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
