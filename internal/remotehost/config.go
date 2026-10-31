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
type Config struct {
	Mode    Mode
	Port    int
	Address string
}

var (
	tailscaleV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailscaleV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// Validate reports whether the config may be served.
func (c Config) Validate() error {
	if c.Mode == ModeOff || c.Mode == "" {
		return nil
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port %d out of range", c.Port)
	}
	switch c.Mode {
	case ModeLoopback:
		return nil
	case ModeLAN, ModeTailscale:
		addr, err := netip.ParseAddr(strings.TrimSpace(c.Address))
		if err != nil {
			return fmt.Errorf("%s mode needs one interface address: %w", c.Mode, err)
		}
		addr = addr.Unmap()
		switch {
		case addr.IsUnspecified():
			return fmt.Errorf("%s mode never listens on every interface (%s)", c.Mode, addr)
		case addr.IsLoopback() || addr.IsMulticast():
			return fmt.Errorf("%s is not a network interface address", addr)
		case c.Mode == ModeLAN && !(addr.IsPrivate() || addr.IsLinkLocalUnicast()):
			return fmt.Errorf("%s is not a private local-network address", addr)
		case c.Mode == ModeTailscale && !(tailscaleV4.Contains(addr) || tailscaleV6.Contains(addr)):
			return fmt.Errorf("%s is not a Tailscale address", addr)
		}
		return nil
	default:
		return fmt.Errorf("unknown remote access mode %q", c.Mode)
	}
}

// ListenAddr returns the host:port to bind. Off has none. It validates first,
// so a bad config can never produce a wildcard bind.
func (c Config) ListenAddr() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	switch c.Mode {
	case ModeOff, "":
		return "", nil
	case ModeLoopback:
		return net.JoinHostPort("127.0.0.1", fmt.Sprint(c.Port)), nil
	default:
		addr, _ := netip.ParseAddr(strings.TrimSpace(c.Address))
		return net.JoinHostPort(addr.Unmap().String(), fmt.Sprint(c.Port)), nil
	}
}
