package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// queryer is what *sql.DB and *sql.Tx share.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const eventColumns = `seq, ts, turn, type, v, payload, raw_sha256, raw_size`

// requireSession returns ErrNotFound unless the session exists.
func requireSession(ctx context.Context, q queryer, id proto.SessionID) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: session %s", ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("store: read session: %w", err)
	}
	return nil
}

// queryEvents runs a query selecting eventColumns for session id.
func queryEvents(ctx context.Context, q queryer, id proto.SessionID, query string, args ...any) ([]proto.Event, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: read events: %w", err)
	}
	defer rows.Close()
	var out []proto.Event
	for rows.Next() {
		var (
			ev      = proto.Event{Session: id}
			ts      int64
			payload string
			rawSHA  sql.NullString
			rawSize sql.NullInt64
		)
		if err := rows.Scan(&ev.Seq, &ts, &ev.Turn, &ev.Type, &ev.V, &payload, &rawSHA, &rawSize); err != nil {
			return nil, fmt.Errorf("store: scan event: %w", err)
		}
		ev.Time = time.UnixMilli(ts).UTC()
		ev.Payload = json.RawMessage(payload)
		if rawSHA.Valid {
			ev.Raw = &proto.BlobRef{SHA256: rawSHA.String, Size: rawSize.Int64}
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read events: %w", err)
	}
	return out, nil
}

// Events returns up to limit events of a session with seq > after, in
// order. Seq gaps are normal: compaction removes superseded deltas. It
// returns ErrNotFound for an unknown session.
func (s *Store) Events(ctx context.Context, id proto.SessionID, after uint64, limit int) ([]proto.Event, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: events limit %d", ErrInvalid, limit)
	}
	if err := requireSession(ctx, s.rdb, id); err != nil {
		return nil, err
	}
	return queryEvents(ctx, s.rdb, id, `SELECT `+eventColumns+`
		FROM events WHERE session_id = ? AND seq > ? ORDER BY seq LIMIT ?`, id, after, limit)
}

// EventsBefore returns up to limit events of a session with seq < before,
// the newest ones, in ascending order. It returns ErrNotFound for an unknown
// session.
func (s *Store) EventsBefore(ctx context.Context, id proto.SessionID, before uint64, limit int) ([]proto.Event, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: events limit %d", ErrInvalid, limit)
	}
	if err := requireSession(ctx, s.rdb, id); err != nil {
		return nil, err
	}
	evs, err := queryEvents(ctx, s.rdb, id, `SELECT `+eventColumns+`
		FROM events WHERE session_id = ? AND seq < ? ORDER BY seq DESC LIMIT ?`, id, before, limit)
	slices.Reverse(evs)
	return evs, err
}

// GapSize reports how many events a session has with seq > after and their
// total payload size in bytes, so a client can decide between replaying the
// gap and fetching a snapshot.
func (s *Store) GapSize(ctx context.Context, id proto.SessionID, after uint64) (count, bytes int64, err error) {
	if err := requireSession(ctx, s.rdb, id); err != nil {
		return 0, 0, err
	}
	err = s.rdb.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(length(CAST(payload AS BLOB))), 0)
		FROM events WHERE session_id = ? AND seq > ?`, id, after).Scan(&count, &bytes)
	if err != nil {
		return 0, 0, fmt.Errorf("store: measure gap: %w", err)
	}
	return count, bytes, nil
}

// PendingApproval is an unresolved approval request and the sequence number
// of the event that raised it.
type PendingApproval struct {
	RequestSeq uint64
	Request    proto.ApprovalRequested
}

// PendingApprovals returns the session's unresolved approval requests in
// request order.
func (s *Store) PendingApprovals(ctx context.Context, id proto.SessionID) ([]PendingApproval, error) {
	return pendingApprovals(ctx, s.rdb, id)
}

func pendingApprovals(ctx context.Context, q queryer, id proto.SessionID) ([]PendingApproval, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.request_seq, e.type, e.v, e.payload FROM approvals a
		JOIN events e ON e.session_id = a.session_id AND e.seq = a.request_seq
		WHERE a.session_id = ? AND a.status = 'pending' ORDER BY a.request_seq`, id)
	if err != nil {
		return nil, fmt.Errorf("store: read pending approvals: %w", err)
	}
	defer rows.Close()
	var out []PendingApproval
	for rows.Next() {
		var ev proto.Event
		var seq uint64
		var payload string
		if err := rows.Scan(&seq, &ev.Type, &ev.V, &payload); err != nil {
			return nil, fmt.Errorf("store: scan approval: %w", err)
		}
		ev.Payload = json.RawMessage(payload)
		p, err := ev.Decode()
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
		req, ok := p.(proto.ApprovalRequested)
		if !ok {
			return nil, fmt.Errorf("store: approval row points at a %s event", ev.Type)
		}
		out = append(out, PendingApproval{RequestSeq: seq, Request: req})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read pending approvals: %w", err)
	}
	return out, nil
}

// IngestOffset returns how far the session's shim spool has been ingested;
// 0 when nothing has been.
func (s *Store) IngestOffset(ctx context.Context, id proto.SessionID) (int64, error) {
	var off int64
	err := s.rdb.QueryRowContext(ctx, `SELECT offset FROM ingest_offsets WHERE session_id = ?`, id).Scan(&off)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: read ingest offset: %w", err)
	}
	return off, nil
}

// SessionSnapshot is a consistent view of a session for a client that is
// catching up: the record, its pending approvals and its recent events.
type SessionSnapshot struct {
	Session proto.Session
	Pending []PendingApproval
	Events  []proto.Event
}

// Snapshot reads, in one read transaction, a session, its pending approvals
// and all events from the start of its last turns turns (all events when it
// has fewer). Events after that point that belong to no turn are included.
func (s *Store) Snapshot(ctx context.Context, id proto.SessionID, turns int) (SessionSnapshot, error) {
	if turns <= 0 {
		return SessionSnapshot{}, fmt.Errorf("%w: snapshot turns %d", ErrInvalid, turns)
	}
	tx, err := s.rdb.BeginTx(ctx, nil)
	if err != nil {
		return SessionSnapshot{}, fmt.Errorf("store: begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var snap SessionSnapshot
	if snap.Session, err = getSession(ctx, tx, id); err != nil {
		return SessionSnapshot{}, err
	}
	if snap.Pending, err = pendingApprovals(ctx, tx, id); err != nil {
		return SessionSnapshot{}, err
	}
	var start uint64
	err = tx.QueryRowContext(ctx, `SELECT start_seq FROM turns WHERE session_id = ? ORDER BY n DESC LIMIT 1 OFFSET ?`, id, turns-1).Scan(&start)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SessionSnapshot{}, fmt.Errorf("store: find snapshot start: %w", err)
	}
	if snap.Events, err = queryEvents(ctx, tx, id, `SELECT `+eventColumns+`
		FROM events WHERE session_id = ? AND seq >= ? ORDER BY seq`, id, start); err != nil {
		return SessionSnapshot{}, err
	}
	return snap, nil
}
