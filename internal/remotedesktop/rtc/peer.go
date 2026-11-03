// Package rtc is the WebRTC transport of Remote Desktop, built on Pion: it answers the phone's
// complete (non-trickle) offer with one H.264 video track (Mac → phone) and accepts the phone's two
// data channels — "control" (reliable, ordered: buttons, keys, text, clipboard, session messages)
// and "pointer" (unordered, no retransmits: moves and scroll). Media is DTLS-SRTP end to end; the
// Host's signalling carries only SDP.
//
// It knows nothing about screens, input semantics or authorisation; the session wires it to a
// platform backend. High-rate video never touches the control path.
//
// [INPUT]: pion/webrtc/v4, pion/rtcp
// [OUTPUT]: Answer → *Peer (WriteVideo, SendControl, Stats, Close), Options, Handlers
// [POS]: the only importer of Pion in Remote Desktop
package rtc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// Data channel labels the phone creates.
const (
	ChannelControl = "control"
	ChannelPointer = "pointer"
)

const (
	// gatherTimeout bounds ICE candidate gathering before the answer is sent with what exists.
	// LAN host candidates are immediate; a slow/blocked STUN server must not hold the session up.
	gatherTimeout = 3 * time.Second
	// gatherGrace is how long the answer waits for more candidates once a srflx/relay one exists.
	gatherGrace = 1500 * time.Millisecond
	// disconnectGrace is how long a transient ICE "disconnected" may recover before the peer is
	// declared gone (Wi-Fi roam, a brief stall).
	disconnectGrace = 8 * time.Second
	// minKeyframeGap collapses a burst of PLI/FIR into one keyframe request.
	minKeyframeGap = 250 * time.Millisecond
	// videoClockRate is RTP's H.264 clock.
	videoClockRate = 90000
	h264Fmtp       = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"
)

// ICEServer is a STUN/TURN entry in WebRTC's own JSON shape.
type ICEServer struct {
	URLs       []string
	Username   string
	Credential string
}

// State is the transport state surfaced to the session.
type State string

const (
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateDisconnected State = "disconnected" // may recover
	StateClosed       State = "closed"       // final: failed, closed, or grace expired
)

// Handlers receive inbound traffic. They run on Pion's goroutines and must not block.
type Handlers struct {
	// OnControl / OnPointer get one data-channel message each.
	OnControl func(data []byte)
	OnPointer func(data []byte)
	// OnKeyframeRequest is a debounced PLI/FIR from the phone's decoder.
	OnKeyframeRequest func()
	// OnState reports transport transitions; StateClosed is delivered once.
	OnState func(State)
	// OnControlOpen fires when the control channel becomes writable.
	OnControlOpen func()
}

// Options configures one peer.
type Options struct {
	ICEServers []ICEServer
	// InterfaceFilter restricts ICE host candidates (virtual interfaces excluded by the Host).
	InterfaceFilter func(name string) bool
	// IncludeLoopback adds loopback candidates (tests only).
	IncludeLoopback bool
	Handlers        Handlers
}

// Peer is one answered WebRTC connection.
type Peer struct {
	pc       *webrtc.PeerConnection
	track    *webrtc.TrackLocalStaticSample
	handlers Handlers

	mu      sync.Mutex
	control *webrtc.DataChannel
	closed  bool
	lastKey time.Time
	timer   *time.Timer

	startedAt  time.Time
	framesSent atomic.Uint64
	bytesSent  atomic.Uint64
	keyReqs    atomic.Uint64
}

// ErrControlNotOpen is returned by SendControl before the phone's control channel is up.
var ErrControlNotOpen = errors.New("rtc: control channel not open")

func newAPI(opts Options) (*webrtc.API, error) {
	m := &webrtc.MediaEngine{}
	fb := []webrtc.RTCPFeedback{{Type: "goog-remb"}, {Type: "ccm", Parameter: "fir"}, {Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "transport-cc"}}
	// Constrained Baseline, packetization-mode 1: the profile every phone decodes in hardware.
	// 42001f (plain Baseline) is registered too — VideoToolbox's Baseline output declares it.
	for i, fmtp := range []string{
		h264Fmtp,
		"level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f",
	} {
		if err := m.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: videoClockRate, SDPFmtpLine: fmtp, RTCPFeedback: fb},
			PayloadType:        webrtc.PayloadType(102 + 4*i),
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil {
		return nil, err
	}
	se := webrtc.SettingEngine{}
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6})
	if opts.InterfaceFilter != nil {
		se.SetInterfaceFilter(opts.InterfaceFilter)
	}
	if opts.IncludeLoopback {
		se.SetIncludeLoopbackCandidate(true)
	}
	return webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se)), nil
}

// Answer builds the peer for the phone's offer and returns it with the answer SDP (all gathered
// candidates inline). On error nothing is left open.
func Answer(ctx context.Context, offerSDP string, opts Options) (*Peer, string, error) {
	api, err := newAPI(opts)
	if err != nil {
		return nil, "", err
	}
	var ice []webrtc.ICEServer
	for _, s := range opts.ICEServers {
		ice = append(ice, webrtc.ICEServer{URLs: s.URLs, Username: s.Username, Credential: s.Credential})
	}
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: ice})
	if err != nil {
		return nil, "", err
	}
	p := &Peer{pc: pc, handlers: opts.Handlers, startedAt: time.Now()}
	fail := func(err error) (*Peer, string, error) {
		_ = pc.Close()
		return nil, "", err
	}

	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: videoClockRate, SDPFmtpLine: h264Fmtp}, "screen", "solco-remote-desktop")
	if err != nil {
		return fail(err)
	}
	p.track = track
	sender, err := pc.AddTrack(track)
	if err != nil {
		return fail(err)
	}
	go p.readRTCP(sender)

	pc.OnDataChannel(p.onDataChannel)
	pc.OnConnectionStateChange(p.onConnectionState)

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
		return fail(fmt.Errorf("%w: %v", ErrBadOffer, err))
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return fail(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	// A dead TURN entry must not hold the answer for the whole timeout: once a reflexive or relayed candidate
	// exists, wait only a short grace for the rest (the phone's own gather does the same).
	early := make(chan struct{})
	var earlyOnce sync.Once
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil && (c.Typ == webrtc.ICECandidateTypeSrflx || c.Typ == webrtc.ICECandidateTypeRelay) {
			earlyOnce.Do(func() { time.AfterFunc(gatherGrace, func() { close(early) }) })
		}
	})
	if err := pc.SetLocalDescription(answer); err != nil {
		return fail(err)
	}
	select {
	case <-gathered:
	case <-early:
	case <-time.After(gatherTimeout):
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	ld := pc.LocalDescription()
	if ld == nil {
		return fail(errors.New("rtc: no local description"))
	}
	return p, ld.SDP, nil
}

// ErrBadOffer marks an offer Pion could not accept.
var ErrBadOffer = errors.New("rtc: bad offer")

func (p *Peer) onDataChannel(dc *webrtc.DataChannel) {
	switch dc.Label() {
	case ChannelControl:
		p.mu.Lock()
		p.control = dc
		p.mu.Unlock()
		dc.OnOpen(func() {
			if p.handlers.OnControlOpen != nil {
				p.handlers.OnControlOpen()
			}
		})
		dc.OnMessage(func(m webrtc.DataChannelMessage) {
			if m.IsString && p.handlers.OnControl != nil {
				p.handlers.OnControl(m.Data)
			}
		})
	case ChannelPointer:
		dc.OnMessage(func(m webrtc.DataChannelMessage) {
			if m.IsString && p.handlers.OnPointer != nil {
				p.handlers.OnPointer(m.Data)
			}
		})
	default:
		_ = dc.Close() // only the two channels of the protocol exist
	}
}

func (p *Peer) onConnectionState(s webrtc.PeerConnectionState) {
	p.mu.Lock()
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	p.mu.Unlock()
	switch s {
	case webrtc.PeerConnectionStateConnected:
		p.emit(StateConnected)
	case webrtc.PeerConnectionStateConnecting, webrtc.PeerConnectionStateNew:
		p.emit(StateConnecting)
	case webrtc.PeerConnectionStateDisconnected:
		p.emit(StateDisconnected)
		p.mu.Lock()
		p.timer = time.AfterFunc(disconnectGrace, func() { p.Close() })
		p.mu.Unlock()
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
		p.Close()
	}
}

func (p *Peer) emit(s State) {
	if p.handlers.OnState != nil {
		p.handlers.OnState(s)
	}
}

// readRTCP turns the phone decoder's PLI/FIR into debounced keyframe requests.
func (p *Peer) readRTCP(sender *webrtc.RTPSender) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		for _, pkt := range pkts {
			switch pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				p.requestKeyframe()
			}
		}
	}
}

func (p *Peer) requestKeyframe() {
	p.mu.Lock()
	now := time.Now()
	if now.Sub(p.lastKey) < minKeyframeGap {
		p.mu.Unlock()
		return
	}
	p.lastKey = now
	p.mu.Unlock()
	p.keyReqs.Add(1)
	if p.handlers.OnKeyframeRequest != nil {
		p.handlers.OnKeyframeRequest()
	}
}

// WriteVideo sends one Annex-B access unit; dur is the time to the next frame.
func (p *Peer) WriteVideo(annexB []byte, dur time.Duration) error {
	if err := p.track.WriteSample(media.Sample{Data: annexB, Duration: dur}); err != nil {
		return err
	}
	p.framesSent.Add(1)
	p.bytesSent.Add(uint64(len(annexB)))
	return nil
}

// SendControl sends one JSON message on the reliable control channel.
func (p *Peer) SendControl(msg []byte) error {
	p.mu.Lock()
	dc := p.control
	p.mu.Unlock()
	if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
		return ErrControlNotOpen
	}
	return dc.SendText(string(msg))
}

// Stats is a cheap snapshot.
type Stats struct {
	FramesSent uint64
	BytesSent  uint64
	KeyframeRq uint64
	RTT        time.Duration
	PathType   string // host | srflx | relay | prflx of the selected local candidate
	Age        time.Duration
}

// Stats samples counters and the selected candidate pair.
func (p *Peer) Stats() Stats {
	st := Stats{FramesSent: p.framesSent.Load(), BytesSent: p.bytesSent.Load(), KeyframeRq: p.keyReqs.Load(), Age: time.Since(p.startedAt)}
	report := p.pc.GetStats()
	var selected string
	for _, s := range report {
		if t, ok := s.(webrtc.TransportStats); ok {
			selected = t.SelectedCandidatePairID
		}
	}
	local := ""
	for _, s := range report {
		if pair, ok := s.(webrtc.ICECandidatePairStats); ok && pair.ID == selected {
			st.RTT = time.Duration(pair.CurrentRoundTripTime * float64(time.Second))
			local = pair.LocalCandidateID
		}
	}
	for _, s := range report {
		if c, ok := s.(webrtc.ICECandidateStats); ok && c.ID == local {
			st.PathType = c.CandidateType.String()
		}
	}
	return st
}

// Close ends the connection; the final StateClosed is delivered once.
func (p *Peer) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
	}
	p.mu.Unlock()
	_ = p.pc.Close()
	p.emit(StateClosed)
}
