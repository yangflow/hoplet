package shim

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// SpoolName is the spool file inside a session directory.
const SpoolName = "spool.jsonl"

// SpoolPath returns the spool file inside dir.
func SpoolPath(dir string) string { return filepath.Join(dir, SpoolName) }

// spool is the append-only record log. Appends are buffered by the OS; the
// exit record is synced so a finished session's spool is complete on disk.
type spool struct {
	mu     sync.Mutex
	f      *os.File
	path   string
	size   int64
	notify chan struct{} // closed and replaced on every append
}

func openSpool(path string) (*spool, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("shim: open spool: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("shim: stat spool: %w", err), f.Close())
	}
	return &spool{f: f, path: path, size: st.Size(), notify: make(chan struct{})}, nil
}

// append writes one record and returns the offset just after it.
func (s *spool) append(rec Record) (int64, error) {
	b, err := json.Marshal(rec)
	if err != nil {
		return 0, fmt.Errorf("shim: encode record: %w", err)
	}
	b = append(b, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.f.Write(b); err != nil {
		return 0, fmt.Errorf("shim: append record: %w", err)
	}
	if rec.Kind == KindExit {
		if err := s.f.Sync(); err != nil {
			return 0, fmt.Errorf("shim: sync spool: %w", err)
		}
	}
	s.size += int64(len(b))
	close(s.notify)
	s.notify = make(chan struct{})
	return s.size, nil
}

// state returns the current size and a channel closed by the next append.
func (s *spool) state() (int64, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size, s.notify
}

func (s *spool) close() error { return s.f.Close() }

// readRecords reads complete records from off up to limit (exclusive) and
// calls fn with each record and the offset just after it. A trailing
// partial line is never returned.
func readRecords(path string, off, limit int64, fn func(Record, int64) error) error {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("shim: open spool for reading: %w", err)
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return fmt.Errorf("shim: seek spool: %w", err)
	}
	r := bufio.NewReaderSize(io.LimitReader(f, limit-off), 64<<10)
	pos := off
	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return nil // a trailing partial line is not a record yet
		}
		if err != nil {
			return fmt.Errorf("shim: read spool: %w", err)
		}
		pos += int64(len(line))
		var rec Record
		if err := json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &rec); err != nil {
			return fmt.Errorf("shim: decode spool record at %d: %w", pos-int64(len(line)), err)
		}
		if err := fn(rec, pos); err != nil {
			return err
		}
	}
}
