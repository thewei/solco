package remotedesktop

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"

	"github.com/thewei/solco/internal/remotedesktop/platform/macos"
	"github.com/thewei/solco/internal/remotedesktop/protocol"
	"github.com/thewei/solco/internal/remotedesktop/rtc"
)

// TestRealHelperToPionPhone is the Phase 0 chain check: the real Swift helper (ScreenCaptureKit →
// VideoToolbox) → Go IPC → Service/session → Pion → a Pion "phone". It needs this Mac's Screen
// Recording grant for the invoking process, so it only runs with SOLCO_RD_E2E=1:
//
//	SOLCO_RD_E2E=1 go test ./internal/remotedesktop -run TestRealHelperToPionPhone -v
func TestRealHelperToPionPhone(t *testing.T) {
	if os.Getenv("SOLCO_RD_E2E") != "1" {
		t.Skip("set SOLCO_RD_E2E=1 to run against the real helper")
	}
	be := macos.New(macos.Options{})
	if ok, why := be.Available(); !ok {
		t.Fatalf("helper unavailable: %s", why)
	}
	s := NewService(Config{Backend: be, InterfaceFilter: func(string) bool { return true }})
	s.SetSettings(Settings{Enabled: true, RequireApproval: false, DefaultTier: protocol.TierSmooth})

	pc, control, msgs, _, sdp := newOffer(t)
	var (
		mu            sync.Mutex
		nals          = map[byte]int{}
		frames, bytes atomic.Int64
		firstFrame    = make(chan struct{}, 1)
		started       = time.Now()
		firstFrameAt  time.Duration
	)
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		var dep codecs.H264Packet
		var au int
		for {
			pkt, _, err := tr.ReadRTP()
			if err != nil {
				return
			}
			out, err := dep.Unmarshal(pkt.Payload)
			if err == nil && len(out) > 0 {
				au += len(out)
				mu.Lock()
				for i := 0; i+4 < len(out); i++ {
					if out[i] == 0 && out[i+1] == 0 && out[i+2] == 0 && out[i+3] == 1 {
						nals[out[i+4]&0x1f]++
					}
				}
				mu.Unlock()
			}
			if pkt.Marker {
				if frames.Add(1) == 1 {
					firstFrameAt = time.Since(started)
					firstFrame <- struct{}{}
				}
				bytes.Add(int64(au))
				au = 0
			}
		}
	})
	_ = control
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	ans, err := s.Open(ctx, protocol.DeviceInfo{ID: "e2e", Name: "Pion phone"}, protocol.Offer{Protocol: protocol.Version, SDP: sdp, Mode: protocol.ModeViewOnly})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Logf("answer: display=%s %dx%d@%d hw=%v mode=%s", ans.State.DisplayID, ans.State.Width, ans.State.Height, ans.State.FPS, ans.State.Hardware, ans.State.Mode)
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: ans.SDP}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstFrame:
		t.Logf("first video frame after %v", firstFrameAt)
	case <-ctx.Done():
		t.Fatal("no video frame within 40 s (Screen Recording granted to this process?)")
	}
	time.Sleep(5 * time.Second)
	// Ask for stats through the control channel, like the phone's overlay does.
	_ = control.SendText(`{"type":"stats.request"}`)
	time.Sleep(300 * time.Millisecond)
	var statsMsg string
	for len(msgs) > 0 {
		if m := <-msgs; contains(m, `"stats"`) {
			statsMsg = m
		}
	}
	mu.Lock()
	t.Logf("frames=%d bytes=%d nal types=%v stats=%s", frames.Load(), bytes.Load(), nals, statsMsg)
	haveSPS, havePPS, haveIDR := nals[7] > 0, nals[8] > 0, nals[5] > 0
	mu.Unlock()
	if !haveSPS || !havePPS || !haveIDR {
		t.Fatalf("stream lacks SPS/PPS/IDR: %v", nals)
	}
	if frames.Load() < 10 {
		t.Fatalf("only %d frames in ~5 s", frames.Load())
	}
	s.Disconnect(ans.SessionID)
	waitUntil(t, func() bool { return len(s.Sessions()) == 0 })
	_ = rtc.ChannelControl
}
