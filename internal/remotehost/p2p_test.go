package remotehost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/thewei/solco/internal/agentcatalog"
	"github.com/thewei/solco/internal/history"
)

// fakePhone is the phone's side of the DataChannel: the same fragmenting
// frame codec, playing the relay's role (it sends hreq/wopen, reads hres/…).
type fakePhone struct {
	t      *testing.T
	pc     *webrtc.PeerConnection
	dc     *webrtc.DataChannel
	frames chan relayFrame
	mu     sync.Mutex
	buf    []byte
	opened chan struct{}
}

func newP2PHarness(t *testing.T) (*relayHarness, *httptest.Server) {
	t.Helper()
	h := newRelayHarness(t, func(_ *fakeBackend, o *Options) { o.Relay = RelayConfig{} })
	h.srv.p2p.testLoopback = true
	h.srv.SetP2P(P2PConfig{Enabled: true})
	ts := httptest.NewServer(h.srv.Handler())
	t.Cleanup(ts.Close)
	return h, ts
}

func dialPhone(t *testing.T, ts *httptest.Server, token string) (*fakePhone, int) {
	t.Helper()
	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	p := &fakePhone{t: t, pc: pc, frames: make(chan relayFrame, 256), opened: make(chan struct{})}
	dc, err := pc.CreateDataChannel(p2pDataChannel, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.dc = dc
	dc.OnOpen(func() { close(p.opened) })
	dc.OnMessage(p.onMessage)
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gathered
	body, _ := json.Marshal(p2pOffer{SDP: pc.LocalDescription().SDP})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v3/p2p/session", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode
	}
	var ans struct{ SessionID, SDP string }
	var raw map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	ans.SessionID, ans.SDP = raw["sessionId"], raw["sdp"]
	if ans.SessionID == "" || !strings.Contains(ans.SDP, "m=application") {
		t.Fatalf("answer = %v", raw)
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: ans.SDP}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.opened:
	case <-time.After(10 * time.Second):
		t.Fatal("data channel never opened")
	}
	return p, http.StatusOK
}

func (p *fakePhone) onMessage(msg webrtc.DataChannelMessage) {
	p.mu.Lock()
	p.buf = append(p.buf, msg.Data[1:]...)
	if msg.Data[0] != 0 {
		p.mu.Unlock()
		return
	}
	frame := p.buf
	p.buf = nil
	p.mu.Unlock()
	var f relayFrame
	if err := json.Unmarshal(frame, &f); err != nil {
		p.t.Errorf("bad frame: %v", err)
		return
	}
	p.frames <- f
}

func (p *fakePhone) send(f relayFrame) {
	data, _ := json.Marshal(f)
	for {
		n, flag := len(data), byte(0)
		if n > p2pFragment {
			n, flag = p2pFragment, 1
		}
		if err := p.dc.Send(append([]byte{flag}, data[:n]...)); err != nil {
			p.t.Fatalf("send: %v", err)
		}
		data = data[n:]
		if flag == 0 {
			return
		}
	}
}

func (p *fakePhone) next(id string, timeout time.Duration) relayFrame {
	deadline := time.After(timeout)
	for {
		select {
		case f := <-p.frames:
			if f.ID == id {
				return f
			}
		case <-deadline:
			p.t.Fatalf("no frame for %s", id)
		}
	}
}

func (p *fakePhone) http(id, method, path, token string, body []byte) (int, []byte) {
	h := map[string]string{}
	if token != "" {
		h["authorization"] = "Bearer " + token
	}
	f := relayFrame{T: "hreq", ID: id, Method: method, Path: path, Headers: h}
	if body != nil {
		f.B = base64.StdEncoding.EncodeToString(body)
		h["content-type"] = "application/json"
	}
	p.send(f)
	var status int
	var out []byte
	for {
		fr := p.next(id, 10*time.Second)
		switch fr.T {
		case "hres":
			status = fr.Status
		case "hchunk":
			raw, _ := base64.StdEncoding.DecodeString(fr.B)
			out = append(out, raw...)
		case "hend":
			return status, out
		case "herr":
			p.t.Fatalf("herr %s", fr.Message)
		}
	}
}

func TestP2PTunnelsHTTPOverDataChannel(t *testing.T) {
	h, ts := newP2PHarness(t)
	phone, status := dialPhone(t, ts, h.token)
	if status != http.StatusOK {
		t.Fatalf("session status %d", status)
	}
	if h.srv.P2PSessions() != 1 {
		t.Fatalf("sessions = %d", h.srv.P2PSessions())
	}

	code, body := phone.http("a", "GET", "/api/v3/bootstrap", h.token, nil)
	var boot struct {
		HostName     string          `json:"hostName"`
		Capabilities map[string]bool `json:"capabilities"`
	}
	if code != 200 || json.Unmarshal(body, &boot) != nil || boot.HostName != "Test Mac" || !boot.Capabilities["p2p"] {
		t.Fatalf("bootstrap over P2P = %d %s", code, body)
	}
	// the handler still guards every request: a channel is not a credential
	if code, _ := phone.http("b", "GET", "/api/v3/bootstrap", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("tokenless request = %d, want 401", code)
	}
	// a /c/<secret> prefix is a relay thing; nothing here is addressed that way
	phone.send(relayFrame{T: "hreq", ID: "c", Method: "GET", Path: "/c/anything-at-all-0000/api/v3/bootstrap", Headers: map[string]string{"authorization": "Bearer " + h.token}})
	if fr := phone.next("c", 5*time.Second); fr.T != "herr" {
		t.Fatalf("foreign /c/ prefix answered %s", fr.T)
	}
}

func TestP2PFragmentsLargeFramesBothWays(t *testing.T) {
	h, ts := newP2PHarness(t)
	big := agentcatalog.Catalog{}
	for i := 0; i < 1500; i++ {
		big.Skills = append(big.Skills, agentcatalog.Entry{Name: "/skill-" + strings.Repeat("x", 40), Insert: "/s ", Detail: strings.Repeat("d", 60), Kind: agentcatalog.KindSkill})
	}
	h.srv.opts.Catalog = func(context.Context, history.AgentID, string) agentcatalog.Catalog { return big }
	phone, _ := dialPhone(t, ts, h.token)

	code, body := phone.http("big", "GET", "/api/v3/panes/w1:p1/chat/catalog?instance=default", h.token, nil)
	if code != 200 || len(body) < 3*p2pFragment {
		t.Fatalf("catalog over P2P = %d, %d bytes (want a response spanning several fragments)", code, len(body))
	}
	var out struct{ Skills []json.RawMessage }
	if err := json.Unmarshal(body, &out); err != nil || len(out.Skills) != 1500 {
		t.Fatalf("catalog reassembled wrong: %v, %d skills", err, len(out.Skills))
	}

	// a request frame far larger than one fragment reaches the handler intact
	payload, _ := json.Marshal(map[string]string{"name": "x.txt", "data": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("a"), 200<<10))})
	code, body = phone.http("up", "POST", "/api/v3/panes/w1:p1/chat/attachments", h.token, payload)
	if code != http.StatusNotFound || !strings.Contains(string(body), "attach_unavailable") {
		t.Fatalf("big POST over P2P = %d %s (the handler must see a whole request)", code, body)
	}
}

func TestP2PBridgesWebSockets(t *testing.T) {
	h, ts := newP2PHarness(t)
	phone, _ := dialPhone(t, ts, h.token)
	phone.send(relayFrame{T: "wopen", ID: "ws1", Path: "/api/v3/events?token=" + h.token})
	if fr := phone.next("ws1", 5*time.Second); fr.T != "wopened" {
		t.Fatalf("wopen answered %s %q", fr.T, fr.Message)
	}
	fr := phone.next("ws1", 5*time.Second)
	if fr.T != "wmsg" || fr.S == nil || !strings.Contains(*fr.S, "hello") {
		t.Fatalf("first events frame = %+v", fr)
	}
	phone.send(relayFrame{T: "wclose", ID: "ws1"})
}

func TestP2PRefusals(t *testing.T) {
	h, ts := newP2PHarness(t)
	if _, status := dialPhone(t, ts, ""); status != http.StatusUnauthorized {
		t.Fatalf("tokenless signalling = %d, want 401", status)
	}
	h.srv.SetP2P(P2PConfig{})
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v3/p2p/ice", nil)
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled P2P ice = %d, want 404", resp.StatusCode)
	}
	boot, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v3/bootstrap", nil)
	boot.Header.Set("Authorization", "Bearer "+h.token)
	resp, _ = http.DefaultClient.Do(boot)
	var b struct{ Capabilities map[string]bool }
	_ = json.NewDecoder(resp.Body).Decode(&b)
	resp.Body.Close()
	if b.Capabilities["p2p"] {
		t.Fatal("bootstrap must not advertise p2p while it is off")
	}
}

func TestP2PRevokeAndDisableCloseSessions(t *testing.T) {
	h, ts := newP2PHarness(t)
	_, _ = dialPhone(t, ts, h.token)
	if h.srv.P2PSessions() != 1 {
		t.Fatalf("sessions = %d", h.srv.P2PSessions())
	}
	h.srv.Devices().Revoke(h.devID)
	waitFor(t, func() bool { return h.srv.P2PSessions() == 0 })

	dev, token, _ := h.srv.Devices().Add("Second Phone")
	_ = dev
	_, _ = dialPhone(t, ts, token)
	if h.srv.P2PSessions() != 1 {
		t.Fatalf("sessions = %d", h.srv.P2PSessions())
	}
	h.srv.SetP2P(P2PConfig{})
	waitFor(t, func() bool { return h.srv.P2PSessions() == 0 })
}

func TestP2PICEServersMergeStaticEntries(t *testing.T) {
	h, ts := newP2PHarness(t)
	h.srv.SetP2P(P2PConfig{Enabled: true, ICEServers: []ICEServer{{URLs: []string{"stun:stun.example.test:3478"}}}})
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v3/p2p/ice", nil)
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct{ IceServers []ICEServer }
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || len(got.IceServers) != 1 || got.IceServers[0].URLs[0] != "stun:stun.example.test:3478" {
		t.Fatalf("ice = %+v (%v)", got, err)
	}
}

func TestParseICEServers(t *testing.T) {
	got := ParseICEServers("stun:a.test:3478, turn:b.test:3478?transport=udp\nhttp://nope.test  turns:c.test:5349 junk", "u", "p")
	if len(got) != 3 ||
		got[0].Username != "" || got[0].URLs[0] != "stun:a.test:3478" ||
		got[1].Username != "u" || got[1].Credential != "p" ||
		got[2].URLs[0] != "turns:c.test:5349" {
		t.Fatalf("parsed = %+v", got)
	}
	if ParseICEServers("", "u", "p") != nil {
		t.Fatal("empty text must yield no servers (the built-in list applies later)")
	}
}

func TestP2PUsesBuiltInSTUNWhenNoneConfigured(t *testing.T) {
	h, _ := newP2PHarness(t)
	got := h.srv.p2pICEServers(context.Background())
	if len(got) != len(DefaultICEServers) || !strings.HasPrefix(got[0].URLs[0], "stun:") {
		t.Fatalf("default ICE = %+v", got)
	}
}

func TestCandidateKindsCountsTypesOnly(t *testing.T) {
	sdp := "v=0\na=candidate:1 1 udp 1 192.168.1.2 5000 typ host\na=candidate:2 1 udp 1 1.2.3.4 5001 typ srflx raddr 0.0.0.0 rport 0\na=candidate:3 1 udp 1 5.6.7.8 5002 typ relay\n"
	got := candidateKinds(sdp)
	if got != "host=1 srflx=1 relay=1 prflx=0" {
		t.Fatalf("candidateKinds = %q", got)
	}
	if strings.Contains(got, "1.2.3.4") {
		t.Fatalf("an address leaked into the summary: %q", got)
	}
}
