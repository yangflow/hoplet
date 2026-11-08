package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// AppendMemorySaved records a memory save in the session's log. A repeated
// key changes nothing.
func (s *Store) AppendMemorySaved(ctx context.Context, id proto.SessionID, now time.Time, saved proto.MemorySaved, key string) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		_, done, err := replay(ctx, tx, id, key)
		if err != nil || done {
			return err
		}
		return appendKeyedRecord(ctx, tx, id, now, saved, key)
	})
}
