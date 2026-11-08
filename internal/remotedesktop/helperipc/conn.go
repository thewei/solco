package helperipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// videoQueueDepth bounds encoded frames waiting for the consumer. Latency beats completeness: when
// the consumer lags, the queue is flushed and the stream resumes at the next keyframe.
const videoQueueDepth = 6

// ErrClosed is returned by calls on a connection that has ended.
var ErrClosed = errors.New("helperipc: connection closed")

// Conn is one handshaken helper link. All methods are safe for concurrent use.
type Conn struct {
	nc net.Conn

	wmu    sync.Mutex // serialises writes (one frame = one Write)
	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan replyEnvelope
	waitKey bool // frames were dropped: discard non-key frames until a keyframe

	video  chan VideoFrame
	events chan Event
	done   chan struct{}
	errv   error
	once   sync.Once

	// Counters (atomic): frames received and frames dropped for backpressure.
	Received atomic.Uint64
	Dropped  atomic.Uint64

	// Hello is what the helper announced.
	Hello Hello
}

func newConn(nc net.Conn, hello Hello) *Conn {
	c := &Conn{
		nc: nc, Hello: hello,
		pending: map[int64]chan replyEnvelope{},
		video:   make(chan VideoFrame, videoQueueDepth),
		events:  make(chan Event, 32),
		done:    make(chan struct{}),
	}
	return c
}

func (c *Conn) start(br *bufio.Reader) { go c.readLoop(br) }

// Video yields encoded frames in order. It is closed when the connection ends.
func (c *Conn) Video() <-chan VideoFrame { return c.video }

// Events yields helper events. It is closed when the connection ends.
func (c *Conn) Events() <-chan Event { return c.events }

// Done is closed when the link ends; Err then says why (nil for a requested close).
func (c *Conn) Done() <-chan struct{} { return c.done }

func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.errv
}

// Close ends the link (the helper sees EOF, releases input and exits).
func (c *Conn) Close() error {
	c.fail(nil)
	return nil
}

func (c *Conn) fail(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.errv = err
		pending := c.pending
		c.pending = map[int64]chan replyEnvelope{}
		c.mu.Unlock()
		_ = c.nc.Close()
		close(c.done)
		for _, ch := range pending {
			close(ch)
		}
	})
}

func (c *Conn) readLoop(br *bufio.Reader) {
	defer func() {
		// Closing after the loop is safe: only this goroutine sends on video/events.
		close(c.video)
		close(c.events)
	}()
	for {
		kind, payload, err := ReadFrame(br)
		if err != nil {
			c.fail(err)
			return
		}
		switch kind {
		case KindVideo:
			frame, err := DecodeVideo(payload)
			if err != nil {
				c.fail(err)
				return
			}
			c.Received.Add(1)
			c.pushVideo(frame)
		case KindControl:
			c.dispatchControl(payload)
		}
	}
}

// pushVideo applies the drop policy: a full queue is flushed (the consumer is behind), the stream
// is held until the next keyframe, and a keyframe is requested. Non-key frames received while holding
// are discarded — they would only decode as corruption.
func (c *Conn) pushVideo(f VideoFrame) {
	c.mu.Lock()
	if c.waitKey {
		if !f.Keyframe {
			c.mu.Unlock()
			c.Dropped.Add(1)
			return
		}
		c.waitKey = false
	}
	c.mu.Unlock()
	select {
	case c.video <- f:
		return
	default:
	}
	// Queue full: flush it, then keep this frame only if it is a keyframe.
	for {
		select {
		case <-c.video:
			c.Dropped.Add(1)
			continue
		default:
		}
		break
	}
	if f.Keyframe {
		select {
		case c.video <- f:
		default:
		}
		return
	}
	c.Dropped.Add(1)
	c.mu.Lock()
	c.waitKey = true
	c.mu.Unlock()
	go func() { _ = c.Send(OpCaptureKeyframe, nil) }()
}

func (c *Conn) dispatchControl(payload []byte) {
	var head struct {
		T string `json:"t"`
	}
	if json.Unmarshal(payload, &head) != nil {
		return
	}
	switch head.T {
	case "reply":
		var r replyEnvelope
		if json.Unmarshal(payload, &r) != nil {
			return
		}
		c.mu.Lock()
		ch := c.pending[r.ID]
		delete(c.pending, r.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- r
		}
	case "event":
		var e eventEnvelope
		if json.Unmarshal(payload, &e) != nil {
			return
		}
		select {
		case c.events <- Event{Name: e.Ev, Data: e.Data}:
		default: // events are state hints; a flooded consumer re-lists on the next one
		}
	}
}

func (c *Conn) write(kind Kind, payload []byte) error {
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.nc.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := WriteFrame(c.nc, kind, payload); err != nil {
		c.fail(err)
		return err
	}
	return nil
}

// Send posts a fire-and-forget command (id 0: no reply on success; a failure arrives as an
// EventError). Input travels this way so it generates no return traffic.
func (c *Conn) Send(op string, args any) error {
	if args == nil {
		args = struct{}{}
	}
	payload, err := json.Marshal(command{T: "cmd", ID: 0, Op: op, Args: args})
	if err != nil {
		return err
	}
	return c.write(KindControl, payload)
}

// Call runs a command and decodes its result into out (nil to ignore it). A helper refusal is
// returned as *ProtocolError.
func (c *Conn) Call(ctx context.Context, op string, args any, out any) error {
	if args == nil {
		args = struct{}{}
	}
	id := c.nextID.Add(1)
	ch := make(chan replyEnvelope, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return ErrClosed
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()
	payload, err := json.Marshal(command{T: "cmd", ID: id, Op: op, Args: args})
	if err == nil {
		err = c.write(KindControl, payload)
	}
	if err != nil {
		c.forget(id)
		return err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if !r.OK {
			if r.Error != nil {
				return r.Error
			}
			return errors.New("helperipc: command failed")
		}
		if out != nil && len(r.Result) > 0 {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	}
}

func (c *Conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}
