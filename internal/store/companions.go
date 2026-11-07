package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/yangflow/hoplet/proto"
)

// Companion is a stored companion. The embedded record carries what the
// store knows: its ID, main session, creation time, and the main
// session's title, harness and archive time; Host and Dir are left for the
// caller.
type Companion struct {
	proto.Companion
	// InSessionDir says the main session works in its session directory,
	// where it worked before companions existed.
	InSessionDir bool
}

const companionQuery = `SELECT c.id, c.main_session, c.in_session_dir, c.created_at,
	s.title, s.harness_kind, s.harness_version, s.native_id, s.archived_at
	FROM companions c JOIN sessions s ON s.id = c.main_session`

// CreateCompanion stores a new Personal session and a companion with ID id
// whose main session it is, in one durable transaction: if either fails,
// neither is stored.
func (s *Store) CreateCompanion(ctx context.Context, sess proto.Session, id proto.CompanionID) (proto.Session, Companion, error) {
	if sess.Mode != proto.ModePersonal || id == "" {
		return proto.Session{}, Companion{}, fmt.Errorf("%w: a companion needs an ID and a personal main session", ErrInvalid)
	}
	var c Companion
	sess, err := s.createSession(ctx, sess, func(tx *sql.Tx, sess proto.Session) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO companions (id, main_session, in_session_dir, created_at) VALUES (?, ?, 0, ?)`,
			id, sess.ID, ms(sess.CreatedAt))
		var serr *sqlite.Error
		if errors.As(err, &serr) && (serr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY || serr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE) {
			return fmt.Errorf("%w: companion %s", ErrExists, id)
		}
		if err != nil {
			return fmt.Errorf("store: create companion %s: %w", id, err)
		}
		c, err = getCompanion(ctx, tx, `WHERE c.id = ?`, id)
		return err
	})
	if err != nil {
		return proto.Session{}, Companion{}, err
	}
	return sess, c, nil
}

// Companions returns every companion, oldest first.
func (s *Store) Companions(ctx context.Context) ([]Companion, error) {
	rows, err := s.rdb.QueryContext(ctx, companionQuery+` ORDER BY c.created_at, c.id`)
	if err != nil {
		return nil, fmt.Errorf("store: companions: %w", err)
	}
	defer rows.Close()
	var out []Companion
	for rows.Next() {
		c, err := scanCompanion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: companions: %w", err)
	}
	return out, nil
}

// minCompanionPrefix is the shortest prefix that names a companion.
const minCompanionPrefix = 4

// Companion returns the companion whose ID is id or, failing that, the one
// whose ID starts with id when id has at least 4 characters and only one
// matches. It is ErrNotFound when none matches and ErrInvalid when several
// do.
func (s *Store) Companion(ctx context.Context, id string) (Companion, error) {
	c, err := getCompanion(ctx, s.rdb, `WHERE c.id = ?`, id)
	if !errors.Is(err, ErrNotFound) || len(id) < minCompanionPrefix {
		return c, err
	}
	all, err := s.Companions(ctx)
	if err != nil {
		return Companion{}, err
	}
	var matches []Companion
	for _, c := range all {
		if strings.HasPrefix(string(c.ID), id) {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 0:
		return Companion{}, fmt.Errorf("%w: companion %s", ErrNotFound, id)
	case 1:
		return matches[0], nil
	default:
		return Companion{}, fmt.Errorf("%w: %s matches %d companions", ErrInvalid, id, len(matches))
	}
}

// CompanionByMain returns the companion whose main session is id, or
// ErrNotFound when the session is no companion's main session.
func (s *Store) CompanionByMain(ctx context.Context, id proto.SessionID) (Companion, error) {
	return getCompanion(ctx, s.rdb, `WHERE c.main_session = ?`, id)
}

func getCompanion(ctx context.Context, q queryer, where string, arg any) (Companion, error) {
	c, err := scanCompanion(q.QueryRowContext(ctx, companionQuery+` `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return Companion{}, fmt.Errorf("%w: companion %v", ErrNotFound, arg)
	}
	return c, err
}

func scanCompanion(r scanner) (Companion, error) {
	var (
		c        Companion
		created  int64
		archived sql.NullInt64
	)
	err := r.Scan(&c.ID, &c.Main, &c.InSessionDir, &created,
		&c.Name, &c.Harness.Kind, &c.Harness.Version, &c.Harness.NativeID, &archived)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Companion{}, err
		}
		return Companion{}, fmt.Errorf("store: scan companion: %w", err)
	}
	c.CreatedAt = time.UnixMilli(created).UTC()
	if archived.Valid {
		t := time.UnixMilli(archived.Int64).UTC()
		c.ArchivedAt = &t
	}
	return c, nil
}
