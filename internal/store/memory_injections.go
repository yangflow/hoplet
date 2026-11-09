package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// The states of a memory injection.
const (
	MemoryInjectionDelivered   = "delivered"
	MemoryInjectionFailed      = "failed"
	MemoryInjectionUnsupported = "unsupported"
)

// MemoryInjectionEntry is one entry a session was offered. Indexed means
// only its index line was.
type MemoryInjectionEntry struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Indexed bool   `json:"indexed,omitempty"`
}

// MemoryInjection records what one session start was offered. Delivered
// means the harness was handed it, not that the model followed it.
type MemoryInjection struct {
	Session   proto.SessionID
	Status    string
	Entries   []MemoryInjectionEntry
	Detail    string
	CreatedAt time.Time
}

// AddMemoryInjection records one injection.
func (s *Store) AddMemoryInjection(ctx context.Context, in MemoryInjection) error {
	if in.Entries == nil {
		in.Entries = []MemoryInjectionEntry{}
	}
	entries, err := json.Marshal(in.Entries)
	if err != nil {
		return fmt.Errorf("store: encode memory injection: %w", err)
	}
	return s.tx(ctx, false, func(tx *sql.Tx) error {
		now := ms(time.Now())
		_, err := tx.ExecContext(ctx, `INSERT INTO memory_injections (session_id, status, entries, detail, created_at)
			VALUES (?, ?, ?, ?, ?)`, string(in.Session), in.Status, string(entries), in.Detail, now)
		if err != nil {
			return fmt.Errorf("store: record memory injection: %w", err)
		}
		if in.Status != MemoryInjectionDelivered {
			return nil
		}
		for _, e := range in.Entries {
			if e.Indexed {
				continue // the title alone is not the entry
			}
			if _, err := tx.ExecContext(ctx, insertMemoryUse, string(in.Session), e.ID, e.Version, MemoryUseInjected, now); err != nil {
				return fmt.Errorf("store: record memory use: %w", err)
			}
		}
		return nil
	})
}

// MemoryInjections returns a session's injections, oldest first.
func (s *Store) MemoryInjections(ctx context.Context, id proto.SessionID) (_ []MemoryInjection, err error) {
	rows, err := s.rdb.QueryContext(ctx, `SELECT session_id, status, entries, detail, created_at
		FROM memory_injections WHERE session_id = ? ORDER BY id`, string(id))
	if err != nil {
		return nil, fmt.Errorf("store: read memory injections: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var out []MemoryInjection
	for rows.Next() {
		var (
			in      MemoryInjection
			entries string
			at      int64
		)
		if err := rows.Scan(&in.Session, &in.Status, &entries, &in.Detail, &at); err != nil {
			return nil, fmt.Errorf("store: scan memory injection: %w", err)
		}
		if err := json.Unmarshal([]byte(entries), &in.Entries); err != nil {
			return nil, fmt.Errorf("store: decode memory injection: %w", err)
		}
		in.CreatedAt = time.UnixMilli(at).UTC()
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read memory injections: %w", err)
	}
	return out, nil
}
