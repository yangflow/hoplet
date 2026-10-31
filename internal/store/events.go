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

// Events returns up to limit events of a session with seq > after, in
// order. Seq gaps are normal: compaction removes superseded deltas.
func (s *Store) Events(ctx context.Context, id proto.SessionID, after uint64, limit int) ([]proto.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, ts, turn, type, v, payload, raw_sha256, raw_size
		FROM events WHERE session_id = ? AND seq > ? ORDER BY seq LIMIT ?`, id, after, limit)
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

// PendingApprovals returns the session's unresolved approval requests in
// request order.
func (s *Store) PendingApprovals(ctx context.Context, id proto.SessionID) ([]proto.ApprovalRequested, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.type, e.v, e.payload FROM approvals a
		JOIN events e ON e.session_id = a.session_id AND e.seq = a.request_seq
		WHERE a.session_id = ? AND a.status = 'pending' ORDER BY a.request_seq`, id)
	if err != nil {
		return nil, fmt.Errorf("store: read pending approvals: %w", err)
	}
	defer rows.Close()
	var out []proto.ApprovalRequested
	for rows.Next() {
		var ev proto.Event
		var payload string
		if err := rows.Scan(&ev.Type, &ev.V, &payload); err != nil {
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
		out = append(out, req)
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
	err := s.db.QueryRowContext(ctx, `SELECT offset FROM ingest_offsets WHERE session_id = ?`, id).Scan(&off)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: read ingest offset: %w", err)
	}
	return off, nil
}
