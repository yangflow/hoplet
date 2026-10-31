package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)

func newStore(t testing.TB) *Store {
	t.Helper()
	s, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenReopenAndRejectNewerSchema(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatalf("schema version %d, want 1", v)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, dir); err == nil || !strings.Contains(err.Error(), "newer than this binary") {
		t.Fatalf("open newer schema: got %v", err)
	}
}

func TestBackupKeepsNewestThree(t *testing.T) {
	s := newStore(t)
	for v := 1; v <= 4; v++ {
		if err := s.backup(context.Background(), v); err != nil {
			t.Fatal(err)
		}
	}
	got, err := filepath.Glob(filepath.Join(s.dir, DBName+".v*.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d backups, want 3: %v", len(got), got)
	}
	if _, err := os.Stat(filepath.Join(s.dir, DBName+".v1.bak")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest backup still present: %v", err)
	}
}
