package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// Entry is one payload to append, with the harness's raw event if any.
type Entry struct {
	Payload proto.Payload
	Raw     *proto.BlobRef
}

// AppendRequest appends entries to a session's log in one transaction.
type AppendRequest struct {
	Session proto.SessionID
	Turn    uint32
	Time    time.Time
	Entries []Entry
	// ReceiptKey, when set, makes the append idempotent: a second append
	// with the same key changes nothing and reports Duplicate.
	ReceiptKey string
	// IngestOffset, when set, records how far the shim spool has been read,
	// in the same transaction as the events it produced.
	IngestOffset *int64
}

// AppendResult reports the sequence numbers assigned.
type AppendResult struct {
	FirstSeq  uint64
	LastSeq   uint64
	Duplicate bool
}

// Append writes events, updates projections (session status, turns,
// approvals), records the receipt and ingest offset, all in one
// transaction. It is durable when it carries a receipt, a turn end or an
// approval decision.
func (s *Store) Append(ctx context.Context, req AppendRequest) (AppendResult, error) {
	if len(req.Entries) == 0 && req.IngestOffset == nil {
		return AppendResult{}, errors.New("store: append needs entries or an ingest offset")
	}
	if req.Time.IsZero() {
		req.Time = time.Now()
	}
	events := make([]proto.Event, 0, len(req.Entries))
	for _, e := range req.Entries {
		ev, err := proto.NewEvent(req.Session, 0, req.Turn, req.Time, e.Payload)
		if err != nil {
			return AppendResult{}, fmt.Errorf("store: %w", err)
		}
		ev.Raw = e.Raw
		events = append(events, ev)
	}
	var res AppendResult
	err := s.tx(ctx, durable(req), func(tx *sql.Tx) error {
		if req.ReceiptKey != "" {
			err := tx.QueryRowContext(ctx, `SELECT first_seq, last_seq FROM receipts WHERE key = ?`, req.ReceiptKey).
				Scan(&res.FirstSeq, &res.LastSeq)
			if err == nil {
				res.Duplicate = true
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("store: read receipt: %w", err)
			}
		}
		var last uint64
		var status proto.SessionStatus
		err := tx.QueryRowContext(ctx, `SELECT last_seq, status FROM sessions WHERE id = ?`, req.Session).Scan(&last, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: session %s", ErrNotFound, req.Session)
		}
		if err != nil {
			return fmt.Errorf("store: read session: %w", err)
		}
		res.FirstSeq = last + 1
		for i := range events {
			last++
			events[i].Seq = last
			if err := insertEvent(ctx, tx, events[i], messageID(req.Entries[i].Payload)); err != nil {
				return err
			}
			if status, err = project(ctx, tx, events[i], req.Entries[i].Payload, status); err != nil {
				return err
			}
		}
		res.LastSeq = last
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET last_seq = ?, status = ?, updated_at = ? WHERE id = ?`,
			last, status, req.Time.UTC().UnixMilli(), req.Session)
		if err != nil {
			return fmt.Errorf("store: update session: %w", err)
		}
		if req.ReceiptKey != "" {
			_, err := tx.ExecContext(ctx, `INSERT INTO receipts (key, session_id, first_seq, last_seq, created_at) VALUES (?, ?, ?, ?, ?)`,
				req.ReceiptKey, req.Session, res.FirstSeq, res.LastSeq, req.Time.UTC().UnixMilli())
			if err != nil {
				return fmt.Errorf("store: write receipt: %w", err)
			}
		}
		if req.IngestOffset != nil {
			_, err := tx.ExecContext(ctx, `INSERT INTO ingest_offsets (session_id, offset) VALUES (?, ?)
				ON CONFLICT (session_id) DO UPDATE SET offset = excluded.offset`, req.Session, *req.IngestOffset)
			if err != nil {
				return fmt.Errorf("store: write ingest offset: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return AppendResult{}, err
	}
	return res, nil
}

func durable(req AppendRequest) bool {
	if req.ReceiptKey != "" {
		return true
	}
	for _, e := range req.Entries {
		switch e.Payload.(type) {
		case proto.TurnEnded, proto.ApprovalResolved:
			return true
		}
	}
	return false
}

// messageID returns the message a delta or message belongs to, so that
// compaction can find superseded deltas.
func messageID(p proto.Payload) sql.NullString {
	switch p := p.(type) {
	case proto.MessageDelta:
		return sql.NullString{String: p.MessageID, Valid: true}
	case proto.Message:
		return sql.NullString{String: p.ID, Valid: true}
	}
	return sql.NullString{}
}

func insertEvent(ctx context.Context, tx *sql.Tx, ev proto.Event, msgID sql.NullString) error {
	var rawSHA sql.NullString
	var rawSize sql.NullInt64
	if ev.Raw != nil {
		rawSHA = sql.NullString{String: ev.Raw.SHA256, Valid: true}
		rawSize = sql.NullInt64{Int64: ev.Raw.Size, Valid: true}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO events (session_id, seq, ts, turn, type, v, payload, raw_sha256, raw_size, message_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.Session, ev.Seq, ev.Time.UnixMilli(), ev.Turn, ev.Type, ev.V, string(ev.Payload), rawSHA, rawSize, msgID)
	if err != nil {
		return fmt.Errorf("store: insert event %d: %w", ev.Seq, err)
	}
	return nil
}

// project applies one event to the projections and returns the session's
// new status.
func project(ctx context.Context, tx *sql.Tx, ev proto.Event, p proto.Payload, status proto.SessionStatus) (proto.SessionStatus, error) {
	exec := func(q string, args ...any) error {
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("store: project %s at seq %d: %w", ev.Type, ev.Seq, err)
		}
		return nil
	}
	switch p := p.(type) {
	case proto.TurnStarted:
		if ev.Turn == 0 {
			return status, fmt.Errorf("store: turn.started at seq %d has no turn number", ev.Seq)
		}
		return proto.StatusRunning, exec(`INSERT INTO turns (session_id, n, start_seq) VALUES (?, ?, ?)`, ev.Session, ev.Turn, ev.Seq)
	case proto.TurnEnded:
		next := proto.StatusIdle
		if p.Reason == proto.TurnFailed || p.Reason == proto.TurnAborted {
			next = proto.StatusErrored
		}
		return next, exec(`UPDATE turns SET end_seq = ?, reason = ? WHERE session_id = ? AND n = ?`, ev.Seq, p.Reason, ev.Session, ev.Turn)
	case proto.ApprovalRequested:
		return proto.StatusWaiting, exec(`INSERT INTO approvals (id, session_id, request_seq, kind, status) VALUES (?, ?, ?, ?, 'pending')`,
			p.ID, ev.Session, ev.Seq, p.Kind)
	case proto.ApprovalResolved:
		if err := exec(`UPDATE approvals SET status = ?, option_id = ?, decided_by = ?, resolved_seq = ? WHERE id = ? AND session_id = ?`,
			p.Outcome, p.OptionID, p.DecidedBy, ev.Seq, p.ID, ev.Session); err != nil {
			return status, err
		}
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM approvals WHERE session_id = ? AND status = 'pending'`, ev.Session).Scan(&pending); err != nil {
			return status, fmt.Errorf("store: count pending approvals: %w", err)
		}
		if pending == 0 && status == proto.StatusWaiting {
			return proto.StatusRunning, nil
		}
		return status, nil
	}
	return status, nil
}
