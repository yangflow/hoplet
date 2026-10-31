package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yangflow/hoplet/proto"
)

// PutBlob stores r's content under its SHA-256 and returns its reference.
// The file is synced before it becomes visible; storing the same content
// twice keeps one copy.
func (s *Store) PutBlob(r io.Reader) (proto.BlobRef, error) {
	dir := filepath.Join(s.dir, "blobs")
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return proto.BlobRef{}, fmt.Errorf("store: create blob: %w", err)
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return proto.BlobRef{}, fmt.Errorf("store: write blob: %w", err)
	}
	ref := proto.BlobRef{SHA256: hex.EncodeToString(h.Sum(nil)), Size: size}
	dst := s.blobPath(ref)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return proto.BlobRef{}, fmt.Errorf("store: create blob dir: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return proto.BlobRef{}, fmt.Errorf("store: publish blob: %w", err)
	}
	return ref, nil
}

// OpenBlob opens a stored blob, or returns ErrNotFound.
func (s *Store) OpenBlob(ref proto.BlobRef) (io.ReadCloser, error) {
	if len(ref.SHA256) != sha256.Size*2 {
		return nil, fmt.Errorf("store: bad blob reference %q", ref.SHA256)
	}
	if _, err := hex.DecodeString(ref.SHA256); err != nil {
		return nil, fmt.Errorf("store: bad blob reference %q: %w", ref.SHA256, err)
	}
	f, err := os.Open(s.blobPath(ref))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: blob %s", ErrNotFound, ref.SHA256)
	}
	if err != nil {
		return nil, fmt.Errorf("store: open blob: %w", err)
	}
	return f, nil
}

func (s *Store) blobPath(ref proto.BlobRef) string {
	return filepath.Join(s.dir, "blobs", ref.SHA256[:2], ref.SHA256[2:])
}
