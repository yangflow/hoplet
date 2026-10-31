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
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// DBName is the database file inside the store directory.
const DBName = "hoplet.db"

// ErrNotFound is returned when a session does not exist.
var ErrNotFound = errors.New("store: not found")

// Store is Hoplet's database. It is safe for concurrent use; writes are
// serialized over a single connection.
type Store struct {
	dir string
	db  *sql.DB
}

// Open opens or creates the store in dir, migrating the schema forward.
func Open(ctx context.Context, dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, fmt.Errorf("store: create dir: %w", err)
	}
	path := filepath.Join(dir, DBName)
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{dir: dir, db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

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
		return errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}
