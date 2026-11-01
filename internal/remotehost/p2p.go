package remotehost

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

/**
 * [INPUT]: 依赖 pion/webrtc/v4、relayclient.go 的 tunnel（hreq/wopen 帧引擎）、server.go 的 guard/Handler/trackConn、relay 的 POST /turn
 * [OUTPUT]: 对外提供 P2PConfig/ICEServer、Server.SetP2P/P2PSessions、GET /api/v3/p2p/ice、POST /api/v3/p2p/session、bootstrap 的 capabilities.p2p
 * [POS]: 手机 ⇄ 桌面的 WebRTC DataChannel 直连（relay/直连路由上已鉴权的信令；不改 relay 协议）：手机经已通的路由取 ICE 服务器并 POST 一份非 trickle 的 offer，Host 等 ICE 收集完成、或首个 srflx 候选后再宽限 1.5 s（gatherEarly，死 TURN 不再拖满 8 s）后回 answer；DataChannel "solco" 上跑与 relay 完全相同的 hreq/hres/wopen/wmsg 帧（同一个 tunnel，手机扮演 relay 的角色），帧经回环桥进既有 Handler，所以每个请求仍要带设备 token、业务 /api/v3 零改动。DataChannel 单条消息有上限，故以 1 字节头（1=后续、0=末片）分片重组。撤销设备经 trackConn 立刻关闭其 P2P 会话；每设备最多 2 个会话（挤掉最旧）；ICE 服务器 = 设置里的静态 STUN/TURN ∪ relay /turn 签发的短期凭据（缓存到过期前一分钟）。日志只记设备与会话 id、候选类型计数（host/srflx/relay/prflx）、ICE 状态迁移与选中对的类型、会话寿命，绝不记 SDP/候选地址/帧内容
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	p2pDataChannel    = "solco"
	p2pFragment       = 16 << 10 // payload bytes per DataChannel message (+1 header byte)
	p2pChunk          = 24 << 10 // raw response bytes per hchunk frame (base64 inflates by 4/3)
	p2pMaxFrame       = relayReadLimit
	p2pMaxPerDevice   = 2
	p2pGatherTimeout  = 8 * time.Second
	p2pSrflxGrace     = 1500 * time.Millisecond // after the first reflexive candidate, how long a relayed one may still arrive
	p2pAttachTimeout  = 30 * time.Second
	p2pBufferHigh     = 1 << 20
	p2pBufferWait     = 30 * time.Second
	p2pTurnFetchLimit = 6 * time.Second
)

// ICEServer is one STUN/TURN entry in WebRTC's own JSON shape, so the phone can
// hand the list straight to its RTCPeerConnection.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// P2PConfig is the opt-in WebRTC direct path. ICEServers are the user's static
// STUN/TURN entries; a relay that serves /turn adds short-lived credentials.
type P2PConfig struct {
	Enabled    bool
	ICEServers []ICEServer
}

// DefaultICEServers is used while the user configured none. Chosen by probing
// from mainland China (2026-11-01): Google's STUN is blocked there; these answer.
var DefaultICEServers = []ICEServer{
	{URLs: []string{"stun:stun.miwifi.com:3478"}},
	{URLs: []string{"stun:stun.cloudflare.com:3478"}},
}

// ParseICEServers turns the Settings text (STUN/TURN URLs separated by commas,
// spaces or newlines) into entries; turn:/turns: URLs share one credential.
// Anything that is not a stun:/turn:/turns: URL is dropped.
func ParseICEServers(text, turnUser, turnPass string) []ICEServer {
	var out []ICEServer
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		switch {
		case strings.HasPrefix(f, "stun:"), strings.HasPrefix(f, "stuns:"):
			out = append(out, ICEServer{URLs: []string{f}})
		case strings.HasPrefix(f, "turn:"), strings.HasPrefix(f, "turns:"):
			out = append(out, ICEServer{URLs: []string{f}, Username: turnUser, Credential: turnPass})
		}
	}
	return out
}

type p2pSession struct {
	id       string
	deviceID string
	at       time.Time
	srv      *Server
	pc       *webrtc.PeerConnection
	untrack  func()

	mu     sync.Mutex
	dc     *webrtc.DataChannel
	tun    *tunnel
	cancel context.CancelFunc
	closed bool
	state  string     // last PeerConnection state, for the close log
	wmu    sync.Mutex // one fragmented frame at a time
}

type p2pState struct {
	mu       sync.Mutex
	cfg      P2PConfig
	sessions map[string]*p2pSession
	bridge   *http.Server
	bridgeAt string
	turn     turnCache

	testLoopback bool // tests only: gather loopback candidates so two in-process peers can connect
}

type turnCache struct {
	servers []ICEServer
	expires time.Time
}

// SetP2P changes the P2P choice. Nothing restarts: the config is read when a
// phone asks for ICE servers or opens a session; disabling ends live sessions.
func (s *Server) SetP2P(cfg P2PConfig) {
	s.p2p.mu.Lock()
	s.p2p.cfg = cfg
	s.p2p.mu.Unlock()
	if !cfg.Enabled {
		s.closeP2P()
	}
}

func (s *Server) p2pConfig() P2PConfig {
	s.p2p.mu.Lock()
	defer s.p2p.mu.Unlock()
	return s.p2p.cfg
}

// P2PSessions is the number of live WebRTC sessions (Settings status, tests).
func (s *Server) P2PSessions() int {
	s.p2p.mu.Lock()
	defer s.p2p.mu.Unlock()
	return len(s.p2p.sessions)
}

// closeP2P ends every session and the loopback bridge (Server.Close, disable).
func (s *Server) closeP2P() {
	s.p2p.mu.Lock()
	sessions := make([]*p2pSession, 0, len(s.p2p.sessions))
	for _, sess := range s.p2p.sessions {
		sessions = append(sessions, sess)
	}
	bridge := s.p2p.bridge
	s.p2p.bridge, s.p2p.bridgeAt = nil, ""
	s.p2p.mu.Unlock()
	for _, sess := range sessions {
		sess.close()
	}
	if bridge != nil {
		_ = bridge.Close()
	}
}

// p2pLoopback lazily starts the loopback bridge P2P sessions share: the
// Host's own Handler on 127.0.0.1:<random>, never a wider listener.
func (s *Server) p2pLoopback() (string, error) {
	s.p2p.mu.Lock()
	defer s.p2p.mu.Unlock()
	if s.p2p.bridgeAt != "" {
		return s.p2p.bridgeAt, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	s.p2p.bridge, s.p2p.bridgeAt = srv, ln.Addr().String()
	return s.p2p.bridgeAt, nil
}

// p2pICEServers is the list both ends use: the static entries plus the
// relay's short-lived TURN credentials when it serves any.
func (s *Server) p2pICEServers(ctx context.Context) []ICEServer {
	cfg := s.p2pConfig()
	out := append([]ICEServer(nil), cfg.ICEServers...)
	if len(out) == 0 {
		out = append(out, DefaultICEServers...)
	}
	return append(out, s.relayTurn(ctx)...)
}

// relayTurn asks the configured relay for TURN credentials (POST /turn). A
// relay without TURN answers 404 and a failure is remembered briefly, so a
// bad network never stalls every signalling request.
func (s *Server) relayTurn(ctx context.Context) []ICEServer {
	rc := s.relayConfig()
	if !rc.Enabled || rc.Validate() != nil {
		return nil
	}
	s.p2p.mu.Lock()
	cached := s.p2p.turn
	s.p2p.mu.Unlock()
	if time.Now().Before(cached.expires) {
		return cached.servers
	}
	servers, ttl := fetchRelayTurn(ctx, rc)
	s.p2p.mu.Lock()
	s.p2p.turn = turnCache{servers: servers, expires: time.Now().Add(ttl)}
	s.p2p.mu.Unlock()
	return servers
}

func fetchRelayTurn(ctx context.Context, rc RelayConfig) ([]ICEServer, time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, p2pTurnFetchLimit)
	defer cancel()
	base := strings.TrimSuffix(strings.TrimSpace(rc.URL), "/")
	base = strings.Replace(base, "wss://", "https://", 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/turn", nil)
	if err != nil {
		return nil, time.Minute
	}
	if rc.RegistrationKey != "" {
		req.Header.Set("Authorization", "Bearer "+rc.RegistrationKey)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: relayTLSConfig()}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, time.Minute
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 10 * time.Minute // 404 = no TURN on this relay; 401 = key refused
	}
	var body struct {
		IceServers []ICEServer `json:"iceServers"`
		TTL        int         `json:"ttl"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return nil, time.Minute
	}
	ttl := time.Duration(body.TTL)*time.Second - time.Minute
	if ttl < time.Minute {
		ttl = time.Minute
	}
	return body.IceServers, ttl
}

// --- signalling -------------------------------------------------------------

func (s *Server) handleP2PICE(w http.ResponseWriter, r *http.Request, _ Device) {
	if !s.p2pConfig().Enabled {
		writeError(w, http.StatusNotFound, "p2p_unavailable", "")
		return
	}
	servers := s.p2pICEServers(r.Context())
	if servers == nil {
		servers = []ICEServer{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"iceServers": servers})
}

type p2pOffer struct {
	SDP string `json:"sdp"`
}

func (s *Server) handleP2PSession(w http.ResponseWriter, r *http.Request, dev Device) {
	if !s.p2pConfig().Enabled {
		writeError(w, http.StatusNotFound, "p2p_unavailable", "")
		return
	}
	var req p2pOffer
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.SDP == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	addr, err := s.p2pLoopback()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "p2p_failed", "")
		return
	}
	answer, err := s.openP2PSession(r.Context(), dev, req.SDP, addr)
	if err != nil {
		slog.Warn("remote host p2p session failed", "device", dev.ID, "error", errKind(err))
		writeError(w, http.StatusBadGateway, "p2p_failed", "")
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

// errKind keeps log lines free of SDP/candidate text that errors can quote.
func errKind(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, errBadOffer):
		return "bad_offer"
	}
	return "error"
}

var errBadOffer = errors.New("bad offer")

func (s *Server) openP2PSession(ctx context.Context, dev Device, offerSDP, bridgeAddr string) (map[string]string, error) {
	se := webrtc.SettingEngine{}
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6})
	se.SetInterfaceFilter(func(name string) bool { return !isVirtualInterface(name) })
	if s.p2p.testLoopback {
		se.SetIncludeLoopbackCandidate(true)
	}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))

	var ice []webrtc.ICEServer
	for _, sv := range s.p2pICEServers(ctx) {
		ice = append(ice, webrtc.ICEServer{URLs: sv.URLs, Username: sv.Username, Credential: sv.Credential})
	}
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: ice})
	if err != nil {
		return nil, err
	}
	sess := &p2pSession{id: randomID(), deviceID: dev.ID, at: time.Now(), srv: s, pc: pc}
	sess.untrack = s.trackConn(dev.ID, sess.close)
	s.registerP2P(sess)

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != p2pDataChannel {
			_ = dc.Close()
			return
		}
		sess.attach(dc, bridgeAddr)
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		sess.mu.Lock()
		sess.state = st.String()
		sess.mu.Unlock()
		slog.Info("remote host p2p state", "device", dev.ID, "session", sess.id, "state", st.String(), "after_ms", time.Since(sess.at).Milliseconds())
		if st == webrtc.PeerConnectionStateConnected {
			slog.Info("remote host p2p path", "device", dev.ID, "session", sess.id, "pair", selectedPairKinds(pc))
		}
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed {
			sess.close()
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
		sess.close()
		return nil, fmt.Errorf("%w: %v", errBadOffer, err)
	}
	ans, err := pc.CreateAnswer(nil)
	if err != nil {
		sess.close()
		return nil, err
	}
	gathered := gatherEarly(pc)
	if err := pc.SetLocalDescription(ans); err != nil {
		sess.close()
		return nil, err
	}
	select {
	case <-gathered:
	case <-time.After(p2pGatherTimeout):
		// Half-gathered is still usable: whatever candidates exist are in the SDP.
	case <-ctx.Done():
		sess.close()
		return nil, ctx.Err()
	}
	time.AfterFunc(p2pAttachTimeout, func() { // a session whose channel never opens is garbage
		sess.mu.Lock()
		attached := sess.dc != nil
		sess.mu.Unlock()
		if !attached {
			sess.close()
		}
	})
	slog.Info("remote host p2p session opened", "device", dev.ID, "session", sess.id,
		"phone_candidates", candidateKinds(offerSDP), "mac_candidates", candidateKinds(pc.LocalDescription().SDP), "ice_servers", iceServerKinds(ice))
	return map[string]string{"sessionId": sess.id, "sdp": pc.LocalDescription().SDP}, nil
}

func randomID() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// registerP2P records the session and evicts the device's oldest beyond the cap.
func (s *Server) registerP2P(sess *p2pSession) {
	s.p2p.mu.Lock()
	if s.p2p.sessions == nil {
		s.p2p.sessions = map[string]*p2pSession{}
	}
	s.p2p.sessions[sess.id] = sess
	var mine []*p2pSession
	for _, other := range s.p2p.sessions {
		if other.deviceID == sess.deviceID {
			mine = append(mine, other)
		}
	}
	s.p2p.mu.Unlock()
	if len(mine) > p2pMaxPerDevice {
		var oldest *p2pSession
		for _, m := range mine {
			if m != sess && (oldest == nil || m.at.Before(oldest.at)) {
				oldest = m
			}
		}
		if oldest != nil {
			oldest.close()
		}
	}
}

// --- data channel ---------------------------------------------------------

func (sess *p2pSession) attach(dc *webrtc.DataChannel, bridgeAddr string) {
	ctx, cancel := context.WithCancel(context.Background())
	tun := newTunnel(randomID()+randomID(), &http.Client{}, p2pChunk) // a random secret: no /c/ prefix is ever "ours" here
	tun.bridgeAddr = bridgeAddr
	tun.send = func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return sess.write(ctx, dc, data)
	}
	sess.mu.Lock()
	if sess.closed {
		sess.mu.Unlock()
		cancel()
		return
	}
	sess.dc, sess.tun, sess.cancel = dc, &tun, cancel
	sess.mu.Unlock()

	var pending []byte
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if len(msg.Data) == 0 {
			return
		}
		if len(pending)+len(msg.Data) > p2pMaxFrame {
			sess.close()
			return
		}
		pending = append(pending, msg.Data[1:]...)
		if msg.Data[0] != 0 {
			return // more fragments follow
		}
		frame := pending
		pending = nil
		var f relayFrame
		if err := json.Unmarshal(frame, &f); err != nil {
			return
		}
		tun.handle(ctx, f)
	})
	dc.OnClose(sess.close)
}

// write sends one frame as ≤p2pFragment pieces, each prefixed with 1 (more
// follow) or 0 (last). The channel is ordered and reliable, so the far side
// only concatenates until it sees a 0.
func (sess *p2pSession) write(ctx context.Context, dc *webrtc.DataChannel, data []byte) error {
	sess.wmu.Lock()
	defer sess.wmu.Unlock()
	for {
		n := len(data)
		flag := byte(0)
		if n > p2pFragment {
			n, flag = p2pFragment, 1
		}
		if err := waitDrain(ctx, dc); err != nil {
			return err
		}
		if err := dc.Send(append([]byte{flag}, data[:n]...)); err != nil {
			return err
		}
		data = data[n:]
		if flag == 0 {
			return nil
		}
	}
}

func waitDrain(ctx context.Context, dc *webrtc.DataChannel) error {
	deadline := time.Now().Add(p2pBufferWait)
	for dc.BufferedAmount() > p2pBufferHigh {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return errors.New("p2p send buffer stuck")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

func (sess *p2pSession) close() {
	sess.mu.Lock()
	if sess.closed {
		sess.mu.Unlock()
		return
	}
	sess.closed = true
	tun, cancel := sess.tun, sess.cancel
	attached, lastState := sess.dc != nil, sess.state
	sess.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if tun != nil {
		tun.teardown()
	}
	if sess.untrack != nil {
		sess.untrack()
	}
	sess.srv.p2p.mu.Lock()
	delete(sess.srv.p2p.sessions, sess.id)
	sess.srv.p2p.mu.Unlock()
	_ = sess.pc.Close()
	slog.Info("remote host p2p session closed", "device", sess.deviceID, "session", sess.id,
		"lived_ms", time.Since(sess.at).Milliseconds(), "attached", attached, "last_state", lastState)
}

// gatherEarly closes its channel when ICE gathering completes, or p2pSrflxGrace
// after the first server-reflexive candidate shows up. The answer is
// non-trickle, so whatever has not arrived by then is left out; but waiting for
// a dead STUN/TURN server to time out (8 s) before answering made every P2P
// attempt crawl, while the first reflexive candidate plus a short grace for a
// relayed one is what a live path needs. With no reflexive candidate at all the
// caller's own timeout still bounds the wait. Call it before SetLocalDescription.
func gatherEarly(pc *webrtc.PeerConnection) <-chan struct{} {
	done := make(chan struct{})
	var finish, armed sync.Once
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		switch {
		case c == nil:
			finish.Do(func() { close(done) })
		case c.Typ == webrtc.ICECandidateTypeSrflx:
			armed.Do(func() { time.AfterFunc(p2pSrflxGrace, func() { finish.Do(func() { close(done) }) }) })
		}
	})
	return done
}

// candidateKinds counts the ICE candidates of an SDP by type ("host=2 srflx=1
// relay=0"). Types only: addresses never reach a log line. It is how a
// session says whether the phone offered a reflexive or relayed candidate.
func candidateKinds(sdp string) string {
	var host, srflx, relay, prflx int
	for _, line := range strings.Split(sdp, "\n") {
		if !strings.HasPrefix(line, "a=candidate:") {
			continue
		}
		switch {
		case strings.Contains(line, " typ host"):
			host++
		case strings.Contains(line, " typ srflx"):
			srflx++
		case strings.Contains(line, " typ relay"):
			relay++
		case strings.Contains(line, " typ prflx"):
			prflx++
		}
	}
	return fmt.Sprintf("host=%d srflx=%d relay=%d prflx=%d", host, srflx, relay, prflx)
}

// iceServerKinds says how many STUN and TURN URLs this session was given.
func iceServerKinds(servers []webrtc.ICEServer) string {
	var stun, turn int
	for _, sv := range servers {
		for _, u := range sv.URLs {
			switch {
			case strings.HasPrefix(u, "stun:"), strings.HasPrefix(u, "stuns:"):
				stun++
			case strings.HasPrefix(u, "turn:"), strings.HasPrefix(u, "turns:"):
				turn++
			}
		}
	}
	return fmt.Sprintf("stun=%d turn=%d", stun, turn)
}

// selectedPairKinds names the nominated pair by candidate types only
// ("host/host", "srflx/srflx", "relay/srflx"); "unknown" before one exists.
func selectedPairKinds(pc *webrtc.PeerConnection) string {
	sctp := pc.SCTP()
	if sctp == nil || sctp.Transport() == nil || sctp.Transport().ICETransport() == nil {
		return "unknown"
	}
	pair, err := sctp.Transport().ICETransport().GetSelectedCandidatePair()
	if err != nil || pair == nil || pair.Local == nil || pair.Remote == nil {
		return "unknown"
	}
	return pair.Local.Typ.String() + "/" + pair.Remote.Typ.String()
}
