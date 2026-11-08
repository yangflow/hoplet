package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// The states of a memory candidate.
const (
	MemoryCandidateOpen     = "open"
	MemoryCandidateAccepted = "accepted"
	MemoryCandidateRejected = "rejected"
)

// MemoryCandidate is what a candidate entry proposes.
type MemoryCandidate struct {
	EntryID         string
	Action          string
	TargetID        string
	ExpectedVersion int
	Actor           string
	Reason          string
	State           string
	CreatedAt       time.Time
	DecidedAt       *time.Time
}

const memoryCandidateColumns = `entry_id, action, target_id, expected_version, actor, reason, state, created_at, decided_at`

func scanMemoryCandidate(row interface{ Scan(...any) error }) (MemoryCandidate, error) {
	var (
		c       MemoryCandidate
		created int64
		decided sql.NullInt64
	)
	if err := row.Scan(&c.EntryID, &c.Action, &c.TargetID, &c.ExpectedVersion, &c.Actor, &c.Reason, &c.State, &created, &decided); err != nil {
		return MemoryCandidate{}, err
	}
	c.CreatedAt = time.UnixMilli(created).UTC()
	if decided.Valid {
		t := time.UnixMilli(decided.Int64).UTC()
		c.DecidedAt = &t
	}
	return c, nil
}

// AddMemoryCandidate durably records an open candidate, ignoring c.State and
// the timestamps. A candidate for this entry is ErrExists.
func (s *Store) AddMemoryCandidate(ctx context.Context, c MemoryCandidate) error {
	if c.EntryID == "" {
		return fmt.Errorf("%w: a memory candidate needs an entry", ErrInvalid)
	}
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO memory_candidates (entry_id, action, target_id, expected_version, actor, reason, state, created_at)
			VALUES (?, ?, ?, ?, ?, ?, 'open', ?)`, c.EntryID, c.Action, c.TargetID, c.ExpectedVersion, c.Actor, c.Reason, ms(time.Now()))
		if isConstraint(err) {
			return fmt.Errorf("%w: memory candidate %s", ErrExists, c.EntryID)
		}
		if err != nil {
			return fmt.Errorf("store: record memory candidate %s: %w", c.EntryID, err)
		}
		return nil
	})
}

// MemoryCandidateByEntry returns the candidate of an entry.
func (s *Store) MemoryCandidateByEntry(ctx context.Context, entryID string) (MemoryCandidate, error) {
	c, err := scanMemoryCandidate(s.rdb.QueryRowContext(ctx, `SELECT `+memoryCandidateColumns+` FROM memory_candidates WHERE entry_id = ?`, entryID))
	if errors.Is(err, sql.ErrNoRows) {
		return MemoryCandidate{}, fmt.Errorf("%w: memory candidate %s", ErrNotFound, entryID)
	}
	if err != nil {
		return MemoryCandidate{}, fmt.Errorf("store: read memory candidate %s: %w", entryID, err)
	}
	return c, nil
}

// OpenMemoryCandidates returns the undecided candidates, oldest first.
func (s *Store) OpenMemoryCandidates(ctx context.Context) (_ []MemoryCandidate, err error) {
	rows, err := s.rdb.QueryContext(ctx, `SELECT `+memoryCandidateColumns+` FROM memory_candidates WHERE state = 'open' ORDER BY created_at, entry_id`)
	if err != nil {
		return nil, fmt.Errorf("store: list memory candidates: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var out []MemoryCandidate
	for rows.Next() {
		c, err := scanMemoryCandidate(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan memory candidate: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list memory candidates: %w", err)
	}
	return out, nil
}

// DecideMemoryCandidate durably closes an open candidate; one already
// decided is ErrConflict.
func (s *Store) DecideMemoryCandidate(ctx context.Context, entryID, state string) error {
	if state != MemoryCandidateAccepted && state != MemoryCandidateRejected {
		return fmt.Errorf("%w: memory candidate state %q", ErrInvalid, state)
	}
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE memory_candidates SET state = ?, decided_at = ? WHERE entry_id = ? AND state = 'open'`, state, ms(time.Now()), entryID)
		if err != nil {
			return fmt.Errorf("store: decide memory candidate %s: %w", entryID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: decide memory candidate %s: %w", entryID, err)
		}
		if n == 0 {
			return fmt.Errorf("%w: no open memory candidate %s", ErrConflict, entryID)
		}
		return nil
	})
}
