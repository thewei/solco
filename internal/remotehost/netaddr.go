package remotehost

import (
	"net"
	"net/netip"
	"slices"
	"strings"
)

/**
 * [INPUT]: 依赖 net 标准库（接口枚举）与 config.go 的 Mode / Tailscale 网段
 * [OUTPUT]: 对外提供 Candidate 与 Candidates(mode)：Settings 在 lan/tailscale 模式下列给用户选的网卡地址
 * [POS]: 只列「Validate 会放行」的地址（lan = 私有网段且非虚拟接口（bridge、feth、vmnet、utun 等 VM/容器/隧道网卡手机进不去）、en* 优先；tailscale = 100.64/10 与 fd7a:115c:a1e0::/48），从不替用户选，也不探测/记录 tailnet 信息；枚举与过滤分开，过滤是纯函数
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Candidate is one interface address the user may pick for a lan/tailscale
// listener.
type Candidate struct {
	Interface string
	Address   string
}

type ifaceAddr struct {
	name string
	addr netip.Addr
}

// Candidates lists this Mac's usable addresses for the mode. Off and loopback
// need none.
func Candidates(mode Mode) ([]Candidate, error) {
	all, err := interfaceAddrs()
	if err != nil {
		return nil, err
	}
	return filterCandidates(mode, all), nil
}

// interfaceAddrs enumerates the machine's up, non-loopback interface addresses.
// A variable so the address watcher's tests can fake the machine's networks.
var interfaceAddrs = func() ([]ifaceAddr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []ifaceAddr
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if addr, ok := netip.AddrFromSlice(ipn.IP); ok {
				out = append(out, ifaceAddr{ifc.Name, addr.Unmap()})
			}
		}
	}
	return out, nil
}

// virtualPrefixes are macOS interfaces a phone can never reach: the NAT
// bridges and fake-ethernet pairs of VMs/containers (bridge100, feth*,
// vmnet*, vboxnet*, vnic*), tunnels (utun*) and system links (awdl, llw, ap, anpi, gif, stf).
var virtualPrefixes = []string{"bridge", "feth", "vmnet", "vboxnet", "vnic", "utun", "awdl", "llw", "ap", "anpi", "gif", "stf"}

func isVirtualInterface(name string) bool {
	for _, p := range virtualPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ICEInterface reports whether Remote Desktop media may gather ICE candidates on an interface. Ordinary
// interfaces pass; VM bridges and system links never do; a tunnel (utun*) passes only when it carries a
// Tailscale address, because that is the one tunnel a phone can arrive on. A proxy's TUN (198.18/15), a
// VPN's own link or a system utun used to multiply every candidate by every STUN/TURN server (measured
// 2026-11-03: host=10 srflx=52 relay=36), and the burst of connectivity checks they brought was enough
// to stall the relay's keepalive on the same Mac.
func ICEInterface(name string) bool {
	if !isVirtualInterface(name) {
		return true
	}
	if !strings.HasPrefix(name, "utun") {
		return false
	}
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return false
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return false
	}
	var ips []netip.Addr
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(ipn.IP); ok {
				ips = append(ips, ip.Unmap())
			}
		}
	}
	return carriesTailscale(ips)
}

func carriesTailscale(ips []netip.Addr) bool {
	for _, ip := range ips {
		if isTailscale(ip) {
			return true
		}
	}
	return false
}

func isTailscale(a netip.Addr) bool { return tailscaleV4.Contains(a) || tailscaleV6.Contains(a) }

func filterCandidates(mode Mode, all []ifaceAddr) []Candidate {
	var out []Candidate
	for _, ia := range all {
		a := ia.addr
		if a.IsUnspecified() || a.IsLoopback() || a.IsMulticast() || a.IsLinkLocalUnicast() {
			continue
		}
		switch mode {
		case ModeLAN:
			if !a.IsPrivate() || isTailscale(a) || isVirtualInterface(ia.name) {
				continue
			}
		case ModeTailscale:
			if !isTailscale(a) {
				continue
			}
		default:
			continue
		}
		out = append(out, Candidate{Interface: ia.name, Address: a.String()})
	}
	slices.SortFunc(out, func(x, y Candidate) int {
		x4, y4 := !strings.Contains(x.Address, ":"), !strings.Contains(y.Address, ":")
		switch {
		case x4 != y4 && x4:
			return -1
		case x4 != y4:
			return 1
		}
		if xe, ye := strings.HasPrefix(x.Interface, "en"), strings.HasPrefix(y.Interface, "en"); xe != ye {
			if xe {
				return -1
			}
			return 1
		}
		if c := strings.Compare(x.Interface, y.Interface); c != 0 {
			return c
		}
		return strings.Compare(x.Address, y.Address)
	})
	return out
}
