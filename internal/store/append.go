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
	// in the same transaction as the events it produced. It must not be
	// smaller than the stored offset.
	IngestOffset *int64
}

// AppendResult reports the sequence numbers assigned. Both are zero when no
// events were written, as for an append that only records an ingest offset.
type AppendResult struct {
	FirstSeq  uint64
	LastSeq   uint64
	Duplicate bool
}

// Append writes events, updates projections (turns, approvals, session
// status), records the receipt and ingest offset, all in one transaction.
//
// Session status is derived from the projections after each append: pending
// approvals mean waiting, else an open turn means running, else the latest
// ended turn decides between errored (failed or aborted) and idle.
//
// A turn.ended entry first expires the session's pending approvals: the
// store appends one approval.resolved event (outcome expired, decided by
// "hoplet:turn-ended") per pending approval, before the turn.ended event, in
// the same transaction. An approval.resolved entry for an approval that is
// unknown or no longer pending is logged but changes no projection; use
// ResolveApproval for decisions where the first answer must win.
//
// Append is durable when it carries a receipt, a turn end or an approval
// decision. Appends carrying an ingest offset otherwise commit with
// synchronous=NORMAL, so a shim must not truncate its spool based on them;
// spools are removed only when a session is archived.
//
// Invalid entries (a nil or malformed payload, turn.started or turn.ended
// without a turn number) fail the whole append with an error wrapping
// ErrInvalid. Callers ingesting harness data must, on ErrInvalid, retry entry
// by entry and replace invalid entries with a proto.Error event so ingestion
// never stalls; the store does not do this itself.
func (s *Store) Append(ctx context.Context, req AppendRequest) (AppendResult, error) {
	if len(req.Entries) == 0 && req.IngestOffset == nil {
		return AppendResult{}, fmt.Errorf("%w: append needs entries or an ingest offset", ErrInvalid)
	}
	if req.Time.IsZero() {
		req.Time = time.Now()
	}
	events, err := newEvents(req)
	if err != nil {
		return AppendResult{}, err
	}
	var res AppendResult
	err = s.tx(ctx, durable(req), func(tx *sql.Tx) error {
		dup, ok, err := replay(ctx, tx, req.Session, req.ReceiptKey)
		if err != nil || ok {
			res = dup
			return err
		}
		res, err = writeBatch(ctx, tx, req, events)
		return err
	})
	if err != nil {
		return AppendResult{}, err
	}
	return res, nil
}

// ResolveApproval records a decision for a pending approval in one durable
// transaction: the first answer wins. If the approval is not pending it
// returns an error wrapping ErrConflict, or ErrNotFound when it does not
// exist, and writes nothing. A repeated call with the same receiptKey returns
// the original result with Duplicate set.
func (s *Store) ResolveApproval(ctx context.Context, session proto.SessionID, turn uint32, t time.Time, res proto.ApprovalResolved, receiptKey string) (AppendResult, error) {
	if t.IsZero() {
		t = time.Now()
	}
	req := AppendRequest{Session: session, Turn: turn, Time: t, Entries: []Entry{{Payload: res}}, ReceiptKey: receiptKey}
	events, err := newEvents(req)
	if err != nil {
		return AppendResult{}, err
	}
	var out AppendResult
	err = s.tx(ctx, true, func(tx *sql.Tx) error {
		dup, ok, err := replay(ctx, tx, session, receiptKey)
		if err != nil || ok {
			out = dup
			return err
		}
		claim, err := tx.ExecContext(ctx, `UPDATE approvals SET status = ?, option_id = ?, decided_by = ?,
			resolved_seq = (SELECT last_seq + 1 FROM sessions WHERE id = ?)
			WHERE session_id = ? AND id = ? AND status = 'pending'`,
			res.Outcome, res.OptionID, res.DecidedBy, session, session, res.ID)
		if err != nil {
			return fmt.Errorf("store: resolve approval %s: %w", res.ID, err)
		}
		if n, err := claim.RowsAffected(); err != nil {
			return fmt.Errorf("store: resolve approval %s: %w", res.ID, err)
		} else if n == 0 {
			var status string
			err := tx.QueryRowContext(ctx, `SELECT status FROM approvals WHERE session_id = ? AND id = ?`, session, res.ID).Scan(&status)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: approval %s in session %s", ErrNotFound, res.ID, session)
			}
			if err != nil {
				return fmt.Errorf("store: read approval %s: %w", res.ID, err)
			}
			return fmt.Errorf("%w: approval %s is already %s", ErrConflict, res.ID, status)
		}
		out, err = writeBatch(ctx, tx, req, events)
		return err
	})
	if err != nil {
		return AppendResult{}, err
	}
	return out, nil
}

// newEvents validates the request's entries and builds their events.
func newEvents(req AppendRequest) ([]proto.Event, error) {
	events := make([]proto.Event, 0, len(req.Entries))
	for _, e := range req.Entries {
		switch e.Payload.(type) {
		case proto.TurnStarted, proto.TurnEnded:
			if req.Turn == 0 {
				return nil, fmt.Errorf("%w: %T needs a turn number", ErrInvalid, e.Payload)
			}
		}
		ev, err := proto.NewEvent(req.Session, 0, req.Turn, req.Time, e.Payload)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		ev.Raw = e.Raw
		events = append(events, ev)
	}
	return events, nil
}

// replay returns the stored result of an earlier append with the same
// receipt, if any.
func replay(ctx context.Context, tx *sql.Tx, session proto.SessionID, key string) (AppendResult, bool, error) {
	if key == "" {
		return AppendResult{}, false, nil
	}
	res := AppendResult{Duplicate: true}
	err := tx.QueryRowContext(ctx, `SELECT first_seq, last_seq FROM receipts WHERE session_id = ? AND key = ?`, session, key).
		Scan(&res.FirstSeq, &res.LastSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return AppendResult{}, false, nil
	}
	if err != nil {
		return AppendResult{}, false, fmt.Errorf("store: read receipt: %w", err)
	}
	return res, true, nil
}

// writeBatch is the body shared by Append and ResolveApproval.
func writeBatch(ctx context.Context, tx *sql.Tx, req AppendRequest, events []proto.Event) (AppendResult, error) {
	var res AppendResult
	var last uint64
	err := tx.QueryRowContext(ctx, `SELECT last_seq FROM sessions WHERE id = ?`, req.Session).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return res, fmt.Errorf("%w: session %s", ErrNotFound, req.Session)
	}
	if err != nil {
		return res, fmt.Errorf("store: read session: %w", err)
	}
	if req.IngestOffset != nil {
		var stored int64
		err := tx.QueryRowContext(ctx, `SELECT offset FROM ingest_offsets WHERE session_id = ?`, req.Session).Scan(&stored)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return res, fmt.Errorf("store: read ingest offset: %w", err)
		}
		if err == nil && *req.IngestOffset < stored {
			return res, fmt.Errorf("%w: ingest offset %d is behind stored offset %d", ErrInvalid, *req.IngestOffset, stored)
		}
	}
	if len(events) > 0 {
		res.FirstSeq = last + 1
	}
	for i := range events {
		p := req.Entries[i].Payload
		if _, ok := p.(proto.TurnEnded); ok {
			if last, err = expirePending(ctx, tx, req, last); err != nil {
				return res, err
			}
		}
		last++
		events[i].Seq = last
		if err := insertEvent(ctx, tx, events[i], p); err != nil {
			return res, err
		}
		if err := project(ctx, tx, events[i], p); err != nil {
			return res, err
		}
	}
	if len(events) > 0 {
		res.LastSeq = last
		status, err := deriveStatus(ctx, tx, req.Session)
		if err != nil {
			return res, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET last_seq = ?, status = ?, updated_at = ? WHERE id = ?`,
			last, status, req.Time.UTC().UnixMilli(), req.Session)
		if err != nil {
			return res, fmt.Errorf("store: update session: %w", err)
		}
		if err := bumpVersion(ctx, tx, req.Session); err != nil {
			return res, err
		}
	}
	if req.ReceiptKey != "" {
		_, err := tx.ExecContext(ctx, `INSERT INTO receipts (session_id, key, first_seq, last_seq, created_at) VALUES (?, ?, ?, ?, ?)`,
			req.Session, req.ReceiptKey, res.FirstSeq, res.LastSeq, req.Time.UTC().UnixMilli())
		if err != nil {
			return res, fmt.Errorf("store: write receipt: %w", err)
		}
	}
	if req.IngestOffset != nil {
		_, err := tx.ExecContext(ctx, `INSERT INTO ingest_offsets (session_id, offset) VALUES (?, ?)
			ON CONFLICT (session_id) DO UPDATE SET offset = excluded.offset`, req.Session, *req.IngestOffset)
		if err != nil {
			return res, fmt.Errorf("store: write ingest offset: %w", err)
		}
	}
	return res, nil
}

// expirePending appends an expired approval.resolved event for every pending
// approval of the session and returns the new last sequence number.
func expirePending(ctx context.Context, tx *sql.Tx, req AppendRequest, last uint64) (uint64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM approvals WHERE session_id = ? AND status = 'pending' ORDER BY request_seq`, req.Session)
	if err != nil {
		return last, fmt.Errorf("store: list pending approvals: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return last, errors.Join(fmt.Errorf("store: scan approval: %w", err), rows.Close())
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return last, fmt.Errorf("store: list pending approvals: %w", err)
	}
	for _, id := range ids {
		p := proto.ApprovalResolved{ID: id, Outcome: proto.OutcomeExpired, DecidedBy: "hoplet:turn-ended"}
		last++
		ev, err := proto.NewEvent(req.Session, last, req.Turn, req.Time, p)
		if err != nil {
			return last, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if err := insertEvent(ctx, tx, ev, p); err != nil {
			return last, err
		}
		if err := project(ctx, tx, ev, p); err != nil {
			return last, err
		}
	}
	return last, nil
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

// outputSHA returns the digest of the blob a tool call's output lives in, so
// blob garbage collection can see the reference.
func outputSHA(p proto.Payload) sql.NullString {
	if tc, ok := p.(proto.ToolCall); ok && tc.Output != nil {
		return sql.NullString{String: tc.Output.SHA256, Valid: true}
	}
	return sql.NullString{}
}

func insertEvent(ctx context.Context, tx *sql.Tx, ev proto.Event, p proto.Payload) error {
	var rawSHA sql.NullString
	var rawSize sql.NullInt64
	if ev.Raw != nil {
		rawSHA = sql.NullString{String: ev.Raw.SHA256, Valid: true}
		rawSize = sql.NullInt64{Int64: ev.Raw.Size, Valid: true}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO events (session_id, seq, ts, turn, type, v, payload, raw_sha256, raw_size, message_id, output_sha256)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.Session, ev.Seq, ev.Time.UnixMilli(), ev.Turn, ev.Type, ev.V, string(ev.Payload), rawSHA, rawSize, messageID(p), outputSHA(p))
	if err != nil {
		return fmt.Errorf("store: insert event %d: %w", ev.Seq, err)
	}
	return nil
}

// project applies one event to the turn and approval projections.
func project(ctx context.Context, tx *sql.Tx, ev proto.Event, p proto.Payload) error {
	var q string
	var args []any
	switch p := p.(type) {
	case proto.TurnStarted:
		q = `INSERT INTO turns (session_id, n, start_seq) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`
		args = []any{ev.Session, ev.Turn, ev.Seq}
	case proto.TurnEnded:
		q = `UPDATE turns SET end_seq = ?, reason = ? WHERE session_id = ? AND n = ?`
		args = []any{ev.Seq, p.Reason, ev.Session, ev.Turn}
	case proto.ApprovalRequested:
		q = `INSERT INTO approvals (id, session_id, request_seq, kind, status) VALUES (?, ?, ?, ?, 'pending') ON CONFLICT DO NOTHING`
		args = []any{p.ID, ev.Session, ev.Seq, p.Kind}
	case proto.ApprovalResolved:
		q = `UPDATE approvals SET status = ?, option_id = ?, decided_by = ?, resolved_seq = ?
			WHERE session_id = ? AND id = ? AND status = 'pending'`
		args = []any{p.Outcome, p.OptionID, p.DecidedBy, ev.Seq, ev.Session, p.ID}
	default:
		return nil
	}
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("store: project %s at seq %d: %w", ev.Type, ev.Seq, err)
	}
	return nil
}

// deriveStatus computes the session status from the projections.
func deriveStatus(ctx context.Context, tx *sql.Tx, id proto.SessionID) (proto.SessionStatus, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM approvals WHERE session_id = ? AND status = 'pending'`, id).Scan(&n); err != nil {
		return "", fmt.Errorf("store: count pending approvals: %w", err)
	}
	if n > 0 {
		return proto.StatusWaiting, nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM turns WHERE session_id = ? AND end_seq IS NULL`, id).Scan(&n); err != nil {
		return "", fmt.Errorf("store: count open turns: %w", err)
	}
	if n > 0 {
		return proto.StatusRunning, nil
	}
	var reason sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT reason FROM turns WHERE session_id = ? AND end_seq IS NOT NULL ORDER BY end_seq DESC LIMIT 1`, id).Scan(&reason)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("store: read last turn: %w", err)
	}
	if reason.Valid && (reason.String == string(proto.TurnFailed) || reason.String == string(proto.TurnAborted)) {
		return proto.StatusErrored, nil
	}
	return proto.StatusIdle, nil
}

// PruneReceipts deletes receipts created before the given time and returns
// how many it deleted. A replayed command older than that is no longer
// recognized as a duplicate.
func (s *Store) PruneReceipts(ctx context.Context, before time.Time) (int64, error) {
	var n int64
	err := s.tx(ctx, false, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM receipts WHERE created_at < ?`, before.UTC().UnixMilli())
		if err != nil {
			return fmt.Errorf("store: prune receipts: %w", err)
		}
		n, err = res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: prune receipts: %w", err)
		}
		return nil
	})
	return n, err
}
