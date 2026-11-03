//go:build !darwin

package helperipc

import "net"

// Other platforms rely on the 0700 socket directory plus the launch token; a port to Linux would
// use SO_PEERCRED here.
func peerPID(c *net.UnixConn) (int, error) { return 0, nil }
