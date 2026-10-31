package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// MaxQueue is how many messages may wait in one session's queue.
const MaxQueue = 100

// Enqueue appends a message to the session's queue and returns it. key makes
// the call idempotent: a message already queued under key is returned as it
// is, with queued false. The write is durable.
func (s *Store) Enqueue(ctx context.Context, id proto.SessionID, key, text string) (msg proto.QueuedMessage, queued bool, err error) {
	if key == "" {
		return proto.QueuedMessage{}, false, fmt.Errorf("%w: queued message needs a key", ErrInvalid)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	err = s.tx(ctx, true, func(tx *sql.Tx) error {
		if err := requireSession(ctx, tx, id); err != nil {
			return err
		}
		m, err := queuedMessage(ctx, tx, id, key)
		if err == nil {
			msg = m
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM queue WHERE session_id = ?`, id).Scan(&n); err != nil {
			return fmt.Errorf("store: count queue: %w", err)
		}
		if n >= MaxQueue {
			return fmt.Errorf("%w: the queue of %s is full", ErrInvalid, id)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO queue (session_id, key, text, position, created_at) VALUES (?, ?, ?, ?, ?)`,
			id, key, text, n, now.UnixMilli())
		if err != nil {
			return fmt.Errorf("store: enqueue: %w", err)
		}
		msg, queued = proto.QueuedMessage{Key: key, Text: text, Position: n, CreatedAt: now}, true
		return nil
	})
	if err != nil {
		return proto.QueuedMessage{}, false, err
	}
	return msg, queued, nil
}

// Queue returns the session's queued messages, next first.
func (s *Store) Queue(ctx context.Context, id proto.SessionID) ([]proto.QueuedMessage, error) {
	var out []proto.QueuedMessage
	err := s.readTx(ctx, func(tx *sql.Tx) error {
		if err := requireSession(ctx, tx, id); err != nil {
			return err
		}
		var err error
		out, err = queue(ctx, tx, id)
		return err
	})
	return out, err
}

func queue(ctx context.Context, q queryer, id proto.SessionID) ([]proto.QueuedMessage, error) {
	rows, err := q.QueryContext(ctx, `SELECT key, text, position, created_at, error FROM queue
		WHERE session_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, fmt.Errorf("store: read queue: %w", err)
	}
	defer rows.Close()
	var out []proto.QueuedMessage
	for rows.Next() {
		m, err := scanQueued(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read queue: %w", err)
	}
	return out, nil
}

func queuedMessage(ctx context.Context, q queryer, id proto.SessionID, key string) (proto.QueuedMessage, error) {
	m, err := scanQueued(q.QueryRowContext(ctx, `SELECT key, text, position, created_at, error FROM queue
		WHERE session_id = ? AND key = ?`, id, key))
	if errors.Is(err, sql.ErrNoRows) {
		return proto.QueuedMessage{}, fmt.Errorf("%w: queued message %s", ErrNotFound, key)
	}
	return m, err
}

func scanQueued(r scanner) (proto.QueuedMessage, error) {
	var m proto.QueuedMessage
	var created int64
	if err := r.Scan(&m.Key, &m.Text, &m.Position, &created, &m.Error); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return proto.QueuedMessage{}, err
		}
		return proto.QueuedMessage{}, fmt.Errorf("store: scan queued message: %w", err)
	}
	m.CreatedAt = time.UnixMilli(created).UTC()
	return m, nil
}

// QueuedSessions returns the sessions whose queue is not empty.
func (s *Store) QueuedSessions(ctx context.Context) ([]proto.SessionID, error) {
	rows, err := s.rdb.QueryContext(ctx, `SELECT DISTINCT session_id FROM queue ORDER BY session_id`)
	if err != nil {
		return nil, fmt.Errorf("store: list queued sessions: %w", err)
	}
	defer rows.Close()
	var out []proto.SessionID
	for rows.Next() {
		var id proto.SessionID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan queued session: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list queued sessions: %w", err)
	}
	return out, nil
}

// EditQueued replaces a queued message's text and clears its error. The
// write is durable.
func (s *Store) EditQueued(ctx context.Context, id proto.SessionID, key, text string) error {
	return s.updateQueued(ctx, id, key, `UPDATE queue SET text = ?, error = '' WHERE session_id = ? AND key = ?`, text, id, key)
}

// FailQueued records why a queued message could not be sent; the message
// stays queued until it is edited, moved or deleted. The write is durable.
func (s *Store) FailQueued(ctx context.Context, id proto.SessionID, key, reason string) error {
	if reason == "" {
		return fmt.Errorf("%w: a failure needs a reason", ErrInvalid)
	}
	return s.updateQueued(ctx, id, key, `UPDATE queue SET error = ? WHERE session_id = ? AND key = ?`, reason, id, key)
}

func (s *Store) updateQueued(ctx context.Context, id proto.SessionID, key, query string, args ...any) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("store: update queued message: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("store: update queued message: %w", err)
		} else if n == 0 {
			return fmt.Errorf("%w: queued message %s of %s", ErrNotFound, key, id)
		}
		return nil
	})
}

// DeleteQueued removes a queued message. The write is durable.
func (s *Store) DeleteQueued(ctx context.Context, id proto.SessionID, key string) error {
	return s.reorder(ctx, id, key, func(keys []string, i int) []string {
		return append(keys[:i:i], keys[i+1:]...)
	})
}

// MoveQueued moves a queued message to position pos, 0 being next; pos is
// clamped to the queue. Moving clears the message's error. The write is
// durable.
func (s *Store) MoveQueued(ctx context.Context, id proto.SessionID, key string, pos int) error {
	return s.reorder(ctx, id, key, func(keys []string, i int) []string {
		rest := append(keys[:i:i], keys[i+1:]...)
		pos = max(0, min(pos, len(rest)))
		out := append(rest[:pos:pos], key)
		return append(out, rest[pos:]...)
	})
}

// reorder rewrites the positions of a session's queue to the order fn
// returns, given the keys in order and the index of key; keys fn drops are
// deleted.
func (s *Store) reorder(ctx context.Context, id proto.SessionID, key string, fn func(keys []string, i int) []string) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		msgs, err := queue(ctx, tx, id)
		if err != nil {
			return err
		}
		keys := make([]string, len(msgs))
		at := -1
		for i, m := range msgs {
			keys[i] = m.Key
			if m.Key == key {
				at = i
			}
		}
		if at < 0 {
			return fmt.Errorf("%w: queued message %s of %s", ErrNotFound, key, id)
		}
		order := fn(keys, at)
		if len(order) < len(keys) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM queue WHERE session_id = ? AND key = ?`, id, key); err != nil {
				return fmt.Errorf("store: delete queued message: %w", err)
			}
		} else if _, err := tx.ExecContext(ctx, `UPDATE queue SET error = '' WHERE session_id = ? AND key = ?`, id, key); err != nil {
			return fmt.Errorf("store: clear queued error: %w", err)
		}
		for pos, k := range order {
			if _, err := tx.ExecContext(ctx, `UPDATE queue SET position = ? WHERE session_id = ? AND key = ?`, pos, id, k); err != nil {
				return fmt.Errorf("store: reorder queue: %w", err)
			}
		}
		return nil
	})
}
