package helperipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotedesktop/protocol"
)

// fakeHelper is the helper's side of the link, driven by tests.
type fakeHelper struct {
	t  *testing.T
	nc net.Conn
	br *bufio.Reader
}

func dialHelper(t *testing.T, s *Server, token string, version int) (*fakeHelper, *Conn, error) {
	t.Helper()
	nc, err := net.Dial("unix", s.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nc.Close() })
	h := &fakeHelper{t: t, nc: nc, br: bufio.NewReader(nc)}
	type result struct {
		c   *Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := s.Accept(0)
		ch <- result{c, err}
	}()
	hello, _ := json.Marshal(Hello{T: "hello", ProtocolVersion: version, HelperVersion: "test", Token: token, PID: 1, Capabilities: []string{"capture.display"}})
	if err := WriteFrame(nc, KindControl, hello); err != nil {
		t.Fatal(err)
	}
	r := <-ch
	if r.err == nil {
		t.Cleanup(func() { _ = r.c.Close() })
		// consume the ack
		if _, _, err := ReadFrame(h.br); err != nil {
			t.Fatal(err)
		}
	}
	return h, r.c, r.err
}

func (h *fakeHelper) readCommand() command {
	h.t.Helper()
	_ = h.nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p, err := ReadFrame(h.br)
	if err != nil {
		h.t.Fatal(err)
	}
	var raw struct {
		T    string          `json:"t"`
		ID   int64           `json:"id"`
		Op   string          `json:"op"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(p, &raw); err != nil {
		h.t.Fatal(err)
	}
	return command{T: raw.T, ID: raw.ID, Op: raw.Op, Args: raw.Args}
}

func (h *fakeHelper) send(kind Kind, payload []byte) {
	h.t.Helper()
	if err := WriteFrame(h.nc, kind, payload); err != nil {
		h.t.Fatal(err)
	}
}

func newServer(t *testing.T) *Server {
	s, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestHandshakeAndCall(t *testing.T) {
	s := newServer(t)
	h, c, err := dialHelper(t, s, s.Token(), ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hello.HelperVersion != "test" {
		t.Fatalf("hello not kept: %+v", c.Hello)
	}
	go func() {
		cmd := h.readCommand()
		if cmd.Op != OpDisplaysList || cmd.ID == 0 {
			t.Errorf("unexpected command %+v", cmd)
		}
		reply, _ := json.Marshal(map[string]any{"t": "reply", "id": cmd.ID, "ok": true, "result": DisplaysResult{Displays: []protocol.DisplayInfo{{ID: "1", Width: 10, Height: 5}}}})
		h.send(KindControl, reply)
	}()
	var out DisplaysResult
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Call(ctx, OpDisplaysList, nil, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Displays) != 1 || out.Displays[0].ID != "1" {
		t.Fatalf("result = %+v", out)
	}
}

func TestCallSurfacesHelperRefusal(t *testing.T) {
	s := newServer(t)
	h, c, _ := dialHelper(t, s, s.Token(), ProtocolVersion)
	go func() {
		cmd := h.readCommand()
		reply, _ := json.Marshal(map[string]any{"t": "reply", "id": cmd.ID, "ok": false, "error": ProtocolError{Code: CodePermissionScreenRecording}})
		h.send(KindControl, reply)
	}()
	err := c.Call(context.Background(), OpCaptureStart, protocol.CaptureConfig{}, nil)
	var pe *ProtocolError
	if !asProtocolError(err, &pe) || pe.Code != CodePermissionScreenRecording {
		t.Fatalf("err = %v", err)
	}
}

func asProtocolError(err error, out **ProtocolError) bool {
	pe, ok := err.(*ProtocolError)
	if ok {
		*out = pe
	}
	return ok
}

func TestSendIsFireAndForget(t *testing.T) {
	s := newServer(t)
	h, c, _ := dialHelper(t, s, s.Token(), ProtocolVersion)
	if err := c.Send(OpInputKey, KeyArgs{Code: 55, Down: true}); err != nil {
		t.Fatal(err)
	}
	cmd := h.readCommand()
	if cmd.ID != 0 || cmd.Op != OpInputKey {
		t.Fatalf("command = %+v", cmd)
	}
}

func TestBadTokenAndProtocolMismatchAreRefused(t *testing.T) {
	s := newServer(t)
	if _, _, err := dialHelper(t, s, "wrong", ProtocolVersion); err == nil {
		t.Fatal("a wrong token must be refused")
	}
	s2 := newServer(t)
	if _, _, err := dialHelper(t, s2, s2.Token(), ProtocolVersion+1); err == nil {
		t.Fatal("a protocol mismatch must be refused")
	}
}

func TestSocketIsPrivate(t *testing.T) {
	s := newServer(t)
	for path, want := range map[string]uint32{s.dir: 0o700, s.path: 0o600} {
		st, err := osStat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st&0o777 != want {
			t.Errorf("%s mode %o, want %o", path, st&0o777, want)
		}
	}
}

func TestVideoGoldenBytes(t *testing.T) {
	// Pinned against the Swift side: VideoPayload(captureID: 1, keyframe: true, ptsMicros: 2, annexB: [9]).
	want := []byte{0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 2, 9}
	got := EncodeVideo(VideoFrame{CaptureID: 1, Keyframe: true, PTS: 2 * time.Microsecond, Data: []byte{9}})
	if !bytes.Equal(got, want) {
		t.Fatalf("encode = %v, want %v", got, want)
	}
	f, err := DecodeVideo(want)
	if err != nil || f.CaptureID != 1 || !f.Keyframe || f.PTS != 2*time.Microsecond || !bytes.Equal(f.Data, []byte{9}) {
		t.Fatalf("decode = %+v, %v", f, err)
	}
	if _, err := DecodeVideo(make([]byte, 12)); err == nil {
		t.Fatal("short payload must fail")
	}
}

func TestFrameBounds(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, KindControl, make([]byte, MaxFrameBytes)); err == nil {
		t.Fatal("oversized write must fail")
	}
	if _, _, err := ReadFrame(bytes.NewReader([]byte{0, 0, 0, 0})); err == nil {
		t.Fatal("empty frame must fail")
	}
	if _, _, err := ReadFrame(bytes.NewReader([]byte{0, 0, 0, 1, 77})); err == nil {
		t.Fatal("unknown kind must fail")
	}
}

// A consumer that does not read must not make latency grow: the queue flushes and the stream holds
// until the next keyframe, which is requested.
func TestVideoBackpressureFlushesAndWaitsForKeyframe(t *testing.T) {
	s := newServer(t)
	h, c, _ := dialHelper(t, s, s.Token(), ProtocolVersion)
	send := func(key bool, tag byte) {
		h.send(KindVideo, EncodeVideo(VideoFrame{CaptureID: 1, Keyframe: key, Data: []byte{tag}}))
	}
	send(true, 0)
	for i := 0; i < videoQueueDepth+3; i++ {
		send(false, byte(i+1))
	}
	// The helper is asked for a fresh keyframe.
	cmd := h.readCommand()
	if cmd.Op != OpCaptureKeyframe {
		t.Fatalf("expected a keyframe request, got %q", cmd.Op)
	}
	// Frames sent now are discarded until a keyframe arrives…
	send(false, 100)
	send(true, 200)
	deadline := time.After(2 * time.Second)
	var last VideoFrame
	for last.Data == nil || last.Data[0] != 200 {
		select {
		case last = <-c.Video():
		case <-deadline:
			t.Fatal("keyframe never delivered")
		}
	}
	if c.Dropped.Load() == 0 {
		t.Fatal("drops were not counted")
	}
}

func TestHelperExitEndsTheConnection(t *testing.T) {
	s := newServer(t)
	h, c, _ := dialHelper(t, s, s.Token(), ProtocolVersion)
	_ = h.nc.Close()
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done never closed")
	}
	if err := c.Call(context.Background(), OpDisplaysList, nil, nil); err == nil {
		t.Fatal("a call on a dead link must fail")
	}
}
