package rtc

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// phone is a Pion client standing in for react-native-webrtc: a receive-only H.264 transceiver plus
// the two data channels, with a complete (non-trickle) offer.
type phone struct {
	pc        *webrtc.PeerConnection
	control   *webrtc.DataChannel
	pointer   *webrtc.DataChannel
	rtpCount  atomic.Int64
	mediaSSRC atomic.Uint32
	ctrlRecv  chan string
}

func newPhone(t *testing.T) (*phone, string) {
	t.Helper()
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		t.Fatal(err)
	}
	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	p := &phone{pc: pc, ctrlRecv: make(chan string, 8)}
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	p.control, _ = pc.CreateDataChannel(ChannelControl, nil)
	ordered, retx := false, uint16(0)
	p.pointer, _ = pc.CreateDataChannel(ChannelPointer, &webrtc.DataChannelInit{Ordered: &ordered, MaxRetransmits: &retx})
	p.control.OnMessage(func(m webrtc.DataChannelMessage) { p.ctrlRecv <- string(m.Data) })
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		p.mediaSSRC.Store(uint32(tr.SSRC()))
		for {
			if _, _, err := tr.ReadRTP(); err != nil {
				return
			}
			p.rtpCount.Add(1)
		}
	})
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	done := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-done
	return p, pc.LocalDescription().SDP
}

func fakeIDR() []byte {
	var b bytes.Buffer
	for _, nal := range [][]byte{{0x67, 0x42, 0x00, 0x1f, 0x8d}, {0x68, 0xce, 0x3c, 0x80}, append([]byte{0x65, 0x88}, bytes.Repeat([]byte{0xab}, 3000)...)} {
		b.Write([]byte{0, 0, 0, 1})
		b.Write(nal)
	}
	return b.Bytes()
}

func TestAnswerConnectsAndCarriesVideoAndData(t *testing.T) {
	ph, offer := newPhone(t)
	var (
		gotControl, gotPointer = make(chan string, 4), make(chan string, 4)
		keyReq                 atomic.Int64
		states                 = make(chan State, 8)
		controlOpen            = make(chan struct{}, 1)
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	peer, answer, err := Answer(ctx, offer, Options{IncludeLoopback: true, Handlers: Handlers{
		OnControl:         func(d []byte) { gotControl <- string(d) },
		OnPointer:         func(d []byte) { gotPointer <- string(d) },
		OnKeyframeRequest: func() { keyReq.Add(1) },
		OnState:           func(s State) { states <- s },
		OnControlOpen:     func() { controlOpen <- struct{}{} },
		OnBandwidth:       func(int) {}, // the congestion-control interceptors must not break negotiation
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := ph.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-controlOpen:
	case <-ctx.Done():
		t.Fatal("control channel never opened")
	}

	// phone → Mac on both channels
	if err := ph.control.SendText(`{"type":"key","code":"KeyA","down":true}`); err != nil {
		t.Fatal(err)
	}
	if err := ph.pointer.SendText(`{"type":"pointer.move","seq":1,"x":0.5,"y":0.5}`); err != nil {
		t.Fatal(err)
	}
	if v := recv(t, gotControl); v != `{"type":"key","code":"KeyA","down":true}` {
		t.Fatalf("control = %q", v)
	}
	if v := recv(t, gotPointer); v != `{"type":"pointer.move","seq":1,"x":0.5,"y":0.5}` {
		t.Fatalf("pointer = %q", v)
	}

	// Mac → phone control
	if err := peer.SendControl([]byte(`{"type":"pong","t":1}`)); err != nil {
		t.Fatal(err)
	}
	if v := recv(t, ph.ctrlRecv); v != `{"type":"pong","t":1}` {
		t.Fatalf("phone got %q", v)
	}

	// Mac → phone video
	deadline := time.Now().Add(10 * time.Second)
	for ph.rtpCount.Load() == 0 && time.Now().Before(deadline) {
		if err := peer.WriteVideo(fakeIDR(), 33*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		time.Sleep(33 * time.Millisecond)
	}
	if ph.rtpCount.Load() == 0 {
		t.Fatal("no RTP reached the phone")
	}

	// phone PLI → keyframe request (debounced to one)
	for i := 0; i < 5; i++ {
		_ = ph.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: ph.mediaSSRC.Load()}})
	}
	deadline = time.Now().Add(5 * time.Second)
	for keyReq.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if keyReq.Load() != 1 {
		t.Fatalf("keyframe requests = %d, want exactly 1 (debounced)", keyReq.Load())
	}

	if st := peer.Stats(); st.FramesSent == 0 || st.BytesSent == 0 {
		t.Fatalf("stats empty: %+v", st)
	}

	peer.Close()
	peer.Close() // idempotent
	var closed int
	for {
		select {
		case s := <-states:
			if s == StateClosed {
				closed++
			}
			continue
		default:
		}
		break
	}
	if closed != 1 {
		t.Fatalf("StateClosed delivered %d times", closed)
	}
	if err := peer.SendControl([]byte("x")); err == nil {
		t.Fatal("send after close must fail")
	}
}

func TestBadOfferIsReported(t *testing.T) {
	_, _, err := Answer(context.Background(), "not sdp", Options{})
	if err == nil {
		t.Fatal("garbage offer must fail")
	}
}

func recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}
