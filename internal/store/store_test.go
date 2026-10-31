package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
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
	if want := len(mustMigrations(t)); v != want {
		t.Fatalf("schema version %d, want %d", v, want)
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
	bak, err := sql.Open("sqlite", dsn(filepath.Join(dir, DBName+".v1.bak"), url.Values{"mode": {"ro"}}))
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

func TestNoTransactionMigration(t *testing.T) {
	ctx := context.Background()
	fsys := fstest.MapFS{
		"0001_init.sql": {Data: []byte(initSQL(t))},
		// PRAGMA foreign_keys is a no-op inside a transaction, so seeing it
		// take effect proves the migration ran outside one.
		"0002_pragma.sql": {Data: []byte("-- hoplet:no-transaction\nPRAGMA foreign_keys = OFF;\nCREATE TABLE extra (a INTEGER);")},
	}
	s, err := open(ctx, t.TempDir(), fsys)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v, fk int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil || v != 2 {
		t.Fatalf("user_version %d, %v; want 2", v, err)
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 0 {
		t.Fatalf("foreign_keys = %d, %v; migration did not run outside a transaction", fk, err)
	}
	if _, err := s.db.ExecContext(ctx, "SELECT a FROM extra"); err != nil {
		t.Fatal(err)
	}
}

func TestReadsProceedDuringWrite(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.tx(ctx, false, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE sessions SET title = 'writing' WHERE id = 's'"); err != nil {
				return err
			}
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	sess, err := s.Session(readCtx, "s")
	close(release)
	if err != nil {
		t.Fatalf("read blocked by an open write transaction: %v", err)
	}
	if sess.Title != "" {
		t.Fatalf("read saw uncommitted data: %q", sess.Title)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReaderIsReadOnly(t *testing.T) {
	s := newStore(t)
	if _, err := s.rdb.ExecContext(context.Background(), "DELETE FROM sessions"); err == nil {
		t.Fatal("read pool accepted a write")
	}
}

func TestOpenRemovesStaleTempBlobs(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "blobs", ".tmp-123")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temp blob survived Open: %v", err)
	}
}

func mustMigrations(t *testing.T) []migration {
	t.Helper()
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	ms, err := loadMigrations(sub)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}
