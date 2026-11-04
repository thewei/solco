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
 * [OUTPUT]: 对外提供 Server（New/Start/Reconfigure/Close/Handler/StartPairing/Devices/RelayStatus）、Options.OnAddressLost、PairingOffer、Scope 与统一 authorize 入口
 * [POS]: Remote Host 的监听器生命周期与 HTTP 面：/api/v3 的 pair（免鉴权）、bootstrap（lan/tailscale 监听中时带 net.addresses＝当前正在服务的那个地址；loopback/绑定失败不带——不真实 serving 的地址绝不下发）、tree；其余端点（events/terminals/peek）在 events.go/terminal.go/peek.go；所有受保护端点都过 guard → authorize（Bearer 头、['solco.bearer', token] 子协议、或 wopen 无子协议场景的 ?token= query），撤销即关闭该设备的所有 WS；relay 隧道（relayclient.go）由 Start/Reconfigure/Close 管理生命周期，仅当 Relay.Enabled 且监听器已启动时运行，StartPairing 在 relay 开启时给 QR 追加 &r=&rt=；绑定失败仍拉 relay 与地址看门狗（watch.go：只报告，换址/重绑归回调方）
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
	// ScopeRemoteDesktop sees and drives the whole Mac desktop — the most powerful bit, asked for by nothing else.
	ScopeRemoteDesktop
	scopeOwner = ScopeView | ScopeTerminalInput | ScopeManage | ScopeRemoteDesktop
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
	// OnAddressLost is called on its own goroutine (one per report, watch.go) when
	// the configured lan/tailscale address is no longer on the machine's
	// interfaces (DHCP, a changed network) while a usable one exists. The
	// callback owns the fix — it re-picks and rebinds through the settings path
	// (Reconfigure); the server never switches an address on its own.
	// Nil = the address is never re-checked.
	OnAddressLost func()
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
	// GitWrite stages, unstages, commits, fetches, pulls and pushes for the phone (POST .../git/*, scope manage); nil = the Git view stays read-only.
	GitWrite GitWriter
	// Files serves each Workspace's folder read-only (GET .../files, .../files/content); false = no Files view.
	Files bool
	// Usage serves the desktop's quota page for the phone (GET /quota); nil = the
	// Host offers no quota view and bootstrap says so. It runs on the Host's
	// goroutines and answers every provider's standing in the page's order.
	Usage func(ctx context.Context, force bool) Quota
	// Automations serves the desktop's Automations page for the phone (GET
	// /automations, read-only): nil, or an answer with ok=false (the plugin gate
	// is closed), means no such view and bootstrap does not advertise it. It
	// runs on the Host's goroutines.
	Automations AutomationsFunc
	// AutomationsAdmin lets the phone manage the Mac's automations (detail,
	// draft/edit, approve, run now, pause/resume, delete, scheduler, seen):
	// nil = read-only list, and bootstrap does not advertise automationsManage.
	AutomationsAdmin AutomationsAdmin
	// PersonalAgent hands the Assistants routes (/personal-agent/…) the desktop's
	// CLI client to the Go core: nil, or ok=false (gate closed), means no such
	// surface and bootstrap does not advertise personalAgent. Runs on the Host's goroutines.
	PersonalAgent PersonalAgentFunc
	// AssistantAgents lists the Agents a Coordinator may run on (the desktop's
	// own chat-capable, switched-on kinds, last used first). nil = none offered:
	// the phone can then start a Coordinator only on the plugin's default.
	AssistantAgents func() []PersonalAgentKind
	// Sessions serves the desktop's session catalog for the phone (GET /sessions):
	// metadata, search and the running-pane link; nil = no such view and bootstrap
	// says so. It runs on the Host's goroutines.
	Sessions SessionsFunc
	// Continuer continues one cataloged session (POST /sessions/{id}/continue, scope
	// manage); nil = the phone gets no Continue and bootstrap says so.
	Continuer Continuer
	// Trasher moves a session into the desktop's Sessions Trash and puts one back
	// (POST /sessions/{id}/trash, GET /sessions/trash, POST /sessions/trash/{entry}/restore);
	// nil = the phone has no Trash and bootstrap says so. Permanent deletion is not on it.
	Trasher Trasher
	// Voice transcribes a phone-recorded clip with the desktop's Voice source (POST /voice/transcribe,
	// scope terminal-input); nil or not Ready = no dictation on the phone and bootstrap says so.
	Voice VoiceTranscriber
	// Catalog lists a pane agent's own Skills, commands and models for the composer; nil = agentcatalog.Load.
	Catalog catalogLoader
	// Attach stores an uploaded Chat attachment and returns its absolute path (POST .../chat/attachments);
	// nil = the Host takes no uploads and bootstrap says so.
	Attach func(data []byte, mime, name string) (string, error)
	// Machines lists and connects the desktop's saved Herdr machines as phone "devices" (POST /devices/{id}/connect,
	// scope manage); nil = the phone sees no devices.
	Machines Machines
	// RemoteDesktop lets the phone watch and control the Mac's whole desktop (GET /remote-desktop, POST /remote-desktop/session,
	// scope remote-desktop); nil or switched off in Settings = no entry on the phone and bootstrap says so.
	RemoteDesktop RemoteDesktop
	Home          string // user home for provider transcript roots ("" = os.UserHomeDir)
	HostName      string
	Version       string
	Now           func() time.Time
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

	mu             sync.Mutex
	httpServer     *http.Server
	listenHost     string
	listenPort     string
	fingerprint    string
	conns          map[string]map[*liveConn]struct{} // deviceID → live sockets
	terminals      map[string]int                    // deviceID → open terminal streams
	chats          map[string][]*chatSlot            // deviceID → open chat streams, oldest first
	chatStreamFeed companionStreamFeed               // bounded read-only OMP extension overlay; independent of Herdr state
	windows        contextWindowCache                // context windows of open chats (60 s)
	asks           map[string]*askCursor             // instance\x00pane → Question state
	trees          map[string]*treeState
	peeks          peekCache
	poke           chan struct{}                    // closed and replaced by Poke; every events pump waits on the current one
	paRevision     uint64                           // Assistants change counter (NotifyPersonalAgent)
	notices        map[chan map[string]any]struct{} // open events sockets' queues for one-off frames (NotifyAutomation)
	netStop        chan struct{}                    // closed by Close; stops the address watcher (watch.go)
	netDone        chan struct{}                    // closed by the watcher goroutine on exit; Close joins it

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
// a wildcard address (Config.ListenAddr guarantees that). A bind that fails still
// brings the relay tunnel up when it is enabled (see below) and starts the
// address watcher (watch.go), which reports the vanished address so Settings can
// follow the Mac to its new one.
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
		// The address can vanish under a running Mac (DHCP, a changed network, Wi-Fi off). The relay does not
		// need it: it dials out and bridges into Handler() through its own 127.0.0.1 loopback, so a phone that
		// was paired through it keeps reaching the Mac. The error still goes up — Settings flags the stale
		// address — and pairing stays refused (no listener, no QR) until the watcher has found the new one.
		s.startRelay()
		s.watchAddresses()
		return fmt.Errorf("remote host listen on %s: %w", addr, err)
	}
	if err := s.serve(ln, cfg); err != nil {
		_ = ln.Close()
		return err
	}
	s.watchAddresses()
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
	if s.netStop != nil {
		close(s.netStop)
		s.netStop = nil
	}
	done := s.netDone
	s.netDone = nil
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
	if done != nil {
		<-done // the watcher stops touching the interfaces before Close returns
	}
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
	mux.Handle("POST "+apiPrefix+"/panes/{paneID}/chat/queue/item/{requestID}/edit", s.guard(ScopeTerminalInput, s.handleChatQueueItemEdit))
	mux.Handle("POST "+apiPrefix+"/panes/{paneID}/chat/queue/item/{requestID}/move", s.guard(ScopeTerminalInput, s.handleChatQueueItemMove))
	mux.Handle("POST "+apiPrefix+"/panes/{paneID}/chat/attachments", s.guard(ScopeTerminalInput, s.handleChatAttach))
	mux.Handle("POST "+apiPrefix+"/panes/{paneID}/chat/stop", s.guard(ScopeTerminalInput, s.handleChatStop))
	mux.Handle("POST "+apiPrefix+"/panes/{paneID}/chat/answer", s.guard(ScopeTerminalInput, s.handleChatAnswer))
	mux.Handle("POST "+apiPrefix+"/launch", s.guard(ScopeManage, s.handleLaunch))
	mux.Handle("GET "+apiPrefix+"/launch/catalog", s.guard(ScopeView, s.handleDraftCatalog))
	mux.Handle("GET "+apiPrefix+"/launch/files", s.guard(ScopeView, s.handleDraftFiles))
	mux.Handle("POST "+apiPrefix+"/launch/attachments", s.guard(ScopeTerminalInput, s.handleDraftAttach))
	mux.Handle("GET "+apiPrefix+"/workspaces/{id}/git", s.guard(ScopeView, s.handleGitStatus))
	mux.Handle("GET "+apiPrefix+"/workspaces/{id}/git/diff", s.guard(ScopeView, s.handleGitDiff))
	mux.Handle("POST "+apiPrefix+"/workspaces/{id}/git/stage", s.guard(ScopeManage, s.handleGitPaths("stage")))
	mux.Handle("POST "+apiPrefix+"/workspaces/{id}/git/unstage", s.guard(ScopeManage, s.handleGitPaths("unstage")))
	mux.Handle("POST "+apiPrefix+"/workspaces/{id}/git/commit", s.guard(ScopeManage, s.handleGitCommit))
	mux.Handle("POST "+apiPrefix+"/workspaces/{id}/git/fetch", s.guard(ScopeManage, s.handleGitSync(GitFetch)))
	mux.Handle("POST "+apiPrefix+"/workspaces/{id}/git/pull", s.guard(ScopeManage, s.handleGitSync(GitPull)))
	mux.Handle("POST "+apiPrefix+"/workspaces/{id}/git/push", s.guard(ScopeManage, s.handleGitSync(GitPush)))
	mux.Handle("GET "+apiPrefix+"/workspaces/{id}/files", s.guard(ScopeView, s.handleFilesList))
	mux.Handle("GET "+apiPrefix+"/workspaces/{id}/files/content", s.guard(ScopeView, s.handleFileContent))
	mux.Handle("POST "+apiPrefix+"/devices/{id}/connect", s.guard(ScopeManage, s.handleDeviceConnect))
	mux.Handle("GET "+apiPrefix+"/quota", s.guard(ScopeView, s.handleQuota))
	mux.Handle("GET "+apiPrefix+"/automations", s.guard(ScopeView, s.handleAutomations))
	mux.Handle("GET "+apiPrefix+"/automations/{name}", s.guard(ScopeView, s.handleAutomationDetail))
	mux.Handle("POST "+apiPrefix+"/automations", s.guard(ScopeManage, s.handleAutomationCreate))
	mux.Handle("POST "+apiPrefix+"/automations/{name}/save", s.guard(ScopeManage, s.handleAutomationUpdate))
	for _, op := range []AutomationOp{OpApproveAutomation, OpRunAutomation, OpPauseAutomation, OpResumeAutomation, OpDeleteAutomation} {
		mux.Handle("POST "+apiPrefix+"/automations/{name}/"+string(op), s.guard(ScopeManage, s.handleAutomationOp(op)))
	}
	mux.Handle("POST "+apiPrefix+"/automations/scheduler/start", s.guard(ScopeManage, s.handleAutomationScheduler))
	mux.Handle("POST "+apiPrefix+"/automations/seen", s.guard(ScopeManage, s.handleAutomationSeen))
	pa := apiPrefix + "/personal-agent"
	mux.Handle("GET "+pa+"/overview", s.guard(ScopeView, s.handlePAOverview))
	mux.Handle("GET "+pa+"/assistants", s.guard(ScopeView, s.handlePAAssistants))
	mux.Handle("GET "+pa+"/assistants/{id}", s.guard(ScopeView, s.handlePAAssistant))
	mux.Handle("GET "+pa+"/goals", s.guard(ScopeView, s.handlePAGoals))
	mux.Handle("GET "+pa+"/tasks/{id}", s.guard(ScopeView, s.handlePATask))
	mux.Handle("GET "+pa+"/approvals", s.guard(ScopeView, s.handlePAApprovals))
	mux.Handle("GET "+pa+"/inbox", s.guard(ScopeView, s.handlePAInbox))
	mux.Handle("POST "+pa+"/assistants", s.guard(ScopeManage, s.handlePACreateAssistant))
	mux.Handle("POST "+pa+"/assistants/{id}/rename", s.guard(ScopeManage, s.handlePARenameAssistant))
	mux.Handle("POST "+pa+"/assistants/{id}/pause", s.guard(ScopeManage, s.handlePAAssistantEnabled(false)))
	mux.Handle("POST "+pa+"/assistants/{id}/resume", s.guard(ScopeManage, s.handlePAAssistantEnabled(true)))
	mux.Handle("POST "+pa+"/assistants/{id}/coordinator/start", s.guard(ScopeManage, s.handlePAStartCoordinator))
	mux.Handle("POST "+pa+"/goals/draft", s.guard(ScopeManage, s.handlePADraftGoal))
	for _, action := range []string{"approve", "pause", "resume", "cancel"} {
		mux.Handle("POST "+pa+"/goals/{id}/"+action, s.guard(ScopeManage, s.handlePAGoalAction(action)))
	}
	mux.Handle("GET "+pa+"/groups", s.guard(ScopeView, s.handlePAGroups))
	mux.Handle("GET "+pa+"/groups/{id}", s.guard(ScopeView, s.handlePAGroupFeed))
	mux.Handle("POST "+pa+"/groups", s.guard(ScopeManage, s.handlePACreateGroup))
	mux.Handle("POST "+pa+"/groups/{id}/messages", s.guard(ScopeManage, s.handlePAGroupMessage))
	mux.Handle("POST "+pa+"/groups/{id}/rename", s.guard(ScopeManage, s.handlePAGroupRename))
	mux.Handle("POST "+pa+"/groups/{id}/members/add", s.guard(ScopeManage, s.handlePAGroupMember(true)))
	mux.Handle("POST "+pa+"/groups/{id}/members/remove", s.guard(ScopeManage, s.handlePAGroupMember(false)))
	mux.Handle("GET "+pa+"/assistants/{id}/instructions", s.guard(ScopeView, s.handlePAInstructions))
	mux.Handle("POST "+pa+"/assistants/{id}/instructions", s.guard(ScopeManage, s.handlePASetInstructions))
	mux.Handle("POST "+pa+"/assistants/{id}/instructions/restore", s.guard(ScopeManage, s.handlePARestoreInstructions))
	mux.Handle("GET "+pa+"/assistants/{id}/runtime", s.guard(ScopeView, s.handlePARuntime))
	mux.Handle("POST "+pa+"/assistants/{id}/runtime", s.guard(ScopeManage, s.handlePASetRuntime))
	mux.Handle("GET "+pa+"/assistants/{id}/mcp", s.guard(ScopeView, s.handlePAMCP))
	mux.Handle("POST "+pa+"/assistants/{id}/mcp/grant", s.guard(ScopeManage, s.handlePAMCPGrant))
	mux.Handle("POST "+pa+"/assistants/{id}/mcp/verify", s.guard(ScopeManage, s.handlePAMCPVerify))
	mux.Handle("GET "+pa+"/goals/{id}/plan", s.guard(ScopeView, s.handlePAGoalPlan))
	mux.Handle("GET "+pa+"/assistants/{id}/facts", s.guard(ScopeView, s.handlePAFacts))
	mux.Handle("GET "+pa+"/assistants/{id}/activity", s.guard(ScopeView, s.handlePAActivity))
	mux.Handle("POST "+pa+"/assistants/{id}/activity/ack", s.guard(ScopeManage, s.handlePAActivityAck))
	mux.Handle("GET "+pa+"/assistants/{id}/activity/notifications", s.guard(ScopeView, s.handlePAActivityNotices))
	mux.Handle("POST "+pa+"/assistants/{id}/activity/notifications/ack", s.guard(ScopeManage, s.handlePAActivityNoticesAck))
	mux.Handle("GET "+pa+"/assistants/{id}/report", s.guard(ScopeView, s.handlePAReport))
	mux.Handle("POST "+pa+"/assistants/{id}/report/resolve", s.guard(ScopeManage, s.handlePAReportResolve))
	mux.Handle("GET "+pa+"/assistants/{id}/routines", s.guard(ScopeView, s.handlePARoutines))
	mux.Handle("GET "+pa+"/assistants/{id}/memory", s.guard(ScopeView, s.handlePAMemory))
	mux.Handle("GET "+pa+"/memory/{id}/history", s.guard(ScopeView, s.handlePAMemoryHistory))
	mux.Handle("POST "+pa+"/assistants/{id}/memory/add", s.guard(ScopeManage, s.handlePAMemoryAdd))
	for _, action := range []string{"edit", "approve", "forget"} {
		mux.Handle("POST "+pa+"/memory/{id}/"+action, s.guard(ScopeManage, s.handlePAMemoryEdit(action)))
	}
	mux.Handle("GET "+pa+"/assistants/{id}/proactivity", s.guard(ScopeView, s.handlePAProactivity))
	mux.Handle("POST "+pa+"/assistants/{id}/proactivity", s.guard(ScopeManage, s.handlePASetProactivity))
	mux.Handle("POST "+pa+"/approvals/{id}/decision", s.guard(ScopeManage, s.handlePADecideApproval))
	mux.Handle("POST "+pa+"/tasks/{id}/ack", s.guard(ScopeManage, s.handlePATaskAction("ack")))
	mux.Handle("POST "+pa+"/tasks/{id}/retry", s.guard(ScopeManage, s.handlePATaskAction("retry")))
	mux.Handle("POST "+pa+"/tasks/{id}/publish", s.guard(ScopeManage, s.handlePATaskAction("publish")))
	mux.Handle("GET "+apiPrefix+"/sessions", s.guard(ScopeView, s.handleSessions))
	mux.Handle("POST "+apiPrefix+"/voice/transcribe", s.guard(ScopeTerminalInput, s.handleVoiceTranscribe))
	mux.Handle("GET "+apiPrefix+"/remote-desktop", s.guard(ScopeRemoteDesktop, s.handleRemoteDesktopInfo))
	mux.Handle("POST "+apiPrefix+"/remote-desktop/session", s.guard(ScopeRemoteDesktop, s.handleRemoteDesktopSession))
	mux.Handle("POST "+apiPrefix+"/sessions/{id}/continue", s.guard(ScopeManage, s.handleSessionContinue))
	mux.Handle("POST "+apiPrefix+"/sessions/{id}/trash", s.guard(ScopeManage, s.handleSessionTrash))
	mux.Handle("GET "+apiPrefix+"/sessions/trash", s.guard(ScopeView, s.handleTrashList))
	mux.Handle("POST "+apiPrefix+"/sessions/trash/{entry}/restore", s.guard(ScopeManage, s.handleTrashRestore))
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
	if s.opts.RemoteDesktop != nil {
		s.opts.RemoteDesktop.DisconnectDevice(deviceID) // a revoked phone loses its desktop session too
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
		ID     string `json:"id"`
		Kind   string `json:"kind,omitempty"`
		Label  string `json:"label,omitempty"`
		Status string `json:"status,omitempty"`
	}
	instances := []instance{}
	for _, n := range names {
		if !isDeviceInstance(n) { // a live bridge is listed below with its status
			instances = append(instances, instance{ID: n})
		}
	}
	for _, m := range s.machineEntries() {
		instances = append(instances, instance{ID: m.Key, Kind: "device", Label: m.Label, Status: m.Status})
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
	// Where this Mac serves right now (loopback and a failed bind offer nothing a
	// phone could reach): the phone's book follows a changed LAN/Tailscale address
	// from the answer of every successful connect, however it reached the Host.
	if advertised := s.advertised(); len(advertised) > 0 {
		body["net"] = map[string]any{"addresses": advertised}
	}
	if s.opts.Launcher != nil {
		capabilities["launch"] = true
		body["launchKinds"] = s.opts.Launcher.Kinds()
	}
	if s.opts.Machines != nil {
		capabilities["devices"] = true
	}
	if s.opts.Usage != nil {
		capabilities["quota"] = true
	}
	if s.opts.Automations != nil {
		if _, ok := s.opts.Automations(r.Context()); ok {
			capabilities["automations"] = true
			if s.opts.AutomationsAdmin != nil && s.opts.AutomationsAdmin.Open() {
				capabilities["automationsManage"] = true
			}
		}
	}
	if s.opts.PersonalAgent != nil {
		if c, ok := s.opts.PersonalAgent(r.Context()); ok && c != nil {
			capabilities["personalAgent"] = true
			capabilities["personalAgentManage"] = true
			capabilities["personalAgentApprove"] = true
			capabilities["personalAgentGroups"] = true
			capabilities["personalAgentPersona"] = true
			capabilities["personalAgentPlans"] = true
			capabilities["personalAgentRoutines"] = true
			capabilities["personalAgentMemory"] = true
			capabilities["personalAgentProactivity"] = true
			capabilities["personalAgentActivityNotices"] = true
			// An older plugin can answer Overview while lacking the report
			// outbox. Probe the actual subcommand instead of guessing by the
			// presence of the Assistants gate.
			if c.SupportsCoordinatorReports(r.Context()) {
				capabilities["personalAgentNarrativeReports"] = true
			}
		}
	}
	if s.opts.Sessions != nil {
		capabilities["sessions"] = true
		if s.opts.Continuer != nil {
			capabilities["sessionContinue"] = true
		}
		if s.opts.Trasher != nil {
			capabilities["sessionTrash"] = true
		}
	}
	if s.opts.Git != nil {
		capabilities["git"] = true
		if s.opts.GitWrite != nil {
			capabilities["gitWrite"] = true
		}
	}
	if s.opts.Files {
		capabilities["files"] = true
	}
	if s.opts.Attach != nil {
		capabilities["attachments"] = true
	}
	if s.voiceReady() {
		capabilities["voice"] = true
	}
	if s.remoteDesktopOn() {
		capabilities["remoteDesktop"] = true
	}
	if s.opts.Queue != nil {
		capabilities["queue"] = true
		capabilities["queueEdit"] = true
	}
	if s.p2pConfig().Enabled {
		capabilities["p2p"] = true
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request, _ Device) {
	instance, ok := s.resolveAnyInstance(w, r)
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

// isDeviceInstance reports whether an instance key names a saved machine (a Herdr
// runtime on another computer) rather than a local session.
func isDeviceInstance(name string) bool { return herdr.ParseRef(name).IsRemote() }

// resolveInstance validates ?instance= against the backend and refuses a device:
// Files, Git, Chat transcripts, attachments and Continue read the Mac's own disk,
// and a remote Workspace's path would otherwise be read (or written) as the same
// path here. Only the routes that go through Herdr alone use resolveAnyInstance.
func (s *Server) resolveInstance(w http.ResponseWriter, r *http.Request) (string, bool) {
	instance, ok := s.resolveAnyInstance(w, r)
	if !ok {
		return "", false
	}
	if isDeviceInstance(instance) {
		writeError(w, http.StatusConflict, "instance_remote", "")
		return "", false
	}
	return instance, true
}

// resolveAnyInstance validates ?instance= against the backend's serveable
// instances: local sessions and the machines whose bridge is up. An unknown
// string never reaches a socket path.
func (s *Server) resolveAnyInstance(w http.ResponseWriter, r *http.Request) (string, bool) {
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

// NotifyAutomation tells every open events socket that one automation took a
// step (kind: draft|started|done|failed; the Mac computed it, the phone only
// words it). It carries a name and a run id and nothing else: no prompt, no
// rationale, no error text. A socket whose queue is full drops the frame
// rather than stall the Mac.
func (s *Server) NotifyAutomation(kind, name, runID string) {
	frame := map[string]any{"t": "automation", "kind": kind, "name": name}
	if runID != "" {
		frame["runId"] = runID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for q := range s.notices {
		select {
		case q <- frame:
		default:
		}
	}
}

// subscribeNotices registers one events socket's frame queue.
func (s *Server) subscribeNotices() (q chan map[string]any, unsubscribe func()) {
	q = make(chan map[string]any, 16)
	s.mu.Lock()
	if s.notices == nil {
		s.notices = map[chan map[string]any]struct{}{}
	}
	s.notices[q] = struct{}{}
	s.mu.Unlock()
	return q, func() {
		s.mu.Lock()
		delete(s.notices, q)
		s.mu.Unlock()
	}
}

func (s *Server) pokeChannel() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.poke
}
