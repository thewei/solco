package remotehost

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeOverlay stands in for the Tailcat tunnel: peers are keyed by the remote address string a request carries.
type fakeOverlay struct {
	mu           sync.Mutex
	addr         string
	peers        map[string]string // remote addr → key
	disconnected []string
}

func (f *fakeOverlay) Listener() net.Listener { return nil }
func (f *fakeOverlay) Addr() string           { return f.addr }
func (f *fakeOverlay) PeerKey(remote string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.peers[remote]
	return k, ok
}
func (f *fakeOverlay) Disconnect(k string) bool {
	f.mu.Lock()
	f.disconnected = append(f.disconnected, k)
	f.mu.Unlock()
	return true
}
func (f *fakeOverlay) Close() error { return nil }

const (
	keyA = "nodekey:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	keyB = "nodekey:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func withOverlay(h *harness, ov *fakeOverlay) {
	h.srv.overlay.mu.Lock()
	h.srv.overlay.ov, h.srv.overlay.status = ov, OverlayStatus{State: OverlayReady}
	h.srv.overlay.mu.Unlock()
	h.srv.mu.Lock()
	h.srv.fingerprint = "a1cc1d2af9c5a03eefb30bfcc11c5bae5554e2cee2ff007c8cde90e0a096ad57"
	h.srv.mu.Unlock()
}

// request sends one request to the handler as if it arrived from remote.
func (h *harness) as(remote, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = remote
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAdmissionOnlyForBoundPeersOrOpenWindow(t *testing.T) {
	h := newHarness(t)
	if h.srv.allowPeer(keyA) {
		t.Fatal("a stranger was admitted with no pairing window")
	}
	if _, _, err := h.srv.pairing.Start(); err != nil {
		t.Fatal(err)
	}
	if !h.srv.allowPeer(keyA) {
		t.Fatal("the pairing window must admit a peer so it can reach /pair")
	}
	h.srv.pairing.Cancel()
	if h.srv.allowPeer(keyB) {
		t.Fatal("a stranger was admitted after the window closed")
	}
	dev, _, err := h.srv.devices.AddWithKey("Phone", keyB)
	if err != nil || dev.TailcatKey != keyB {
		t.Fatal(err)
	}
	if !h.srv.allowPeer(keyB) {
		t.Fatal("a bound device's key must be admitted")
	}
}

func TestSweepDropsUnboundWindowPeers(t *testing.T) {
	h := newHarness(t)
	ov := &fakeOverlay{addr: "tcX"}
	withOverlay(h, ov)
	if _, _, err := h.srv.pairing.Start(); err != nil {
		t.Fatal(err)
	}
	h.srv.allowPeer(keyA)
	h.srv.allowPeer(keyB)
	if _, _, err := h.srv.devices.AddWithKey("Phone", keyB); err != nil {
		t.Fatal(err)
	}
	h.srv.CancelPairing()
	if len(ov.disconnected) != 1 || ov.disconnected[0] != keyA {
		t.Fatalf("swept %v, want only the unbound %s", ov.disconnected, keyA)
	}
}

func TestPairOverTunnelBindsHandshakeKeyNotClaim(t *testing.T) {
	h := newHarness(t)
	ov := &fakeOverlay{addr: "tcX", peers: map[string]string{"[fd7a:115c:a1e0::1]:5000": keyA}}
	withOverlay(h, ov)
	offer, err := h.srv.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	rec := h.as("[fd7a:115c:a1e0::1]:5000", "POST", "/api/v3/pair", "", `{"code":"`+offer.Code+`","deviceName":"P","clientKey":"`+keyB+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pair = %d %s", rec.Code, rec.Body)
	}
	var bound string
	for _, d := range h.srv.devices.List() {
		if d.Name == "P" {
			bound = d.TailcatKey
		}
	}
	if bound != keyA {
		t.Fatalf("bound %q, want the handshake key %q (a self-reported key must be ignored on the tunnel)", bound, keyA)
	}
}

func TestTokenThroughTunnelNeedsItsBoundPeer(t *testing.T) {
	h := newHarness(t)
	dev, token, err := h.srv.devices.AddWithKey("Phone", keyA)
	if err != nil {
		t.Fatal(err)
	}
	_ = dev
	ov := &fakeOverlay{addr: "tcX", peers: map[string]string{"[fd7a::1]:1": keyA, "[fd7a::2]:2": keyB}}
	withOverlay(h, ov)
	if rec := h.as("[fd7a::1]:1", "GET", "/api/v3/bootstrap", token, ""); rec.Code != http.StatusOK {
		t.Fatalf("bound peer = %d", rec.Code)
	}
	if rec := h.as("[fd7a::2]:2", "GET", "/api/v3/bootstrap", token, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token stolen onto another peer = %d, want 401", rec.Code)
	}
	// a non-tunnel route (LAN) is unaffected by the binding
	if rec := h.as("192.168.1.9:4000", "GET", "/api/v3/bootstrap", token, ""); rec.Code != http.StatusOK {
		t.Fatalf("lan route = %d", rec.Code)
	}
	// a legacy device (no key) cannot use the tunnel at all
	_, legacyToken, _ := h.srv.devices.Add("Old")
	if rec := h.as("[fd7a::1]:1", "GET", "/api/v3/bootstrap", legacyToken, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("legacy device through the tunnel = %d, want 401", rec.Code)
	}
}

func TestRevokeDisconnectsAndStopsAdmission(t *testing.T) {
	h := newHarness(t)
	ov := &fakeOverlay{addr: "tcX"}
	withOverlay(h, ov)
	dev, _, err := h.srv.devices.AddWithKey("Phone", keyA)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.srv.devices.Revoke(dev.ID); err != nil {
		t.Fatal(err)
	}
	if len(ov.disconnected) != 1 || ov.disconnected[0] != keyA {
		t.Fatalf("disconnected %v", ov.disconnected)
	}
	if h.srv.allowPeer(keyA) {
		t.Fatal("a revoked key was admitted again")
	}
}

func TestEnrollBindsLegacyDeviceOnce(t *testing.T) {
	h := newHarness(t)
	ov := &fakeOverlay{addr: "tcENROLLED"}
	withOverlay(h, ov)
	_, token, _ := h.srv.devices.Add("Old")
	rec := h.as("192.168.1.9:4000", "POST", "/api/v3/connect/enroll", token, `{"clientKey":"`+keyA+`"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "tcENROLLED") {
		t.Fatalf("enroll = %d %s", rec.Code, rec.Body)
	}
	if !h.srv.devices.HasTailcatKey(keyA) {
		t.Fatal("key not bound")
	}
	if rec := h.as("192.168.1.9:4000", "POST", "/api/v3/connect/enroll", token, `{"clientKey":"`+keyA+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("idempotent enroll = %d", rec.Code)
	}
	if rec := h.as("192.168.1.9:4000", "POST", "/api/v3/connect/enroll", token, `{"clientKey":"`+keyB+`"}`); rec.Code != http.StatusConflict {
		t.Fatalf("rebinding to another key = %d, want 409", rec.Code)
	}
	if rec := h.as("192.168.1.9:4000", "POST", "/api/v3/connect/enroll", token, `{"clientKey":"nope"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("garbage key = %d, want 400", rec.Code)
	}
	if rec := h.as("192.168.1.9:4000", "POST", "/api/v3/connect/enroll", "", `{"clientKey":"`+keyA+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", rec.Code)
	}
}
