package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

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
	if _, err := os.Stat(dst); err == nil {
		return ref, nil // same content already stored; the deferred remove drops the temp file
	} else if !errors.Is(err, os.ErrNotExist) {
		return proto.BlobRef{}, fmt.Errorf("store: stat blob: %w", err)
	}
	// Two syncs make the blob durable: the shard directory after the rename
	// (its new entry), and the blobs directory when the shard is created
	// (the shard's own entry).
	shard := filepath.Dir(dst)
	_, statErr := os.Stat(shard)
	newShard := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !newShard {
		return proto.BlobRef{}, fmt.Errorf("store: stat blob dir: %w", statErr)
	}
	if err := os.MkdirAll(shard, 0o700); err != nil {
		return proto.BlobRef{}, fmt.Errorf("store: create blob dir: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return proto.BlobRef{}, fmt.Errorf("store: publish blob: %w", err)
	}
	if err := syncDir(shard); err != nil {
		return proto.BlobRef{}, fmt.Errorf("store: sync blob dir: %w", err)
	}
	if newShard {
		if err := syncDir(filepath.Dir(shard)); err != nil {
			return proto.BlobRef{}, fmt.Errorf("store: sync blobs dir: %w", err)
		}
	}
	return ref, nil
}

// syncDir flushes a directory's entries to disk.
func syncDir(path string) error {
	d, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// OpenBlob opens a stored blob, or returns ErrNotFound.
func (s *Store) OpenBlob(ref proto.BlobRef) (io.ReadCloser, error) {
	if !validDigest(ref.SHA256) {
		return nil, fmt.Errorf("store: bad blob reference %q", ref.SHA256)
	}
	f, err := os.Open(s.blobPath(ref))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: blob %s", ErrNotFound, ref.SHA256)
	}
	if err != nil {
		return nil, fmt.Errorf("store: open blob: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("store: stat blob: %w", err), f.Close())
	}
	if info.Size() != ref.Size {
		return nil, errors.Join(fmt.Errorf("store: blob %s has %d bytes, reference says %d", ref.SHA256, info.Size(), ref.Size), f.Close())
	}
	return f, nil
}

// validDigest reports whether s is a lowercase hex SHA-256.
func validDigest(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) blobPath(ref proto.BlobRef) string {
	return filepath.Join(s.dir, "blobs", ref.SHA256[:2], ref.SHA256[2:])
}

// GCBlobs deletes blob files that no event references (as raw event or tool
// output) and that were last modified more than olderThan ago, and returns
// how many it deleted. The age floor protects blobs stored just before the
// event that will reference them is appended.
func (s *Store) GCBlobs(ctx context.Context, olderThan time.Duration) (int, error) {
	root := filepath.Join(s.dir, "blobs")
	shards, err := os.ReadDir(root)
	if err != nil {
		return 0, fmt.Errorf("store: list blobs: %w", err)
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, shard := range shards {
		if !shard.IsDir() || len(shard.Name()) != 2 {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, shard.Name()))
		if err != nil {
			return removed, fmt.Errorf("store: list blobs: %w", err)
		}
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				return removed, err
			}
			digest := shard.Name() + f.Name()
			if f.IsDir() || !validDigest(digest) {
				continue
			}
			info, err := f.Info()
			if err != nil {
				return removed, fmt.Errorf("store: stat blob: %w", err)
			}
			if !info.ModTime().Before(cutoff) {
				continue
			}
			var referenced bool
			err = s.rdb.QueryRowContext(ctx, `SELECT EXISTS (
				SELECT 1 FROM events WHERE raw_sha256 = ?1 OR output_sha256 = ?1)`, digest).Scan(&referenced)
			if err != nil {
				return removed, fmt.Errorf("store: check blob references: %w", err)
			}
			if referenced {
				continue
			}
			if err := os.Remove(filepath.Join(root, shard.Name(), f.Name())); err != nil {
				return removed, fmt.Errorf("store: remove blob: %w", err)
			}
			removed++
		}
	}
	return removed, nil
}
