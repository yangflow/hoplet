package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// The ways a session can have had an entry.
const (
	MemoryUseInjected = "injected"
	MemoryUseRead     = "read"
)

const insertMemoryUse = `INSERT INTO memory_uses (session_id, entry_id, version, way, created_at) VALUES (?, ?, ?, ?, ?)`

// AddMemoryUse records that a session read an entry.
func (s *Store) AddMemoryUse(ctx context.Context, id proto.SessionID, entry string, version int, way string) error {
	return s.tx(ctx, false, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, insertMemoryUse, string(id), entry, version, way, ms(time.Now())); err != nil {
			return fmt.Errorf("store: record memory use: %w", err)
		}
		return nil
	})
}

// MemoryUseCounts returns, by entry, how many sessions have used it.
func (s *Store) MemoryUseCounts(ctx context.Context) (_ map[string]int, err error) {
	rows, err := s.rdb.QueryContext(ctx, `SELECT entry_id, COUNT(DISTINCT session_id) FROM memory_uses GROUP BY entry_id`)
	if err != nil {
		return nil, fmt.Errorf("store: read memory uses: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out := map[string]int{}
	for rows.Next() {
		var (
			id string
			n  int
		)
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("store: scan memory uses: %w", err)
		}
		out[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read memory uses: %w", err)
	}
	return out, nil
}

// MemoryUsesOf returns the distinct entries a session has used, first used
// first.
func (s *Store) MemoryUsesOf(ctx context.Context, id proto.SessionID) (_ []string, err error) {
	rows, err := s.rdb.QueryContext(ctx, `SELECT entry_id FROM memory_uses WHERE session_id = ?
		GROUP BY entry_id ORDER BY MIN(id)`, string(id))
	if err != nil {
		return nil, fmt.Errorf("store: read memory uses: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var out []string
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			return nil, fmt.Errorf("store: scan memory uses: %w", err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read memory uses: %w", err)
	}
	return out, nil
}
