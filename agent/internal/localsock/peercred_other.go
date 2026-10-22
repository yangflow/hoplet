//go:build !linux && !darwin

package localsock

import (
	"errors"
	"net"
)

// Supported says whether the other end of a Unix socket can be identified on this system.
const Supported = false

// PeerUID cannot tell who is at the other end here, so the sockets that need it are not started and the drivers report
// that the rules act on prompts only (ADR 0032).
func PeerUID(*net.UnixConn) (uint32, error) {
	return 0, errors.New("peer credentials are not supported on this system")
}
