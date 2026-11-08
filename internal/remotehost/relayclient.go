package remotehost

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

/**
 * [INPUT]: 依赖 RelayConfig/LoadOrCreateRelaySecret、server.go 的 Handler（mux/guard/WS 全复用）、coder/websocket、net/http
 * [OUTPUT]: 对外提供 RelayState/RelayStatus 与 Server.RelayStatus；内部 relayClient（拨出注册、hreq/wopen 透传、重连退避）
 * [POS]: 自托管 alma-relay 的桌面侧（docs/PROTOCOL.md 线协议逐字实现）：仅当 Relay.Enabled 且 Server 运行时启动；拨出 wss://<relay>/agent?th=<sha256hex(secret)>，注册后把 hreq/wopen 打到仅绑 127.0.0.1 随机端口的内部 http.Server（复用既有 Handler，回环桥是实现细节，绝不产生非回环监听）；重连指数退避 1s→30s 带抖动、成功归零，close 4000（哈希被占）是终态错误不再重拨；同一 agent 连接上所有 id 并发多路复用。对外拨出固定经典 TLS 群组（X25519/P-256，排除 X25519MLKEM768——部分网络会静默丢弃后量子首选 key share 的握手，2026-11 实测）；回环桥不走该配置。日志只记操作、id 与错误类别，绝不记 URL query（token 在里面）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// RelayState is the coarse state of the relay tunnel.
type RelayState string

const (
	RelayIdle       RelayState = "" // relay off, or the Host is stopped
	RelayConnecting RelayState = "connecting"
	RelayOnline     RelayState = "online"
	RelayBackoff    RelayState = "backoff" // waiting to redial
	RelayError      RelayState = "error"   // gave up (close 4000): see Message
)

// RelayStatus is what Settings shows about the relay tunnel.
type RelayStatus struct {
	State   RelayState
	Message string // why the tunnel is in error (or last refused reason)
}

const (
	relayWriteTimeout     = 10 * time.Second
	relayBridgeDial_limit = 10 * time.Second
	relayChunkSize        = 64 << 10
	// relayReadLimit covers the largest legal hreq: the Cloudflare relay
	// caps request bodies at 24 MB, which base64-inflates to 32 MB inside
	// one WebSocket frame.
	relayReadLimit = 48 << 20
	// 保活：NAT/代理/中间层会静默清除空闲的 WSS 流（2026-11 实测：注册后
	// 数秒即死，hreq 投进半开套接字）。CF hibernation 在协议层自动应答
	// ping、不唤醒 DO；pong 超时即 CloseNow 触发快速重拨。
	// 2026-11-03 实测（Mac 开全局代理 TUN）：半开套接字平均悬挂 ~30 s（20 s 间隔 + 10 s 超时），
	// 期间手机的 /remote-desktop/session 请求被投进黑洞、整个重连卡死；缩到 6 s + 4 s，
	// 最坏 10 s 内发现并重拨（一次 ping 帧的流量可忽略）。
	relayPingInterval = 6 * time.Second
	relayPongTimeout  = 4 * time.Second
	// closeReplaced is the relay's close code when another agent took the
	// hash: redialling would just fight the winner, so it is terminal.
	closeReplaced websocket.StatusCode = 4000
)

// relayDialTimeout bounds one outbound dial attempt (a var so a test can shrink it).
var relayDialTimeout = 8 * time.Second

// relayTLSConfig pins classic key-exchange groups for the outbound dial.
// Go 1.24+ offers X25519MLKEM768 first and some networks silently drop that
// handshake (observed against Cloudflare edges from China, 2026-11: forcing
// classic groups restores connectivity). It applies only to the relay dial —
// never to the loopback bridge.
func relayTLSConfig() *tls.Config {
	return &tls.Config{CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256}}
}

// dialRelay is the production outbound dial: one transport per tunnel, its
// TLS pinned to the classic groups above.
func dialRelay(transport *http.Transport) func(context.Context, string) (*websocket.Conn, error) {
	return func(ctx context.Context, addr string) (*websocket.Conn, error) {
		// A lossy path (proxy/TUN in front of Cloudflare) can stall a TLS
		// handshake indefinitely; bound the attempt so the backoff loop retries.
		// The context only covers the handshake, not the connection's life.
		ctx, cancel := context.WithTimeout(ctx, relayDialTimeout)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, addr, &websocket.DialOptions{
			HTTPClient: &http.Client{Transport: transport},
		})
		return conn, err
	}
}

// relayTestHooks carries the seams the tests inject; nil fields keep the
// production behaviour.
type relayTestHooks struct {
	dial       func(ctx context.Context, addr string) (*websocket.Conn, error)
	backoffMin time.Duration
	backoffMax time.Duration
	chunk      int
}

// relayFrame is one JSON text frame on the agent socket (both directions).
type relayFrame struct {
	T         string            `json:"t"`
	ID        string            `json:"id,omitempty"`
	TokenHash string            `json:"tokenHash,omitempty"`
	Name      string            `json:"name,omitempty"`
	Ver       string            `json:"ver,omitempty"`
	Key       string            `json:"key,omitempty"`
	Message   string            `json:"message,omitempty"`
	Method    string            `json:"method,omitempty"`
	Path      string            `json:"path,omitempty"` // full /c/<secret>/… path with query
	Headers   map[string]string `json:"headers,omitempty"`
	B         string            `json:"b,omitempty"` // base64 body / binary message
	Status    int               `json:"status,omitempty"`
	Code      int               `json:"code,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	S         *string           `json:"s,omitempty"` // text WebSocket message (pointer: "" is a real message)
}

// tunnel is the transport-independent half of the Host's tunnelled access:
// it answers hreq/wopen/wmsg/wclose/hcancel frames by calling the loopback
// bridge (the Host's own Handler on 127.0.0.1) and writes responses through
// send. The relay's agent socket and a phone's WebRTC DataChannel both speak
// exactly these frames, so both run the same tunnel.
type tunnel struct {
	secret     string // a /c/<secret>/ prefix addressed to anyone else is refused
	http       *http.Client
	bridgeAddr string // 127.0.0.1:<random> — implementation detail, never published
	chunk      int
	send       func(v any) error

	mu       sync.Mutex
	inflight map[string]context.CancelFunc // request id → its cancel
	bridges  map[string]*relayBridge       // bridged socket id → its local socket
}

func newTunnel(secret string, client *http.Client, chunk int) tunnel {
	return tunnel{
		secret: secret, http: client, chunk: chunk,
		inflight: map[string]context.CancelFunc{},
		bridges:  map[string]*relayBridge{},
	}
}

// handle runs one tunnel frame; it reports whether the frame was one of them.
func (c *tunnel) handle(ctx context.Context, f relayFrame) bool {
	switch f.T {
	case "hreq":
		c.spawnRequest(ctx, f)
	case "hcancel":
		c.cancelInflight(f.ID)
	case "wopen":
		go c.bridgeSocket(ctx, f)
	case "wmsg":
		c.routeToBridge(ctx, f)
	case "wclose":
		c.closeBridge(f)
	default:
		return false
	}
	return true
}

type relayClient struct {
	tunnel
	srv       *Server
	cfg       RelayConfig
	tokenHash string

	transport *http.Transport
	bridge    http.Server

	writeMu sync.Mutex // serialises every frame written to the agent socket
	agent   *websocket.Conn

	dialFn     func(ctx context.Context, addr string) (*websocket.Conn, error)
	backoffMin time.Duration
	backoffMax time.Duration

	cancel  context.CancelFunc
	runDone chan struct{}
}

// relayBridge is one phone WebSocket forwarded onto the loopback bridge.
type relayBridge struct {
	conn   *websocket.Conn // the loopback connection into the Host's own handler
	cancel context.CancelFunc
}

func newRelayClient(s *Server, cfg RelayConfig, secret string) *relayClient {
	transport := &http.Transport{TLSClientConfig: relayTLSConfig()}
	c := &relayClient{
		tunnel: newTunnel(secret, &http.Client{Transport: transport}, relayChunkSize),
		srv:    s, cfg: cfg, tokenHash: RelayTokenHash(secret),
		transport:  transport,
		dialFn:     dialRelay(transport),
		backoffMin: time.Second,
		backoffMax: 30 * time.Second,
	}
	c.send = func(v any) error { return c.writeFrame(v) }
	if h := s.relayHooks; h != nil {
		if h.dial != nil {
			c.dialFn = h.dial
		}
		if h.backoffMin > 0 {
			c.backoffMin = h.backoffMin
		}
		if h.backoffMax > 0 {
			c.backoffMax = h.backoffMax
		}
		if h.chunk > 0 {
			c.chunk = h.chunk
		}
	}
	return c
}

// start binds the loopback bridge and begins dialling. Any failure ends in
// the error state; nothing here can widen the listener beyond 127.0.0.1.
func (c *relayClient) start() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		c.setStatus(RelayError, "relay bridge: "+err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel, c.runDone = cancel, make(chan struct{})
	c.bridgeAddr = ln.Addr().String()
	c.bridge = http.Server{Handler: c.srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := c.bridge.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// The bridge dying kills every future hreq/wopen; surface it
			// instead of looping against a dead bridge.
			c.stop()
			c.setStatus(RelayError, "relay bridge: "+err.Error())
			slog.Warn("remote host relay bridge stopped", "error", err)
		}
	}()
	go c.run(ctx)
	slog.Info("remote host relay starting", "relay", c.relayHost())
}

func (c *relayClient) stop() {
	if c.cancel != nil {
		c.cancel()
	}
	_ = c.bridge.Close()
	c.transport.CloseIdleConnections()
	if c.runDone != nil {
		<-c.runDone
	}
	c.teardown()
}

// run dials, registers and serves until ctx ends. Close 4000 is terminal;
// everything else retries on a growing, jittered backoff that resets after a
// connection that made it online.
func (c *relayClient) run(ctx context.Context) {
	defer close(c.runDone)
	base := c.backoffMin
	for ctx.Err() == nil {
		c.setStatus(RelayConnecting, "")
		conn, err := c.dialFn(ctx, c.agentURL())
		online := false
		if err == nil {
			online, err = c.serve(ctx, conn)
		}
		if ctx.Err() != nil {
			return
		}
		if code, reason, closed := closeInfo(err); closed && code == closeReplaced {
			c.setStatus(RelayError, "relay: "+reason)
			slog.Warn("remote host relay gave up", "relay", c.relayHost(), "reason", reason)
			return
		}
		if online {
			base = c.backoffMin // a connection that worked buys a fresh ladder
		}
		wait := c.nextDelay(base)
		c.setStatus(RelayBackoff, errText(err))
		slog.Warn("remote host relay disconnected", "relay", c.relayHost(), "retry_ms", wait.Milliseconds())
		if !sleepCtx(ctx, wait) {
			return
		}
		base *= 2
		if base > c.backoffMax {
			base = c.backoffMax
		}
	}
}

// nextDelay is base plus up to base of jitter, capped at backoffMax: the
// ladder grows 1s→2s→4s→…→30s with every interval widened by noise.
func (c *relayClient) nextDelay(base time.Duration) time.Duration {
	wait := base + time.Duration(rand.Int64N(int64(base)+1))
	if wait > c.backoffMax {
		wait = c.backoffMax
	}
	return wait
}

// serve runs one agent connection to its end. It reports whether the
// connection got as far as registered.
func (c *relayClient) serve(ctx context.Context, conn *websocket.Conn) (online bool, err error) {
	conn.SetReadLimit(relayReadLimit)
	defer func() { _ = conn.CloseNow() }()
	c.writeMu.Lock()
	c.agent = conn
	c.writeMu.Unlock()
	defer func() {
		c.writeMu.Lock()
		if c.agent == conn {
			c.agent = nil
		}
		c.writeMu.Unlock()
	}()
	// Everything spawned for this connection — requests, bridged sockets —
	// dies with it: a redial must not inherit the previous socket's work.
	connCtx, cancelConn := context.WithCancel(ctx)
	defer cancelConn()
	reg := relayFrame{T: "register", TokenHash: c.tokenHash, Name: c.srv.opts.HostName, Ver: c.srv.opts.Version}
	if c.cfg.RegistrationKey != "" {
		reg.Key = c.cfg.RegistrationKey
	}
	if err := c.writeFrame(reg); err != nil {
		return false, err
	}
	go c.keepalive(connCtx, conn)
	for {
		typ, data, err := conn.Read(connCtx)
		if err != nil {
			return online, err
		}
		if typ != websocket.MessageText {
			return online, errors.New("relay sent a non-text frame")
		}
		var f relayFrame
		if err := json.Unmarshal(data, &f); err != nil {
			return online, fmt.Errorf("bad relay frame: %w", err)
		}
		switch f.T {
		case "registered":
			online = true
			c.setStatus(RelayOnline, "")
			slog.Info("remote host relay online", "relay", c.relayHost())
		case "error":
			// Registration refused (bad key, bad hash, bad relay token); the
			// relay closes the socket right after. Recorded and retried on
			// the backoff — only close 4000 stops the redial loop.
			c.setStatus(RelayBackoff, f.Message)
			slog.Warn("remote host relay refused", "relay", c.relayHost(), "message", f.Message)
		default:
			c.handle(connCtx, f)
		}
	}
}

func (c *relayClient) agentURL() string {
	base := strings.TrimSuffix(c.cfg.URL, "/")
	return strings.Replace(base, "https://", "wss://", 1) + "/agent?th=" + c.tokenHash
}

// keepalive pings the relay on a fixed cadence so middleboxes keep the flow,
// and turns a silently-dead socket into a quick redial: a pong that misses
// its deadline kills the connection, whose Read then fails into the backoff.
func (c *relayClient) keepalive(ctx context.Context, conn *websocket.Conn) {
	t := time.NewTicker(relayPingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pingCtx, cancel := context.WithTimeout(ctx, relayPongTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("remote host relay keepalive failed", "relay", c.relayHost(), "error", err)
				}
				conn.CloseNow()
				return
			}
		}
	}
}

func (c *relayClient) relayHost() string {
	if u, err := url.Parse(strings.TrimSpace(c.cfg.URL)); err == nil {
		return u.Host
	}
	return c.cfg.URL
}

func (c *relayClient) setStatus(state RelayState, message string) {
	c.srv.setRelayStatus(RelayStatus{State: state, Message: message})
}

func (c *relayClient) writeFrame(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.agent == nil {
		return errors.New("relay socket is closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), relayWriteTimeout)
	defer cancel()
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.agent.Write(ctx, websocket.MessageText, data)
}

// teardown cancels every in-flight request and closes every bridged socket:
// once the agent socket is gone nothing can answer the relay any more.
func (c *tunnel) teardown() {
	c.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(c.inflight))
	for id, cancel := range c.inflight {
		cancels = append(cancels, cancel)
		delete(c.inflight, id)
	}
	bridges := make([]*relayBridge, 0, len(c.bridges))
	for id, b := range c.bridges {
		bridges = append(bridges, b)
		delete(c.bridges, id)
	}
	c.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	for _, b := range bridges {
		_ = b.conn.CloseNow()
		b.cancel()
	}
}

// --- tunneled HTTP ----------------------------------------------------------

func (c *tunnel) spawnRequest(connCtx context.Context, f relayFrame) {
	ctx, cancel := context.WithCancel(connCtx)
	c.mu.Lock()
	c.inflight[f.ID] = cancel
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.inflight, f.ID)
			c.mu.Unlock()
			cancel()
		}()
		c.handleRequest(ctx, f)
	}()
}

func (c *tunnel) handleRequest(ctx context.Context, f relayFrame) {
	req, err := c.loopbackRequest(ctx, f)
	if err != nil {
		c.failRequest(f.ID, err)
		return
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return // hcancel or a disconnected agent: the relay already knows
		}
		c.failRequest(f.ID, err)
		return
	}
	defer resp.Body.Close()
	if err := c.send(relayFrame{T: "hres", ID: f.ID, Status: resp.StatusCode, Headers: relayHeadersOut(resp.Header)}); err != nil {
		return
	}
	buf := make([]byte, c.chunk)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if err := c.send(relayFrame{T: "hchunk", ID: f.ID, B: base64.StdEncoding.EncodeToString(buf[:n])}); err != nil {
				return
			}
		}
		if rerr == io.EOF {
			_ = c.send(relayFrame{T: "hend", ID: f.ID})
			return
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return
			}
			c.failRequest(f.ID, rerr)
			return
		}
	}
}

func (c *tunnel) failRequest(id string, err error) {
	// The error text can embed the loopback URL (whose query carries the
	// phone's token) — log and answer with a fixed phrase instead.
	slog.Warn("remote host tunnel request failed", "id", id)
	_ = c.send(relayFrame{T: "herr", ID: id, Message: "upstream failed"})
}

func (c *tunnel) cancelInflight(id string) {
	c.mu.Lock()
	cancel := c.inflight[id]
	delete(c.inflight, id)
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// loopbackRequest turns an hreq into a request against the loopback bridge.
// The relay passes the full /c/<secret>/… path; anything not addressed to
// this Mac's own secret is refused.
func (c *tunnel) loopbackRequest(ctx context.Context, f relayFrame) (*http.Request, error) {
	target, err := c.loopbackURL(f.Path)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if f.B != "" {
		data, err := base64.StdEncoding.DecodeString(f.B)
		if err != nil {
			return nil, fmt.Errorf("bad request body: %w", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, f.Method, target, body)
	if err != nil {
		return nil, err
	}
	for k, v := range f.Headers {
		lk := strings.ToLower(k)
		if hopByHop[lk] || lk == "host" || lk == "content-length" {
			continue
		}
		req.Header.Add(k, v)
	}
	return req, nil
}

// loopbackURL points the relayed path at the loopback bridge, keeping the
// query (it carries the phone's ?token= on WebSocket opens — see bearerToken).
// Both relay editions forward the bare path (the /c/<code> prefix is consumed
// by routing); a full /c/<own-secret>/ path is accepted for the documented
// shape, and a foreign /c/ prefix is refused — it is never for this Mac.
func (c *tunnel) loopbackURL(path string) (string, error) {
	u, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("relay path: %w", err)
	}
	esc := u.EscapedPath()
	if rest, ok := strings.CutPrefix(esc, "/c/"+c.secret); ok {
		if rest != "" && !strings.HasPrefix(rest, "/") {
			return "", errors.New("relay path is not for this Mac")
		}
		esc = rest
	} else if strings.HasPrefix(esc, "/c/") {
		return "", errors.New("relay path is not for this Mac")
	}
	if esc == "" {
		esc = "/"
	}
	out := "http://" + c.bridgeAddr + esc
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out, nil
}

// relayHeadersOut lower-cases and joins headers for the relay (it rebuilds a
// Headers object from the map) and drops the hop-by-hop set it ignores too.
func relayHeadersOut(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		lk := strings.ToLower(k)
		if hopByHop[lk] || lk == "content-length" {
			continue
		}
		out[lk] = strings.Join(vs, ", ")
	}
	return out
}

var hopByHop = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true,
	"proxy-authorization": true, "te": true, "trailer": true,
	"transfer-encoding": true, "upgrade": true,
}

// --- tunneled WebSockets ----------------------------------------------------

// bridgeSocket answers wopen by dialling the same path on the loopback bridge
// (the query, including ?token=, survives untouched) and pumping both ways.
func (c *tunnel) bridgeSocket(connCtx context.Context, f relayFrame) {
	target, err := c.loopbackURL(f.Path)
	if err != nil {
		_ = c.send(relayFrame{T: "werr", ID: f.ID, Message: "bad path"})
		return
	}
	ctx, cancel := context.WithCancel(connCtx)
	defer cancel()
	dialCtx, cancelDial := context.WithTimeout(ctx, relayBridgeDial_limit)
	// The phone's token rides the query: wopen carries neither headers nor
	// subprotocols, and bearerToken accepts ?token= for exactly this.
	local, _, err := websocket.Dial(dialCtx, "ws"+strings.TrimPrefix(target, "http"), nil)
	cancelDial()
	if err != nil {
		// Never log the error text: it quotes the URL with its query token.
		slog.Warn("remote host tunnel socket refused", "id", f.ID)
		_ = c.send(relayFrame{T: "werr", ID: f.ID, Message: "could not open"})
		return
	}
	local.SetReadLimit(relayReadLimit)
	b := &relayBridge{conn: local, cancel: cancel}
	c.mu.Lock()
	c.bridges[f.ID] = b
	c.mu.Unlock()
	if err := c.send(relayFrame{T: "wopened", ID: f.ID}); err != nil {
		c.removeBridge(f.ID)
		_ = local.CloseNow()
		return
	}
	pumpErr := c.pumpToRelay(ctx, f.ID, local)
	if c.removeBridge(f.ID) { // still ours: the agent side has not closed it first
		code, reason, closed := closeInfo(pumpErr)
		if !closed || code < 0 {
			code = websocket.StatusInternalError
		}
		_ = c.send(relayFrame{T: "wclose", ID: f.ID, Code: int(code), Reason: truncate(reason, closeReasonMax)})
	}
	_ = local.CloseNow()
}

// pumpToRelay forwards loopback messages to the relay: text stays text,
// binary goes as base64. It returns when the socket ends or a write fails.
func (c *tunnel) pumpToRelay(ctx context.Context, id string, local *websocket.Conn) error {
	for {
		typ, data, err := local.Read(ctx)
		if err != nil {
			return err
		}
		msg := relayFrame{T: "wmsg", ID: id}
		if typ == websocket.MessageText {
			s := string(data)
			msg.S = &s
		} else {
			msg.B = base64.StdEncoding.EncodeToString(data)
		}
		if err := c.send(msg); err != nil {
			return err
		}
	}
}

func (c *tunnel) routeToBridge(ctx context.Context, f relayFrame) {
	c.mu.Lock()
	b := c.bridges[f.ID]
	c.mu.Unlock()
	if b == nil {
		return
	}
	switch {
	case f.S != nil:
		_ = b.conn.Write(ctx, websocket.MessageText, []byte(*f.S))
	case f.B != "":
		data, err := base64.StdEncoding.DecodeString(f.B)
		if err != nil {
			return
		}
		_ = b.conn.Write(ctx, websocket.MessageBinary, data)
	}
}

// closeBridge ends a bridged socket because the phone (or the relay) closed
// it; the local pump then finds the socket closed and stays silent — the
// bridge was already removed, so it does not echo a wclose back.
func (c *tunnel) closeBridge(f relayFrame) {
	c.mu.Lock()
	b := c.bridges[f.ID]
	delete(c.bridges, f.ID)
	c.mu.Unlock()
	if b == nil {
		return
	}
	code := f.Code
	if !validRelayCloseCode(code) { // mirror the relay's sanitizeCloseCode
		code = int(websocket.StatusNormalClosure)
	}
	reason := truncate(f.Reason, closeReasonMax)
	go func() {
		_ = b.conn.Close(websocket.StatusCode(code), reason)
		b.cancel()
	}()
}

func (c *tunnel) removeBridge(id string) bool {
	c.mu.Lock()
	_, had := c.bridges[id]
	delete(c.bridges, id)
	c.mu.Unlock()
	return had
}

// validRelayCloseCode is what a server may legally send (1000, 3000–4999);
// anything else the relay sanitises to 1000, and so do we.
func validRelayCloseCode(code int) bool {
	return code == 1000 || (code >= 3000 && code <= 4999)
}

// closeInfo extracts a close code and reason from a WebSocket read error;
// closed reports whether the error is one at all.
func closeInfo(err error) (code websocket.StatusCode, reason string, closed bool) {
	if err == nil {
		return 0, "", false
	}
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		return ce.Code, ce.Reason, true
	}
	return -1, "", false
}

// errText keeps statuses informative without leaking anything sensitive:
// relay errors are addresses and close codes only.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
