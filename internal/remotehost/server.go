package remotehost

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: 依赖 Config/RelayConfig/DeviceStore/Pairing/LoadOrCreateCert、Backend（Herdr 缝）、net/http
 * [OUTPUT]: 对外提供 Server（New/Start/Reconfigure/Close/Handler/StartPairing/Devices/RelayStatus）、PairingOffer、Scope 与统一 authorize 入口
 * [POS]: Remote Host 的监听器生命周期与 HTTP 面：/api/v3 的 pair（免鉴权）、bootstrap、tree；其余端点（events/terminals/peek）在 events.go/terminal.go/peek.go；所有受保护端点都过 guard → authorize（Bearer 头、['solco.bearer', token] 子协议、或 wopen 无子协议场景的 ?token= query），撤销即关闭该设备的所有 WS；relay 隧道（relayclient.go）由 Start/Reconfigure/Close 管理生命周期，仅当 Relay.Enabled 且监听器已启动时运行，StartPairing 在 relay 开启时给 QR 追加 &r=&rt=
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Scope is a permission bit. V1 grants every paired device all of them; the
// bits exist so endpoints already ask for the narrowest one (raw terminal
// input is more powerful than reading the tree).
type Scope uint8

const (
	ScopeView Scope = 1 << iota
	ScopeTerminalInput
	ScopeManage
	scopeOwner = ScopeView | ScopeTerminalInput | ScopeManage
)

// subprotocolBearer is the WebSocket subprotocol that carries the token
// (browsers and React Native cannot set an Authorization header on a WS).
const subprotocolBearer = "solco.bearer"

const apiPrefix = "/api/v3"

// Options builds a Server.
type Options struct {
	Config  Config
	Dir     string // <userDataDir>/RemoteHost
	Backend Backend
	// Relay is the opt-in route through a self-hosted alma-relay for when no
	// direct network path exists; it dials out, so it needs no extra port and
	// its loopback bridge is an implementation detail (relayclient.go).
	Relay RelayConfig
	// Identity supplies a session identity Herdr's agent list lacks (Claude
	// Code and Codex report none; the desktop learns it from its own hooks).
	// It must match provider and terminal itself; nil disables the fallback.
	Identity func(instance, paneID, terminalID, kind string) *herdr.AgentSessionIdentity
	// AgentFacts supplies the desktop's resolved title and last-worked time per agent terminal for the
	// Herdr session it shows (nil map for any other); nil = the tree carries provider names only.
	AgentFacts func(instance string) map[string]AgentFact
	// Launcher starts new sessions for the phone (POST /launch); nil = the Host
	// offers no launch and bootstrap says so.
	Launcher Launcher
	// Queue is the desktop's follow-up queue (POST .../chat/send {queue}, .../chat/queue/cancel, .../chat/queue/item/{requestID}/cancel); nil = no queueing.
	Queue FollowUps
	// Git reads a Workspace's working-tree changes for the phone (GET .../git); nil = no Git view.
	Git GitReader
	// Usage serves the desktop's quota page for the phone (GET /quota); nil = the
	// Host offers no quota view and bootstrap says so. It runs on the Host's
	// goroutines and answers every provider's standing in the page's order.
	Usage func(ctx context.Context, force bool) Quota
	// Catalog lists a pane agent's own Skills, commands and models for the composer; nil = agentcatalog.Load.
	Catalog catalogLoader
	// Attach stores an uploaded Chat attachment and returns its absolute path (POST .../chat/attachments);
	// nil = the Host takes no uploads and bootstrap says so.
	Attach   func(data []byte, mime, name string) (string, error)
	Home     string // user home for provider transcript roots ("" = os.UserHomeDir)
	HostName string
	Version  string
	Now      func() time.Time
}

// PairingOffer is what Settings shows as a QR code.
type PairingOffer struct {
	URL         string
	Code        string
	ExpiresAt   time.Time
	Fingerprint string
}

// Server is the optional phone-client Host. The zero Config mode is off: New
// succeeds, Start does nothing, no port is opened.
type Server struct {
	opts    Options
	devices *DeviceStore
	pairing *Pairing

	mu          sync.Mutex
	httpServer  *http.Server
	listenHost  string
	listenPort  string
	fingerprint string
	conns       map[string]map[*liveConn]struct{} // deviceID → live sockets
	terminals   map[string]int                    // deviceID → open terminal streams
	chats       map[string]int                    // deviceID → open chat streams
	asks        map[string]*askCursor             // instance\x00pane → Question state
	trees       map[string]*treeState
	peeks       peekCache
	poke        chan struct{} // closed and replaced by Poke; every events pump waits on the current one

	relay       *relayClient // non-nil while the relay tunnel is running
	relayStatus RelayStatus  // what Settings shows; written by startRelay and the client
	secretCache string       // cached relay connect code, loaded once from Dir
	relayHooks  *relayTestHooks
	p2p         p2pState // WebRTC direct path (p2p.go)
}

// liveConn is one open WebSocket as the server tracks it. close sends a close
// frame (the handshake waits for the phone, so callers run it in a goroutine)
// and then cancels the connection's work, killing any observe child. The close
// frame must precede the cancel: cancelling a context mid-I/O makes the
// WebSocket library drop the connection without one.
type liveConn struct{ close func() }

// New opens the device store and builds the Server; it opens no socket.
func New(opts Options) (*Server, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	devices, err := OpenDeviceStore(opts.Dir, opts.Now)
	if err != nil {
		return nil, err
	}
	s := &Server{
		opts: opts, devices: devices, pairing: NewPairing(opts.Now),
		conns: map[string]map[*liveConn]struct{}{}, terminals: map[string]int{}, trees: map[string]*treeState{},
		poke: make(chan struct{}),
	}
	devices.OnRevoke(s.closeDevice)
	return s, nil
}

// Devices exposes the paired-device list for Settings (List/Revoke).
func (s *Server) Devices() *DeviceStore { return s.devices }

// Start binds the configured address with TLS. Off is a no-op. It never binds
// a wildcard address (Config.ListenAddr guarantees that).
func (s *Server) Start() error {
	cfg := s.config()
	addr, err := cfg.ListenAddr()
	if err != nil {
		return err
	}
	if addr == "" {
		return nil
	}
	ln, err := listenReliable(addr)
	if err != nil {
		return fmt.Errorf("remote host listen on %s: %w", addr, err)
	}
	if err := s.serve(ln, cfg); err != nil {
		_ = ln.Close()
		return err
	}
	return nil
}

// listenReliable binds addr, re-trying briefly while the address winds down:
// rebinding the very port that was just closed can fail with EADDRINUSE for
// a few tens of milliseconds on darwin (Reconfigure rebinds the same listener
// whenever the user flips any Remote Access switch). A port genuinely held by
// another process stays refused — the retry never widens the exposure.
func listenReliable(addr string) (net.Listener, error) {
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil || !errors.Is(err, syscall.EADDRINUSE) || time.Now().After(deadline) {
			return ln, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *Server) config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opts.Config
}

// ListenAddress is the host:port currently served ("" while stopped).
func (s *Server) ListenAddress() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.httpServer == nil {
		return ""
	}
	return net.JoinHostPort(s.listenHost, s.listenPort)
}

// Reconfigure applies new listener and relay choices (Settings changed the
// mode, address, port or relay): every live socket, the listener and the
// relay tunnel go first, then the new address binds and the tunnel redials.
// A pairing code is dropped because its QR carries the old address. If the
// new config is invalid or the bind fails, the Host stays stopped — it never
// falls back to a wider address.
func (s *Server) Reconfigure(cfg Config, relay RelayConfig) error {
	s.Close()
	s.pairing.Cancel()
	s.mu.Lock()
	s.opts.Config = cfg
	s.opts.Relay = relay
	s.mu.Unlock()
	return s.Start()
}

func (s *Server) serve(ln net.Listener, cfg Config) error {
	cert, fp, err := LoadOrCreateCert(s.opts.Dir, s.opts.Now())
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13},
	}
	s.mu.Lock()
	if s.httpServer != nil {
		s.mu.Unlock()
		return errors.New("remote host already started")
	}
	s.httpServer, s.listenHost, s.listenPort, s.fingerprint = srv, host, port, fp
	s.mu.Unlock()
	go func() {
		if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("remote host stopped", "error", err)
		}
	}()
	slog.Info("remote host listening", "mode", cfg.Mode, "port", port)
	s.startRelay() // tunnels through its own loopback bridge; needs a running Host
	return nil
}

// startRelay brings the relay tunnel up when the user enabled it. It runs
// only on a started Host: the tunnel bridges into Handler(), but pairing and
// the Settings status assume the listener is up. A broken relay config or
// secret leaves the Host serving and just puts the relay in the error state.
func (s *Server) startRelay() {
	rc := s.relayConfig()
	if !rc.Enabled {
		return
	}
	if err := rc.Validate(); err != nil {
		s.setRelayStatus(RelayStatus{State: RelayError, Message: err.Error()})
		slog.Warn("remote host relay not started", "error", err)
		return
	}
	secret, err := s.relaySecret()
	if err != nil {
		s.setRelayStatus(RelayStatus{State: RelayError, Message: err.Error()})
		slog.Warn("remote host relay not started", "error", err)
		return
	}
	c := newRelayClient(s, rc, secret)
	s.mu.Lock()
	s.relay = c
	s.mu.Unlock()
	c.start()
}

func (s *Server) stopRelay() {
	s.mu.Lock()
	c := s.relay
	s.relay = nil
	s.mu.Unlock()
	if c != nil {
		c.stop()
	}
	s.setRelayStatus(RelayStatus{})
}

// RelayStatus is what Settings shows about the relay tunnel (idle while the
// relay is off or the Host is stopped).
func (s *Server) RelayStatus() RelayStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.relayStatus
}

func (s *Server) setRelayStatus(st RelayStatus) {
	s.mu.Lock()
	s.relayStatus = st
	s.mu.Unlock()
}

func (s *Server) relayConfig() RelayConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opts.Relay
}

// relaySecret loads the Mac's relay connect code once and caches it.
func (s *Server) relaySecret() (string, error) {
	s.mu.Lock()
	cached := s.secretCache
	s.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	secret, err := LoadOrCreateRelaySecret(s.opts.Dir)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.secretCache == "" {
		s.secretCache = secret
	}
	s.mu.Unlock()
	return secret, nil
}

// Close stops listening and drops every live socket. The relay tunnel goes
// first: it dials home and bridges into the handler that is about to die.
func (s *Server) Close() {
	s.stopRelay()
	s.closeP2P()
	s.mu.Lock()
	srv := s.httpServer
	s.httpServer = nil
	s.listenHost, s.listenPort, s.fingerprint = "", "", "" // nothing to pair against once stopped
	var handles []*liveConn
	for _, set := range s.conns {
		for h := range set {
			handles = append(handles, h)
		}
	}
	s.mu.Unlock()
	for _, h := range handles {
		go h.close()
	}
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = srv.Close()
	}
}

// StartPairing begins a six-minute single-use pairing and returns the QR
// payload. It needs a started listener: the QR carries its address and pin.
func (s *Server) StartPairing() (PairingOffer, error) {
	s.mu.Lock()
	host, port, fp := s.listenHost, s.listenPort, s.fingerprint
	s.mu.Unlock()
	if fp == "" {
		return PairingOffer{}, errors.New("remote access is off")
	}
	code, expires, err := s.pairing.Start()
	if err != nil {
		return PairingOffer{}, err
	}
	q := url.Values{}
	q.Set("h", host)
	q.Set("p", port)
	q.Set("fp", fp)
	q.Set("c", code)
	q.Set("n", s.opts.HostName)
	// Phones can also reach this Mac through the user's relay; the offer
	// carries the relay base and this Mac's secret so the phone may use
	// either path. The one-time pairing code stays single-use either way.
	if rc := s.relayConfig(); rc.Enabled {
		if secret, err := s.relaySecret(); err == nil {
			q.Set("r", strings.TrimSuffix(rc.URL, "/"))
			q.Set("rt", secret)
		} else {
			slog.Warn("remote host pairing: relay secret unavailable", "error", err)
		}
	}
	return PairingOffer{URL: "solco://pair?" + q.Encode(), Code: code, ExpiresAt: expires, Fingerprint: fp}, nil
}

// CancelPairing drops the active code (the pairing sheet closed).
func (s *Server) CancelPairing() { s.pairing.Cancel() }

// Handler is the whole HTTP surface (also used directly by tests).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+apiPrefix+"/pair", s.handlePair)
	mux.Handle("GET "+apiPrefix+"/bootstrap", s.guard(ScopeView, s.handleBootstrap))
	mux.Handle("GET "+apiPrefix+"/tree", s.guard(ScopeView, s.handleTree))
	mux.Handle("GET "+apiPrefix+"/events", s.guard(ScopeView, s.handleEvents))
	mux.Handle("GET "+apiPrefix+"/panes/{paneID}/peek", s.guard(ScopeView, s.handlePeek))
	mux.Handle("GET "+apiPrefix+"/panes/{paneID}/chat", s.guard(ScopeView, s.handleChat))
	mux.Handle("GET "+apiPrefix+"/panes/{paneID}/chat/catalog", s.guard(ScopeView, s.handleChatCatalog))
	mux.Handle("GET "+apiPrefix+"/panes/{paneID}/chat/files", s.guard(ScopeView, s.handleChatFiles))
	mux.Handle("GET "+apiPrefix+"/panes/{paneID}/chat/subagents/{subID}", s.guard(ScopeView, s.handleChatSubagent))
	mux.Handle("GET "+apiPrefix+"/panes/{paneID}/chat/stream", s.guard(ScopeView, s.handleChatStream))
	mux.Handle("POST "+apiPrefix+"/panes/{paneID}/chat/send", s.guard(ScopeTerminalInput, s.handleChatSend))
	mux.Handle("POST "+apiPrefix+"/panes/{paneID}/chat/queue/cancel", s.guard(ScopeTerminalInput, s.handleChatQueueCancel))
	mux.Handle("POST "+apiPrefix+"/panes/{paneID}/chat/queue/item/{requestID}/cancel", s.guard(ScopeTerminalInput, s.handleChatQueueItemCancel))
	mux.Handle("POST "+apiPrefix+"/panes/{paneID}/chat/attachments", s.guard(ScopeTerminalInput, s.handleChatAttach))
	mux.Handle("POST "+apiPrefix+"/panes/{paneID}/chat/stop", s.guard(ScopeTerminalInput, s.handleChatStop))
	mux.Handle("POST "+apiPrefix+"/panes/{paneID}/chat/answer", s.guard(ScopeTerminalInput, s.handleChatAnswer))
	mux.Handle("POST "+apiPrefix+"/launch", s.guard(ScopeManage, s.handleLaunch))
	mux.Handle("GET "+apiPrefix+"/workspaces/{id}/git", s.guard(ScopeView, s.handleGitStatus))
	mux.Handle("GET "+apiPrefix+"/workspaces/{id}/git/diff", s.guard(ScopeView, s.handleGitDiff))
	mux.Handle("GET "+apiPrefix+"/quota", s.guard(ScopeView, s.handleQuota))
	mux.Handle("GET "+apiPrefix+"/p2p/ice", s.guard(ScopeView, s.handleP2PICE))
	mux.Handle("POST "+apiPrefix+"/p2p/session", s.guard(ScopeView, s.handleP2PSession))
	mux.Handle("GET "+apiPrefix+"/terminals/{terminalID}/stream", s.guard(ScopeView, s.handleTerminal))
	return mux
}

// --- authorization ------------------------------------------------------

// authorize is the one place a request becomes a Device with scopes.
func (s *Server) authorize(r *http.Request, need Scope) (Device, int) {
	token := bearerToken(r)
	if token == "" {
		return Device{}, http.StatusUnauthorized
	}
	dev, ok := s.devices.Authenticate(token)
	if !ok {
		return Device{}, http.StatusUnauthorized
	}
	if scopesFor(dev)&need != need {
		return Device{}, http.StatusForbidden
	}
	return dev, 0
}

func scopesFor(Device) Scope { return scopeOwner }

func (s *Server) guard(need Scope, h func(http.ResponseWriter, *http.Request, Device)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dev, status := s.authorize(r, need)
		if status != 0 {
			writeError(w, status, map[int]string{401: "unauthorized", 403: "forbidden"}[status], "")
			return
		}
		h(w, r, dev)
	})
}

// bearerToken reads the Authorization header or, for WebSockets, the
// ["solco.bearer", token] subprotocol pair. A third channel — the same
// ?token= query parameter /chat/files already uses for its search term —
// covers sockets that arrive with neither headers nor subprotocols: relayed
// WebSocket connections (the relay's wopen carries only a path). Query
// values must never reach a log.
func bearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if scheme, tok, ok := strings.Cut(h, " "); ok && strings.EqualFold(scheme, "Bearer") {
			return strings.TrimSpace(tok)
		}
		return ""
	}
	var protos []string
	for _, line := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(line, ",") {
			protos = append(protos, strings.TrimSpace(p))
		}
	}
	if i := slices.Index(protos, subprotocolBearer); i >= 0 && i+1 < len(protos) {
		return protos[i+1]
	}
	if tok := r.URL.Query().Get("token"); tok != "" {
		return tok
	}
	return ""
}

// trackConn registers a live socket for a device so Revoke can end it.
func (s *Server) trackConn(deviceID string, closeFn func()) (untrack func()) {
	h := &liveConn{close: closeFn}
	s.mu.Lock()
	if s.conns[deviceID] == nil {
		s.conns[deviceID] = map[*liveConn]struct{}{}
	}
	s.conns[deviceID][h] = struct{}{}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.conns[deviceID], h)
		if len(s.conns[deviceID]) == 0 {
			delete(s.conns, deviceID)
		}
		s.mu.Unlock()
	}
}

// DeviceOnline reports whether the device holds a live socket right now.
func (s *Server) DeviceOnline(deviceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns[deviceID]) > 0
}

// closeDevice ends every socket of a revoked device without blocking the
// caller (Settings' Revoke) on a phone's close handshake.
func (s *Server) closeDevice(deviceID string) {
	s.mu.Lock()
	var handles []*liveConn
	for h := range s.conns[deviceID] {
		handles = append(handles, h)
	}
	s.mu.Unlock()
	for _, h := range handles {
		go h.close()
	}
}

// --- handlers -------------------------------------------------------------

type pairRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"deviceName"`
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var req pairRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if !s.pairing.Consume(req.Code) {
		writeError(w, http.StatusUnauthorized, "pairing_refused", "")
		return
	}
	dev, token, err := s.devices.Add(req.DeviceName)
	if err != nil {
		slog.Warn("remote host pairing failed to save device", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "")
		return
	}
	slog.Info("remote host device paired", "device", dev.ID)
	writeJSON(w, http.StatusOK, map[string]string{"deviceId": dev.ID, "token": token})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request, _ Device) {
	names, err := s.opts.Backend.Instances()
	if err != nil {
		slog.Warn("remote host bootstrap: list instances failed", "error", err)
		writeError(w, http.StatusBadGateway, "backend_unavailable", "")
		return
	}
	type instance struct {
		ID string `json:"id"`
	}
	instances := []instance{}
	for _, n := range names {
		instances = append(instances, instance{ID: n})
	}
	s.mu.Lock()
	fp := s.fingerprint
	s.mu.Unlock()
	hostID := fp
	if len(hostID) > 16 {
		hostID = hostID[:16]
	}
	capabilities := map[string]bool{"terminal": true, "chat": true}
	body := map[string]any{
		"hostId":       hostID,
		"hostName":     s.opts.HostName,
		"version":      s.opts.Version,
		"capabilities": capabilities,
		"instances":    instances,
	}
	if s.opts.Launcher != nil {
		capabilities["launch"] = true
		body["launchKinds"] = s.opts.Launcher.Kinds()
	}
	if s.opts.Usage != nil {
		capabilities["quota"] = true
	}
	if s.opts.Git != nil {
		capabilities["git"] = true
	}
	if s.opts.Attach != nil {
		capabilities["attachments"] = true
	}
	if s.opts.Queue != nil {
		capabilities["queue"] = true
	}
	if s.p2pConfig().Enabled {
		capabilities["p2p"] = true
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request, _ Device) {
	instance, ok := s.resolveInstance(w, r)
	if !ok {
		return
	}
	tree, err := s.currentTree(instance)
	if err != nil {
		slog.Warn("remote host tree failed", "instance", instance, "error", err)
		writeError(w, http.StatusBadGateway, "backend_unavailable", "")
		return
	}
	etag := `"` + strconv.FormatUint(tree.Revision, 10) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

// resolveInstance validates ?instance= against the backend's local sessions;
// an unknown string never reaches a socket path.
func (s *Server) resolveInstance(w http.ResponseWriter, r *http.Request) (string, bool) {
	names, err := s.opts.Backend.Instances()
	if err != nil {
		slog.Warn("remote host: list instances failed", "error", err)
		writeError(w, http.StatusBadGateway, "backend_unavailable", "")
		return "", false
	}
	want := r.URL.Query().Get("instance")
	if want == "" {
		want = "default"
	}
	if !slices.Contains(names, want) {
		writeError(w, http.StatusNotFound, "unknown_instance", "")
		return "", false
	}
	return want, true
}

// --- tree revisions -------------------------------------------------------

type treeState struct {
	mu       sync.Mutex
	revision uint64
	hash     string
}

// currentTree reads the projection and numbers it: the revision only moves
// when the content does.
func (s *Server) currentTree(instance string) (Tree, error) {
	p, err := s.opts.Backend.Projection(instance)
	if err != nil {
		return Tree{}, err
	}
	var facts map[string]AgentFact
	if s.opts.AgentFacts != nil {
		facts = s.opts.AgentFacts(instance)
	}
	tree := buildTree(instance, p, facts)
	h := tree.hash()
	s.mu.Lock()
	st := s.trees[instance]
	if st == nil {
		st = &treeState{}
		s.trees[instance] = st
	}
	s.mu.Unlock()
	st.mu.Lock()
	defer st.mu.Unlock()
	if h != st.hash {
		st.hash = h
		st.revision++
	}
	tree.Revision = st.revision
	return tree, nil
}

// --- helpers --------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	body := map[string]string{"code": code}
	if msg != "" {
		body["message"] = msg
	}
	writeJSON(w, status, map[string]any{"error": body})
}

// Poke tells every open events socket to re-read its tree now. Facts the Host
// does not learn from Herdr (the desktop's resolved Agent titles) change
// without any Herdr event; the owner of those facts pokes when they move.
func (s *Server) Poke() {
	s.mu.Lock()
	close(s.poke)
	s.poke = make(chan struct{})
	s.mu.Unlock()
}

func (s *Server) pokeChannel() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.poke
}
