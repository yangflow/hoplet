package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yangflow/hoplet/proto"
)

const sessionColumns = `id, mode, harness_kind, harness_version, native_id, directory, isolation,
	owner, origin, status, title, created_at, updated_at, archived_at, last_seq`

// CreateSession stores a new session. Status defaults to idle and
// CreatedAt to now; UpdatedAt equals CreatedAt. The write is durable.
func (s *Store) CreateSession(ctx context.Context, sess proto.Session) (proto.Session, error) {
	if sess.ID == "" || sess.Mode == "" || sess.Harness.Kind == "" || sess.Owner == "" || sess.Origin == "" {
		return proto.Session{}, errors.New("store: session needs id, mode, harness kind, owner and origin")
	}
	if sess.Status == "" {
		sess.Status = proto.StatusIdle
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}
	sess.CreatedAt = sess.CreatedAt.UTC().Truncate(time.Millisecond)
	sess.UpdatedAt = sess.CreatedAt
	sess.ArchivedAt = nil
	sess.LastSeq = 0
	err := s.tx(ctx, true, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO sessions (`+sessionColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 0)`,
			sess.ID, sess.Mode, sess.Harness.Kind, sess.Harness.Version, sess.Harness.NativeID,
			sess.Directory, sess.Isolation, sess.Owner, sess.Origin, sess.Status, sess.Title,
			sess.CreatedAt.UnixMilli(), sess.UpdatedAt.UnixMilli())
		if err != nil {
			return fmt.Errorf("store: create session %s: %w", sess.ID, err)
		}
		return nil
	})
	if err != nil {
		return proto.Session{}, err
	}
	return sess, nil
}

// Session returns one session, or ErrNotFound.
func (s *Store) Session(ctx context.Context, id proto.SessionID) (proto.Session, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return proto.Session{}, fmt.Errorf("%w: session %s", ErrNotFound, id)
	}
	return sess, err
}

// Sessions returns all sessions, most recently updated first.
func (s *Store) Sessions(ctx context.Context) ([]proto.Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionColumns+` FROM sessions ORDER BY updated_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	defer rows.Close()
	var out []proto.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	return out, nil
}

// ArchiveSession marks a session archived at t. The write is durable.
func (s *Store) ArchiveSession(ctx context.Context, id proto.SessionID, t time.Time) error {
	ms := t.UTC().Truncate(time.Millisecond).UnixMilli()
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE sessions SET archived_at = ?, updated_at = ? WHERE id = ?`, ms, ms, id)
		if err != nil {
			return fmt.Errorf("store: archive session %s: %w", id, err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("store: archive session %s: %w", id, err)
		} else if n == 0 {
			return fmt.Errorf("%w: session %s", ErrNotFound, id)
		}
		return nil
	})
}

type scanner interface{ Scan(dest ...any) error }

func scanSession(r scanner) (proto.Session, error) {
	var (
		sess             proto.Session
		created, updated int64
		archived         sql.NullInt64
	)
	err := r.Scan(&sess.ID, &sess.Mode, &sess.Harness.Kind, &sess.Harness.Version, &sess.Harness.NativeID,
		&sess.Directory, &sess.Isolation, &sess.Owner, &sess.Origin, &sess.Status, &sess.Title,
		&created, &updated, &archived, &sess.LastSeq)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return proto.Session{}, err
		}
		return proto.Session{}, fmt.Errorf("store: scan session: %w", err)
	}
	sess.CreatedAt = time.UnixMilli(created).UTC()
	sess.UpdatedAt = time.UnixMilli(updated).UTC()
	if archived.Valid {
		t := time.UnixMilli(archived.Int64).UTC()
		sess.ArchivedAt = &t
	}
	return sess, nil
}
