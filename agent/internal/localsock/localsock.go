// Package localsock holds what the agent's local Unix sockets share (ADR 0032, ADR 0034): the directory and
// socket modes, the peer user check and the per-process nonce that names the process a caller belongs to.
package localsock

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// MaxPath is below the shortest sun_path limit of the supported systems (104 bytes on macOS).
const MaxPath = 100

const nonceBytes = 32

// NewNonce is a random 256-bit value, hex encoded.
func NewNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Listen creates the socket `<dir>/<name>` (the directory mode 0700, the socket 0600). It fails when the system
// cannot check who connects, or when the path is too long for a Unix socket.
func Listen(dir, name string) (net.Listener, string, error) {
	if !Supported {
		return nil, "", fmt.Errorf("this system cannot check who connects to a Unix socket")
	}
	path := filepath.Join(dir, name)
	if len(path) > MaxPath {
		return nil, "", fmt.Errorf("the socket path %s is longer than %d bytes", name, MaxPath)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	if st, err := os.Stat(dir); err != nil {
		return nil, "", err
	} else if st.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, "", err
		}
	}
	// A socket left by an earlier run is replaced; anything else at the path is not ours to remove.
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, "", fmt.Errorf("%s exists and is not a socket", path)
		}
		_ = os.Remove(path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, "", err
	}
	return ln, path, nil
}
