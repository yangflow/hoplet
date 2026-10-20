package workspace

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// The parts of a handoff transfer.
const (
	PartSnapshot = "snapshot"
	PartSession  = "session"
)

// ChunkBytes is the most bytes one chunk of a transfer carries once decoded.
const ChunkBytes = 1 << 20

// MaxSessionBytes bounds the session stream a transfer may announce. Sessions are hundreds of megabytes; the
// bound only keeps a wrong or hostile size from reserving the disk.
const MaxSessionBytes int64 = 8 << 30

// maxEncodedChunk is the most characters of a chunk's base64 text: a gzip stream of a full chunk plus its
// framing, base64 encoded.
const maxEncodedChunk = (ChunkBytes+ChunkBytes/100+1024)/3*4 + 8

var (
	opaqueID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	hex64    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ErrTransfer reports a chunk the receiver refuses; the transfer is over.
var ErrTransfer = errors.New("invalid-transfer")

// TransferHeader is what `handoff.install` announces: the sizes and checksums the received bytes are held to.
type TransferHeader struct {
	HandoffID      string
	SessionID      string
	Provider       string // the session's vendor
	Configuration  string // the target's provider configuration; empty means the vendor's own
	TargetPath     string
	SnapshotBytes  int64
	SnapshotSHA256 string
	SessionBytes   int64
	SessionSHA256  string
}

func (h TransferHeader) validate() error {
	switch {
	case !opaqueID.MatchString(h.HandoffID):
		return fmt.Errorf("%w: handoff id", ErrTransfer)
	case h.SnapshotBytes <= 0 || h.SnapshotBytes > MaxSnapshotCompressed:
		return fmt.Errorf("%w: %w", ErrTransfer, ErrTooLarge)
	case h.SessionBytes <= 0 || h.SessionBytes > MaxSessionBytes:
		return fmt.Errorf("%w: session size", ErrTransfer)
	case !hex64.MatchString(h.SnapshotSHA256) || !hex64.MatchString(h.SessionSHA256):
		return fmt.Errorf("%w: checksum", ErrTransfer)
	}
	return nil
}

type receiving struct {
	file     *os.File
	path     string
	hash     hash.Hash
	want     int64
	wantSHA  string
	received int64
	next     int
	done     bool
}

// Transfer receives the two parts of a handoff into a private staging directory, in order, chunk by chunk.
// Every chunk is bounded before it is decoded and held to the announced total, so the staging never holds
// more than the header said. Nothing leaves the directory until the install uses it.
type Transfer struct {
	Header TransferHeader
	dir    string
	parts  map[string]*receiving
}

// BeginTransfer validates the header and makes the staging directory under the agent's state root.
func BeginTransfer(home string, h TransferHeader) (*Transfer, error) {
	if err := h.validate(); err != nil {
		return nil, err
	}
	dir := filepath.Join(home, "handoff", "transfer-"+h.HandoffID)
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	t := &Transfer{Header: h, dir: dir, parts: map[string]*receiving{}}
	for name, spec := range map[string]struct {
		size int64
		sha  string
	}{
		PartSnapshot: {h.SnapshotBytes, h.SnapshotSHA256},
		PartSession:  {h.SessionBytes, h.SessionSHA256},
	} {
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Close()
			return nil, err
		}
		t.parts[name] = &receiving{file: f, path: path, hash: sha256.New(), want: spec.size, wantSHA: spec.sha}
	}
	return t, nil
}

// decodeChunk turns the text of a chunk into at most ChunkBytes bytes. A gzip chunk that expands past the
// bound is refused as soon as it does, without reading the rest.
func decodeChunk(encoding, data string) ([]byte, error) {
	if data == "" || len(data) > maxEncodedChunk {
		return nil, fmt.Errorf("%w: chunk size", ErrTransfer)
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("%w: base64", ErrTransfer)
	}
	switch encoding {
	case "base64":
	case "gzip+base64":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%w: gzip", ErrTransfer)
		}
		raw, err = io.ReadAll(io.LimitReader(zr, ChunkBytes+1))
		if err != nil {
			return nil, fmt.Errorf("%w: gzip", ErrTransfer)
		}
	default:
		return nil, fmt.Errorf("%w: encoding", ErrTransfer)
	}
	if len(raw) == 0 || len(raw) > ChunkBytes {
		return nil, fmt.Errorf("%w: chunk size", ErrTransfer)
	}
	return raw, nil
}

// Write stores one chunk of a part. Chunks come in order from 0; the last one completes the part, whose size
// and SHA-256 must then be the announced ones.
func (t *Transfer) Write(part string, seq int, last bool, encoding, data string) error {
	p := t.parts[part]
	if p == nil || p.done || seq != p.next {
		return fmt.Errorf("%w: out of order", ErrTransfer)
	}
	raw, err := decodeChunk(encoding, data)
	if err != nil {
		return err
	}
	if p.received+int64(len(raw)) > p.want {
		return fmt.Errorf("%w: more than announced", ErrTransfer)
	}
	if _, err := p.file.Write(raw); err != nil {
		return err
	}
	p.hash.Write(raw)
	p.received += int64(len(raw))
	p.next++
	if !last {
		if p.received == p.want {
			return fmt.Errorf("%w: the last chunk is missing", ErrTransfer)
		}
		return nil
	}
	if p.received != p.want {
		return fmt.Errorf("%w: shorter than announced", ErrTransfer)
	}
	if hex.EncodeToString(p.hash.Sum(nil)) != p.wantSHA {
		return fmt.Errorf("%w: checksum", ErrTransfer)
	}
	p.done = true
	return p.file.Close()
}

// Complete reports whether both parts arrived whole.
func (t *Transfer) Complete() bool {
	return t.parts[PartSnapshot].done && t.parts[PartSession].done
}

// Path is where a completed part is staged.
func (t *Transfer) Path(part string) string { return t.parts[part].path }

// Dir is the staging directory.
func (t *Transfer) Dir() string { return t.dir }

// Close removes the staging directory and everything in it.
func (t *Transfer) Close() {
	for _, p := range t.parts {
		if p.file != nil {
			_ = p.file.Close()
		}
	}
	_ = os.RemoveAll(t.dir)
}
