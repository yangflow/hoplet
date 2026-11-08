package memory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// recoverOps removes abandoned temp files and settles each pending op: if
// the file already holds its content it committed, otherwise it failed.
func (s *Service) recoverOps(ctx context.Context) error {
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), tempPrefix) {
			return os.Remove(p) //nolint:gosec // the walk stays in the private profile directory
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("memory: remove temp files: %w", err)
	}
	ops, err := s.store.PendingMemoryOps(ctx)
	if err != nil {
		return err
	}
	for _, op := range ops {
		e, err := Decode([]byte(op.Content))
		if err != nil {
			if err := s.store.FailMemoryOp(ctx, op.ID); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(path(s.root, e.Category, e.ID))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("memory: read entry %s: %w", e.ID, err)
		}
		if err == nil && hashOf(data) == op.Hash {
			err = s.store.FinishMemoryOp(ctx, op.ID, rowOf(e, op.Hash))
		} else {
			err = s.store.FailMemoryOp(ctx, op.ID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
