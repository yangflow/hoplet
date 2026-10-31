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
	"time"
)

// SpoolName is the spool file inside a session directory.
const SpoolName = "spool.jsonl"

// SpoolPath returns the spool file inside dir.
func SpoolPath(dir string) string { return filepath.Join(dir, SpoolName) }

// errCorrupt marks a spool line that does not decode.
var errCorrupt = errors.New("corrupt spool record")

// spoolFile is the write side of the spool file. *os.File satisfies it; tests
// substitute one that fails.
type spoolFile interface {
	Write([]byte) (int, error)
	Sync() error
	Truncate(int64) error
	Close() error
}

// spool is the append-only record log. Appends are buffered by the OS; the
// exit record is synced so a finished session's spool is complete on disk.
type spool struct {
	mu     sync.Mutex
	f      *os.File  // reads (boundary checks) and the underlying file
	w      spoolFile // every write goes through here
	path   string
	size   int64
	notify chan struct{} // closed and replaced on every append
}

// openSpool opens (creating if needed) the spool at path. Bytes after the
// last newline are a record torn by a crash; they are cut off so appends
// start on a record boundary.
func openSpool(path string) (*spool, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("shim: open spool: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("shim: stat spool: %w", err), f.Close())
	}
	size, err := completeLength(f, st.Size())
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	if size != st.Size() {
		if err := f.Truncate(size); err != nil {
			return nil, errors.Join(fmt.Errorf("shim: truncate torn spool tail: %w", err), f.Close())
		}
	}
	return &spool{f: f, w: f, path: path, size: size, notify: make(chan struct{})}, nil
}

// completeLength returns the offset just after the last newline in the first
// size bytes of f, or 0 when there is none.
func completeLength(f *os.File, size int64) (int64, error) {
	buf := make([]byte, 64<<10)
	for end := size; end > 0; {
		start := max(end-int64(len(buf)), 0)
		chunk := buf[:end-start]
		if _, err := f.ReadAt(chunk, start); err != nil {
			return 0, fmt.Errorf("shim: scan spool tail: %w", err)
		}
		if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
			return start + int64(i) + 1, nil
		}
		end = start
	}
	return 0, nil
}

// append stamps rec with the current time, writes it and returns the offset
// just after it. A failed write is rolled back so the spool never keeps a
// partial record.
func (s *spool) append(rec Record) (int64, error) {
	rec.T = time.Now().UnixMilli()
	b, err := json.Marshal(rec)
	if err != nil {
		return 0, fmt.Errorf("shim: encode record: %w", err)
	}
	b = append(b, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Write(b); err != nil {
		return 0, errors.Join(fmt.Errorf("shim: append record: %w", err), s.w.Truncate(s.size))
	}
	s.size += int64(len(b))
	close(s.notify)
	s.notify = make(chan struct{})
	if rec.Kind == KindExit {
		// The record is written and visible to readers even if it is not yet
		// durable, so the size and the returned offset stay truthful.
		if err := s.w.Sync(); err != nil {
			return s.size, fmt.Errorf("shim: sync spool: %w", err)
		}
	}
	return s.size, nil
}

// state returns the current size and a channel closed by the next append.
func (s *spool) state() (int64, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size, s.notify
}

// boundary reports whether off is a place a record can start: the beginning
// of the spool or just after a newline.
func (s *spool) boundary(off int64) bool {
	if off == 0 {
		return true
	}
	var b [1]byte
	if _, err := s.f.ReadAt(b[:], off-1); err != nil {
		return false
	}
	return b[0] == '\n'
}

func (s *spool) close() error { return s.w.Close() }

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
			return fmt.Errorf("shim: spool record at %d: %w: %w", pos-int64(len(line)), errCorrupt, err)
		}
		if err := fn(rec, pos); err != nil {
			return err
		}
	}
}
