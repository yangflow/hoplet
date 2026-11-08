package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The states of a memory operation.
const (
	MemoryOpPending   = "pending"
	MemoryOpCommitted = "committed"
	MemoryOpFailed    = "failed"
)

// MemoryOp is one write to a memory entry.
type MemoryOp struct {
	ID              string
	EntryID         string
	ExpectedVersion int
	NewVersion      int
	Actor           string
	Reason          string
	Content         string
	Hash            string
	State           string
	CreatedAt       time.Time
	FinishedAt      *time.Time
}

// MemoryRow is an entry's row in the search index.
type MemoryRow struct {
	ID         string
	Version    int
	Category   string
	Status     string
	ScopeKind  string
	ScopeValue string
	Title      string
	Body       string
	Hash       string
	UpdatedAt  time.Time
}

// MemoryProblem is an entry file a scan could not use. Path is relative to
// the memory root.
type MemoryProblem struct {
	Path string
	Err  string
}

// MemoryQuery filters the search index. Empty fields match everything, and a
// Limit of zero or less means no limit.
type MemoryQuery struct {
	Category   string
	Status     string
	ScopeKind  string
	ScopeValue string
	Text       string
	Limit      int
}

const memoryOpColumns = `id, entry_id, expected_version, new_version, actor, reason, content, hash, state, created_at, finished_at`

func scanMemoryOp(row interface{ Scan(...any) error }) (MemoryOp, error) {
	var (
		op       MemoryOp
		created  int64
		finished sql.NullInt64
	)
	if err := row.Scan(&op.ID, &op.EntryID, &op.ExpectedVersion, &op.NewVersion, &op.Actor,
		&op.Reason, &op.Content, &op.Hash, &op.State, &created, &finished); err != nil {
		return MemoryOp{}, err
	}
	op.CreatedAt = time.UnixMilli(created).UTC()
	if finished.Valid {
		t := time.UnixMilli(finished.Int64).UTC()
		op.FinishedAt = &t
	}
	return op, nil
}

// BeginMemoryOp durably records op as pending, ignoring op.State and the
// timestamps. An existing operation with this ID and hash is returned with
// ErrExists; one with another hash is ErrConflict.
func (s *Store) BeginMemoryOp(ctx context.Context, op MemoryOp) (MemoryOp, error) {
	if op.ID == "" || op.EntryID == "" || op.Hash == "" || op.NewVersion < 1 {
		return MemoryOp{}, fmt.Errorf("%w: a memory operation needs an ID, an entry, a hash and a version", ErrInvalid)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	op.State, op.CreatedAt, op.FinishedAt = MemoryOpPending, now, nil
	var out MemoryOp
	err := s.tx(ctx, true, func(tx *sql.Tx) error {
		cur, err := scanMemoryOp(tx.QueryRowContext(ctx, `SELECT `+memoryOpColumns+` FROM memory_ops WHERE id = ?`, op.ID))
		switch {
		case err == nil && cur.Hash == op.Hash:
			out = cur
			return fmt.Errorf("%w: memory operation %s", ErrExists, op.ID)
		case err == nil:
			return fmt.Errorf("%w: memory operation %s has other content", ErrConflict, op.ID)
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("store: read memory operation %s: %w", op.ID, err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO memory_ops (id, entry_id, expected_version, new_version, actor, reason, content, hash, state, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, op.ID, op.EntryID, op.ExpectedVersion, op.NewVersion,
			op.Actor, op.Reason, op.Content, op.Hash, op.State, ms(now))
		if err != nil {
			return fmt.Errorf("store: record memory operation %s: %w", op.ID, err)
		}
		out = op
		return nil
	})
	return out, err
}

func finishMemoryOp(ctx context.Context, tx *sql.Tx, id, state string) error {
	res, err := tx.ExecContext(ctx, `UPDATE memory_ops SET state = ?, finished_at = ? WHERE id = ? AND state = 'pending'`,
		state, ms(time.Now()), id)
	if isConstraint(err) {
		return fmt.Errorf("%w: memory operation %s repeats a committed version", ErrConflict, id)
	}
	if err != nil {
		return fmt.Errorf("store: finish memory operation %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: finish memory operation %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: no pending memory operation %s", ErrNotFound, id)
	}
	return nil
}

func upsertMemoryRow(ctx context.Context, tx *sql.Tx, r MemoryRow) error {
	_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO memory_index
		(id, version, category, status, scope_kind, scope_value, title, body, hash, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, r.Version, r.Category, r.Status, r.ScopeKind,
		r.ScopeValue, r.Title, r.Body, r.Hash, ms(r.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: index memory entry %s: %w", r.ID, err)
	}
	return nil
}

// FinishMemoryOp durably marks the pending operation committed and puts its
// entry in the index.
func (s *Store) FinishMemoryOp(ctx context.Context, id string, row MemoryRow) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		if err := finishMemoryOp(ctx, tx, id, MemoryOpCommitted); err != nil {
			return err
		}
		return upsertMemoryRow(ctx, tx, row)
	})
}

// FailMemoryOp durably marks the pending operation failed.
func (s *Store) FailMemoryOp(ctx context.Context, id string) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		return finishMemoryOp(ctx, tx, id, MemoryOpFailed)
	})
}

// MemoryOpByID returns one operation in any state.
func (s *Store) MemoryOpByID(ctx context.Context, id string) (MemoryOp, error) {
	op, err := scanMemoryOp(s.rdb.QueryRowContext(ctx, `SELECT `+memoryOpColumns+` FROM memory_ops WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return MemoryOp{}, fmt.Errorf("%w: memory operation %s", ErrNotFound, id)
	}
	if err != nil {
		return MemoryOp{}, fmt.Errorf("store: read memory operation %s: %w", id, err)
	}
	return op, nil
}

func (s *Store) queryMemoryOps(ctx context.Context, query string, args ...any) (_ []MemoryOp, err error) {
	rows, err := s.rdb.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list memory operations: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var out []MemoryOp
	for rows.Next() {
		op, err := scanMemoryOp(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan memory operation: %w", err)
		}
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list memory operations: %w", err)
	}
	return out, nil
}

// PendingMemoryOps returns the unfinished operations, oldest first.
func (s *Store) PendingMemoryOps(ctx context.Context) ([]MemoryOp, error) {
	return s.queryMemoryOps(ctx, `SELECT `+memoryOpColumns+` FROM memory_ops WHERE state = 'pending' ORDER BY created_at, id`)
}

// MemoryOps returns an entry's committed operations, newest version first.
func (s *Store) MemoryOps(ctx context.Context, entryID string) ([]MemoryOp, error) {
	return s.queryMemoryOps(ctx, `SELECT `+memoryOpColumns+` FROM memory_ops WHERE entry_id = ? AND state = 'committed' ORDER BY new_version DESC`, entryID)
}

const memoryRowColumns = `id, version, category, status, scope_kind, scope_value, title, body, hash, updated_at`

func scanMemoryRow(row interface{ Scan(...any) error }) (MemoryRow, error) {
	var (
		r       MemoryRow
		updated int64
	)
	if err := row.Scan(&r.ID, &r.Version, &r.Category, &r.Status, &r.ScopeKind, &r.ScopeValue,
		&r.Title, &r.Body, &r.Hash, &updated); err != nil {
		return MemoryRow{}, err
	}
	r.UpdatedAt = time.UnixMilli(updated).UTC()
	return r, nil
}

// MemoryIndexEntry returns one entry's index row.
func (s *Store) MemoryIndexEntry(ctx context.Context, id string) (MemoryRow, error) {
	r, err := scanMemoryRow(s.rdb.QueryRowContext(ctx, `SELECT `+memoryRowColumns+` FROM memory_index WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return MemoryRow{}, fmt.Errorf("%w: memory entry %s", ErrNotFound, id)
	}
	if err != nil {
		return MemoryRow{}, fmt.Errorf("store: read memory entry %s: %w", id, err)
	}
	return r, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// SearchMemoryIndex returns the matching rows, most recently updated first.
// Text is a literal substring of the title or body.
func (s *Store) SearchMemoryIndex(ctx context.Context, q MemoryQuery) (_ []MemoryRow, err error) {
	limit, like := -1, ""
	if q.Limit > 0 {
		limit = q.Limit
	}
	if q.Text != "" {
		like = "%" + likeEscaper.Replace(q.Text) + "%"
	}
	rows, err := s.rdb.QueryContext(ctx, `SELECT `+memoryRowColumns+` FROM memory_index
		WHERE (?1 = '' OR category = ?1) AND (?2 = '' OR status = ?2)
			AND (?3 = '' OR scope_kind = ?3) AND (?4 = '' OR scope_value = ?4)
			AND (?5 = '' OR title LIKE ?5 ESCAPE '\' OR body LIKE ?5 ESCAPE '\')
		ORDER BY updated_at DESC, id LIMIT ?6`, q.Category, q.Status, q.ScopeKind, q.ScopeValue, like, limit)
	if err != nil {
		return nil, fmt.Errorf("store: search memory: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var out []MemoryRow
	for rows.Next() {
		r, err := scanMemoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan memory entry: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: search memory: %w", err)
	}
	return out, nil
}

// ReplaceMemoryIndex replaces the index and the problem list in one
// transaction. It never touches the operation log.
func (s *Store) ReplaceMemoryIndex(ctx context.Context, rows []MemoryRow, problems []MemoryProblem) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_index`); err != nil {
			return fmt.Errorf("store: clear memory index: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_problems`); err != nil {
			return fmt.Errorf("store: clear memory problems: %w", err)
		}
		for _, r := range rows {
			if err := upsertMemoryRow(ctx, tx, r); err != nil {
				return err
			}
		}
		for _, p := range problems {
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO memory_problems (path, err) VALUES (?, ?)`, p.Path, p.Err); err != nil {
				return fmt.Errorf("store: record memory problem: %w", err)
			}
		}
		return nil
	})
}

// MemoryProblems returns the files the last scan could not use, by path.
func (s *Store) MemoryProblems(ctx context.Context) (_ []MemoryProblem, err error) {
	rows, err := s.rdb.QueryContext(ctx, `SELECT path, err FROM memory_problems ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("store: list memory problems: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var out []MemoryProblem
	for rows.Next() {
		var p MemoryProblem
		if err := rows.Scan(&p.Path, &p.Err); err != nil {
			return nil, fmt.Errorf("store: scan memory problem: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list memory problems: %w", err)
	}
	return out, nil
}
