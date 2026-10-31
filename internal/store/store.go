// Package store keeps Hoplet's durable state: sessions, their event logs,
// projections derived from those events, command receipts and blobs.
//
// Loss contract: ordinary appends commit with synchronous=NORMAL, so a power
// loss may drop the latest appends. Appends that carry a receipt, a turn end
// or an approval decision, and session creation, commit with
// synchronous=FULL and are on disk when the call returns.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// DBName is the database file inside the store directory.
const DBName = "hoplet.db"

// ErrNotFound is returned when a session does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrInvalid is returned when a request carries invalid values.
var ErrInvalid = errors.New("store: invalid")

// ErrExists is returned when creating something that already exists.
var ErrExists = errors.New("store: already exists")

// ErrConflict is returned when a decision arrives for something already
// decided; the first decision stands.
var ErrConflict = errors.New("store: conflict")

// readConns is the size of the read-only connection pool.
const readConns = 4

// Store is Hoplet's database. It is safe for concurrent use; writes are
// serialized over a single connection, reads use a separate read-only pool
// and are not blocked by writes.
type Store struct {
	dir string
	db  *sql.DB // single writer
	rdb *sql.DB // read-only pool

	mu     sync.Mutex
	change chan struct{} // closed and replaced after every committed write
}

// Changes returns a channel that is closed after the next committed write
// to the store, whichever session or catalog it changes. Take the channel
// before reading the state you wait on, so no write is missed.
func (s *Store) Changes() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.change
}

func (s *Store) signalChange() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.change)
	s.change = make(chan struct{})
}

// dsn builds a SQLite URI for path with the given query parameters, escaping
// path characters such as ?, # and %.
func dsn(path string, query url.Values) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: query.Encode()}
	return u.String()
}

// Open opens or creates the store in dir, migrating the schema forward.
func Open(ctx context.Context, dir string) (*Store, error) {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: migrations: %w", err)
	}
	return open(ctx, dir, fsys)
}

// open is Open with the migrations supplied, so tests can use several steps.
func open(ctx context.Context, dir string, fsys fs.FS) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, fmt.Errorf("store: create dir: %w", err)
	}
	tmps, err := filepath.Glob(filepath.Join(dir, "blobs", ".tmp-*"))
	if err != nil {
		return nil, fmt.Errorf("store: list stale blobs: %w", err)
	}
	for _, f := range tmps {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("store: remove stale blob: %w", err)
		}
	}
	path, err := filepath.Abs(filepath.Join(dir, DBName))
	if err != nil {
		return nil, fmt.Errorf("store: resolve path: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(path, url.Values{
		"_pragma": {"journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(1)", "busy_timeout(5000)"},
		"_txlock": {"immediate"},
	}))
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{dir: dir, db: db, change: make(chan struct{})}
	if err := s.migrate(ctx, fsys); err != nil {
		db.Close()
		return nil, err
	}
	rdb, err := sql.Open("sqlite", dsn(path, url.Values{
		"_pragma": {"busy_timeout(5000)", "query_only(1)"},
	}))
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("store: open reader: %w", err)
	}
	rdb.SetMaxOpenConns(readConns)
	if err := rdb.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("store: open reader: %w", err), rdb.Close(), db.Close())
	}
	s.rdb = rdb
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return errors.Join(s.rdb.Close(), s.db.Close()) }

// tx runs fn in a transaction; durable transactions commit with
// synchronous=FULL.
func (s *Store) tx(ctx context.Context, durable bool, fn func(*sql.Tx) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: conn: %w", err)
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	if durable {
		if _, err := conn.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
			return fmt.Errorf("store: set synchronous: %w", err)
		}
		defer func() {
			_, rerr := conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA synchronous=NORMAL")
			err = errors.Join(err, rerr)
		}()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			err = errors.Join(err, rerr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	s.signalChange()
	return nil
}
