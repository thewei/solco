package usage

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
)

/**
 * [INPUT]: 一组 Fetcher 与调用方给的 ids/force 刷新请求
 * [OUTPUT]: Service（NewService/Refresh/Result/Busy/Clear）：每家独立的内存缓存、节流、限流退避与“失败保留上次读数”
 * [POS]: Usage 页与 Fetcher 之间的唯一调度者：只在页面打开/手动刷新时被调用（无后台定时器，2026-10-30 产品决策）；不落盘，退出即清空；日志只记 provider、状态与耗时
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	// refreshTTL is how long a reading is reused when the page opens again.
	refreshTTL = 30 * time.Second
	// claudeTTL is longer: Anthropic's usage endpoint throttles hard.
	claudeTTL = 5 * time.Minute
	// forceFloor is the least gap a manual Refresh honours, so a mashed
	// button cannot flood a vendor.
	forceFloor = 10 * time.Second
	// claudeForceFloor is the same for Claude.
	claudeForceFloor = time.Minute
	// defaultBackoff is the wait after a 429 that names no time.
	defaultBackoff = 5 * time.Minute
	// serviceTimeout bounds one provider's whole read.
	serviceTimeout = 25 * time.Second
)

func ttlOf(id ID) time.Duration {
	if id == Claude {
		return claudeTTL
	}
	return refreshTTL
}

func floorOf(id ID) time.Duration {
	if id == Claude {
		return claudeForceFloor
	}
	return forceFloor
}

// Service caches the latest reading of each provider and decides when a new
// one is due.
type Service struct {
	mu       sync.Mutex
	fetchers map[ID]Fetcher
	results  map[ID]Result
	busy     map[ID]bool
	now      func() time.Time
}

// NewService serves the given fetchers.
func NewService(fetchers ...Fetcher) *Service { return NewServiceClock(time.Now, fetchers...) }

// NewServiceClock is NewService on a given clock, so a caller can step past
// the reuse window and floor without sleeping.
func NewServiceClock(now func() time.Time, fetchers ...Fetcher) *Service {
	s := &Service{
		fetchers: map[ID]Fetcher{},
		results:  map[ID]Result{},
		busy:     map[ID]bool{},
		now:      now,
	}
	for _, f := range fetchers {
		s.fetchers[f.ID()] = f
	}
	return s
}

// Result is the standing of one provider.
func (s *Service) Result(id ID) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.results[id]
	r.ID = id
	return r
}

// Busy reports a read in flight for id.
func (s *Service) Busy(id ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy[id]
}

// Clear forgets a provider's reading (it was switched off, or its sign-in
// changed).
func (s *Service) Clear(id ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.results, id)
}

// due reports whether a read of id should start now.
func (s *Service) due(id ID, force bool) bool {
	r, seen := s.results[id]
	now := s.now()
	if r.State == StateRateLimited && now.Before(r.RetryAt) {
		return false
	}
	if !seen || r.Checked.IsZero() {
		return true
	}
	gap := now.Sub(r.Checked)
	if force {
		return gap >= floorOf(id)
	}
	if r.State == StateLive || r.State == StateCustomEndpoint {
		return gap >= ttlOf(id)
	}
	// A failed read is retried when the page is reopened, after the floor.
	return gap >= floorOf(id)
}

// Refresh reads the given providers that are due, concurrently, and returns
// when all are done. changed is called (from any goroutine) when a read
// starts and when it ends, so the caller can repaint. force is a manual
// Refresh: it ignores the reuse window but not the floor or a rate limit.
func (s *Service) Refresh(ctx context.Context, ids []ID, force bool, changed func()) {
	if changed == nil {
		changed = func() {}
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		f := s.fetchers[id]
		if f == nil {
			continue
		}
		s.mu.Lock()
		if s.busy[id] || !s.due(id, force) {
			s.mu.Unlock()
			continue
		}
		s.busy[id] = true
		s.mu.Unlock()
		changed()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.read(ctx, f)
			changed()
		}()
	}
	wg.Wait()
}

func (s *Service) read(ctx context.Context, f Fetcher) {
	id := f.ID()
	started := s.now()
	ctx, cancel := context.WithTimeout(ctx, serviceTimeout)
	defer cancel()
	snap, err := f.Fetch(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy[id] = false
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return // the page went away; keep what was there
	}
	r := s.results[id]
	r.ID, r.Checked = id, s.now()
	if err == nil {
		r.Snapshot, r.Has, r.State, r.Detail, r.RetryAt = snap, true, StateLive, "", time.Time{}
	} else {
		r.State, r.Detail = classify(err)
		r.RetryAt = time.Time{}
		var limited *RateLimitedError
		if errors.As(err, &limited) {
			wait := limited.RetryAfter
			if wait <= 0 {
				wait = defaultBackoff
				if id == Claude {
					wait = 15 * time.Minute
				}
			}
			r.RetryAt = s.now().Add(wait)
		}
	}
	s.results[id] = r
	slog.Info("usage read", "provider", string(id), "state", int(r.State), "ms", s.now().Sub(started).Milliseconds())
}

// classify maps a failure to its State and a short, secret-free text.
func classify(err error) (State, string) {
	var limited *RateLimitedError
	switch {
	case errors.As(err, &limited):
		return StateRateLimited, "Rate limited"
	case errors.Is(err, ErrSetup):
		return StateSetup, detailOf(err, ErrSetup)
	case errors.Is(err, ErrUnauthorized):
		return StateExpired, detailOf(err, ErrUnauthorized)
	case errors.Is(err, ErrCustomEndpoint):
		return StateCustomEndpoint, detailOf(err, ErrCustomEndpoint)
	case errors.Is(err, ErrUnavailable):
		return StateUnavailable, detailOf(err, ErrUnavailable)
	}
	return StateUnavailable, "Couldn't read usage"
}

// detailOf is the part of err after its sentinel ("sentinel: detail"), or the
// sentinel's own text.
func detailOf(err, sentinel error) string {
	text := err.Error()
	prefix := sentinel.Error() + ": "
	if strings.HasPrefix(text, prefix) {
		text = strings.TrimPrefix(text, prefix)
	}
	if text == "" {
		return sentinel.Error()
	}
	return strings.ToUpper(text[:1]) + text[1:]
}
