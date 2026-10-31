package nativeui

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

// [INPUT]: 依赖 remote_settings.go 与 internal/remotehost 的真实 Server（loopback 空闲端口）
// [OUTPUT]: Remote Access 设置页的守卫：默认关闭且无监听、选「This Mac only」才监听并显示地址、二维码对话框画出矩阵并在关闭时作废配对码、撤销手机、坏模式回落为关、设置文件不含配对码/指纹
// [POS]: 2026-10-31 Remote Host 呈现层回归；真机二维码扫描与手机连接在 P1 任务 10
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
	for _, want := range []string{"Access", "Nothing listens. The phone app cannot reach this Mac."} {
		if !tester.HasText(want) {
			t.Fatalf("Remote Access page lacks %q; texts=%q", want, tester.Texts())
		}
	}
	if tester.HasText("Show QR Code…") || tester.HasText("Port") {
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
	tester := ui.NewTester(s.View, 1200, 800)
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
	if !tester.HasText("Listening on "+srv.ListenAddress()+" over HTTPS.") || !tester.HasText("Show QR Code…") {
		t.Fatalf("status or pairing entry missing; texts=%q", tester.Texts())
	}

	// pairing sheet: a matrix is drawn and closing voids the code
	if err := tester.Click("Show QR Code…"); err != nil {
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
