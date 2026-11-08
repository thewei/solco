package helperipc

import (
	"net"
	"syscall"
)

// macOS: getsockopt(SOL_LOCAL, LOCAL_PEERPID) names the process on the other end of a Unix socket.
const (
	solLocal     = 0
	localPeerPID = 0x002
)

func peerPID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var serr error
	if err := raw.Control(func(fd uintptr) {
		pid, serr = syscall.GetsockoptInt(int(fd), solLocal, localPeerPID)
	}); err != nil {
		return 0, err
	}
	return pid, serr
}
