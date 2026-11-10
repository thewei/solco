package rtc

import (
	"net"
	"net/netip"
	"strings"
	"sync"
)

// Why this file: Pion gathers one server-reflexive candidate per (local address × STUN URL) and one relayed
// candidate per (local address × TURN URL). A Mac with a few IPv6 privacy addresses and a Tailscale tunnel,
// handed the relay's 4 STUN + 6 TURN URLs, produced host=14 srflx=52 relay=82 (measured 2026-11-03): ~150
// candidates, ~1000 pairs the phone and Pion re-ping every 200 ms. The burst alone stalled the relay's
// keepalive on the same Mac, and ICE took seconds to minutes. One working STUN and one UDP + one TCP/TLS
// TURN entry are all a path ever needs, and so is one IPv6 address.

// maxSTUN / maxTURNPerKind cap the URLs one peer uses.
const (
	maxSTUN        = 2
	maxTURNPerKind = 1
)

// PruneICEServers keeps at most 2 distinct STUN hosts and 1 UDP + 1 TCP/TLS TURN entry.
func PruneICEServers(in []ICEServer) []ICEServer { return pruneICEServers(in) }

// IPFilter returns a per-peer filter keeping IPv4, Tailscale IPv6 ULA, and one global IPv6 address.
func IPFilter() func(net.IP) bool { return ipFilter() }

// pruneICEServers keeps at most maxSTUN STUN URLs (distinct hosts, list order — the Host puts the entries
// that work from mainland China first) and the first TURN URL of each transport family (UDP, TCP/TLS).
func pruneICEServers(in []ICEServer) []ICEServer {
	var out []ICEServer
	stunHosts := map[string]bool{}
	turnKinds := map[string]int{}
	for _, s := range in {
		var urls []string
		for _, u := range s.URLs {
			switch {
			case strings.HasPrefix(u, "stun:"), strings.HasPrefix(u, "stuns:"):
				host := iceHost(u)
				if len(stunHosts) >= maxSTUN || stunHosts[host] {
					continue
				}
				stunHosts[host] = true
				urls = append(urls, u)
			case strings.HasPrefix(u, "turn:"), strings.HasPrefix(u, "turns:"):
				kind := turnKind(u)
				if turnKinds[kind] >= maxTURNPerKind {
					continue
				}
				turnKinds[kind]++
				urls = append(urls, u)
			}
		}
		if len(urls) > 0 {
			out = append(out, ICEServer{URLs: urls, Username: s.Username, Credential: s.Credential})
		}
	}
	return out
}

func iceHost(u string) string {
	_, rest, _ := strings.Cut(u, ":")
	rest, _, _ = strings.Cut(rest, "?")
	if h, _, err := net.SplitHostPort(rest); err == nil {
		rest = h
	}
	return strings.ToLower(rest) // two ports of one host are one server
}

func turnKind(u string) string {
	if strings.HasPrefix(u, "turns:") || strings.Contains(u, "transport=tcp") {
		return "tcp"
	}
	return "udp"
}

// ipFilter keeps the addresses worth gathering on: every IPv4 one (never link-local), Tailscale's IPv6 ULA,
// and a single global IPv6 address — the Mac's several privacy/temporary addresses are the same path. One
// filter per peer: it remembers which IPv6 address it took.
func ipFilter() func(net.IP) bool {
	var mu sync.Mutex
	took := false
	return func(ip net.IP) bool {
		a, ok := netip.AddrFromSlice(ip)
		if !ok {
			return false
		}
		a = a.Unmap()
		if a.IsLinkLocalUnicast() || a.IsLoopback() || a.IsMulticast() || a.IsUnspecified() {
			return false
		}
		if a.Is4() {
			return true
		}
		if tailscaleV6.Contains(a) {
			return true
		}
		if !a.IsGlobalUnicast() || a.IsPrivate() {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		if took {
			return false
		}
		took = true
		return true
	}
}

var tailscaleV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
