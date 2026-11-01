package verified

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockPoll is how often Lock retries a lock another holder has.
const lockPoll = 100 * time.Millisecond

// Lock takes the exclusive lock file at path, creating its directory if
// needed. If another holder (another process included) has it, Lock calls
// waiting once, if not nil, and retries every 100 ms until it gets the lock
// or ctx ends, in which case it returns the context's error. It serializes
// work, such as an install or a version check, that a CLI command and the
// daemon may start at once. unlock releases the lock.
func Lock(ctx context.Context, path string, waiting func()) (unlock func(), err error) {
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("verified: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("verified: %w", err)
	}
	told := false
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if errors.Is(err, syscall.EINTR) {
			continue // interrupted, not held by another: try again at once
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("verified: lock: %w", err), f.Close())
		}
		if !told && waiting != nil {
			told = true
			waiting()
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(fmt.Errorf("verified: lock: %w", ctx.Err()), f.Close())
		case <-time.After(lockPoll):
		}
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
