package store

import (
	"context"
	"errors"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func appendOK(t testing.TB, s *Store, req AppendRequest) AppendResult {
	t.Helper()
	if req.Time.IsZero() {
		req.Time = t0
	}
	res, err := s.Append(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAppendProjectsStatusTurnsAndApprovals(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	status := func(want proto.SessionStatus) {
		t.Helper()
		got, err := s.Session(ctx, "s")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want {
			t.Fatalf("status %s, want %s", got.Status, want)
		}
	}

	res := appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{
		{Payload: proto.TurnStarted{}},
		{Payload: proto.MessageDelta{MessageID: "m1", Text: "hi"}, Raw: &proto.BlobRef{SHA256: "ab", Size: 2}},
	}})
	if res.FirstSeq != 1 || res.LastSeq != 2 {
		t.Fatalf("seqs %+v", res)
	}
	status(proto.StatusRunning)

	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.ApprovalRequested{
		ID: "a1", Kind: proto.ApprovalCommand, Title: "Run command",
		Options: []proto.ApprovalOption{{ID: "once", Label: "Allow once", Scope: proto.ScopeOnce}},
	}}}})
	status(proto.StatusWaiting)
	pending, err := s.PendingApprovals(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "a1" {
		t.Fatalf("pending %+v", pending)
	}

	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.ApprovalResolved{
		ID: "a1", Outcome: proto.OutcomeAllowed, OptionID: "once", DecidedBy: "device:mac",
	}}}})
	status(proto.StatusRunning)
	if pending, _ := s.PendingApprovals(ctx, "s"); len(pending) != 0 {
		t.Fatalf("approval still pending: %+v", pending)
	}

	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnEnded{Reason: proto.TurnFailed, Message: "harness exited"}}}})
	status(proto.StatusErrored)
	var endSeq uint64
	var reason string
	if err := s.db.QueryRowContext(ctx, `SELECT end_seq, reason FROM turns WHERE session_id = 's' AND n = 1`).Scan(&endSeq, &reason); err != nil {
		t.Fatal(err)
	}
	if endSeq != 5 || reason != string(proto.TurnFailed) {
		t.Fatalf("turn row end_seq=%d reason=%s", endSeq, reason)
	}

	evs, err := s.Events(ctx, "s", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 5 {
		t.Fatalf("got %d events, want 5", len(evs))
	}
	for i, ev := range evs {
		if ev.Seq != uint64(i+1) || ev.Session != "s" || !ev.Time.Equal(t0) {
			t.Fatalf("event %d envelope: %+v", i, ev)
		}
		if _, err := ev.Decode(); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}
	if evs[1].Raw == nil || evs[1].Raw.SHA256 != "ab" {
		t.Fatalf("raw ref lost: %+v", evs[1].Raw)
	}
	if after, _ := s.Events(ctx, "s", 3, 100); len(after) != 2 || after[0].Seq != 4 {
		t.Fatalf("events after 3: %+v", after)
	}
}

func TestAppendReceiptIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	req := AppendRequest{Session: "s", Turn: 1, ReceiptKey: "cmd-1", Entries: []Entry{
		{Payload: proto.TurnStarted{}},
		{Payload: proto.Message{ID: "u1", Role: proto.RoleUser, Text: "fix it"}},
	}}
	first := appendOK(t, s, req)
	second := appendOK(t, s, req)
	if first.Duplicate || !second.Duplicate || second.FirstSeq != first.FirstSeq || second.LastSeq != first.LastSeq {
		t.Fatalf("first %+v second %+v", first, second)
	}
	if evs, _ := s.Events(ctx, "s", 0, 100); len(evs) != 2 {
		t.Fatalf("duplicate append wrote events: %d", len(evs))
	}
}

func TestAppendIngestOffset(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	if off, err := s.IngestOffset(ctx, "s"); err != nil || off != 0 {
		t.Fatalf("initial offset %d, %v", off, err)
	}
	for _, want := range []int64{120, 480} {
		off := want
		appendOK(t, s, AppendRequest{Session: "s", Entries: []Entry{{Payload: proto.Thinking{Text: "x"}}}, IngestOffset: &off})
		if got, err := s.IngestOffset(ctx, "s"); err != nil || got != want {
			t.Fatalf("offset %d, %v; want %d", got, err, want)
		}
	}
}

func TestAppendFailsAtomically(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	_, err := s.Append(ctx, AppendRequest{Session: "s", Time: t0, Entries: []Entry{
		{Payload: proto.Thinking{Text: "kept only if all succeed"}},
		{Payload: proto.TurnStarted{}}, // turn 0 is invalid for turn.started
	}})
	if err == nil {
		t.Fatal("append with an invalid entry succeeded")
	}
	if evs, _ := s.Events(ctx, "s", 0, 100); len(evs) != 0 {
		t.Fatalf("partial append left %d events", len(evs))
	}
	if sess, _ := s.Session(ctx, "s"); sess.LastSeq != 0 {
		t.Fatalf("last_seq moved to %d", sess.LastSeq)
	}
	if _, err := s.Append(ctx, AppendRequest{Session: "missing", Entries: []Entry{{Payload: proto.Thinking{}}}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}
	if _, err := s.Append(ctx, AppendRequest{Session: "s"}); err == nil {
		t.Fatal("empty append accepted")
	}
	if _, err := s.Append(ctx, AppendRequest{Session: "s", Entries: []Entry{{Payload: proto.ApprovalResolved{ID: "a", Outcome: "maybe"}}}}); !errors.Is(err, proto.ErrInvalidPayload) {
		t.Fatalf("invalid payload: %v", err)
	}
}

func TestDurableAppendRestoresSynchronous(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, ReceiptKey: "k", Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	var sync int
	if err := s.db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatal(err)
	}
	if sync != 1 { // 1 = NORMAL
		t.Fatalf("synchronous = %d after a durable append, want 1 (NORMAL)", sync)
	}
}

func approvalRequest(id string) proto.ApprovalRequested {
	return proto.ApprovalRequested{
		ID: id, Kind: proto.ApprovalCommand, Title: "Run command",
		Options: []proto.ApprovalOption{{ID: "once", Label: "Allow once", Scope: proto.ScopeOnce}},
	}
}

func TestApprovalIDsArePerSession(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s1")
	newSession(t, s, "s2")
	for _, id := range []proto.SessionID{"s1", "s2"} {
		appendOK(t, s, AppendRequest{Session: id, Turn: 1, Entries: []Entry{{Payload: approvalRequest("a1")}}})
	}
	// A repeated request in one session must not fail the append.
	appendOK(t, s, AppendRequest{Session: "s1", Turn: 1, Entries: []Entry{{Payload: approvalRequest("a1")}}})
	for _, id := range []proto.SessionID{"s1", "s2"} {
		pending, err := s.PendingApprovals(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 1 {
			t.Fatalf("%s: pending %+v, want one", id, pending)
		}
		if sess, _ := s.Session(ctx, id); sess.Status != proto.StatusWaiting {
			t.Fatalf("%s: status %s, want waiting", id, sess.Status)
		}
	}
}

func TestRepeatedTurnStartedKeepsFirst(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	var start uint64
	if err := s.db.QueryRowContext(ctx, `SELECT start_seq FROM turns WHERE session_id = 's' AND n = 1`).Scan(&start); err != nil {
		t.Fatal(err)
	}
	if start != 1 {
		t.Fatalf("start_seq = %d, want 1", start)
	}
}

func TestReceiptsArePerSession(t *testing.T) {
	s := newStore(t)
	newSession(t, s, "s1")
	newSession(t, s, "s2")
	for _, id := range []proto.SessionID{"s1", "s2"} {
		res := appendOK(t, s, AppendRequest{Session: id, ReceiptKey: "same", Entries: []Entry{{Payload: proto.Thinking{Text: "x"}}}})
		if res.Duplicate || res.FirstSeq != 1 {
			t.Fatalf("%s: %+v", id, res)
		}
	}
}

func TestIngestOnlyAppendWritesNoEvents(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Entries: []Entry{{Payload: proto.Thinking{Text: "x"}}}})
	off := int64(77)
	res := appendOK(t, s, AppendRequest{Session: "s", IngestOffset: &off})
	if res.FirstSeq != 0 || res.LastSeq != 0 || res.Duplicate {
		t.Fatalf("ingest-only result %+v, want zero seqs", res)
	}
	if got, _ := s.IngestOffset(ctx, "s"); got != 77 {
		t.Fatalf("offset %d", got)
	}
	if sess, _ := s.Session(ctx, "s"); sess.LastSeq != 1 {
		t.Fatalf("last_seq %d, want 1", sess.LastSeq)
	}
}

func TestEventsRejectsBadLimit(t *testing.T) {
	s := newStore(t)
	newSession(t, s, "s")
	for _, limit := range []int{0, -1} {
		if _, err := s.Events(context.Background(), "s", 0, limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
}
