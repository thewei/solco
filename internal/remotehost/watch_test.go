package remotehost

import (
	"net/netip"
	"sync"
	"testing"
	"time"
)

/**
 * [INPUT]: 依赖 watch.go 的 netWatchInterval/watchAddresses/checkAddress/advertised、netaddr.go 可注入的 interfaceAddrs、server.go 的 Start/Reconfigure/Close
 * [OUTPUT]: 地址看门狗与 net 广播的行为测试（换址触发回调、健康不打扰、无候选不打扰、loopback 不看、Close 即停、广播只含正在服务的地址）
 * [POS]: 用注入的网卡地址表驱动（fakeNet 自带锁：看门狗在自己 goroutine 里读表）：配置地址消失且网卡有新地址＝OnAddressLost（修好即静默）；配置地址仍在＝静默；候选为空＝静默（relay 继续服务）；Close 后不再触发；advertised 在 loopback/未监听时为空，lan 监听时恰为绑定地址
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type lostReporter struct {
	mu    sync.Mutex
	fired int
	hit   chan struct{}
}

func (r *lostReporter) onLost() {
	r.mu.Lock()
	r.fired++
	r.mu.Unlock()
	select {
	case r.hit <- struct{}{}:
	default:
	}
}

func (r *lostReporter) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fired
}

// fakeNet is the test's interface table, mutex-guarded because the watcher
// reads it from its own goroutine.
type fakeNet struct {
	mu    sync.Mutex
	addrs []ifaceAddr
}

func (f *fakeNet) set(addrs []ifaceAddr) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addrs = addrs
}

func (f *fakeNet) table() []ifaceAddr {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addrs
}

// fakeInterfaces swaps the machine's addresses for the test's table and hands
// back the slot so a later stage of the same test can move the network.
func fakeInterfaces(t *testing.T, addrs []ifaceAddr) *fakeNet {
	t.Helper()
	orig := interfaceAddrs
	f := &fakeNet{addrs: addrs}
	interfaceAddrs = func() ([]ifaceAddr, error) { return f.table(), nil }
	t.Cleanup(func() { interfaceAddrs = orig })
	return f
}

func waitForFire(t *testing.T, hit chan struct{}, want int) {
	t.Helper()
	for i := range want {
		select {
		case <-hit:
		case <-time.After(5 * time.Second):
			t.Fatalf("OnAddressLost fired %d times, want %d", i, want)
		}
	}
}

func shortenWatcher(t *testing.T) {
	t.Helper()
	old := netWatchInterval
	netWatchInterval = 2 * time.Millisecond
	t.Cleanup(func() { netWatchInterval = old })
}

func watcherRunning(s *Server) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.netStop != nil
}

func TestAddressWatcherFollowsChangedAddress(t *testing.T) {
	ifaces := fakeInterfaces(t, []ifaceAddr{{"en0", netip.MustParseAddr("192.168.1.10")}})
	reporter := &lostReporter{hit: make(chan struct{}, 8)}
	srv, err := New(Options{
		Config:        Config{Mode: ModeLAN, Port: 7788, Address: "10.9.0.1"}, // configured address is no longer on the machine
		Dir:           t.TempDir(),
		Backend:       &fakeBackend{instances: []string{"default"}},
		OnAddressLost: reporter.onLost,
	})
	if err != nil {
		t.Fatal(err)
	}
	shortenWatcher(t)

	// Start fails (the stale address binds nowhere) and the watcher still runs —
	// a paired phone's relay route depends on it reporting the vanished address.
	if err := srv.Start(); err == nil {
		t.Fatal("a vanished address must not bind")
	}
	if !watcherRunning(srv) {
		t.Fatal("a failed bind must still start the address watcher")
	}
	waitForFire(t, reporter.hit, 1)

	// The heal rides the normal path (Reconfigure): quiet once the configured
	// address is on the interfaces again, whatever the bind outcome.
	ifaces.set([]ifaceAddr{{"en0", netip.MustParseAddr("192.168.5.42")}})
	if err := srv.Reconfigure(Config{Mode: ModeLAN, Port: 7788, Address: "192.168.5.42"}, RelayConfig{}); err != nil {
		t.Logf("reconfigure bind failed (address not on this machine): %v", err)
	}
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if n := reporter.count(); n > 1 {
			t.Fatalf("a healed config must not keep firing (count=%d)", n)
		}
		time.Sleep(2 * time.Millisecond)
	}
	srv.Close()
}

func TestAddressWatcherQuietWhileHealthy(t *testing.T) {
	ifaces := fakeInterfaces(t, []ifaceAddr{{"en0", netip.MustParseAddr("192.168.1.10")}})
	reporter := &lostReporter{hit: make(chan struct{}, 8)}
	srv, err := New(Options{
		Config:        Config{Mode: ModeLAN, Port: 7788, Address: "192.168.1.10"},
		Dir:           t.TempDir(),
		Backend:       &fakeBackend{instances: []string{"default"}},
		OnAddressLost: reporter.onLost,
	})
	if err != nil {
		t.Fatal(err)
	}
	shortenWatcher(t)
	if err := srv.Start(); err != nil {
		t.Logf("bind failed: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if n := reporter.count(); n != 0 {
		t.Fatalf("healthy address fired %d times, want 0", n)
	}
	// The network moves and the Mac now serves the new address (the heal has
	// already run): quiet.
	ifaces.set([]ifaceAddr{{"en0", netip.MustParseAddr("10.0.0.9")}})
	if err := srv.Reconfigure(Config{Mode: ModeLAN, Port: 7788, Address: "10.0.0.9"}, RelayConfig{}); err != nil {
		t.Logf("bind failed: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if n := reporter.count(); n != 0 {
		t.Fatalf("healed config fired %d times, want 0", n)
	}
	srv.Close()
	if watcherRunning(srv) {
		t.Fatal("Close must stop the address watcher")
	}
}

func TestAddressWatcherQuietWithoutCandidates(t *testing.T) {
	fakeInterfaces(t, nil)
	reporter := &lostReporter{hit: make(chan struct{}, 8)}
	srv, err := New(Options{
		Config:        Config{Mode: ModeLAN, Port: 7788, Address: "192.168.1.10"},
		Dir:           t.TempDir(),
		Backend:       &fakeBackend{instances: []string{"default"}},
		OnAddressLost: reporter.onLost,
	})
	if err != nil {
		t.Fatal(err)
	}
	shortenWatcher(t)
	if err := srv.Start(); err == nil {
		t.Fatal("a vanished address must not bind")
	}
	time.Sleep(20 * time.Millisecond)
	if n := reporter.count(); n != 0 {
		t.Fatalf("no candidate to follow fired %d times, want 0", n)
	}
	srv.Close()
}

func TestAddressWatcherSkipsLoopback(t *testing.T) {
	reporter := &lostReporter{hit: make(chan struct{}, 8)}
	srv, err := New(Options{
		Config:        Config{Mode: ModeLoopback, Port: 7788},
		Dir:           t.TempDir(),
		Backend:       &fakeBackend{instances: []string{"default"}},
		OnAddressLost: reporter.onLost,
	})
	if err != nil {
		t.Fatal(err)
	}
	shortenWatcher(t)
	if err := srv.Start(); err != nil {
		t.Logf("bind failed: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if n := reporter.count(); n != 0 {
		t.Fatalf("loopback fired %d times, want 0", n)
	}
	srv.Close()
}

func TestAdvertisedNamesOnlyTheServingAddress(t *testing.T) {
	srv, err := New(Options{Config: Config{Mode: ModeLoopback, Port: 7788}, Dir: t.TempDir(), Backend: &fakeBackend{instances: []string{"default"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	if got := srv.advertised(); got != nil {
		t.Fatalf("a stopped listener advertises %+v, want nothing", got)
	}
	ln, err := listenReliable("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := srv.serve(ln, Config{Mode: ModeLoopback}); err != nil {
		t.Fatal(err)
	}
	if got := srv.advertised(); got != nil {
		t.Fatalf("loopback serves nothing a phone can reach, got %+v", got)
	}
	srv.mu.Lock()
	srv.listenHost, srv.listenPort = "192.168.1.10", "7788"
	srv.mu.Unlock()
	srv.opts.Config = Config{Mode: ModeLAN, Port: 7788, Address: "192.168.1.10"}
	got := srv.advertised()
	if len(got) != 1 || got[0]["host"] != "192.168.1.10" || got[0]["port"] != 7788 {
		t.Fatalf("advertised = %+v, want the one serving address", got)
	}
}
