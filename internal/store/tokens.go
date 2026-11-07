package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// SetSessionToken records hash as the session's tool token, replacing any
// earlier one.
func (s *Store) SetSessionToken(ctx context.Context, id proto.SessionID, hash []byte, t time.Time) error {
	if len(hash) == 0 {
		return fmt.Errorf("%w: empty token hash", ErrInvalid)
	}
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO session_tokens (session_id, hash, issued_at) VALUES (?, ?, ?)
			ON CONFLICT (session_id) DO UPDATE SET hash = excluded.hash, issued_at = excluded.issued_at`,
			id, hash, ms(t))
		if err != nil {
			return fmt.Errorf("store: set token of %s: %w", id, err)
		}
		return nil
	})
}

// SessionByToken returns the unarchived session whose tool token hashes to
// hash.
func (s *Store) SessionByToken(ctx context.Context, hash []byte) (proto.SessionID, error) {
	var id proto.SessionID
	err := s.rdb.QueryRowContext(ctx, `SELECT t.session_id FROM session_tokens t JOIN sessions s ON s.id = t.session_id
		WHERE t.hash = ? AND s.archived_at IS NULL`, hash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: token", ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("store: look up token: %w", err)
	}
	return id, nil
}

// HasSessionToken reports whether the session has a tool token.
func (s *Store) HasSessionToken(ctx context.Context, id proto.SessionID) (bool, error) {
	var n int
	if err := s.rdb.QueryRowContext(ctx, `SELECT count(*) FROM session_tokens WHERE session_id = ?`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("store: token of %s: %w", id, err)
	}
	return n > 0, nil
}

// TokenHash is the stored form of a session token: tokens themselves are
// never stored.
func TokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
