package remotehost

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/thewei/solco/internal/herdr"
)

// --- fakes -------------------------------------------------------------------

type fakeSource struct {
	events chan herdr.RuntimeEvent
	done   chan struct{}
	once   sync.Once
}

func (f *fakeSource) Events() <-chan herdr.RuntimeEvent { return f.events }
func (f *fakeSource) Done() <-chan struct{}             { return f.done }
func (f *fakeSource) Close()                            { f.once.Do(func() { close(f.done) }) }

type sentInput struct {
	pane, text string
	keys       []string
}

type fakeBackend struct {
	mu        sync.Mutex
	instances []string
	proj      herdr.Projection
	projErr   error
	sources   []*fakeSource
	subPanes  [][]string
	inputs    []sentInput
	inputErr  error
	prompts   []sentInput
	promptErr error
	agentKeys []sentInput
	keysErr   error
	observe   func(terminalID string, cols, rows int) []string
	observed  []grid
	screen    string
	readErr   error
	reads     int
}

func (b *fakeBackend) Instances() ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.instances, nil
}

func (b *fakeBackend) Projection(string) (herdr.Projection, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.proj, b.projErr
}

func (b *fakeBackend) set(p herdr.Projection) {
	b.mu.Lock()
	b.proj = p
	b.mu.Unlock()
}

func (b *fakeBackend) Subscribe(_ string, panes []string) (EventSource, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	src := &fakeSource{events: make(chan herdr.RuntimeEvent, 8), done: make(chan struct{})}
	b.sources = append(b.sources, src)
	b.subPanes = append(b.subPanes, append([]string(nil), panes...))
	return src, nil
}

func (b *fakeBackend) source(i int) *fakeSource {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		if i < len(b.sources) {
			s := b.sources[i]
			b.mu.Unlock()
			return s
		}
		b.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (b *fakeBackend) SendPaneInput(_ context.Context, _, paneID, text string, keys []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inputs = append(b.inputs, sentInput{paneID, text, keys})
	return b.inputErr
}

func (b *fakeBackend) PromptAgent(_ context.Context, _, paneID, text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prompts = append(b.prompts, sentInput{paneID, text, nil})
	return b.promptErr
}

func (b *fakeBackend) SendAgentKeys(_ context.Context, _, paneID string, keys []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.agentKeys = append(b.agentKeys, sentInput{paneID, "", keys})
	return b.keysErr
}

func (b *fakeBackend) ReadPane(_ context.Context, _, _ string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reads++
	return b.screen, b.readErr
}

func (b *fakeBackend) ObserveCommand(_, terminalID string, cols, rows int) ([]string, error) {
	b.mu.Lock()
	b.observed = append(b.observed, grid{cols, rows})
	fn := b.observe
	b.mu.Unlock()
	return fn(terminalID, cols, rows), nil
}

func (b *fakeBackend) observedGrids() []grid {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]grid(nil), b.observed...)
}

func fixtureProjection() herdr.Projection {
	return herdr.Projection{
		Projects: []herdr.Project{{ID: "w1", Label: "solco", AgentStatus: "working", CWD: "/work/solco"}},
		Tabs:     []herdr.Tab{{ID: "w1:t1", ProjectID: "w1", Label: "main", AgentStatus: "working"}},
		Panes: []herdr.Pane{
			{ID: "w1:p1", TerminalID: "term_a", TabID: "w1:t1", ProjectID: "w1", Label: "claude", Agent: "claude", AgentStatus: "working", CWD: "/work/solco", Focused: true},
			{ID: "w1:p2", TerminalID: "term_b", TabID: "w1:t1", ProjectID: "w1", Label: "zsh", AgentStatus: "unknown"},
		},
	}
}

func frameLine(payload string, seq int, full bool) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(payload))
	return fmt.Sprintf(`{"type":"terminal.frame","bytes":"%s","full":%v,"seq":%d,"width":51,"height":19}`, b64, full, seq)
}

// gridScript prints one frame naming its grid, records its pid, then idles.
func gridScript(pidFile string) func(string, int, int) []string {
	return func(_ string, cols, rows int) []string {
		line := frameLine(fmt.Sprintf("GRID%dx%d", cols, rows), 1, true)
		return []string{"/bin/sh", "-c", fmt.Sprintf("echo $$ >> %s; echo '%s'; exec sleep 30", pidFile, line)}
	}
}

type harness struct {
	t       *testing.T
	srv     *Server
	back    *fakeBackend
	http    *httptest.Server
	token   string
	devID   string
	pidFile string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	back := &fakeBackend{instances: []string{"default"}, proj: fixtureProjection()}
	pidFile := filepath.Join(t.TempDir(), "pids")
	back.observe = gridScript(pidFile)
	srv, err := New(Options{Config: Config{Mode: ModeLoopback, Port: 7788}, Dir: t.TempDir(), Backend: back, HostName: "Test Mac", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	dev, token, err := srv.Devices().Add("Test Phone")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { srv.Close(); ts.Close() })
	return &harness{t: t, srv: srv, back: back, http: ts, token: token, devID: dev.ID, pidFile: pidFile}
}

func (h *harness) get(path string, token string, header http.Header) *http.Response {
	req, _ := http.NewRequest("GET", h.http.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (h *harness) dial(path string, subprotocolAuth bool) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := &websocket.DialOptions{}
	if subprotocolAuth {
		opts.Subprotocols = []string{subprotocolBearer, h.token}
	} else {
		opts.HTTPHeader = http.Header{"Authorization": {"Bearer " + h.token}}
	}
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(h.http.URL, "http")+path, opts)
	if conn != nil {
		h.t.Cleanup(func() { conn.CloseNow() })
	}
	return conn, resp, err
}

func readJSON(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("want a text frame, got %v (%q)", typ, data)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("bad json %q: %v", data, err)
	}
	return m
}

func readBinary(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageBinary {
		t.Fatalf("want a binary frame, got %v (%q)", typ, data)
	}
	return data
}

func writeClient(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	raw, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
}

// --- auth ---------------------------------------------------------------------

func TestProtectedEndpointsRefuseWithoutAValidToken(t *testing.T) {
	h := newHarness(t)
	paths := []string{"/api/v3/bootstrap", "/api/v3/tree", "/api/v3/events", "/api/v3/terminals/term_a/stream?cols=51&rows=19"}
	for _, p := range paths {
		if got := h.get(p, "", nil).StatusCode; got != http.StatusUnauthorized {
			t.Errorf("%s without token = %d, want 401", p, got)
		}
		if got := h.get(p, "not-a-token", nil).StatusCode; got != http.StatusUnauthorized {
			t.Errorf("%s with a wrong token = %d, want 401", p, got)
		}
		if got := h.get(p, "", http.Header{"Authorization": {"Basic " + h.token}}).StatusCode; got != http.StatusUnauthorized {
			t.Errorf("%s with a non-Bearer scheme = %d, want 401", p, got)
		}
	}
	if got := h.get("/api/v3/bootstrap", h.token, nil).StatusCode; got != http.StatusOK {
		t.Fatalf("a valid token must pass, got %d", got)
	}
}

func TestWebSocketAuthNeedsTheBearerSubprotocolPair(t *testing.T) {
	h := newHarness(t)
	if _, resp, err := h.dial("/api/v3/events", false); err != nil || resp.Header.Get("Sec-WebSocket-Protocol") != "" {
		t.Fatalf("an Authorization header works for native clients: %v", err)
	}
	conn, resp, err := h.dial("/api/v3/events", true)
	if err != nil {
		t.Fatalf("the [solco.bearer, token] pair must authenticate: %v", err)
	}
	if got := resp.Header.Get("Sec-WebSocket-Protocol"); got != subprotocolBearer {
		t.Fatalf("server must select %q, got %q (the token must never be echoed)", subprotocolBearer, got)
	}
	_ = conn
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, resp, err = websocket.Dial(ctx, "ws"+strings.TrimPrefix(h.http.URL, "http")+"/api/v3/events",
		&websocket.DialOptions{Subprotocols: []string{"shardlane.bearer", h.token}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the retired shardlane.bearer prefix must be refused: %v", err)
	}
}

func TestBootstrapNamesTheHostAndWhatItCanDo(t *testing.T) {
	h := newHarness(t)
	var body struct {
		HostName     string          `json:"hostName"`
		Capabilities map[string]bool `json:"capabilities"`
		Instances    []struct {
			ID string `json:"id"`
		} `json:"instances"`
	}
	if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.HostName != "Test Mac" || !body.Capabilities["terminal"] || !body.Capabilities["chat"] || len(body.Instances) != 1 || body.Instances[0].ID != "default" {
		t.Fatalf("bootstrap = %+v", body)
	}
}

// --- pairing --------------------------------------------------------------------

func postPair(t *testing.T, base, code, name string) (*http.Response, map[string]string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code, "deviceName": name})
	resp, err := http.Post(base+"/api/v3/pair", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestPairingHandsOutOneTokenPerCodeAndRefusesTheRest(t *testing.T) {
	h := newHarness(t)
	code, _, _ := h.srv.pairing.Start()
	if resp, _ := postPair(t, h.http.URL, "WRONGCODE", "x"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a wrong code = %d, want 401", resp.StatusCode)
	}
	resp, out := postPair(t, h.http.URL, code, "Wilson's iPhone")
	if resp.StatusCode != http.StatusOK || out["token"] == "" || out["deviceId"] == "" {
		t.Fatalf("pair = %d %v", resp.StatusCode, out)
	}
	if resp, _ := postPair(t, h.http.URL, code, "again"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a used code = %d, want 401", resp.StatusCode)
	}
	if got := h.get("/api/v3/bootstrap", out["token"], nil).StatusCode; got != http.StatusOK {
		t.Fatalf("the new token must work, got %d", got)
	}
	var found bool
	for _, d := range h.srv.Devices().List() {
		if d.ID == out["deviceId"] {
			found = d.Name == "Wilson's iPhone"
		}
	}
	if !found {
		t.Fatal("the paired device must be recorded under its name")
	}
	if resp, _ := postPair(t, h.http.URL, "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an empty code = %d, want 401", resp.StatusCode)
	}
}

func TestOffModeOpensNoPortAndOffersNoPairing(t *testing.T) {
	srv, err := New(Options{Config: Config{}, Dir: t.TempDir(), Backend: &fakeBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if srv.httpServer != nil {
		t.Fatal("off mode must not listen")
	}
	if _, err := srv.StartPairing(); err == nil {
		t.Fatal("off mode must not offer pairing")
	}
	bad, _ := New(Options{Config: Config{Mode: ModeLAN, Port: 7788, Address: "0.0.0.0"}, Dir: t.TempDir(), Backend: &fakeBackend{}})
	if err := bad.Start(); err == nil {
		t.Fatal("a wildcard LAN address must be refused at Start")
	}
}

// pinnedClient trusts exactly one certificate fingerprint, like the phone does.
func pinnedClient(fp string) *http.Client {
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, // verification is the pin below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || Fingerprint(raw[0]) != fp {
				return errors.New("certificate does not match the pinned fingerprint")
			}
			return nil
		},
	}}}
}

func TestPairingOfferWorksEndToEndOverPinnedTLS(t *testing.T) {
	back := &fakeBackend{instances: []string{"default"}, proj: fixtureProjection()}
	srv, err := New(Options{Config: Config{Mode: ModeLoopback, Port: 7788}, Dir: t.TempDir(), Backend: back, HostName: "Test Mac"})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.serve(ln, Config{Mode: ModeLoopback}); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	offer, err := srv.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(offer.URL)
	if err != nil || u.Scheme != "solco" || u.Host != "pair" {
		t.Fatalf("offer URL %q (%v)", offer.URL, err)
	}
	q := u.Query()
	if q.Get("fp") != offer.Fingerprint || q.Get("c") != offer.Code || q.Get("n") != "Test Mac" || q.Get("h") != "127.0.0.1" {
		t.Fatalf("offer query = %v", q)
	}
	base := "https://" + net.JoinHostPort(q.Get("h"), q.Get("p"))

	good := pinnedClient(q.Get("fp"))
	body, _ := json.Marshal(map[string]string{"code": q.Get("c"), "deviceName": "Phone"})
	resp, err := good.Post(base+"/api/v3/pair", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("a pinned client must connect: %v", err)
	}
	var out map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out["token"] == "" {
		t.Fatalf("pair over TLS = %d %v", resp.StatusCode, out)
	}

	if _, err := pinnedClient(strings.Repeat("0", 64)).Get(base + "/api/v3/bootstrap"); err == nil {
		t.Fatal("a client pinned to another fingerprint must refuse the Host")
	}
	if got := ln.Addr().String(); strings.HasPrefix(got, "0.0.0.0") || strings.HasPrefix(got, "[::]") {
		t.Fatalf("listener must not be a wildcard: %s", got)
	}
}

// --- tree -----------------------------------------------------------------------

func TestTreeKeepsHerdrOrderAndHidesFocus(t *testing.T) {
	h := newHarness(t)
	resp := h.get("/api/v3/tree", h.token, nil)
	raw := new(strings.Builder)
	var tree Tree
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&tree); err != nil {
		t.Fatal(err)
	}
	if tree.Instance != "default" || tree.Revision != 1 || len(tree.Workspaces) != 1 {
		t.Fatalf("tree = %+v", tree)
	}
	panes := tree.Workspaces[0].Tabs[0].Panes
	if len(panes) != 2 || panes[0].PaneID != "w1:p1" || panes[0].TerminalID != "term_a" || panes[0].Agent == nil || panes[0].Agent.Provider != "claude" || panes[1].Agent != nil {
		t.Fatalf("panes = %+v", panes)
	}
	b, _ := json.Marshal(tree)
	raw.Write(b)
	if strings.Contains(strings.ToLower(raw.String()), "focus") {
		t.Fatalf("the tree must not carry Herdr's global focus (phone navigation is client-local): %s", raw)
	}
}

func TestTreeRevisionMovesOnlyWhenContentDoes(t *testing.T) {
	h := newHarness(t)
	etag := h.get("/api/v3/tree", h.token, nil).Header.Get("ETag")
	if etag != `"1"` {
		t.Fatalf("first ETag = %q", etag)
	}
	if got := h.get("/api/v3/tree", h.token, http.Header{"If-None-Match": {etag}}).StatusCode; got != http.StatusNotModified {
		t.Fatalf("an unchanged tree with If-None-Match = %d, want 304", got)
	}
	focusOnly := fixtureProjection()
	focusOnly.Panes[0].Focused, focusOnly.Panes[1].Focused = false, true
	h.back.set(focusOnly)
	if got := h.get("/api/v3/tree", h.token, http.Header{"If-None-Match": {etag}}).StatusCode; got != http.StatusNotModified {
		t.Fatalf("a Mac-side focus move must not bump the revision, got %d", got)
	}
	changed := fixtureProjection()
	changed.Panes[0].AgentStatus = "blocked"
	h.back.set(changed)
	resp := h.get("/api/v3/tree", h.token, http.Header{"If-None-Match": {etag}})
	if resp.StatusCode != http.StatusOK || resp.Header.Get("ETag") != `"2"` {
		t.Fatalf("a real change = %d etag %q, want 200 \"2\"", resp.StatusCode, resp.Header.Get("ETag"))
	}
}

func TestUnknownInstanceNeverReachesTheBackend(t *testing.T) {
	h := newHarness(t)
	for _, inst := range []string{"nope", "../../etc", "default/../x", "machine:abc"} {
		if got := h.get("/api/v3/tree?instance="+url.QueryEscape(inst), h.token, nil).StatusCode; got != http.StatusNotFound {
			t.Errorf("instance %q = %d, want 404", inst, got)
		}
	}
}

func TestBackendFailureIsAGenericBadGateway(t *testing.T) {
	h := newHarness(t)
	h.back.mu.Lock()
	h.back.projErr = errors.New("dial unix /Users/wilson/.config/herdr/herdr.sock: connection refused")
	h.back.mu.Unlock()
	resp := h.get("/api/v3/tree", h.token, nil)
	body := new(strings.Builder)
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	body.Write(buf[:n])
	if resp.StatusCode != http.StatusBadGateway || strings.Contains(body.String(), "/Users/") {
		t.Fatalf("status %d body %q: backend errors must not leak paths", resp.StatusCode, body.String())
	}
}

// --- events ---------------------------------------------------------------------

func TestEventsHelloThenTreeChangedOnlyForRealChanges(t *testing.T) {
	h := newHarness(t)
	conn, _, err := h.dial("/api/v3/events", false)
	if err != nil {
		t.Fatal(err)
	}
	hello := readJSON(t, conn)
	if hello["t"] != "hello" || hello["revision"] != float64(1) {
		t.Fatalf("hello = %v", hello)
	}
	src := h.back.source(0)
	if src == nil {
		t.Fatal("the Host never subscribed to Herdr events")
	}
	if got := h.back.subPanes[0]; len(got) != 2 {
		t.Fatalf("the subscription must cover the session's panes, got %v", got)
	}

	// An event that changes nothing visible (e.g. a Mac-side focus move) stays silent.
	src.events <- herdr.RuntimeEvent{Event: "pane.updated"}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("no frame is expected when the tree content did not change")
	}
	cancel()
}

func TestEventsPushRevisionWhenTheTreeChanges(t *testing.T) {
	h := newHarness(t)
	conn, _, err := h.dial("/api/v3/events", true)
	if err != nil {
		t.Fatal(err)
	}
	readJSON(t, conn) // hello
	src := h.back.source(0)
	if src == nil {
		t.Fatal("no subscription")
	}
	changed := fixtureProjection()
	changed.Panes[0].AgentStatus = "blocked"
	h.back.set(changed)
	src.events <- herdr.RuntimeEvent{Event: "pane.agent_status_changed"}
	msg := readJSON(t, conn)
	if msg["t"] != "tree.changed" || msg["revision"] != float64(2) {
		t.Fatalf("frame = %v", msg)
	}
}

func TestEventsResubscribeWhenThePaneSetChanges(t *testing.T) {
	h := newHarness(t)
	conn, _, err := h.dial("/api/v3/events", false)
	if err != nil {
		t.Fatal(err)
	}
	readJSON(t, conn)
	src := h.back.source(0)
	grown := fixtureProjection()
	grown.Panes = append(grown.Panes, herdr.Pane{ID: "w1:p3", TerminalID: "term_c", TabID: "w1:t1", ProjectID: "w1", Label: "new"})
	h.back.set(grown)
	src.events <- herdr.RuntimeEvent{Event: "pane.created"}
	if msg := readJSON(t, conn); msg["t"] != "tree.changed" {
		t.Fatalf("frame = %v", msg)
	}
	second := h.back.source(1)
	if second == nil {
		t.Fatal("a new pane needs a new subscription (agent_status is per pane)")
	}
	h.back.mu.Lock()
	got := h.back.subPanes[1]
	h.back.mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("resubscription panes = %v", got)
	}
	select {
	case <-src.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the old subscription must be closed")
	}
}

// --- terminal ---------------------------------------------------------------------

func TestTerminalForwardsFramesInOrderWithTheirHeaders(t *testing.T) {
	h := newHarness(t)
	h.back.observe = func(string, int, int) []string {
		return []string{"/bin/sh", "-c", fmt.Sprintf("echo '%s'; echo '%s'; exec sleep 30", frameLine("FULL", 1, true), frameLine("delta", 2, false))}
	}
	conn, _, err := h.dial("/api/v3/terminals/term_a/stream?cols=51&rows=19", true)
	if err != nil {
		t.Fatal(err)
	}
	head1 := readJSON(t, conn)
	if head1["t"] != "frame" || head1["seq"] != float64(1) || head1["full"] != true || head1["w"] != float64(51) || head1["h"] != float64(19) {
		t.Fatalf("header 1 = %v", head1)
	}
	if got := string(readBinary(t, conn)); got != "FULL" {
		t.Fatalf("frame 1 bytes = %q", got)
	}
	head2 := readJSON(t, conn)
	if head2["seq"] != float64(2) || head2["full"] != false {
		t.Fatalf("header 2 = %v", head2)
	}
	if got := string(readBinary(t, conn)); got != "delta" {
		t.Fatalf("frame 2 bytes = %q", got)
	}
	if g := h.back.observedGrids(); len(g) != 1 || g[0] != (grid{51, 19}) {
		t.Fatalf("observe grids = %v", g)
	}
}

func TestTerminalRefusesUnknownIDsAndBadGrids(t *testing.T) {
	h := newHarness(t)
	cases := map[string]int{
		"/api/v3/terminals/term_a/stream":                              http.StatusBadRequest,
		"/api/v3/terminals/term_a/stream?cols=5&rows=19":               http.StatusBadRequest,
		"/api/v3/terminals/term_a/stream?cols=51&rows=1":               http.StatusBadRequest,
		"/api/v3/terminals/term_a/stream?cols=9999&rows=19":            http.StatusBadRequest,
		"/api/v3/terminals/term_zzz/stream?cols=51&rows=19":            http.StatusNotFound,
		"/api/v3/terminals/--takeover/stream?cols=51&rows=19":          http.StatusNotFound,
		"/api/v3/terminals/term_a%20--takeover/stream?cols=51&rows=19": http.StatusNotFound,
		"/api/v3/terminals/term_a/stream?cols=51&rows=19&instance=x":   http.StatusNotFound,
	}
	for path, want := range cases {
		if got := h.get(path, h.token, nil).StatusCode; got != want {
			t.Errorf("%s = %d, want %d", path, got, want)
		}
	}
	if len(h.back.observedGrids()) != 0 {
		t.Fatal("a refused request must never build an observe command")
	}
}

func TestTerminalInputGoesThroughHerdrAndRejectsBadKeys(t *testing.T) {
	h := newHarness(t)
	conn, _, err := h.dial("/api/v3/terminals/term_b/stream?cols=51&rows=19", false)
	if err != nil {
		t.Fatal(err)
	}
	readJSON(t, conn)
	readBinary(t, conn)

	writeClient(t, conn, map[string]any{"t": "input", "text": "ls", "keys": []string{"enter"}})
	writeClient(t, conn, map[string]any{"t": "input", "keys": []string{"ctrl+c"}})
	waitFor(t, func() bool { h.back.mu.Lock(); defer h.back.mu.Unlock(); return len(h.back.inputs) == 2 })
	h.back.mu.Lock()
	in := append([]sentInput(nil), h.back.inputs...)
	h.back.mu.Unlock()
	if in[0].pane != "w1:p2" || in[0].text != "ls" || len(in[0].keys) != 1 || in[0].keys[0] != "enter" || in[1].keys[0] != "ctrl+c" {
		t.Fatalf("inputs = %+v (a terminal's input belongs to its own pane)", in)
	}

	for _, bad := range []map[string]any{
		{"t": "input", "keys": []string{"enter; rm -rf"}},
		{"t": "input", "text": "", "keys": []string{}},
		{"t": "input", "text": strings.Repeat("a", maxInputText+1)},
	} {
		writeClient(t, conn, bad)
		if msg := readJSON(t, conn); msg["t"] != "input.error" || msg["code"] != "bad_input" {
			t.Fatalf("bad input %v answered %v", bad, msg)
		}
	}
	h.back.mu.Lock()
	if len(h.back.inputs) != 2 {
		h.back.mu.Unlock()
		t.Fatal("refused input must never reach Herdr")
	}
	h.back.inputErr = errors.New("agent is busy")
	h.back.mu.Unlock()
	writeClient(t, conn, map[string]any{"t": "input", "text": "x"})
	if msg := readJSON(t, conn); msg["code"] != "refused" || !strings.Contains(fmt.Sprint(msg["message"]), "busy") {
		t.Fatalf("a Herdr refusal must come back as-is, got %v", msg)
	}
}

func TestTerminalResizeRestartsObserveOnTheNewGridAndReapsTheOldChild(t *testing.T) {
	h := newHarness(t)
	conn, _, err := h.dial("/api/v3/terminals/term_a/stream?cols=51&rows=19", false)
	if err != nil {
		t.Fatal(err)
	}
	readJSON(t, conn)
	if got := string(readBinary(t, conn)); got != "GRID51x19" {
		t.Fatalf("first grid = %q", got)
	}
	firstPID := lastPID(t, h.pidFile, 1)

	writeClient(t, conn, map[string]any{"t": "resize", "cols": 40, "rows": 10}) // keyboard animation: only the last counts
	writeClient(t, conn, map[string]any{"t": "resize", "cols": 60, "rows": 22})
	readJSON(t, conn)
	if got := string(readBinary(t, conn)); got != "GRID60x22" {
		t.Fatalf("after resize = %q, want GRID60x22", got)
	}
	if g := h.back.observedGrids(); len(g) != 2 || g[1] != (grid{60, 22}) {
		t.Fatalf("observe grids = %v: a resize burst must restart once", g)
	}
	waitFor(t, func() bool { return !alive(firstPID) })

	writeClient(t, conn, map[string]any{"t": "resize", "cols": 2, "rows": 2}) // invalid: ignored
	writeClient(t, conn, map[string]any{"t": "ping"})
	if msg := readJSON(t, conn); msg["t"] != "pong" {
		t.Fatalf("frame = %v", msg)
	}
	if len(h.back.observedGrids()) != 2 {
		t.Fatal("an invalid grid must not restart observe")
	}
}

func TestTerminalClosedByHerdrClosesTheSocketWithAReason(t *testing.T) {
	h := newHarness(t)
	h.back.observe = func(string, int, int) []string {
		return []string{"/bin/sh", "-c", `echo '{"type":"terminal.closed","reason":"terminal gone"}'`}
	}
	conn, _, err := h.dial("/api/v3/terminals/term_a/stream?cols=51&rows=19", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err = conn.Read(ctx)
	var ce websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != statusTerminalGone || ce.Reason != "terminal_closed" {
		t.Fatalf("close = %v, want code %d reason terminal_closed", err, statusTerminalGone)
	}
}

func TestTerminalPerDeviceCapAndRelease(t *testing.T) {
	h := newHarness(t)
	var conns []*websocket.Conn
	for i := 0; i < maxTerminalsPerDevice; i++ {
		c, _, err := h.dial("/api/v3/terminals/term_a/stream?cols=51&rows=19", false)
		if err != nil {
			t.Fatalf("stream %d: %v", i, err)
		}
		conns = append(conns, c)
	}
	if got := h.get("/api/v3/terminals/term_a/stream?cols=51&rows=19", h.token, nil).StatusCode; got != http.StatusTooManyRequests {
		t.Fatalf("one stream over the cap = %d, want 429", got)
	}
	_ = conns[0].Close(websocket.StatusNormalClosure, "")
	waitFor(t, func() bool {
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		return h.srv.terminals[h.devID] == 1
	})
	if _, _, err := h.dial("/api/v3/terminals/term_a/stream?cols=51&rows=19", false); err != nil {
		t.Fatalf("a freed slot must be reusable: %v", err)
	}
}

func TestRevokeClosesTheDevicesSocketsAndKillsItsChildren(t *testing.T) {
	h := newHarness(t)
	term, _, err := h.dial("/api/v3/terminals/term_a/stream?cols=51&rows=19", false)
	if err != nil {
		t.Fatal(err)
	}
	events, _, err := h.dial("/api/v3/events", false)
	if err != nil {
		t.Fatal(err)
	}
	readJSON(t, term)
	readBinary(t, term)
	readJSON(t, events)
	pid := lastPID(t, h.pidFile, 1)

	if err := h.srv.Devices().Revoke(h.devID); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*websocket.Conn{"terminal": term, "events": events} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _, err := c.Read(ctx)
		cancel()
		var ce websocket.CloseError
		if !errors.As(err, &ce) || ce.Code != websocket.StatusPolicyViolation {
			t.Errorf("%s socket after revoke: %v, want a policy-violation close", name, err)
		}
	}
	waitFor(t, func() bool { return !alive(pid) })
	if got := h.get("/api/v3/tree", h.token, nil).StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("a revoked token = %d, want 401", got)
	}
}

// --- helpers ----------------------------------------------------------------------

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}

// lastPID waits until the pid file holds at least n lines and returns the last.
func lastPID(t *testing.T, path string, n int) int {
	t.Helper()
	var pid int
	waitFor(t, func() bool {
		raw, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		lines := strings.Fields(string(raw))
		if len(lines) < n {
			return false
		}
		pid, _ = strconv.Atoi(lines[len(lines)-1])
		return pid > 0
	})
	return pid
}

func alive(pid int) bool {
	// A reaped child may linger as a zombie until Wait returns; signal 0 still
	// succeeds on a zombie, so also treat "no such process" as gone.
	return syscall.Kill(pid, 0) == nil
}

func TestDeviceOnlineFollowsItsLiveSockets(t *testing.T) {
	h := newHarness(t)
	if h.srv.DeviceOnline(h.devID) {
		t.Fatal("a device with no socket is not online")
	}
	untrack := h.srv.trackConn(h.devID, func() {})
	if !h.srv.DeviceOnline(h.devID) {
		t.Fatal("a device holding a socket is online")
	}
	untrack()
	if h.srv.DeviceOnline(h.devID) {
		t.Fatal("the device goes offline when its last socket ends")
	}
}
