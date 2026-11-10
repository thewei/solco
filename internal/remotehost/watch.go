package remotehost

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

/**
 * [INPUT]: 依赖 netaddr.go 的 Candidates（其 interfaceAddrs 可注入）、server.go 的 config/listenHost/listenPort/netStop、Options.OnAddressLost
 * [OUTPUT]: 对外提供 Server.watchAddresses（Start 拉起、Close 停止的地址看门狗）与 Server.advertised（bootstrap 的 net.addresses）
 * [POS]: DHCP/换网让配置地址从网卡上消失时的两个动作都只描述「事实」：看门狗每 netWatchInterval 检查一次 lan/tailscale 配置地址是否仍在网卡上，不在而网卡有可用地址时回调 OnAddressLost（候选为空＝没地方可去，relay 继续服务；loopback/off 永不检查）——真正的换址、落盘与重绑归回调方（nativeui 经 setRemote → Reconfigure，单一重绑路径）；advertised 只广播当前正在服务的那个地址（绑定失败时什么都不广播，不给手机一个没人应答的地址）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// netWatchInterval is how often the address watcher re-reads the machine's
// interfaces. A variable so a test can shorten it.
var netWatchInterval = 10 * time.Second

// deviceWatchInterval is how often the same loop looks for a paired-phone list another process rewrote — the
// desktop app and the resident service share the directory, so a Revoke made in one must also cut the other's
// live sockets and tunnel peers.
var deviceWatchInterval = 2 * time.Second

// watchAddresses starts the address watcher unless one is already running.
// Reconfigure runs Close + Start, so every new listener gets a fresh watcher.
func (s *Server) watchAddresses() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.netStop != nil {
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	s.netStop, s.netDone = stop, done
	go func() {
		defer close(done)
		tick := time.NewTicker(netWatchInterval)
		defer tick.Stop()
		devTick := time.NewTicker(deviceWatchInterval)
		defer devTick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				s.checkAddress()
			case <-devTick.C:
				s.Devices().Refresh()
			}
		}
	}()
}

func modeAddressStale(mode Mode, configured string) bool {
	cands, err := Candidates(mode)
	if err != nil || len(cands) == 0 {
		return false // nothing to follow the Mac to; the relay keeps phones served meanwhile
	}
	address := strings.TrimSpace(configured)
	return !slices.ContainsFunc(cands, func(c Candidate) bool { return c.Address == address })
}

// checkAddress fires OnAddressLost while a configured lan/tailscale address is
// no longer on the machine's interfaces and a usable one exists. Firing repeats
// every tick until the callback heals the config — its own dedupe (the
// recommended address is already saved) turns the repeats into no-ops.
func (s *Server) checkAddress() {
	cfg := s.config()
	stale := false
	if cfg.Mode == ModeLAN || cfg.Mode == ModeTailscale {
		stale = modeAddressStale(cfg.Mode, cfg.Address)
	}
	if !stale && cfg.TailscaleEnabled && cfg.Mode != ModeTailscale {
		stale = modeAddressStale(ModeTailscale, cfg.TailscaleAddress)
	}
	if !stale {
		return
	}
	s.mu.Lock()
	cb := s.opts.OnAddressLost
	s.mu.Unlock()
	if cb != nil {
		// Own goroutine: the callback hops to the app's UI lane, and Close joins
		// this watcher — a synchronous callback here could deadlock the two.
		go cb()
	}
}

// advertised names the address(es) the Host is serving right now — every
// explicit LAN / Tailscale listener bound by this Host. A stopped or
// loopback-only listener offers nothing: advertising an address nobody answers
// would send phones probing it.
func (s *Server) advertised() []map[string]any {
	s.mu.Lock()
	host, port := s.listenHost, s.listenPort
	addrs := append([]boundAddr(nil), s.listenAddrs...)
	s.mu.Unlock()
	if len(addrs) > 0 && (host == "" || addrs[0].host == host) {
		var out []map[string]any
		for _, a := range addrs {
			if a.mode != ModeLAN && a.mode != ModeTailscale {
				continue
			}
			n, err := strconv.Atoi(a.port)
			if err != nil {
				continue
			}
			out = append(out, map[string]any{"host": a.host, "port": n})
		}
		return out
	}
	if host == "" || port == "" {
		return nil
	}
	switch s.config().Mode {
	case ModeLAN, ModeTailscale:
	default:
		return nil
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return nil
	}
	return []map[string]any{{"host": host, "port": n}}
}
