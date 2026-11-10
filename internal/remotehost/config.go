package remotehost

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

/**
 * [INPUT]: 依赖 net/netip 标准库
 * [OUTPUT]: 对外提供 Mode（off/loopback/lan/tailscale）、Config、ParseMode、Config.Validate 与 Config.ListenAddr
 * [POS]: Remote Host 的监听策略纯数据层：决定「绑哪个地址」，永不返回 0.0.0.0/::；不开任何 socket
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// DefaultPort is the Host's default TCP port.
const DefaultPort = 7788

// Mode is the explicit listener policy. The zero value is Off.
type Mode string

const (
	ModeOff       Mode = "off"
	ModeLoopback  Mode = "loopback"
	ModeLAN       Mode = "lan"
	ModeTailscale Mode = "tailscale"
)

// ParseMode maps stored text to a Mode; "" is Off, anything unknown is an error
// (a typo must never silently widen exposure).
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case "", ModeOff:
		return ModeOff, nil
	case ModeLoopback, ModeLAN, ModeTailscale:
		return m, nil
	default:
		return ModeOff, fmt.Errorf("unknown remote access mode %q", s)
	}
}

// Config is the user's Remote Access choice. Address is the single interface
// address the user selected for lan/tailscale; it is ignored for off/loopback.
// TailscaleEnabled / TailscaleAddress optionally bind the Mac's Tailscale
// interface concurrently alongside ModeLAN so both LAN and Tailscale IPs serve.
type Config struct {
	Mode             Mode
	Port             int
	Address          string
	TailscaleEnabled bool
	TailscaleAddress string
	// Tailcat serves the same Host over the built-in Tailcat tunnel (Options.Overlay). It needs no address or
	// port: nothing is opened on the Mac's own interfaces for it. Mode then names only the optional direct fallback.
	Tailcat bool
}

// Enabled reports whether this config serves anything at all (an OS listener, the tunnel, or both).
func (c Config) Enabled() bool {
	return c.Tailcat || (c.Mode != ModeOff && c.Mode != "") || c.TailscaleEnabled || strings.TrimSpace(c.TailscaleAddress) != ""
}

var (
	tailscaleV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailscaleV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

func validateInterfaceAddr(mode Mode, raw string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%s mode needs one interface address: %w", mode, err)
	}
	addr = addr.Unmap()
	switch {
	case addr.IsUnspecified():
		return netip.Addr{}, fmt.Errorf("%s mode never listens on every interface (%s)", mode, addr)
	case addr.IsLoopback() || addr.IsMulticast():
		return netip.Addr{}, fmt.Errorf("%s is not a network interface address", addr)
	case mode == ModeLAN && !(addr.IsPrivate() || addr.IsLinkLocalUnicast()):
		return netip.Addr{}, fmt.Errorf("%s is not a private local-network address", addr)
	case mode == ModeTailscale && !(tailscaleV4.Contains(addr) || tailscaleV6.Contains(addr)):
		return netip.Addr{}, fmt.Errorf("%s is not a Tailscale address", addr)
	}
	return addr, nil
}

// Validate reports whether the config may be served.
func (c Config) Validate() error {
	hasPrimary := c.Mode != ModeOff && c.Mode != ""
	hasTS := c.TailscaleEnabled || strings.TrimSpace(c.TailscaleAddress) != ""
	if !hasPrimary && !hasTS {
		return nil
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port %d out of range", c.Port)
	}
	if hasPrimary {
		switch c.Mode {
		case ModeLoopback:
		case ModeLAN, ModeTailscale:
			if _, err := validateInterfaceAddr(c.Mode, c.Address); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown remote access mode %q", c.Mode)
		}
	}
	if hasTS {
		if _, err := validateInterfaceAddr(ModeTailscale, c.TailscaleAddress); err != nil {
			return err
		}
	}
	return nil
}

// ListenAddr returns the primary host:port to bind. Off has none. It validates first,
// so a bad config can never produce a wildcard bind.
func (c Config) ListenAddr() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	switch c.Mode {
	case ModeOff, "":
		if c.TailscaleEnabled || strings.TrimSpace(c.TailscaleAddress) != "" {
			addr, _ := netip.ParseAddr(strings.TrimSpace(c.TailscaleAddress))
			return net.JoinHostPort(addr.Unmap().String(), fmt.Sprint(c.Port)), nil
		}
		return "", nil
	case ModeLoopback:
		return net.JoinHostPort("127.0.0.1", fmt.Sprint(c.Port)), nil
	default:
		addr, _ := netip.ParseAddr(strings.TrimSpace(c.Address))
		return net.JoinHostPort(addr.Unmap().String(), fmt.Sprint(c.Port)), nil
	}
}

type listenTarget struct {
	mode Mode
	addr string
	err  error
}

// listenTargets returns every explicit OS interface address to bind (primary plus
// optional concurrent Tailscale interface). Never emits a wildcard bind.
func (c Config) listenTargets() []listenTarget {
	var out []listenTarget
	seen := map[string]bool{}
	add := func(mode Mode, hostPort string, err error) {
		if err == nil && hostPort != "" {
			if seen[hostPort] {
				return
			}
			seen[hostPort] = true
		}
		out = append(out, listenTarget{mode: mode, addr: hostPort, err: err})
	}
	if c.Mode != ModeOff && c.Mode != "" {
		if c.Port < 1 || c.Port > 65535 {
			add(c.Mode, "", fmt.Errorf("port %d out of range", c.Port))
		} else {
			switch c.Mode {
			case ModeLoopback:
				add(ModeLoopback, net.JoinHostPort("127.0.0.1", fmt.Sprint(c.Port)), nil)
			case ModeLAN, ModeTailscale:
				if addr, err := validateInterfaceAddr(c.Mode, c.Address); err != nil {
					add(c.Mode, "", err)
				} else {
					add(c.Mode, net.JoinHostPort(addr.String(), fmt.Sprint(c.Port)), nil)
				}
			default:
				add(c.Mode, "", fmt.Errorf("unknown remote access mode %q", c.Mode))
			}
		}
	}
	if c.TailscaleEnabled || strings.TrimSpace(c.TailscaleAddress) != "" {
		if c.Port < 1 || c.Port > 65535 {
			add(ModeTailscale, "", fmt.Errorf("port %d out of range", c.Port))
		} else if addr, err := validateInterfaceAddr(ModeTailscale, c.TailscaleAddress); err != nil {
			add(ModeTailscale, "", err)
		} else {
			add(ModeTailscale, net.JoinHostPort(addr.String(), fmt.Sprint(c.Port)), nil)
		}
	}
	return out
}
