package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/yangflow/hoplet/proto"
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

func TestOpenPathWithSpecialCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a?b#c%d")
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateSession(context.Background(), proto.Session{
		ID: "s", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"},
		Owner: proto.OwnerMe, Origin: proto.OriginMe,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, DBName)); err != nil {
		t.Fatalf("database not at the requested path: %v", err)
	}
}

func initSQL(t *testing.T) string {
	t.Helper()
	b, err := migrations.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMigrateTwoSteps(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	one := fstest.MapFS{"0001_init.sql": {Data: []byte(initSQL(t))}}
	two := fstest.MapFS{
		"0001_init.sql":  {Data: []byte(initSQL(t))},
		"0002_extra.sql": {Data: []byte("ALTER TABLE sessions ADD COLUMN test_col TEXT")},
	}
	s, err := open(ctx, dir, one)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = open(ctx, dir, two)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil || v != 2 {
		t.Fatalf("user_version %d, %v; want 2", v, err)
	}
	if _, err := s.db.ExecContext(ctx, "SELECT test_col FROM sessions"); err != nil {
		t.Fatalf("migration 2 not applied: %v", err)
	}
	bak, err := sql.Open("sqlite", "file:"+filepath.Join(dir, DBName+".v1.bak"))
	if err != nil {
		t.Fatal(err)
	}
	defer bak.Close()
	var bv int
	if err := bak.QueryRowContext(ctx, "PRAGMA user_version").Scan(&bv); err != nil || bv != 1 {
		t.Fatalf("backup user_version %d, %v; want 1", bv, err)
	}
	var n int
	if err := bak.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name = 'sessions'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("backup is not a valid database: %d, %v", n, err)
	}
}

func TestMigrationGapRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_init.sql": {Data: []byte("SELECT 1")},
		"0003_skip.sql": {Data: []byte("SELECT 1")},
	}
	if _, err := open(context.Background(), t.TempDir(), fsys); err == nil || !strings.Contains(err.Error(), "without gaps") {
		t.Fatalf("gap accepted: %v", err)
	}
}
