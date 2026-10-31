// Package logfile is a size-rotated log file: when the file would grow past
// its limit it is renamed to <name>.1 (older copies shift up to <name>.N)
// and a new file is started.
package logfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Defaults.
const (
	DefaultMaxSize = 10 << 20
	DefaultKeep    = 3
)

// File is a rotating log file. It is safe for concurrent use; each Write
// lands whole in one file. One process owns a file: another process opening
// the same path would keep writing into a file this one has renamed.
type File struct {
	path    string
	maxSize int64
	keep    int

	mu     sync.Mutex
	f      *os.File // nil after Close, or after a rotation that lost the file
	closed bool
	size   int64
}

// Open opens path for appending (mode 0600, creating its directory 0700).
// maxSize and keep of 0 mean the defaults.
func Open(path string, maxSize int64, keep int) (*File, error) {
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	if keep <= 0 {
		keep = DefaultKeep
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("logfile: %w", err)
	}
	l := &File{path: filepath.Clean(path), maxSize: maxSize, keep: keep}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *File) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("logfile: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		return errors.Join(fmt.Errorf("logfile: %w", err), f.Close())
	}
	l.f, l.size = f, fi.Size()
	return nil
}

// Write appends p, rotating first if p would take the file past its limit.
func (l *File) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, os.ErrClosed
	}
	if l.f == nil { // an earlier rotation failed to reopen the file
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	if l.size > 0 && l.size+int64(len(p)) > l.maxSize {
		if err := l.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// rotate renames the file to .1 and starts a new one. If that fails, the
// current path is reopened for appending (best effort) so that later
// writes keep working, and the rotation error is returned.
func (l *File) rotate() error {
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("logfile: %w", err)
	}
	l.f = nil
	if err := l.shift(); err != nil {
		_ = l.open() // on failure the next Write tries again
		return err
	}
	return l.open()
}

func (l *File) shift() error {
	for i := l.keep - 1; i >= 1; i-- {
		err := os.Rename(l.path+"."+strconv.Itoa(i), l.path+"."+strconv.Itoa(i+1))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("logfile: %w", err)
		}
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil {
		return fmt.Errorf("logfile: %w", err)
	}
	return nil
}

// Close closes the file.
func (l *File) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
