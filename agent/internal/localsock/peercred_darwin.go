//go:build darwin

package localsock

import (
	"net"

	"golang.org/x/sys/unix"
)

// Supported says whether the other end of a Unix socket can be identified on this system.
const Supported = true

// PeerUID is the user ID of the process at the other end of a Unix socket connection (LOCAL_PEERCRED).
func PeerUID(c *net.UnixConn) (uint32, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Xucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if cerr != nil {
		return 0, cerr
	}
	return cred.Uid, nil
}
