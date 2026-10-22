package codex

import (
	"net"

	"github.com/yangflow/hoplet/agent/internal/localsock"
)

const peerCredSupported = localsock.Supported

// peerUID is the user ID of the process at the other end of a Unix socket connection.
func peerUID(c *net.UnixConn) (uint32, error) { return localsock.PeerUID(c) }
