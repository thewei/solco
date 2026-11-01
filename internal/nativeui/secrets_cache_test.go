package nativeui

import (
	"sync"
	"testing"
)

type countingSecrets struct {
	mu    sync.Mutex
	items map[string]string
	gets  map[string]int
}

func (c *countingSecrets) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets[key]++
	v, ok := c.items[key]
	return v, ok
}
func (c *countingSecrets) Set(key, value string) error { c.items[key] = value; return nil }
func (c *countingSecrets) Delete(key string) error     { delete(c.items, key); return nil }

// Each Keychain item is read at most once per process: a hit and a miss both
// stay answered, and only a write or delete through the cache changes them.
func TestCachedSecretsReadEachItemOnce(t *testing.T) {
	inner := &countingSecrets{items: map[string]string{"have": "v"}, gets: map[string]int{}}
	c := newCachedSecrets(inner)
	for range 3 {
		if v, ok := c.Get("have"); !ok || v != "v" {
			t.Fatalf("have = %q,%v", v, ok)
		}
		if _, ok := c.Get("missing"); ok {
			t.Fatal("missing item reported present")
		}
	}
	if inner.gets["have"] != 1 || inner.gets["missing"] != 1 {
		t.Fatalf("keychain reads = %v, want one per item", inner.gets)
	}
	if err := c.Set("missing", "now"); err != nil {
		t.Fatal(err)
	}
	if v, ok := c.Get("missing"); !ok || v != "now" || inner.gets["missing"] != 1 {
		t.Fatalf("after Set: %q,%v reads=%d", v, ok, inner.gets["missing"])
	}
	if err := c.Delete("have"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("have"); ok || inner.gets["have"] != 1 {
		t.Fatalf("after Delete: present=%v reads=%d", ok, inner.gets["have"])
	}
}

// Launching the window must not touch the Keychain: the commit key is probed
// only when a surface asks, and a test shell (no window) never probes.
func TestCommitKeyProbeIsLazyAndWindowOnly(t *testing.T) {
	s := NewShell()
	s.commitSecrets = commitMapSecrets{}
	if s.commitKeyProbed {
		t.Fatal("a fresh shell must not have probed the Keychain")
	}
	s.probeCommitKey() // no window: nothing happens
	if s.commitKeyProbed {
		t.Fatal("probe ran without a window")
	}
}
