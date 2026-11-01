package remotehost

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// [INPUT]: 依赖 relay.go/relayclient.go、server_test.go 的 fakeBackend 夹具、coder/websocket、httptest
// [OUTPUT]: relay 全链路回归——secret 载入/重建/0600、RelayConfig 校验、拨出 TLS 群组（排除 ML-KEM）、注册成功/坏 key/坏 hash/4000 终态、hreq 全链路（base64 body、多 chunk 流式、hcancel 取消）、wopen/wmsg 文本+二进制/wclose 双向、agent 断开→桥接全关并重拨、退避阶梯、QR 含/不含 r/rt、query token 401 矩阵
// [POS]: 2026-11-01 relay 接入（2026-11-01）的守卫：假 relay 在内存里按 docs/PROTOCOL.md 帧驱动，短退避注入保证时序测试确定
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

// --- secret ------------------------------------------------------------------

func TestRelaySecretIsStableURLEncodedAndRebuilt(t *testing.T) {
	dir := t.TempDir()
	s1, err := LoadOrCreateRelaySecret(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !validRelaySecret(s1) || len(s1) != 43 {
		t.Fatalf("secret %q is not 43 chars of the relay alphabet", s1)
	}
	raw, err := base64.RawURLEncoding.DecodeString(s1)
	if err != nil || len(raw) != 32 {
		t.Fatalf("secret must decode to 256 bits: %v (%d bytes)", err, len(raw))
	}
	info, err := os.Stat(filepath.Join(dir, relaySecretFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("relay-secret mode = %v, want 0600", info.Mode())
	}
	if s2, _ := LoadOrCreateRelaySecret(dir); s2 != s1 {
		t.Fatal("a stored secret must load unchanged")
	}
	// corrupt or unreadable content rebuilds, like the certificate does
	for _, bad := range []string{"", "short!!", strings.Repeat("é", 40), "x\x00y"} {
		if err := os.WriteFile(filepath.Join(dir, relaySecretFile), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		s3, err := LoadOrCreateRelaySecret(dir)
		if err != nil || !validRelaySecret(s3) || (s3 == s1 && bad != "") {
			t.Fatalf("corrupt secret %q did not rebuild: %q %v", bad, s3, err)
		}
		s1 = s3
	}
	sum := sha256.Sum256([]byte(s1))
	if got, want := RelayTokenHash(s1), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("token hash = %s, want %s", got, want)
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(RelayTokenHash(s1)) {
		t.Fatal("the relay only accepts a lower-case hex sha256 as th=")
	}
}

func TestRelayConfigValidate(t *testing.T) {
	for _, tc := range []struct {
		url     string
		wantErr bool
	}{
		{"https://relay.7200.online", false},
		{"wss://relay.7200.online", false},
		{"https://relay.7200.online/", false}, // a trailing slash is still bare
		{"http://relay.7200.online", true},
		{"ws://relay.7200.online", true},
		{"https://relay.7200.online/base", true},
		{"https://relay.7200.online/?x=1", true},
		{"https://relay.7200.online#frag", true},
		{"relay.7200.online", true},
		{"", true},
	} {
		err := RelayConfig{Enabled: true, URL: tc.url}.Validate()
		if (err != nil) != tc.wantErr {
			t.Errorf("%q: err = %v, wantErr %v", tc.url, err, tc.wantErr)
		}
	}
	// a disabled relay is never validated: a half-remembered address must not
	// block the page while the switch is off
	if err := (RelayConfig{URL: "http://garbage/x?y=z"}).Validate(); err != nil {
		t.Fatalf("disabled relay validated anyway: %v", err)
	}
}

// TestRelayDialUsesClassicTLSGroups pins the GFW finding (2026-11): Go offers
// X25519MLKEM768 first and those handshakes are silently dropped, so the
// outbound relay dial must pin the classic groups. The loopback bridge never
// touches this config.
func TestRelayDialUsesClassicTLSGroups(t *testing.T) {
	cfg := relayTLSConfig()
	want := []tls.CurveID{tls.X25519, tls.CurveP256}
	if !slices.Equal(cfg.CurvePreferences, want) {
		t.Fatalf("CurvePreferences = %v, want %v", cfg.CurvePreferences, want)
	}
	if slices.Contains(cfg.CurvePreferences, tls.X25519MLKEM768) {
		t.Fatal("X25519MLKEM768 must not be offered: those handshakes get dropped")
	}
}

// --- fake relay ---------------------------------------------------------------

// fakeRelay is an in-memory alma-relay: it accepts the agent socket on
// /agent, speaks docs/PROTOCOL.md frames, and records what the agent sends.
type fakeRelay struct {
	t     *testing.T
	wsURL string // ws://127.0.0.1:port/agent

	mu            sync.Mutex
	dials         int
	conn          *websocket.Conn
	registrations []relayFrame
	regKey        string // RELAY_REGISTRATION_KEY demanded when non-empty
	expectHash    string // when non-empty, register.tokenHash must match
	forceClose    int    // close code sent right after registered (0 = stay open)

	frames chan relayFrame // frames from the agent, in order
}

func newFakeRelay(t *testing.T) *fakeRelay {
	f := &fakeRelay{t: t, frames: make(chan relayFrame, 256)}
	mux := http.NewServeMux()
	mux.HandleFunc("/agent", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(relayReadLimit)
		f.mu.Lock()
		f.conn, f.dials = conn, f.dials+1
		f.mu.Unlock()
		ctx := r.Context()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var fr relayFrame
			if json.Unmarshal(data, &fr) != nil {
				continue
			}
			if fr.T != "register" {
				f.emit(fr)
				continue
			}
			f.mu.Lock()
			f.registrations = append(f.registrations, fr)
			regKey, expectHash, force := f.regKey, f.expectHash, f.forceClose
			f.mu.Unlock()
			switch {
			case regKey != "" && fr.Key != regKey:
				f.write(conn, relayFrame{T: "error", Message: "Invalid registration key"})
				_ = conn.Close(4003, "Invalid registration key")
				return
			case expectHash != "" && fr.TokenHash != expectHash:
				f.write(conn, relayFrame{T: "error", Message: "Token hash mismatch"})
				_ = conn.Close(4002, "Token hash mismatch")
				return
			}
			f.write(conn, relayFrame{T: "registered"})
			if force != 0 {
				_ = conn.Close(websocket.StatusCode(force), "forced")
				return
			}
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	f.wsURL = "ws" + strings.TrimPrefix(ts.URL, "http")
	return f
}

func (f *fakeRelay) dial(ctx context.Context, _ string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, f.wsURL+"/agent", nil)
	return conn, err
}

func (f *fakeRelay) write(conn *websocket.Conn, fr relayFrame) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, _ := json.Marshal(fr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		f.t.Logf("fake relay write %s: %v", fr.T, err)
	}
}

func (f *fakeRelay) send(fr relayFrame) {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	if conn == nil {
		f.t.Fatal("no agent socket to send on")
	}
	f.write(conn, fr)
}

func (f *fakeRelay) dropAgent() {
	f.mu.Lock()
	conn := f.conn
	f.conn = nil
	f.mu.Unlock()
	if conn != nil {
		_ = conn.CloseNow()
	}
}

func (f *fakeRelay) countDials() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dials
}

func (f *fakeRelay) lastRegistration() relayFrame {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.registrations) == 0 {
		f.t.Fatal("no register frame arrived")
	}
	return f.registrations[len(f.registrations)-1]
}

func (f *fakeRelay) emit(fr relayFrame) {
	select {
	case f.frames <- fr:
	default: // the test is too slow to drain; dropping keeps the relay honest
	}
}

func (f *fakeRelay) next(timeout time.Duration) (relayFrame, bool) {
	select {
	case fr := <-f.frames:
		return fr, true
	case <-time.After(timeout):
		return relayFrame{}, false
	}
}

func (f *fakeRelay) nextFor(id string, timeout time.Duration) (relayFrame, bool) {
	deadline := time.Now().Add(timeout)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return relayFrame{}, false
		}
		fr, ok := f.next(left)
		if !ok {
			return relayFrame{}, false
		}
		if fr.ID == id {
			return fr, true
		}
	}
}

// collectHTTP reads one tunneled response to its end.
func (f *fakeRelay) collectHTTP(id string, timeout time.Duration) (status int, headers map[string]string, body []byte, errMsg string) {
	deadline := time.Now().Add(timeout)
	chunks := [][]byte{}
	for {
		left := time.Until(deadline)
		if left <= 0 {
			f.t.Fatalf("response %s never finished", id)
		}
		fr, ok := f.nextFor(id, left)
		if !ok {
			f.t.Fatalf("response %s never finished", id)
		}
		switch fr.T {
		case "hres":
			status, headers = fr.Status, fr.Headers
		case "hchunk":
			raw, err := base64.StdEncoding.DecodeString(fr.B)
			if err != nil {
				f.t.Fatalf("bad hchunk: %v", err)
			}
			chunks = append(chunks, raw)
		case "hend":
			return status, headers, bytesJoin(chunks), ""
		case "herr":
			return 0, nil, nil, fr.Message
		default:
			f.t.Fatalf("unexpected frame %s for %s", fr.T, id)
		}
	}
}

func bytesJoin(bs [][]byte) []byte {
	var out []byte
	for _, b := range bs {
		out = append(out, b...)
	}
	return out
}

// --- harness ------------------------------------------------------------------

type relayHarness struct {
	t     *testing.T
	srv   *Server
	back  *fakeBackend
	opts  Options
	relay *fakeRelay
	token string
	devID string
}

// newRelayHarness builds a started Host with the relay switched on against a
// fake relay and short injected backoff ticks (5ms→20ms). mutate runs before
// New and may replace the backend or any option.
func newRelayHarness(t *testing.T, mutate func(back *fakeBackend, o *Options)) *relayHarness {
	t.Helper()
	back := &fakeBackend{instances: []string{"default"}, proj: fixtureProjection(), observe: gridScript(filepath.Join(t.TempDir(), "pids"))}
	opts := Options{
		Config:   Config{Mode: ModeLoopback, Port: relayFreePort(t)},
		Dir:      t.TempDir(),
		Backend:  back,
		HostName: "Test Mac",
		Version:  "test-1",
		Relay:    RelayConfig{Enabled: true, URL: "https://relay.test"},
	}
	if mutate != nil {
		mutate(back, &opts)
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	dev, token, err := srv.Devices().Add("Test Phone")
	if err != nil {
		t.Fatal(err)
	}
	relay := newFakeRelay(t)
	srv.relayHooks = &relayTestHooks{
		dial:       relay.dial,
		backoffMin: 5 * time.Millisecond,
		backoffMax: 20 * time.Millisecond,
		chunk:      16,
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return &relayHarness{t: t, srv: srv, back: back, opts: opts, relay: relay, token: token, devID: dev.ID}
}

func relayFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func (h *relayHarness) waitOnline() {
	h.waitStatus(RelayOnline, 5*time.Second)
}

func (h *relayHarness) waitStatus(state RelayState, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st := h.srv.RelayStatus(); st.State == state {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	tStatus := h.srv.RelayStatus()
	h.t.Fatalf("relay never reached %s (now %s %+q)", state, tStatus.State, tStatus.Message)
}

func (h *relayHarness) secret() string {
	s, err := h.srv.relaySecret()
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

func (h *relayHarness) client() *relayClient {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	return h.srv.relay
}

func relayInflight(c *relayClient) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inflight)
}

func relayBridges(c *relayClient) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bridges)
}

// --- tunnel end to end ----------------------------------------------------------

func TestRelayRegistersAndTunnelsHTTP(t *testing.T) {
	h := newRelayHarness(t, nil)
	h.waitOnline()

	reg := h.relay.lastRegistration()
	if reg.TokenHash != RelayTokenHash(h.secret()) {
		t.Fatal("register must carry sha256 hex of the stored secret")
	}
	if reg.Name != "Test Mac" || reg.Ver != "test-1" || reg.Key != "" {
		t.Fatalf("register = %+v", reg)
	}
	if c := h.client(); c == nil || !strings.HasPrefix(c.bridgeAddr, "127.0.0.1:") {
		t.Fatalf("the loopback bridge must bind 127.0.0.1 only, got %v", c)
	}

	// GET with the phone's Authorization header passed through
	h.relay.send(relayFrame{
		T: "hreq", ID: "h1", Method: "GET",
		Path:    "/api/v3/bootstrap",
		Headers: map[string]string{"authorization": "Bearer " + h.token},
	})
	status, _, body, errMsg := h.relay.collectHTTP("h1", 5*time.Second)
	if errMsg != "" || status != http.StatusOK {
		t.Fatalf("bootstrap through the relay = %d %q", status, errMsg)
	}
	var boot struct {
		HostName string `json:"hostName"`
	}
	if err := json.Unmarshal(body, &boot); err != nil || boot.HostName != "Test Mac" {
		t.Fatalf("bootstrap body = %q (%v)", body, err)
	}

	// POST with a base64 body lands on the real handler (no Attach → 404 JSON)
	payload, _ := json.Marshal(map[string]string{"name": "x", "data": base64.StdEncoding.EncodeToString([]byte("hi"))})
	h.relay.send(relayFrame{
		T: "hreq", ID: "h2", Method: "POST",
		Path:    "/c/" + h.secret() + "/api/v3/panes/w1:p1/chat/attachments",
		Headers: map[string]string{"authorization": "Bearer " + h.token, "content-type": "application/json"},
		B:       base64.StdEncoding.EncodeToString(payload),
	})
	status, _, body, errMsg = h.relay.collectHTTP("h2", 5*time.Second)
	if errMsg != "" || status != http.StatusNotFound || !strings.Contains(string(body), "attach_unavailable") {
		t.Fatalf("POST through the relay = %d %q %q", status, errMsg, body)
	}

	// a path not addressed to this Mac's secret is refused, never dialled in
	h.relay.send(relayFrame{
		T: "hreq", ID: "h3", Method: "GET",
		Path:    "/c/notthismac000000000000/api/v3/bootstrap",
		Headers: map[string]string{"authorization": "Bearer " + h.token},
	})
	if _, _, _, errMsg = h.relay.collectHTTP("h3", 5*time.Second); errMsg == "" {
		t.Fatal("a foreign /c/ prefix must answer herr")
	}
	if st := h.srv.RelayStatus(); st.State != RelayOnline {
		t.Fatalf("relay left %s after traffic", st.State)
	}
}

// gatedBackend holds bootstrap inside the handler so the test can hcancel it.
type gatedBackend struct {
	*fakeBackend
	entered sync.Once
	gate    chan struct{}
}

func (b *gatedBackend) Instances() ([]string, error) {
	b.entered.Do(func() { close(b.gate) })
	time.Sleep(50 * time.Millisecond) // the cancel must win the race
	return b.fakeBackend.Instances()
}

func TestRelayStreamsChunksAndHonoursHCancel(t *testing.T) {
	// chunk injected to 16 bytes: a bootstrap body must stream in pieces
	h := newRelayHarness(t, nil)
	h.waitOnline()
	h.relay.send(relayFrame{
		T: "hreq", ID: "h1", Method: "GET",
		Path:    "/api/v3/bootstrap",
		Headers: map[string]string{"authorization": "Bearer " + h.token},
	})
	deadline := time.Now().Add(5 * time.Second)
	chunks := 0
	for {
		left := time.Until(deadline)
		if left <= 0 {
			t.Fatal("bootstrap never finished")
		}
		fr, ok := h.relay.nextFor("h1", left)
		if !ok {
			t.Fatal("bootstrap never finished")
		}
		if fr.T == "hchunk" {
			chunks++
			continue
		}
		if fr.T == "hend" {
			break
		}
		if fr.T == "hres" {
			continue
		}
		t.Fatalf("unexpected %s", fr.T)
	}
	if chunks < 2 {
		t.Fatalf("bootstrap streamed in %d chunks, want several (chunk=16)", chunks)
	}

	// hcancel aborts the request while the handler is inside the backend
	gate := make(chan struct{})
	gh := newRelayHarness(t, func(back *fakeBackend, o *Options) {
		o.Backend = &gatedBackend{fakeBackend: back, gate: gate}
	})
	gh.waitOnline()
	gh.relay.send(relayFrame{
		T: "hreq", ID: "h9", Method: "GET",
		Path:    "/api/v3/bootstrap",
		Headers: map[string]string{"authorization": "Bearer " + gh.token},
	})
	select {
	case <-gate:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never reached the backend")
	}
	gh.relay.send(relayFrame{T: "hcancel", ID: "h9"})
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if relayInflight(gh.client()) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := relayInflight(gh.client()); n != 0 {
		t.Fatalf("hcancel left %d in-flight requests", n)
	}
	if fr, arrived := gh.relay.nextFor("h9", 300*time.Millisecond); arrived {
		t.Fatalf("a cancelled request still answered %+v", fr)
	}
}

func TestRelayBridgesWebSockets(t *testing.T) {
	h := newRelayHarness(t, nil)
	h.waitOnline()
	secret := h.secret()

	// events: query token only (wopen carries no headers or subprotocols)
	h.relay.send(relayFrame{T: "wopen", ID: "w1", Path: "/c/" + secret + "/api/v3/events?token=" + h.token})
	if fr, ok := h.relay.nextFor("w1", 5*time.Second); !ok || fr.T != "wopened" {
		t.Fatalf("events bridge: %+v", fr)
	}
	fr, ok := h.relay.nextFor("w1", 5*time.Second)
	if !ok || fr.T != "wmsg" || fr.S == nil {
		t.Fatalf("events hello = %+v", fr)
	}
	var hello map[string]any
	if err := json.Unmarshal([]byte(*fr.S), &hello); err != nil || hello["t"] != "hello" {
		t.Fatalf("events hello = %v (%v)", hello, err)
	}

	// terminal: JSON head text frame + ANSI binary frame, input flows back
	h.relay.send(relayFrame{T: "wopen", ID: "w2", Path: "/api/v3/terminals/term_a/stream?cols=51&rows=19&token=" + h.token})
	if fr, ok := h.relay.nextFor("w2", 5*time.Second); !ok || fr.T != "wopened" {
		t.Fatalf("terminal bridge: %+v", fr)
	}
	head, ok := h.relay.nextFor("w2", 5*time.Second)
	if !ok || head.T != "wmsg" || head.S == nil || !strings.Contains(*head.S, `"w":51`) {
		t.Fatalf("terminal head = %+v", head)
	}
	bin, ok := h.relay.nextFor("w2", 5*time.Second)
	if !ok || bin.T != "wmsg" || bin.S != nil || bin.B == "" {
		t.Fatalf("terminal frame = %+v", bin)
	}
	if raw, err := base64.StdEncoding.DecodeString(bin.B); err != nil || string(raw) != "GRID51x19" {
		t.Fatalf("terminal ANSI = %q (%v)", raw, err)
	}
	h.relay.send(relayFrame{T: "wmsg", ID: "w2", S: new(`{"t":"input","text":"echo hi"}`)})
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.back.mu.Lock()
		n := len(h.back.inputs)
		h.back.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("input never reached the backend through the bridge")
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.back.mu.Lock()
	sent := append([]sentInput(nil), h.back.inputs...)
	h.back.mu.Unlock()
	if len(sent) != 1 || sent[0].pane != "w1:p1" || sent[0].text != "echo hi" {
		t.Fatalf("inputs = %+v", sent)
	}

	// the phone closing a bridged socket closes the local one (its observe
	// child is reclaimed and the per-device slot freed)
	h.relay.send(relayFrame{T: "wclose", ID: "w2", Code: 1000, Reason: "bye"})
	deadline = time.Now().Add(5 * time.Second)
	for {
		h.srv.mu.Lock()
		n := h.srv.terminals[h.devID]
		h.srv.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("wclose never freed the terminal stream slot")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// a revoke closes the device's remaining sockets; the bridge reports the
	// close back (the terminal bridge already went with the phone's wclose)
	h.srv.Devices().Revoke(h.devID)
	deadline = time.Now().Add(5 * time.Second)
	var closed relayFrame
	for time.Now().Before(deadline) {
		left := time.Until(deadline)
		if fr, ok := h.relay.next(left); ok && fr.T == "wclose" && fr.ID == "w1" {
			closed = fr
			break
		}
	}
	if closed.ID != "w1" {
		t.Fatalf("revoke never closed the events bridge: %+v", closed)
	}
	if closed.Code != int(websocket.StatusPolicyViolation) || closed.Reason != "revoked" {
		t.Fatalf("wclose w1 = %d %q", closed.Code, closed.Reason)
	}
	if n := relayBridges(h.client()); n != 0 {
		t.Fatalf("%d bridges survived the revoke", n)
	}
}

func TestRelayAgentDropClosesBridgesAndRedials(t *testing.T) {
	h := newRelayHarness(t, nil)
	h.waitOnline()
	h.relay.send(relayFrame{T: "wopen", ID: "w1", Path: "/c/" + h.secret() + "/api/v3/events?token=" + h.token})
	h.relay.send(relayFrame{T: "wopen", ID: "w2", Path: "/api/v3/terminals/term_b/stream?cols=51&rows=19&token=" + h.token})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if n := relayBridges(h.client()); n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bridges = %d, want 2", relayBridges(h.client()))
		}
		time.Sleep(5 * time.Millisecond)
	}

	// the agent socket drops: every bridge must die and the tunnel redial
	h.relay.dropAgent()
	deadline = time.Now().Add(5 * time.Second)
	for {
		if relayBridges(h.client()) == 0 && h.srv.RelayStatus().State == RelayOnline && h.relay.countDials() >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the drop: bridges=%d dials=%d state=%s", relayBridges(h.client()), h.relay.countDials(), h.srv.RelayStatus().State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if reg := h.relay.lastRegistration(); reg.TokenHash != RelayTokenHash(h.secret()) {
		t.Fatal("the redial must re-register with the same hash")
	}
}

// --- registration outcomes -----------------------------------------------------

func TestRelayRegistrationOutcomes(t *testing.T) {
	otherSecret := strings.Repeat("a", 43)
	for _, tc := range []struct {
		name       string
		relayKey   string // key the fake relay demands
		clientKey  string
		expectHash string // hash the fake tunnel answers to
		forceClose int
		wantState  RelayState
		wantMsg    string // substring of the recorded status message
	}{
		{"registered", "", "", "", 0, RelayOnline, ""},
		{"bad key", "k1", "wrong-key", "", 0, RelayBackoff, "Invalid registration key"},
		{"bad hash", "", "", RelayTokenHash(otherSecret), 0, RelayBackoff, "Token hash mismatch"},
		{"replaced", "", "", "", 4000, RelayError, "forced"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRelayHarness(t, func(_ *fakeBackend, o *Options) {
				o.Relay = RelayConfig{Enabled: true, URL: "https://relay.test", RegistrationKey: tc.clientKey}
			})
			h.relay.mu.Lock()
			h.relay.regKey, h.relay.expectHash, h.relay.forceClose = tc.relayKey, tc.expectHash, tc.forceClose
			h.relay.mu.Unlock()

			if tc.wantState == RelayOnline {
				h.waitOnline()
				if dials := h.relay.countDials(); dials != 1 {
					t.Fatalf("a healthy tunnel redialled: %d dials", dials)
				}
				return
			}
			// a refused or replaced tunnel records the reason; a refused one
			// retries on the backoff, a replaced one (4000) never redials
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if st := h.srv.RelayStatus(); st.Message != "" && strings.Contains(st.Message, tc.wantMsg) {
					break
				}
				time.Sleep(2 * time.Millisecond)
			}
			st := h.srv.RelayStatus()
			if !strings.Contains(st.Message, tc.wantMsg) {
				t.Fatalf("status message = %+q, want it to name %q", st.Message, tc.wantMsg)
			}
			if tc.forceClose == 4000 {
				time.Sleep(300 * time.Millisecond)
				if dials := h.relay.countDials(); dials != 1 {
					t.Fatalf("close 4000 redialled %d times", dials-1)
				}
				if h.srv.RelayStatus().State != RelayError {
					t.Fatalf("close 4000 state = %s, want error", h.srv.RelayStatus().State)
				}
				return
			}
			deadline = time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if h.relay.countDials() >= 2 {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			t.Fatalf("a refused tunnel never retried: %d dials, state %s", h.relay.countDials(), h.srv.RelayStatus().State)
		})
	}
}

func TestRelayBackoffLadder(t *testing.T) {
	c := &relayClient{backoffMin: time.Second, backoffMax: 30 * time.Second}
	base := time.Second
	for base <= c.backoffMax { // 1s → 2s → 4s → 8s → 16s → 30s, each jittered
		wait := c.nextDelay(base)
		if wait < base || wait > c.backoffMax {
			t.Fatalf("nextDelay(%v) = %v, want [%v, %v]", base, wait, base, c.backoffMax)
		}
		base *= 2
	}
	for range 20 {
		if wait := c.nextDelay(c.backoffMax); wait != c.backoffMax {
			t.Fatalf("nextDelay at the cap = %v, want %v", wait, c.backoffMax)
		}
	}
	if d := (&relayClient{backoffMin: 5 * time.Millisecond, backoffMax: 20 * time.Millisecond}).nextDelay(5 * time.Millisecond); d > 20*time.Millisecond {
		t.Fatalf("short injected ticks escape the cap: %v", d)
	}
}

// --- pairing offer and query token ---------------------------------------------

func TestPairingOfferCarriesRelayOnlyWhenEnabled(t *testing.T) {
	h := newRelayHarness(t, nil)
	h.waitOnline()
	offer, err := h.srv.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(offer.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if got := q.Get("r"); got != "https://relay.test" {
		t.Fatalf("QR r = %q", got)
	}
	if got := q.Get("rt"); got != h.secret() {
		t.Fatal("QR rt must be the stored secret")
	}
	if q.Get("h") == "" || q.Get("p") == "" || q.Get("fp") == "" || q.Get("c") == "" {
		t.Fatalf("QR lost its direct-connect fields: %s", offer.URL)
	}
	h.srv.CancelPairing()

	// switching the relay off drops r/rt from new offers
	_, port, err := net.SplitHostPort(h.srv.ListenAddress())
	if err != nil {
		t.Fatal(err)
	}
	portNum, _ := strconv.Atoi(port)
	if err := h.srv.Reconfigure(Config{Mode: ModeLoopback, Port: portNum}, RelayConfig{}); err != nil {
		t.Fatal(err)
	}
	offer, err = h.srv.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(offer.URL)
	q = u.Query()
	if q.Has("r") || q.Has("rt") {
		t.Fatalf("a plain offer must not carry relay params: %s", offer.URL)
	}
	if h.srv.RelayStatus().State != RelayIdle {
		t.Fatalf("relay state after disable = %s", h.srv.RelayStatus().State)
	}
}

func TestRelayConfigRejectionAndRestart(t *testing.T) {
	h := newRelayHarness(t, nil)
	h.waitOnline()
	// a malformed relay address never starts a client but keeps serving
	bad := RelayConfig{Enabled: true, URL: "https://relay.test/base"}
	if err := h.srv.Reconfigure(h.opts.Config, bad); err != nil {
		t.Fatal(err)
	}
	if h.client() != nil {
		t.Fatal("an invalid relay config must not start a client")
	}
	st := h.srv.RelayStatus()
	if st.State != RelayError || !strings.Contains(st.Message, "bare base address") {
		t.Fatalf("status = %+v", st)
	}
	// fixing the config dials again
	if err := h.srv.Reconfigure(h.opts.Config, h.opts.Relay); err != nil {
		t.Fatal(err)
	}
	h.waitOnline()
	// and disabling it stops the client and the loopback bridge
	if err := h.srv.Reconfigure(h.opts.Config, RelayConfig{}); err != nil {
		t.Fatal(err)
	}
	if c := h.client(); c != nil {
		t.Fatal("a disabled relay must stop its client")
	}
	if st := h.srv.RelayStatus(); st.State != RelayIdle {
		t.Fatalf("state after disable = %s", st.State)
	}
}

// The address a Host was set to can vanish (DHCP hands the Mac another one, Wi-Fi changes): the bind
// fails and Settings must hear about it, but the relay dials out and bridges through its own
// loopback, so a phone that was paired through it keeps reaching the Mac.
func TestRelayStaysUpWhenTheListenAddressCannotBind(t *testing.T) {
	hold, err := net.Listen("tcp", "127.0.0.1:0") // the port is taken: the same failure as an address that is gone
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	back := &fakeBackend{instances: []string{"default"}, proj: fixtureProjection(), observe: gridScript(filepath.Join(t.TempDir(), "pids"))}
	opts := Options{
		Config:   Config{Mode: ModeLoopback, Port: hold.Addr().(*net.TCPAddr).Port},
		Dir:      t.TempDir(),
		Backend:  back,
		HostName: "Test Mac",
		Version:  "test-1",
		Relay:    RelayConfig{Enabled: true, URL: "https://relay.test"},
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := srv.Devices().Add("Test Phone")
	if err != nil {
		t.Fatal(err)
	}
	relay := newFakeRelay(t)
	srv.relayHooks = &relayTestHooks{dial: relay.dial, backoffMin: 5 * time.Millisecond, backoffMax: 20 * time.Millisecond, chunk: 16}
	t.Cleanup(srv.Close)

	if err := srv.Start(); err == nil {
		t.Fatal("a bind failure must still be reported to Settings")
	}
	if got := srv.ListenAddress(); got != "" {
		t.Fatalf("listening on %q after a failed bind", got)
	}
	h := &relayHarness{t: t, srv: srv, back: back, opts: opts, relay: relay, token: token}
	h.waitOnline()
	h.relay.send(relayFrame{T: "hreq", ID: "h1", Method: "GET", Path: "/api/v3/bootstrap", Headers: map[string]string{"authorization": "Bearer " + token}})
	if status, _, _, errMsg := h.relay.collectHTTP("h1", 5*time.Second); errMsg != "" || status != http.StatusOK {
		t.Fatalf("bootstrap through a relay-only Host = %d %q", status, errMsg)
	}
	// nothing to pair against: the QR carries the address and the pin
	if _, err := srv.StartPairing(); err == nil {
		t.Fatal("pairing needs a listener")
	}
	// fixing the address (here: freeing the port) brings the listener up beside the same tunnel
	_ = hold.Close()
	if err := srv.Reconfigure(opts.Config, opts.Relay); err != nil {
		t.Fatalf("Reconfigure after the address came back: %v", err)
	}
	if srv.ListenAddress() == "" {
		t.Fatal("the listener did not come back")
	}
	h.waitOnline()
	// and turning Remote Access off takes the tunnel down with it
	if err := srv.Reconfigure(Config{Mode: ModeOff}, opts.Relay); err != nil {
		t.Fatal(err)
	}
	if st := srv.RelayStatus(); st.State != RelayIdle {
		t.Fatalf("relay state with Remote Access off = %s", st.State)
	}
}

func TestQueryTokenAuthenticatesLikeTheHeader(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		path  string
		token string
		want  int
	}{
		{"/api/v3/bootstrap", h.token, http.StatusOK},
		{"/api/v3/bootstrap", "wrong", http.StatusUnauthorized},
		{"/api/v3/bootstrap", "", http.StatusUnauthorized},
		{"/api/v3/tree", h.token, http.StatusOK},
		{"/api/v3/tree", "wrong", http.StatusUnauthorized},
		{"/api/v3/panes/w1:p1/peek", h.token, http.StatusOK},
		{"/api/v3/panes/w1:p1/peek", "wrong", http.StatusUnauthorized},
	} {
		if got := h.get(tc.path+"?token="+url.QueryEscape(tc.token), "", nil).StatusCode; got != tc.want {
			t.Errorf("%s with query token %q = %d, want %d", tc.path, tc.token, got, tc.want)
		}
	}
	// the header wins when both are present, so /chat/files keeps reading its
	// own ?token= search term from an authenticated request
	if got := h.get("/api/v3/panes/w1:p1/chat/files?token=ma", h.token, nil).StatusCode; got != http.StatusOK {
		t.Fatalf("files with a header and a search term = %d", got)
	}
	if got := h.get("/api/v3/panes/w1:p1/chat/files?token=ma", "", nil).StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("files without any real credential = %d, want 401", got)
	}
	// a relayed WebSocket dials with nothing but the query token
	wsURL := "ws" + strings.TrimPrefix(h.http.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL+"/api/v3/events?token="+h.token, nil)
	if err != nil {
		t.Fatalf("query-token WS refused: %v", err)
	}
	defer conn.CloseNow()
	typ, data, err := conn.Read(ctx)
	if err != nil || typ != websocket.MessageText || !strings.Contains(string(data), `"hello"`) {
		t.Fatalf("query-token WS first frame = %s %q (%v)", typ, data, err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if _, resp, err := websocket.Dial(ctx2, wsURL+"/api/v3/events?token=wrong", nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a wrong query token = %v", err)
	}
}

func TestRelayStatusIdleWhenOffOrStopped(t *testing.T) {
	h := newRelayHarness(t, nil)
	h.waitOnline()
	if st := h.srv.RelayStatus(); st.State != RelayOnline {
		t.Fatalf("status while tunnelling = %+v", st)
	}
	h.srv.Close()
	if st := h.srv.RelayStatus(); st.State != RelayIdle {
		t.Fatalf("status after Close = %+v", st)
	}
	// a Host that never had the relay on is idle too, and Close stays safe
	plain := newHarness(t)
	if st := plain.srv.RelayStatus(); st.State != RelayIdle {
		t.Fatalf("status with the relay off = %+v", st)
	}
	plain.srv.Close()
	if st := plain.srv.RelayStatus(); st.State != RelayIdle {
		t.Fatalf("status after a plain Close = %+v", st)
	}
}

// A path that accepts TCP but never finishes TLS must not park the dial
// forever: the attempt times out so the backoff loop can retry.
func TestDialRelayTimesOutOnStalledHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // hold it open, say nothing
		}
	}()
	old := relayDialTimeout
	relayDialTimeout = 300 * time.Millisecond
	defer func() { relayDialTimeout = old }()

	tr := &http.Transport{TLSClientConfig: relayTLSConfig()}
	start := time.Now()
	conn, err := dialRelay(tr)(context.Background(), "wss://"+ln.Addr().String()+"/agent")
	if err == nil {
		_ = conn.CloseNow()
		t.Fatal("dial against a silent peer succeeded")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("dial took %v, want it bounded near the timeout", d)
	}
}
