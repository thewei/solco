package usage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeFetcher struct {
	id    ID
	calls atomic.Int32
	mu    sync.Mutex
	snap  Snapshot
	err   error
	gate  chan struct{} // when set, Fetch blocks until closed
}

func (f *fakeFetcher) ID() ID { return f.id }

func (f *fakeFetcher) Fetch(ctx context.Context) (Snapshot, error) {
	f.calls.Add(1)
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap, f.err
}

func (f *fakeFetcher) set(snap Snapshot, err error) {
	f.mu.Lock()
	f.snap, f.err = snap, err
	f.mu.Unlock()
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestService(f ...Fetcher) (*Service, *clock) {
	s := NewService(f...)
	c := &clock{t: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	s.now = c.now
	return s, c
}

var oneWindow = Snapshot{Windows: []Window{{Name: "5-hour", UsedPct: 10}}}

func TestRefreshReusesFreshReadingUnlessForced(t *testing.T) {
	f := &fakeFetcher{id: Codex, snap: oneWindow}
	s, clk := newTestService(f)
	ctx := context.Background()

	s.Refresh(ctx, []ID{Codex}, false, nil)
	s.Refresh(ctx, []ID{Codex}, false, nil) // page reopened at once: reused
	if f.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", f.calls.Load())
	}
	s.Refresh(ctx, []ID{Codex}, true, nil) // mashing Refresh inside the floor
	if f.calls.Load() != 1 {
		t.Fatalf("forced inside the floor: calls = %d, want 1", f.calls.Load())
	}
	clk.advance(forceFloor)
	s.Refresh(ctx, []ID{Codex}, true, nil)
	if f.calls.Load() != 2 {
		t.Fatalf("forced past the floor: calls = %d, want 2", f.calls.Load())
	}
	clk.advance(refreshTTL)
	s.Refresh(ctx, []ID{Codex}, false, nil)
	if f.calls.Load() != 3 {
		t.Fatalf("past the TTL: calls = %d, want 3", f.calls.Load())
	}
	if r := s.Result(Codex); r.State != StateLive || !r.Has || len(r.Snapshot.Windows) != 1 {
		t.Fatalf("result = %+v", r)
	}
}

func TestClaudeIsThrottledHarder(t *testing.T) {
	f := &fakeFetcher{id: Claude, snap: oneWindow}
	s, clk := newTestService(f)
	ctx := context.Background()
	s.Refresh(ctx, []ID{Claude}, false, nil)
	clk.advance(time.Minute - time.Second)
	s.Refresh(ctx, []ID{Claude}, true, nil)
	clk.advance(3 * time.Minute)
	s.Refresh(ctx, []ID{Claude}, false, nil)
	if f.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 inside the 5 minute reuse window", f.calls.Load())
	}
	clk.advance(2 * time.Minute)
	s.Refresh(ctx, []ID{Claude}, false, nil)
	if f.calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", f.calls.Load())
	}
}

func TestFailureKeepsLastReadingAsStale(t *testing.T) {
	f := &fakeFetcher{id: Cursor, snap: oneWindow}
	s, clk := newTestService(f)
	ctx := context.Background()
	s.Refresh(ctx, []ID{Cursor}, false, nil)

	f.set(Snapshot{}, fmt.Errorf("%w: Cursor's sign-in expired", ErrUnauthorized))
	clk.advance(time.Minute)
	s.Refresh(ctx, []ID{Cursor}, false, nil)
	r := s.Result(Cursor)
	if r.State != StateExpired || !r.Has || !r.Stale() {
		t.Fatalf("result = %+v", r)
	}
	if r.Detail != "Cursor's sign-in expired" {
		t.Errorf("detail = %q", r.Detail)
	}
	if len(r.Snapshot.Windows) != 1 {
		t.Fatal("a failed read must keep the last good windows")
	}

	f.set(oneWindow, nil)
	clk.advance(time.Minute)
	s.Refresh(ctx, []ID{Cursor}, false, nil)
	if r := s.Result(Cursor); r.State != StateLive || r.Stale() {
		t.Fatalf("recovered result = %+v", r)
	}
}

func TestRateLimitBacksOff(t *testing.T) {
	f := &fakeFetcher{id: Claude, err: &RateLimitedError{RetryAfter: 4 * time.Minute}}
	s, clk := newTestService(f)
	ctx := context.Background()
	s.Refresh(ctx, []ID{Claude}, false, nil)
	r := s.Result(Claude)
	if r.State != StateRateLimited || !r.RetryAt.Equal(clk.now().Add(4*time.Minute)) {
		t.Fatalf("result = %+v", r)
	}
	f.set(oneWindow, nil)
	clk.advance(3 * time.Minute)
	s.Refresh(ctx, []ID{Claude}, true, nil) // even a manual Refresh waits out the vendor's ask
	if f.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 while rate limited", f.calls.Load())
	}
	clk.advance(2 * time.Minute)
	s.Refresh(ctx, []ID{Claude}, false, nil)
	if f.calls.Load() != 2 || s.Result(Claude).State != StateLive {
		t.Fatalf("after the backoff: calls = %d, result = %+v", f.calls.Load(), s.Result(Claude))
	}
}

func TestStatesMapFromErrors(t *testing.T) {
	cases := []struct {
		err  error
		want State
	}{
		{fmt.Errorf("%w: none", ErrSetup), StateSetup},
		{ErrUnauthorized, StateExpired},
		{fmt.Errorf("%w: custom", ErrCustomEndpoint), StateCustomEndpoint},
		{fmt.Errorf("%w: down", ErrUnavailable), StateUnavailable},
		{errors.New("boom"), StateUnavailable},
	}
	for _, c := range cases {
		if got, _ := classify(c.err); got != c.want {
			t.Errorf("classify(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestConcurrentRefreshReadsOnce(t *testing.T) {
	f := &fakeFetcher{id: Codex, snap: oneWindow, gate: make(chan struct{})}
	s, _ := newTestService(f)
	done := make(chan struct{})
	go func() {
		s.Refresh(context.Background(), []ID{Codex}, true, nil)
		close(done)
	}()
	for !s.Busy(Codex) {
		time.Sleep(time.Millisecond)
	}
	s.Refresh(context.Background(), []ID{Codex}, true, nil) // joins nothing: skipped while busy
	close(f.gate)
	<-done
	if f.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", f.calls.Load())
	}
}

func TestRefreshIgnoresUnknownAndNotifies(t *testing.T) {
	f := &fakeFetcher{id: Codex, snap: oneWindow}
	s, _ := newTestService(f)
	var changes atomic.Int32
	s.Refresh(context.Background(), []ID{Zhipu, Codex}, false, func() { changes.Add(1) })
	if changes.Load() != 2 { // read started + read finished
		t.Fatalf("changes = %d, want 2", changes.Load())
	}
	if r := s.Result(Zhipu); r.State != StateIdle || r.Has {
		t.Fatalf("an unregistered provider stays idle: %+v", r)
	}
}

func TestClearForgetsReading(t *testing.T) {
	f := &fakeFetcher{id: Codex, snap: oneWindow}
	s, _ := newTestService(f)
	s.Refresh(context.Background(), []ID{Codex}, false, nil)
	s.Clear(Codex)
	if r := s.Result(Codex); r.Has || r.State != StateIdle {
		t.Fatalf("result = %+v", r)
	}
}
