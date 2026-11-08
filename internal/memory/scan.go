package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/yangflow/hoplet/internal/store"
)

// scanReason is the audit reason of an edit a scan finds.
const scanReason = "edited on disk"

// scanState is what one pass over the entry files has found.
type scanState struct {
	seen     map[string]string // entry ID to relative path
	rows     []store.MemoryRow
	problems map[string]string // relative path to error
}

// Scan reconciles the entry files with the index. A changed file becomes a
// new version written back to it; a file it cannot use is left alone,
// dropped from the index and listed in Problems.
func (s *Service) Scan(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := scanState{seen: map[string]string{}, problems: map[string]string{}}
	dir := filepath.Join(s.root, entriesDir)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), fileExt) {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		e, data, err := load(p, rel, d)
		if err == nil {
			if other, dup := st.seen[e.ID]; dup {
				err = invalid("entry %s is also at %s", e.ID, other)
			}
		}
		if err != nil {
			st.problems[rel] = err.Error()
			return nil
		}
		st.seen[e.ID] = rel
		row, err := s.reconcile(ctx, e, hashOf(data))
		if err != nil {
			return err
		}
		st.rows = append(st.rows, row)
		return nil
	})
	if err != nil {
		return fmt.Errorf("memory: scan: %w", err)
	}
	indexed, err := s.store.SearchMemoryIndex(ctx, store.MemoryQuery{})
	if err != nil {
		return err
	}
	for _, row := range indexed {
		rel := row.Category + "/" + row.ID + fileExt
		if _, ok := st.seen[row.ID]; ok {
			continue
		}
		if _, ok := st.problems[rel]; !ok {
			st.problems[rel] = "the file is missing"
		}
	}
	problems := make([]store.MemoryProblem, 0, len(st.problems))
	for p, msg := range st.problems {
		problems = append(problems, store.MemoryProblem{Path: p, Err: msg})
	}
	if err := s.store.ReplaceMemoryIndex(ctx, st.rows, problems); err != nil {
		return err
	}
	if err := s.writeProfile(ctx); err != nil {
		s.log.WarnContext(ctx, "memory: write profile", "err", err)
	}
	return nil
}

// load reads and checks one file at rel, which must be <category>/<id>.md.
func load(p, rel string, d fs.DirEntry) (Entry, []byte, error) {
	if !d.Type().IsRegular() {
		return Entry{}, nil, invalid("not a regular file")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Entry{}, nil, fmt.Errorf("memory: read: %w", err)
	}
	e, err := Decode(data)
	if err != nil {
		return Entry{}, nil, err
	}
	if rel != string(e.Category)+"/"+e.ID+fileExt {
		return Entry{}, nil, invalid("an entry %s of category %s cannot be at this path", e.ID, e.Category)
	}
	return e, data, nil
}

// reconcile returns the index row for a valid file, committing it first if
// it holds an edit the index and audit log have not seen.
func (s *Service) reconcile(ctx context.Context, e Entry, hash string) (store.MemoryRow, error) {
	expected := 0
	row, err := s.store.MemoryIndexEntry(ctx, e.ID)
	switch {
	case err == nil && row.Hash == hash:
		return row, nil
	case err == nil:
		expected = row.Version
	case !errors.Is(err, store.ErrNotFound):
		return store.MemoryRow{}, err
	default:
		ops, err := s.store.MemoryOps(ctx, e.ID)
		if err != nil {
			return store.MemoryRow{}, err
		}
		for _, op := range ops {
			if op.Hash == hash { // the index was lost, not the file edited
				return rowOf(e, hash), nil
			}
		}
		if len(ops) > 0 {
			expected = ops[0].NewVersion
		}
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return store.MemoryRow{}, fmt.Errorf("memory: new op ID: %w", err)
	}
	_, out, err := s.write(ctx, Op{
		ID:              "scan_" + hex.EncodeToString(b[:]),
		Entry:           e,
		ExpectedVersion: expected,
		Actor:           ActorFile,
		Reason:          scanReason,
	})
	return out, err
}
