package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yangflow/hoplet/proto"
)

// Compact deletes a session's message deltas that a complete message with
// the same ID has superseded, and returns how many it deleted. Replaying
// the log afterwards yields the same final messages.
func (s *Store) Compact(ctx context.Context, id proto.SessionID) (int64, error) {
	var n int64
	err := s.tx(ctx, false, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM events WHERE session_id = ? AND type = ? AND message_id IN
			(SELECT message_id FROM events WHERE session_id = ? AND type = ?)`,
			id, proto.TypeMessageDelta, id, proto.TypeMessage)
		if err != nil {
			return fmt.Errorf("store: compact %s: %w", id, err)
		}
		n, err = res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: compact %s: %w", id, err)
		}
		return nil
	})
	return n, err
}
