package codex

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/klauspost/compress/zstd"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// readStep is how far Read extends its window while looking for the LF of an oversized line.
const readStep int64 = 1 << 20

// source is one Codex thread. Its bytes are the uncompressed rollout, whichever encoding the file is in.
type source struct {
	mu   sync.Mutex
	file rolloutFile // the last scan's stat

	metaOK bool
	id     string
	cwd    string

	// total is the uncompressed length of the .jsonl.zst described by totalInfo, learned when a read reached
	// its end. A compressed rollout is immutable, so the length holds while the file is the same.
	total     int64
	totalInfo os.FileInfo

	// rmu serializes Read and guards stream.
	rmu    sync.Mutex
	stream *zstStream
}

var _ provider.Source = (*source)(nil)

// update installs the stat of a new scan.
func (s *source) update(f rolloutFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.file = f
}

// ready reads the first line once the rollout has one. A rollout is created by its first turn, so a file
// whose first line is still being written is not a session yet.
func (s *source) ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metaOK {
		return true
	}
	for _, path := range []string{s.file.base, s.file.base + ".zst"} {
		if id, cwd, ok := readMeta(path); ok {
			s.id, s.cwd, s.metaOK = id, cwd, true
			return true
		}
	}
	return false
}

func (s *source) Provider() string { return Name }

func (s *source) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Dir is the rollout's date directory under the sessions directory, YYYY/MM/DD.
func (s *source) Dir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.dir
}

func (s *source) Cwd() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cwd
}

func (s *source) InProject(path string) bool {
	cwd := s.Cwd()
	return cwd != "" && filepath.Clean(cwd) == path
}

// Size is the uncompressed length as of the last scan: the file size of a .jsonl, and for a .jsonl.zst the
// length a read has found, or -1 until one has.
func (s *source) Size(_ context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file.plain != nil {
		return s.file.plain.Size(), nil
	}
	if s.file.zst != nil && s.totalInfo != nil && sameFile(s.totalInfo, s.file.zst) {
		return s.total, nil
	}
	return -1, nil
}

// Read returns the uncompressed bytes in [offset, offset+max) cut at the last LF. A .jsonl is read at the
// offset; a .jsonl.zst is decompressed from its start up to the offset, once per run of sequential reads. When
// both encodings exist, or one replaces the other between two calls, the one that holds the offset is read:
// the bytes of a rollout are the same prefix in either.
func (s *source) Read(ctx context.Context, offset, max int64) ([]byte, error) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	s.mu.Lock()
	base := s.file.base
	s.mu.Unlock()

	var lastErr error
	plainSize := int64(-1)
	for attempt := 0; attempt < 2; attempt++ {
		plainSize = -1
		if fi, err := os.Stat(base); err == nil {
			plainSize = fi.Size()
		}
		if plainSize >= offset {
			out, err := readPlain(base, offset, max)
			if err == nil || !errors.Is(err, fs.ErrNotExist) {
				return out, err
			}
			lastErr = err
			continue
		}
		out, err := s.readZst(base+".zst", offset, max)
		if err == nil {
			return out, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			// Codex decompressed the rollout between the stat and the open: look at the .jsonl again.
			lastErr = err
			continue
		}
		if errors.Is(err, provider.ErrBeyondEnd) && plainSize >= 0 {
			// A .jsonl being written back can be shorter than the cursor for a moment; it is not shrunk.
			return nil, nil
		}
		return nil, err
	}
	if plainSize >= 0 {
		return nil, provider.ErrBeyondEnd // only a .jsonl, and shorter than the cursor
	}
	return nil, lastErr
}

func (s *source) close() {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	s.stream.close()
	s.stream = nil
}

func sameFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// readPlain reads a .jsonl window cut at the last LF; the first line is extended up to MaxLineBytes.
func readPlain(path string, offset, max int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if offset > size {
		return nil, provider.ErrBeyondEnd
	}
	if offset >= size || max <= 0 {
		return nil, nil
	}
	max = min(max, provider.MaxLineBytes)
	buf := make([]byte, min(size-offset, max))
	n, err := file.ReadAt(buf, offset)
	sessionBytesRead.Add(int64(n))
	if n <= 0 {
		return nil, err
	}
	chunk := buf[:n]
	if nl := bytes.LastIndexByte(chunk, '\n'); nl >= 0 {
		return chunk[:nl+1], nil
	}
	for int64(len(chunk)) < provider.MaxLineBytes && offset+int64(len(chunk)) < size {
		step := min(readStep, provider.MaxLineBytes-int64(len(chunk)), size-offset-int64(len(chunk)))
		old := len(chunk)
		chunk = append(chunk, make([]byte, step)...)
		n, _ := file.ReadAt(chunk[old:], offset+int64(old))
		sessionBytesRead.Add(int64(n))
		chunk = chunk[:old+n]
		if nl := bytes.IndexByte(chunk[old:], '\n'); nl >= 0 {
			return chunk[:old+nl+1], nil
		}
		if n < int(step) {
			break
		}
	}
	if int64(len(chunk)) >= provider.MaxLineBytes {
		return nil, &provider.LineTooLargeError{Offset: offset}
	}
	return nil, nil // an unfinished tail: nothing until the file grows
}

// zstStream is a decompression positioned in a .jsonl.zst. pending holds the decompressed bytes from
// startOff that Read has not returned: the incomplete last line of a window, kept so that the next Read, which
// starts there, does not decompress from the start again. Once the end is reached the file is closed and only
// pending remains.
type zstStream struct {
	info     os.FileInfo
	f        *os.File
	dec      *zstd.Decoder
	startOff int64
	pending  []byte
	eof      bool
}

func (z *zstStream) close() {
	if z == nil {
		return
	}
	if z.dec != nil {
		z.dec.Close()
		z.dec = nil
	}
	if z.f != nil {
		z.f.Close()
		z.f = nil
	}
}

func (s *source) readZst(path string, offset, max int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if max <= 0 {
		return nil, nil
	}
	max = min(max, provider.MaxLineBytes)

	s.mu.Lock()
	known := s.totalInfo != nil && sameFile(s.totalInfo, fi)
	total := s.total
	s.mu.Unlock()
	if known {
		if offset > total {
			return nil, provider.ErrBeyondEnd
		}
		if offset == total {
			return nil, nil
		}
	}

	z := s.stream
	if z != nil && (z.startOff != offset || !sameFile(z.info, fi)) {
		z.close()
		z = nil
	}
	if z == nil {
		var skipped int64
		z, skipped, err = openStream(path, fi, offset)
		if err != nil {
			s.stream = nil
			switch {
			case errors.Is(err, io.EOF):
				s.setTotal(fi, skipped)
				return nil, provider.ErrBeyondEnd
			case errors.Is(err, io.ErrUnexpectedEOF):
				return nil, nil // a rollout still being compressed
			}
			return nil, err
		}
	}
	s.stream = z

	want := max
	for {
		for !z.eof && int64(len(z.pending)) < want {
			n := min(want-int64(len(z.pending)), 256<<10)
			old := len(z.pending)
			z.pending = append(z.pending, make([]byte, n)...)
			got, err := z.dec.Read(z.pending[old:])
			sessionBytesRead.Add(int64(got))
			z.pending = z.pending[:old+got]
			if err == io.EOF {
				z.eof = true
				z.close()
				s.setTotal(fi, z.startOff+int64(len(z.pending)))
			} else if err != nil {
				z.close()
				s.stream = nil
				if errors.Is(err, io.ErrUnexpectedEOF) {
					return nil, nil // a rollout still being compressed
				}
				return nil, err
			}
		}
		if nl := bytes.LastIndexByte(z.pending, '\n'); nl >= 0 {
			out := append([]byte(nil), z.pending[:nl+1]...)
			z.pending = append([]byte(nil), z.pending[nl+1:]...)
			z.startOff += int64(nl + 1)
			return out, nil
		}
		if int64(len(z.pending)) >= provider.MaxLineBytes {
			return nil, &provider.LineTooLargeError{Offset: offset}
		}
		if z.eof {
			return nil, nil // an unfinished tail
		}
		want = min(int64(len(z.pending))+readStep, provider.MaxLineBytes)
	}
}

func (s *source) setTotal(fi os.FileInfo, total int64) {
	s.mu.Lock()
	s.total, s.totalInfo = total, fi
	s.mu.Unlock()
}

// openStream decompresses path from its start up to offset. When the stream ends first, skipped is the length
// it reached.
func openStream(path string, fi os.FileInfo, offset int64) (z *zstStream, skipped int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	z = &zstStream{info: fi, f: f, dec: dec}
	skipped, err = io.CopyN(io.Discard, countingReader{dec}, offset)
	if err != nil {
		z.close()
		return nil, skipped, err
	}
	z.startOff = offset
	return z, offset, nil
}
