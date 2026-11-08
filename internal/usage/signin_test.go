package usage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

/**
 * [INPUT]: 注入内存 SigninCache 的 Claude/Cursor Fetcher、可计数的 keychain/auto 假源、httptest 401
 * [OUTPUT]: 借用登录副本的行为回归：同意后的首读取源并存副本，之后的读取只碰副本；副本过期/损坏即弃并重取；API 401 拒收即弃；手填令牌优先且不入副本
 * [POS]: 2026-10-31 凭据缓存的守卫测试（读一次后不再每次触发 macOS 钥匙串询问）；全部走假源与内存缓存，不碰真实 Keychain/网络
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// memCache is an in-memory SigninCache.
type memCache struct {
	mu   sync.Mutex
	data map[string]string
}

func newMemCache() *memCache { return &memCache{data: map[string]string{}} }

func (m *memCache) Get(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	return v, ok
}

func (m *memCache) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *memCache) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func TestClaudeCachedSigninSkipsKeychain(t *testing.T) {
	srv := jsonServer(t, 200, claudeBody, nil)
	blob := fmt.Sprintf(claudeBlob, time.Now().Add(time.Hour).UnixMilli())
	calls := 0
	source := func(context.Context) ([]byte, error) { calls++; return []byte(blob), nil }
	cache := newMemCache()
	c := &claude{
		client: newHTTPClient(), endpoint: srv.URL, home: t.TempDir(),
		getenv:   func(string) string { return "" },
		keychain: source,
		cache:    cache, cacheKey: "usage.claude.signin",
	}
	for range 2 {
		if _, err := c.Fetch(ctx5(t)); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("keychain reads = %d, want 1 (the copy serves the rest)", calls)
	}
	stored, ok := cache.Get("usage.claude.signin")
	if !ok || !strings.Contains(stored, "tok-claude") {
		t.Fatalf("stored copy = %q (ok=%v), want the parsed credential", stored, ok)
	}

	// A fresh fetcher — the next app run — still reads only the copy.
	before := calls
	c2 := &claude{
		client: newHTTPClient(), endpoint: srv.URL, home: t.TempDir(),
		getenv:   func(string) string { return "" },
		keychain: source,
		cache:    cache, cacheKey: "usage.claude.signin",
	}
	if _, err := c2.Fetch(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	if calls != before {
		t.Fatalf("a fresh fetcher must serve from the copy, read %d times", calls-before)
	}
}

func TestClaudeExpiredCopyRereadsKeychain(t *testing.T) {
	srv := jsonServer(t, 200, claudeBody, nil)
	blob := fmt.Sprintf(claudeBlob, time.Now().Add(time.Hour).UnixMilli())
	calls := 0
	cache := newMemCache()
	if err := cache.Set("usage.claude.signin", `{"accessToken":"tok-old","expiresAt":1000,"subscriptionType":"max","rateLimitTier":"default_claude_max_5x"}`); err != nil {
		t.Fatal(err)
	}
	c := &claude{
		client: newHTTPClient(), endpoint: srv.URL, home: t.TempDir(),
		getenv:   func(string) string { return "" },
		keychain: func(context.Context) ([]byte, error) { calls++; return []byte(blob), nil },
		cache:    cache, cacheKey: "usage.claude.signin",
	}
	if _, err := c.Fetch(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("keychain reads = %d, want 1 after the expired copy", calls)
	}
	if stored, _ := cache.Get("usage.claude.signin"); !strings.Contains(stored, "tok-claude") {
		t.Fatalf("copy after re-source = %q, want the fresh credential", stored)
	}
}

func TestClaudeRejectedCopyIsDropped(t *testing.T) {
	srv := jsonServer(t, 401, `{}`, nil)
	cache := newMemCache()
	if err := cache.Set("usage.claude.signin", `{"accessToken":"tok-stale","expiresAt":0,"subscriptionType":"max","rateLimitTier":"default_claude_max_5x"}`); err != nil {
		t.Fatal(err)
	}
	c := &claude{
		client: newHTTPClient(), endpoint: srv.URL, home: t.TempDir(),
		getenv: func(string) string { return "" },
		keychain: func(context.Context) ([]byte, error) {
			t.Fatal("a rejected copy must not fall back to the Keychain within the same read")
			return nil, nil
		},
		cache: cache, cacheKey: "usage.claude.signin",
	}
	if _, err := c.Fetch(ctx5(t)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if _, ok := cache.Get("usage.claude.signin"); ok {
		t.Fatal("a copy the API rejected must be dropped so the next read re-sources it")
	}
}

func TestCursorCachedTokenSkipsAuto(t *testing.T) {
	srv := jsonServer(t, 200, cursorBodyJSON, nil)
	token := jwt("auth0|user_9", time.Now().Add(time.Hour))
	calls := 0
	cache := newMemCache()
	c := &cursor{
		client: newHTTPClient(), endpoint: srv.URL,
		manual: func() string { return "" },
		auto:   func(context.Context) (string, error) { calls++; return token, nil },
		cache:  cache, cacheKey: "usage.cursor.signin",
	}
	for range 2 {
		if _, err := c.Fetch(ctx5(t)); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("auto reads = %d, want 1 (the copy serves the rest)", calls)
	}
	if stored, _ := cache.Get("usage.cursor.signin"); stored != token {
		t.Fatalf("stored copy = %q, want the sourced token", stored)
	}
}

func TestCursorManualTokenWinsAndIsNeverCached(t *testing.T) {
	srv := jsonServer(t, 200, cursorBodyJSON, nil)
	cache := newMemCache()
	if err := cache.Set("usage.cursor.signin", "borrowed-but-valid-is-not-a-jwt"); err != nil {
		t.Fatal(err)
	}
	pasted := jwt("auth0|user_9", time.Now().Add(time.Hour))
	c := &cursor{
		client: newHTTPClient(), endpoint: srv.URL,
		manual: func() string { return pasted },
		auto: func(context.Context) (string, error) {
			t.Fatal("a pasted token must spare the source reads")
			return "", ErrSetup
		},
		cache: cache, cacheKey: "usage.cursor.signin",
	}
	if _, err := c.Fetch(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	if stored, _ := cache.Get("usage.cursor.signin"); stored != "borrowed-but-valid-is-not-a-jwt" {
		t.Fatalf("copy changed to %q; a pasted token never touches the copy", stored)
	}
}

func TestCursorExpiredCopyRereadsSource(t *testing.T) {
	srv := jsonServer(t, 200, cursorBodyJSON, nil)
	token := jwt("auth0|user_9", time.Now().Add(time.Hour))
	calls := 0
	cache := newMemCache()
	if err := cache.Set("usage.cursor.signin", jwt("auth0|user_9", time.Now().Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	c := &cursor{
		client: newHTTPClient(), endpoint: srv.URL,
		manual: func() string { return "" },
		auto:   func(context.Context) (string, error) { calls++; return token, nil },
		cache:  cache, cacheKey: "usage.cursor.signin",
	}
	if _, err := c.Fetch(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("auto reads = %d, want 1 after the expired copy", calls)
	}
	if stored, _ := cache.Get("usage.cursor.signin"); stored != token {
		t.Fatalf("copy after re-source = %q, want the fresh token", stored)
	}
}

func TestCursorRejectedCopyIsDropped(t *testing.T) {
	srv := jsonServer(t, 401, `{}`, nil)
	cache := newMemCache()
	if err := cache.Set("usage.cursor.signin", jwt("auth0|user_9", time.Now().Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	c := &cursor{
		client: newHTTPClient(), endpoint: srv.URL,
		manual: func() string { return "" },
		auto: func(context.Context) (string, error) {
			t.Fatal("a rejected copy must not fall back to the source within the same read")
			return "", ErrSetup
		},
		cache: cache, cacheKey: "usage.cursor.signin",
	}
	if _, err := c.Fetch(ctx5(t)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if _, ok := cache.Get("usage.cursor.signin"); ok {
		t.Fatal("a copy the API rejected must be dropped so the next read re-sources it")
	}
}

// An expired sign-in (or a denied read) at the source is not asked for again
// while it rests: each Keychain read makes macOS prompt.
func TestClaudeExpiredSourceRestsInsteadOfAskingAgain(t *testing.T) {
	blob := fmt.Sprintf(claudeBlob, time.Now().Add(-time.Hour).UnixMilli())
	calls := 0
	c := &claude{
		client: newHTTPClient(), endpoint: "http://127.0.0.1:1", home: t.TempDir(),
		getenv:   func(string) string { return "" },
		keychain: func(context.Context) ([]byte, error) { calls++; return []byte(blob), nil },
	}
	for range 3 {
		if _, err := c.Fetch(ctx5(t)); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("err = %v, want ErrUnauthorized", err)
		}
	}
	if calls != 1 {
		t.Fatalf("Keychain read %d times, want 1 while the source rests", calls)
	}
	c.blockedUntil = time.Now().Add(-time.Second) // the rest is over
	_, _ = c.Fetch(ctx5(t))
	if calls != 2 {
		t.Fatalf("Keychain read %d times after the rest, want 2", calls)
	}
}

func TestClaudeDeniedKeychainRests(t *testing.T) {
	calls := 0
	c := &claude{
		client: newHTTPClient(), endpoint: "http://127.0.0.1:1", home: t.TempDir(),
		getenv: func(string) string { return "" },
		keychain: func(context.Context) ([]byte, error) {
			calls++
			return nil, fmt.Errorf("%w: the Keychain did not allow reading", ErrUnauthorized)
		},
	}
	for range 3 {
		_, _ = c.Fetch(ctx5(t))
	}
	if calls != 1 {
		t.Fatalf("a denied Keychain was asked %d times, want 1", calls)
	}
}
