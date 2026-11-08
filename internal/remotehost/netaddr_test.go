package remotehost

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"testing"
	"time"
)

func ia(name, addr string) ifaceAddr { return ifaceAddr{name, netip.MustParseAddr(addr)} }

var sampleAddrs = []ifaceAddr{
	ia("en0", "192.168.1.20"),
	ia("en0", "fe80::1"),
	ia("en0", "fd12:3456::9"),
	ia("utun4", "100.101.102.103"),
	ia("utun4", "fd7a:115c:a1e0::1"),
	ia("en5", "10.0.0.7"),
	ia("en9", "203.0.113.5"), // public
	ia("lo0", "127.0.0.1"),
	ia("bridge0", "169.254.3.4"),
	ia("en1", "0.0.0.0"),
	ia("bridge100", "192.168.139.3"), // VM NAT bridge: the phone can never reach it
	ia("feth1893", "10.10.10.1"),
	ia("utun5", "10.64.0.9"), // a VPN's private address
}

func addrs(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Interface+"="+c.Address)
	}
	return out
}

func TestCandidatesListOnlyAddressesTheModeWouldAccept(t *testing.T) {
	lan := addrs(filterCandidates(ModeLAN, sampleAddrs))
	wantLAN := []string{"en0=192.168.1.20", "en5=10.0.0.7", "en0=fd12:3456::9"}
	if !equalStrings(lan, wantLAN) {
		t.Fatalf("lan = %v, want %v", lan, wantLAN)
	}
	ts := addrs(filterCandidates(ModeTailscale, sampleAddrs))
	wantTS := []string{"utun4=100.101.102.103", "utun4=fd7a:115c:a1e0::1"}
	if !equalStrings(ts, wantTS) {
		t.Fatalf("tailscale = %v, want %v", ts, wantTS)
	}
	for _, m := range []Mode{ModeOff, ModeLoopback, ""} {
		if got := filterCandidates(m, sampleAddrs); len(got) != 0 {
			t.Fatalf("%q lists %v", m, got)
		}
	}
	// whatever is offered must pass Validate for its mode
	for _, m := range []Mode{ModeLAN, ModeTailscale} {
		for _, c := range filterCandidates(m, sampleAddrs) {
			if err := (Config{Mode: m, Port: 7788, Address: c.Address}).Validate(); err != nil {
				t.Errorf("%s offered %s but Validate refuses: %v", m, c.Address, err)
			}
		}
	}
}

func TestLANPutsPhysicalInterfacesFirstAndHidesVirtualOnes(t *testing.T) {
	got := addrs(filterCandidates(ModeLAN, []ifaceAddr{
		ia("bridge100", "192.168.139.3"),
		ia("pdp_ip0", "10.9.9.9"),
		ia("en0", "192.168.5.59"),
		ia("feth1893", "10.10.10.1"),
	}))
	want := []string{"en0=192.168.5.59", "pdp_ip0=10.9.9.9"}
	if !equalStrings(got, want) {
		t.Fatalf("lan = %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCandidatesEnumeratesTheRealMachineWithoutError(t *testing.T) {
	for _, m := range []Mode{ModeLAN, ModeTailscale} {
		if _, err := Candidates(m); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func reachable(port int) bool {
	c := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := c.Get("https://127.0.0.1:" + strconv.Itoa(port) + "/api/v3/bootstrap")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusUnauthorized // up, and refusing strangers
}

func TestReconfigureRebindsAndNeverWidens(t *testing.T) {
	h := newHarness(t)
	p1, p2 := freePort(t), freePort(t)
	srv, err := New(Options{Config: Config{Mode: ModeLoopback, Port: p1}, Dir: t.TempDir(), Backend: h.back, HostName: "m"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	if !reachable(p1) {
		t.Fatal("not listening on the first port")
	}
	offer, err := srv.StartPairing()
	if err != nil {
		t.Fatal(err)
	}

	if err := srv.Reconfigure(Config{Mode: ModeLoopback, Port: p2}, RelayConfig{}); err != nil {
		t.Fatal(err)
	}
	if reachable(p1) || !reachable(p2) {
		t.Fatalf("rebind: old=%v new=%v", reachable(p1), reachable(p2))
	}
	if srv.pairing.Consume(offer.Code) {
		t.Fatal("a code from before the rebind still works (its QR names the old address)")
	}

	// an invalid or wildcard config leaves the Host stopped, never wider
	for _, bad := range []Config{
		{Mode: ModeLAN, Port: p1, Address: "0.0.0.0"},
		{Mode: ModeLAN, Port: p1, Address: "::"},
		{Mode: ModeLAN, Port: p1, Address: ""},
		{Mode: ModeTailscale, Port: p1, Address: "192.168.1.5"},
	} {
		if err := srv.Reconfigure(bad, RelayConfig{}); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
		if reachable(p1) || reachable(p2) {
			t.Fatalf("%+v left a listener open", bad)
		}
	}

	if err := srv.Reconfigure(Config{Mode: ModeOff}, RelayConfig{}); err != nil {
		t.Fatal(err)
	}
	if reachable(p1) || reachable(p2) {
		t.Fatal("off still listens")
	}
	if _, err := srv.StartPairing(); err == nil {
		t.Fatal("pairing offered while off")
	}
}

func TestReconfigureFailsCleanlyWhenThePortIsTaken(t *testing.T) {
	h := newHarness(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	srv, err := New(Options{Config: Config{}, Dir: t.TempDir(), Backend: h.back})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	if err := srv.Reconfigure(Config{Mode: ModeLoopback, Port: busy.Addr().(*net.TCPAddr).Port}, RelayConfig{}); err == nil {
		t.Fatal("bind over a busy port succeeded")
	}
}

func TestICEInterfaceKeepsOnlyTailscaleTunnels(t *testing.T) {
	for _, name := range []string{"bridge100", "feth1893", "awdl0", "llw0", "utun1024-missing"} {
		if ICEInterface(name) {
			t.Errorf("%s must not gather ICE candidates", name)
		}
	}
	if !ICEInterface("en0") || !ICEInterface("en7") {
		t.Fatal("ordinary interfaces must pass")
	}
	proxy := []netip.Addr{netip.MustParseAddr("198.18.0.1")}
	vpn := []netip.Addr{netip.MustParseAddr("10.8.0.2")}
	ts := []netip.Addr{netip.MustParseAddr("fe80::1"), netip.MustParseAddr("100.101.102.103")}
	if carriesTailscale(proxy) || carriesTailscale(vpn) || !carriesTailscale(ts) {
		t.Fatal("only a tunnel with a Tailscale address stays")
	}
}
