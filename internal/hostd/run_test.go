package hostd

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/internal/remotehost"
	"github.com/thewei/solco/internal/settings"
)

type fakeHost struct {
	mu     sync.Mutex
	cfgs   []remotehost.Config
	closed bool
	failOn bool
}

func (h *fakeHost) SetP2P(remotehost.P2PConfig) {}
func (h *fakeHost) Reconfigure(c remotehost.Config, _ remotehost.RelayConfig) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failOn {
		return errors.New("bind failed")
	}
	h.cfgs = append(h.cfgs, c)
	return nil
}
func (h *fakeHost) Close()         { h.mu.Lock(); h.closed = true; h.mu.Unlock() }
func (h *fakeHost) isClosed() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.closed }
func (h *fakeHost) configs() int   { h.mu.Lock(); defer h.mu.Unlock(); return len(h.cfgs) }

type rig struct {
	dir  string
	mode string
	mu   sync.Mutex
	made []*fakeHost
	stop context.CancelFunc
	done chan struct{}
}

func (r *rig) setMode(m string) { r.mu.Lock(); r.mode = m; r.mu.Unlock() }
func (r *rig) hosts() []*fakeHost {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*fakeHost(nil), r.made...)
}

func newRig(t *testing.T, mode string) *rig {
	t.Helper()
	r := &rig{dir: t.TempDir(), mode: mode, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	r.stop = cancel
	go func() {
		defer close(r.done)
		_ = Run(ctx, Options{
			Dir: r.dir,
			Settings: func() settings.RemoteAccessSettings {
				r.mu.Lock()
				defer r.mu.Unlock()
				return settings.RemoteAccessSettings{Mode: r.mode, Port: 7788, Address: "192.168.1.5"}
			},
			New: func() (Host, error) {
				h := &fakeHost{}
				r.mu.Lock()
				r.made = append(r.made, h)
				r.mu.Unlock()
				return h, nil
			},
			Poll: 10 * time.Millisecond,
		})
	}()
	t.Cleanup(func() { r.stop(); <-r.done })
	return r
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

// SOLCO-R01/R02: the resident service serves while the desktop is away, yields when the desktop claims, and
// takes the port back when the desktop leaves — never two owners at once.
func TestResidentHostYieldsToTheDesktopAndTakesTheLeaseBack(t *testing.T) {
	r := newRig(t, "lan")
	eventually(t, "daemon serves", func() bool { return len(r.hosts()) == 1 && r.hosts()[0].configs() == 1 })

	lease, err := Claim(r.dir, 3*time.Second)
	if err != nil {
		t.Fatalf("desktop claim: %v", err)
	}
	if !r.hosts()[0].isClosed() {
		t.Fatal("the resident Host must be closed before the desktop owns the port")
	}
	if _, err := TryAcquire(r.dir); !errors.Is(err, ErrHeld) {
		t.Fatalf("a second owner got the lease: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if len(r.hosts()) != 1 {
		t.Fatal("the resident service re-took the port while the desktop holds it")
	}

	lease.Release() // the desktop quits
	eventually(t, "daemon takes over again", func() bool { return len(r.hosts()) == 2 && r.hosts()[1].configs() == 1 })
}

func TestResidentHostIsQuietWhileSwitchedOffAndFollowsSettings(t *testing.T) {
	r := newRig(t, "off")
	time.Sleep(60 * time.Millisecond)
	if len(r.hosts()) != 0 {
		t.Fatal("an off Host must not listen")
	}
	if l, err := TryAcquire(r.dir); err != nil {
		t.Fatalf("an off resident service must not hold the lease: %v", err)
	} else {
		l.Release()
	}
	r.setMode("lan")
	eventually(t, "serves once switched on", func() bool { return len(r.hosts()) == 1 && r.hosts()[0].configs() == 1 })
	r.setMode("off")
	eventually(t, "closes when switched off", func() bool { return r.hosts()[0].isClosed() })
	if l, err := TryAcquire(r.dir); err != nil {
		t.Fatalf("lease not released after off: %v", err)
	} else {
		l.Release()
	}
}

func TestClaimWithoutAnOwnerIsImmediateAndAStuckOwnerIsRefused(t *testing.T) {
	dir := t.TempDir()
	l, err := Claim(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Another claimant against a holder that never yields: refused, not double-listening.
	if _, err := Claim(dir, 150*time.Millisecond); !errors.Is(err, ErrHeld) {
		t.Fatalf("want ErrHeld, got %v", err)
	}
	if YieldRequested(dir, time.Now()) {
		t.Fatal("a failed claim must withdraw its yield request")
	}
	l.Release()
	l.Release() // twice is safe
}

func TestStaleYieldRequestDoesNotKeepTheResidentServiceDown(t *testing.T) {
	dir := t.TempDir()
	if err := RequestYield(dir); err != nil {
		t.Fatal(err)
	}
	if YieldRequested(dir, time.Now().Add(YieldFresh+time.Second)) {
		t.Fatal("an old request must expire")
	}
}
