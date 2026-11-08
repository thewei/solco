//go:build android

package tailcatbridge

import "tailscale.com/net/netmon"

func applyDefaults(iface, gateway string) {
	netmon.UpdateLastKnownDefaultRouteInterface(iface)
	netmon.UpdateLastKnownDefaultGateway(gateway)
}
