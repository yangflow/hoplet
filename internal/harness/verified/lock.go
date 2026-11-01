package verified

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock takes the exclusive lock of dir, creating the directory if needed,
// waiting for another holder (another process included) to release it, and
// returns its release. It serializes the work of one adapter directory,
// such as an install or a version check, that a CLI command and the daemon
// may start at once.
func Lock(dir string) (unlock func(), err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("verified: %w", err)
	}
	f, err := os.OpenFile(filepath.Clean(filepath.Join(dir, ".install.lock")), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("verified: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, errors.Join(fmt.Errorf("verified: lock: %w", err), f.Close())
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
