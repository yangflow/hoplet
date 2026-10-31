package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/yangflow/hoplet/proto"
)

// TurnState reports a session's highest turn number (0 when it has none)
// and whether that turn is still open (started, not yet ended).
func (s *Store) TurnState(ctx context.Context, id proto.SessionID) (last uint32, open bool, err error) {
	err = s.readTx(ctx, func(tx *sql.Tx) error {
		if err := requireSession(ctx, tx, id); err != nil {
			return err
		}
		var end sql.NullInt64
		row := tx.QueryRowContext(ctx, `SELECT n, end_seq FROM turns WHERE session_id = ? ORDER BY n DESC LIMIT 1`, id)
		switch err := row.Scan(&last, &end); {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("store: read turn state: %w", err)
		}
		open = !end.Valid
		return nil
	})
	return last, open, err
}

// Receipt returns the result of the append that carried the receipt key, and
// whether one exists. The result has Duplicate set.
func (s *Store) Receipt(ctx context.Context, id proto.SessionID, key string) (AppendResult, bool, error) {
	var res AppendResult
	var found bool
	err := s.readTx(ctx, func(tx *sql.Tx) error {
		if err := requireSession(ctx, tx, id); err != nil {
			return err
		}
		var err error
		res, found, err = replay(ctx, tx, id, key)
		return err
	})
	return res, found, err
}
