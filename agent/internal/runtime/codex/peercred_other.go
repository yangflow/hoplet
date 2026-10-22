//go:build !linux && !darwin

package codex

import (
	"errors"
	"net"
)

const peerCredSupported = false

// peerUID cannot tell who is at the other end here, so the hook server is not started and the driver reports
// that the rules act on prompts only (ADR 0032).
func peerUID(*net.UnixConn) (uint32, error) {
	return 0, errors.New("peer credentials are not supported on this system")
}
