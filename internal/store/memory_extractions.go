package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// The states of an extraction run.
const (
	ExtractionPending = "pending"
	ExtractionRunning = "running"
	ExtractionParsed  = "parsed"
	ExtractionDone    = "done"
	ExtractionFailed  = "failed"
)

// MemoryExtraction is one background extraction over a range of a session's
// events; From and To are inclusive. Output is the harness's reply, kept once
// it parses so that a crash replays the commit without calling the model.
type MemoryExtraction struct {
	ID        int64
	Session   proto.SessionID
	Harness   string
	Model     string
	From, To  uint64
	Trigger   string
	State     string
	Attempts  int
	Output    string
	Usage     proto.Usage
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const extractionColumns = `id, session_id, harness, model, from_seq, to_seq, trigger, state, attempts, output, usage, error, created_at, updated_at`

func scanExtraction(row interface{ Scan(...any) error }) (MemoryExtraction, error) {
	var (
		r            MemoryExtraction
		session      string
		usage        string
		created, upd int64
	)
	if err := row.Scan(&r.ID, &session, &r.Harness, &r.Model, &r.From, &r.To, &r.Trigger, &r.State, &r.Attempts, &r.Output, &usage, &r.Error, &created, &upd); err != nil {
		return MemoryExtraction{}, err
	}
	if err := json.Unmarshal([]byte(usage), &r.Usage); err != nil {
		return MemoryExtraction{}, fmt.Errorf("store: decode extraction usage: %w", err)
	}
	r.Session = proto.SessionID(session)
	r.CreatedAt, r.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(upd).UTC()
	return r, nil
}

// CreateMemoryExtraction records a pending run and returns it with its ID. A
// session has at most one unfinished run: another fails with ErrExists.
func (s *Store) CreateMemoryExtraction(ctx context.Context, r MemoryExtraction, now time.Time) (MemoryExtraction, error) {
	if r.Session == "" || r.Harness == "" || r.Model == "" || r.From == 0 || r.From > r.To || r.Trigger == "" {
		return MemoryExtraction{}, fmt.Errorf("%w: extraction of %q over %d-%d", ErrInvalid, r.Session, r.From, r.To)
	}
	r.State, r.CreatedAt, r.UpdatedAt = ExtractionPending, now, now
	err := s.tx(ctx, true, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO memory_extractions (session_id, harness, model, from_seq, to_seq, trigger, state, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, string(r.Session), r.Harness, r.Model, r.From, r.To, r.Trigger, r.State, ms(now), ms(now))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return fmt.Errorf("%w: session %s has an unfinished extraction", ErrExists, r.Session)
			}
			return fmt.Errorf("store: create extraction: %w", err)
		}
		r.ID, err = res.LastInsertId()
		return err
	})
	return r, err
}

// OpenMemoryExtractions returns the unfinished runs, oldest first.
func (s *Store) OpenMemoryExtractions(ctx context.Context) ([]MemoryExtraction, error) {
	rows, err := s.rdb.QueryContext(ctx, `SELECT `+extractionColumns+` FROM memory_extractions
		WHERE state IN ('pending', 'running', 'parsed') ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: open extractions: %w", err)
	}
	defer rows.Close()
	var out []MemoryExtraction
	for rows.Next() {
		r, err := scanExtraction(rows)
		if err != nil {
			return nil, fmt.Errorf("store: read extraction: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastMemoryExtraction returns a session's latest run, or ErrNotFound.
func (s *Store) LastMemoryExtraction(ctx context.Context, id proto.SessionID) (MemoryExtraction, error) {
	r, err := scanExtraction(s.rdb.QueryRowContext(ctx, `SELECT `+extractionColumns+` FROM memory_extractions
		WHERE session_id = ? ORDER BY id DESC LIMIT 1`, string(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return MemoryExtraction{}, fmt.Errorf("%w: extraction of %s", ErrNotFound, id)
	}
	if err != nil {
		return MemoryExtraction{}, fmt.Errorf("store: last extraction: %w", err)
	}
	return r, nil
}

func (s *Store) updateExtraction(ctx context.Context, id int64, query string, args ...any) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, query, append(args, id)...)
		if err != nil {
			return fmt.Errorf("store: update extraction %d: %w", id, err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return fmt.Errorf("%w: extraction %d", ErrNotFound, id)
		}
		return nil
	})
}

// StartMemoryExtraction marks a run as calling the harness and counts the
// attempt.
func (s *Store) StartMemoryExtraction(ctx context.Context, id int64, now time.Time) error {
	return s.updateExtraction(ctx, id, `UPDATE memory_extractions SET state = 'running', attempts = attempts + 1, updated_at = ? WHERE id = ?`, ms(now))
}

// RetryMemoryExtraction returns a run to pending, for another attempt.
func (s *Store) RetryMemoryExtraction(ctx context.Context, id int64, reason string, now time.Time) error {
	return s.updateExtraction(ctx, id, `UPDATE memory_extractions SET state = 'pending', error = ?, updated_at = ? WHERE id = ?`, reason, ms(now))
}

// FailMemoryExtraction gives up on a run. Progress does not move, so a later
// trigger reads the same events again.
func (s *Store) FailMemoryExtraction(ctx context.Context, id int64, reason string, now time.Time) error {
	return s.updateExtraction(ctx, id, `UPDATE memory_extractions SET state = 'failed', error = ?, updated_at = ? WHERE id = ?`, reason, ms(now))
}

// SetMemoryExtractionParsed keeps a run's reply, which has parsed.
func (s *Store) SetMemoryExtractionParsed(ctx context.Context, id int64, output string, usage proto.Usage, now time.Time) error {
	u, err := json.Marshal(usage)
	if err != nil {
		return fmt.Errorf("store: encode extraction usage: %w", err)
	}
	return s.updateExtraction(ctx, id, `UPDATE memory_extractions SET state = 'parsed', output = ?, usage = ?, error = '', updated_at = ? WHERE id = ?`, output, string(u), ms(now))
}

// FinishMemoryExtraction marks a committed run done and moves its session's
// progress to the end of the run. Progress only moves forward.
func (s *Store) FinishMemoryExtraction(ctx context.Context, r MemoryExtraction, now time.Time) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE memory_extractions SET state = 'done', updated_at = ? WHERE id = ?`, ms(now), r.ID); err != nil {
			return fmt.Errorf("store: finish extraction %d: %w", r.ID, err)
		}
		return advanceExtraction(ctx, tx, r.Session, r.To)
	})
}

// AdvanceMemoryExtraction records that a session's events through seq need
// no extraction.
func (s *Store) AdvanceMemoryExtraction(ctx context.Context, id proto.SessionID, seq uint64) error {
	return s.tx(ctx, false, func(tx *sql.Tx) error { return advanceExtraction(ctx, tx, id, seq) })
}

func advanceExtraction(ctx context.Context, tx *sql.Tx, id proto.SessionID, seq uint64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO memory_extraction_progress (session_id, through_seq) VALUES (?, ?)
		ON CONFLICT (session_id) DO UPDATE SET through_seq = MAX(through_seq, excluded.through_seq)`, string(id), seq)
	if err != nil {
		return fmt.Errorf("store: advance extraction of %s: %w", id, err)
	}
	return nil
}

// LastSeqExcept returns the sequence number of the session's last event of
// any type but skip, 0 when it has none.
func (s *Store) LastSeqExcept(ctx context.Context, id proto.SessionID, skip proto.EventType) (uint64, error) {
	var seq uint64
	err := s.rdb.QueryRowContext(ctx, `SELECT seq FROM events WHERE session_id = ? AND type <> ? ORDER BY seq DESC LIMIT 1`,
		string(id), string(skip)).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: read last event of %s: %w", id, err)
	}
	return seq, nil
}

// MemoryExtractionProgress returns the last event of the session that has
// been read; 0 when none has.
func (s *Store) MemoryExtractionProgress(ctx context.Context, id proto.SessionID) (uint64, error) {
	var seq uint64
	err := s.rdb.QueryRowContext(ctx, `SELECT through_seq FROM memory_extraction_progress WHERE session_id = ?`, string(id)).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: extraction progress of %s: %w", id, err)
	}
	return seq, nil
}

// CountMemoryExtractions counts runs created in [from, to), for one harness,
// or every harness when kind is empty.
func (s *Store) CountMemoryExtractions(ctx context.Context, kind string, from, to time.Time) (int, error) {
	var n int
	err := s.rdb.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_extractions
		WHERE (? = '' OR harness = ?) AND created_at >= ? AND created_at < ?`, kind, kind, ms(from), ms(to)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count extractions: %w", err)
	}
	return n, nil
}

// CountFailedMemoryExtractions counts the runs that gave up in [from, to).
func (s *Store) CountFailedMemoryExtractions(ctx context.Context, from, to time.Time) (int, error) {
	var n int
	err := s.rdb.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_extractions
		WHERE state = 'failed' AND created_at >= ? AND created_at < ?`, ms(from), ms(to)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count failed extractions: %w", err)
	}
	return n, nil
}

// MemoryExtractionEnabled reports whether background extraction is on.
func (s *Store) MemoryExtractionEnabled(ctx context.Context) (bool, error) {
	var on bool
	if err := s.rdb.QueryRowContext(ctx, `SELECT enabled FROM memory_extraction_state WHERE id = 1`).Scan(&on); err != nil {
		return false, fmt.Errorf("store: read extraction switch: %w", err)
	}
	return on, nil
}

// SetMemoryExtractionEnabled turns background extraction on or off.
func (s *Store) SetMemoryExtractionEnabled(ctx context.Context, on bool) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE memory_extraction_state SET enabled = ? WHERE id = 1`, on); err != nil {
			return fmt.Errorf("store: set extraction switch: %w", err)
		}
		return nil
	})
}
