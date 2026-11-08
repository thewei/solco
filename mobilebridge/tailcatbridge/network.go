package tailcatbridge

// [INPUT]: a JSON network snapshot supplied by the phone app (Android ConnectivityManager / LinkProperties)
// [OUTPUT]: SetNetworkState, and a tailscale netmon interface getter that serves that snapshot
// [POS]: Android 11+ (SELinux) forbids an app's own netlink RTM_GETLINK/GETADDR, so Go's net.Interfaces — which the
//
//	tunnel's network monitor calls at start — fails with "netlinkrib: permission denied" (measured 2026-11-06 on an
//	API 35 emulator under an app uid). The documented upstream answer is netmon.RegisterInterfaceGetter fed by the
//	platform's own connectivity API; the app calls SetNetworkState at start and on every network change. Without a
//	snapshot (iOS, desktop tests) the getter falls back to net.Interfaces.
//
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

import (
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"sync"

	"tailscale.com/net/netmon"
)

type ifaceState struct {
	Name     string   `json:"name"`
	Index    int      `json:"index"`
	MTU      int      `json:"mtu"`
	Up       bool     `json:"up"`
	Loopback bool     `json:"loopback"`
	Addrs    []string `json:"addrs"` // CIDR text
}

type networkState struct {
	Interfaces       []ifaceState `json:"interfaces"`
	DefaultInterface string       `json:"defaultInterface"`
	Gateway          string       `json:"gateway"`
}

var (
	netMu   sync.RWMutex
	netSnap []netmon.Interface
	netSet  bool
)

func init() { netmon.RegisterInterfaceGetter(interfaceList) }

// SetNetworkState installs the phone's current interfaces. It is cheap and may be called on every network callback.
func SetNetworkState(stateJSON string) error {
	var st networkState
	if err := json.Unmarshal([]byte(stateJSON), &st); err != nil {
		return errors.New("invalid network state")
	}
	list := make([]netmon.Interface, 0, len(st.Interfaces))
	for _, i := range st.Interfaces {
		if i.Name == "" || len(i.Name) > 64 {
			continue
		}
		var flags net.Flags
		if i.Up {
			flags |= net.FlagUp | net.FlagRunning
		}
		if i.Loopback {
			flags |= net.FlagLoopback
		} else {
			flags |= net.FlagMulticast | net.FlagBroadcast
		}
		ni := &net.Interface{Index: i.Index, MTU: i.MTU, Name: i.Name, Flags: flags}
		var addrs []net.Addr
		for _, a := range i.Addrs {
			p, err := netip.ParsePrefix(a)
			if err != nil {
				continue
			}
			addrs = append(addrs, &net.IPNet{IP: p.Addr().AsSlice(), Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen())})
		}
		if addrs == nil {
			addrs = []net.Addr{}
		}
		list = append(list, netmon.Interface{Interface: ni, AltAddrs: addrs})
	}
	netMu.Lock()
	netSnap, netSet = list, true
	netMu.Unlock()
	applyDefaults(st.DefaultInterface, st.Gateway)
	return nil
}

func interfaceList() ([]netmon.Interface, error) {
	netMu.RLock()
	snap, set := netSnap, netSet
	netMu.RUnlock()
	if set {
		return append([]netmon.Interface(nil), snap...), nil
	}
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]netmon.Interface, len(ifs))
	for i := range ifs {
		out[i].Interface = &ifs[i]
	}
	return out, nil
}
