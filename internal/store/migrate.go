package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrations embed.FS

// keepBackups is how many pre-migration copies of the database are kept.
const keepBackups = 3

// noTxMarker, as the first line of a migration, makes it run outside a
// transaction. Some schema changes (PRAGMA foreign_keys, for one) cannot run
// inside one.
const noTxMarker = "-- hoplet:no-transaction"

// migration is one numbered schema step.
type migration struct {
	version int
	sql     string
}

func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		n, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("store: bad migration name %q", e.Name())
		}
		v, err := strconv.Atoi(n)
		if err != nil {
			return nil, fmt.Errorf("store: bad migration name %q: %w", e.Name(), err)
		}
		b, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: read %s: %w", e.Name(), err)
		}
		ms = append(ms, migration{v, string(b)})
	}
	slices.SortFunc(ms, func(a, b migration) int { return a.version - b.version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("store: migrations must be numbered 1..n without gaps, got %d at position %d", m.version, i+1)
		}
	}
	return ms, nil
}

// migrate applies pending migrations forward, one transaction each, after
// backing up an existing database.
func (s *Store) migrate(ctx context.Context, fsys fs.FS) error {
	ms, err := loadMigrations(fsys)
	if err != nil {
		return err
	}
	var current int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if current > len(ms) {
		return fmt.Errorf("store: database schema version %d is newer than this binary (%d)", current, len(ms))
	}
	if current == len(ms) {
		return nil
	}
	if current > 0 {
		if err := s.backup(ctx, current); err != nil {
			return err
		}
	}
	for _, m := range ms[current:] {
		if first, _, _ := strings.Cut(m.sql, "\n"); strings.TrimSuffix(first, "\r") == noTxMarker {
			if _, err := s.db.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("store: migration %d: %w", m.version, err)
			}
			if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
				return fmt.Errorf("store: set schema version %d: %w", m.version, err)
			}
			continue
		}
		err := s.tx(ctx, true, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("store: migration %d: %w", m.version, err)
			}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
				return fmt.Errorf("store: set schema version %d: %w", m.version, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// backup copies the database before migrating from version v and prunes
// older copies beyond keepBackups.
func (s *Store) backup(ctx context.Context, v int) error {
	dst := filepath.Join(s.dir, fmt.Sprintf("%s.v%d.bak", DBName, v))
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("store: remove old backup: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("store: backup before migration: %w", err)
	}
	old, err := filepath.Glob(filepath.Join(s.dir, DBName+".v*.bak"))
	if err != nil {
		return fmt.Errorf("store: list backups: %w", err)
	}
	slices.SortFunc(old, func(a, b string) int { return backupVersion(a) - backupVersion(b) })
	for len(old) > keepBackups {
		if err := os.Remove(old[0]); err != nil {
			return fmt.Errorf("store: prune backup: %w", err)
		}
		old = old[1:]
	}
	return nil
}

func backupVersion(path string) int {
	s := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), DBName+".v"), ".bak")
	v, _ := strconv.Atoi(s)
	return v
}
