package store

import (
	"context"
	"errors"
	"testing"
	"time"

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
	if len(pending) != 1 || pending[0].Request.ID != "a1" {
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

func approvalRow(t *testing.T, s *Store, session, id string) (status, option string) {
	t.Helper()
	err := s.db.QueryRowContext(context.Background(),
		`SELECT status, option_id FROM approvals WHERE session_id = ? AND id = ?`, session, id).Scan(&status, &option)
	if err != nil {
		t.Fatal(err)
	}
	return status, option
}

func TestResolveApprovalFirstAnswerWins(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}, {Payload: approvalRequest("a1")}}})

	first, err := s.ResolveApproval(ctx, "s", 1, t0, proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeAllowed, OptionID: "once", DecidedBy: "device:mac"}, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if first.FirstSeq != 3 || first.LastSeq != 3 {
		t.Fatalf("first %+v", first)
	}
	if sess, _ := s.Session(ctx, "s"); sess.Status != proto.StatusRunning {
		t.Fatalf("status %s, want running", sess.Status)
	}
	_, err = s.ResolveApproval(ctx, "s", 1, t0, proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeDenied, DecidedBy: "device:phone"}, "r2")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second resolve: %v, want ErrConflict", err)
	}
	if status, opt := approvalRow(t, s, "s", "a1"); status != "allowed" || opt != "once" {
		t.Fatalf("approval row %s/%s, first decision lost", status, opt)
	}
	if evs, _ := s.Events(ctx, "s", 0, 100); len(evs) != 3 {
		t.Fatalf("conflicting resolve wrote events: %d", len(evs))
	}
	if _, err := s.ResolveApproval(ctx, "s", 1, t0, proto.ApprovalResolved{ID: "nope", Outcome: proto.OutcomeAllowed}, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown approval: %v, want ErrNotFound", err)
	}
	if _, err := s.ResolveApproval(ctx, "missing", 1, t0, proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeAllowed}, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: %v, want ErrNotFound", err)
	}
	replay, err := s.ResolveApproval(ctx, "s", 1, t0, proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeAllowed, OptionID: "once", DecidedBy: "device:mac"}, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Duplicate || replay.FirstSeq != first.FirstSeq || replay.LastSeq != first.LastSeq {
		t.Fatalf("replay %+v, want duplicate of %+v", replay, first)
	}
	if _, err := s.ResolveApproval(ctx, "s", 1, t0, proto.ApprovalResolved{ID: "a1", Outcome: "maybe"}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid resolution: %v", err)
	}
}

func TestAppendStaleResolvedChangesNothing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}, {Payload: approvalRequest("a1")}}})
	if _, err := s.ResolveApproval(ctx, "s", 1, t0, proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeAllowed, OptionID: "once"}, ""); err != nil {
		t.Fatal(err)
	}
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{
		{Payload: proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeDenied}},
		{Payload: proto.ApprovalResolved{ID: "unknown", Outcome: proto.OutcomeDenied}},
	}})
	if status, opt := approvalRow(t, s, "s", "a1"); status != "allowed" || opt != "once" {
		t.Fatalf("stale resolved changed the projection: %s/%s", status, opt)
	}
	if evs, _ := s.Events(ctx, "s", 0, 100); len(evs) != 5 {
		t.Fatalf("stale events not logged: %d", len(evs))
	}
}

func TestStatusIsDerived(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	status := func(want proto.SessionStatus) {
		t.Helper()
		if got, _ := s.Session(ctx, "s"); got.Status != want {
			t.Fatalf("status %s, want %s", got.Status, want)
		}
	}
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}, {Payload: approvalRequest("a1")}}})
	status(proto.StatusWaiting)
	// A new turn starting while an approval is pending does not unstick it.
	appendOK(t, s, AppendRequest{Session: "s", Turn: 2, Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	status(proto.StatusWaiting)
	if _, err := s.ResolveApproval(ctx, "s", 1, t0, proto.ApprovalResolved{ID: "a1", Outcome: proto.OutcomeAllowed, OptionID: "once"}, ""); err != nil {
		t.Fatal(err)
	}
	status(proto.StatusRunning)
	// Repeating the request for the resolved approval must not resurrect it.
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: approvalRequest("a1")}}})
	status(proto.StatusRunning)
	if pending, _ := s.PendingApprovals(ctx, "s"); len(pending) != 0 {
		t.Fatalf("resolved approval pending again: %+v", pending)
	}
}

func TestTurnEndedExpiresPendingApprovals(t *testing.T) {
	for _, tc := range []struct {
		reason proto.TurnEndReason
		want   proto.SessionStatus
	}{{proto.TurnCompleted, proto.StatusIdle}, {proto.TurnFailed, proto.StatusErrored}} {
		t.Run(string(tc.reason), func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			newSession(t, s, "s")
			appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{
				{Payload: proto.TurnStarted{}}, {Payload: approvalRequest("a1")}, {Payload: approvalRequest("a2")},
			}})
			res := appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnEnded{Reason: tc.reason}}}})
			if res.FirstSeq != 4 || res.LastSeq != 6 {
				t.Fatalf("result %+v, want seqs 4..6 (two expiries then turn.ended)", res)
			}
			evs, _ := s.Events(ctx, "s", 3, 100)
			if len(evs) != 3 || evs[0].Type != proto.TypeApprovalResolved || evs[1].Type != proto.TypeApprovalResolved || evs[2].Type != proto.TypeTurnEnded {
				t.Fatalf("events %+v", evs)
			}
			p, err := evs[0].Decode()
			if err != nil {
				t.Fatal(err)
			}
			if r := p.(proto.ApprovalResolved); r.Outcome != proto.OutcomeExpired || r.DecidedBy != "hoplet:turn-ended" {
				t.Fatalf("expiry %+v", r)
			}
			if pending, _ := s.PendingApprovals(ctx, "s"); len(pending) != 0 {
				t.Fatalf("still pending: %+v", pending)
			}
			if got, _ := s.Session(ctx, "s"); got.Status != tc.want {
				t.Fatalf("status %s, want %s", got.Status, tc.want)
			}
		})
	}
}

func TestAppendInvalidEntries(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	for name, e := range map[string]Entry{
		"turn.started without turn": {Payload: proto.TurnStarted{}},
		"turn.ended without turn":   {Payload: proto.TurnEnded{Reason: proto.TurnCompleted}},
	} {
		if _, err := s.Append(ctx, AppendRequest{Session: "s", Entries: []Entry{e}}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	_, err := s.Append(ctx, AppendRequest{Session: "s", Entries: []Entry{{Payload: proto.ApprovalResolved{ID: "a", Outcome: "maybe"}}}})
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, proto.ErrInvalidPayload) {
		t.Fatalf("bad payload: %v, want both ErrInvalid and ErrInvalidPayload", err)
	}
}

func TestIngestOffsetIsMonotonic(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	hi, lo := int64(500), int64(100)
	appendOK(t, s, AppendRequest{Session: "s", Entries: []Entry{{Payload: proto.Thinking{Text: "x"}}}, IngestOffset: &hi})
	_, err := s.Append(ctx, AppendRequest{Session: "s", Entries: []Entry{{Payload: proto.Thinking{Text: "y"}}}, IngestOffset: &lo})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("backwards offset: %v", err)
	}
	if evs, _ := s.Events(ctx, "s", 0, 100); len(evs) != 1 {
		t.Fatalf("rejected append wrote events: %d", len(evs))
	}
	if got, _ := s.IngestOffset(ctx, "s"); got != 500 {
		t.Fatalf("offset %d", got)
	}
	appendOK(t, s, AppendRequest{Session: "s", IngestOffset: &hi}) // equal is fine
}

func TestIngestOnlyLeavesSessionAlone(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	before, _ := s.Session(ctx, "s")
	off := int64(9)
	appendOK(t, s, AppendRequest{Session: "s", Time: t0.Add(time.Hour), IngestOffset: &off})
	after, _ := s.Session(ctx, "s")
	if after.UpdatedAt != before.UpdatedAt || after.Status != before.Status || after.Version != before.Version {
		t.Fatalf("ingest-only append changed the session: before %+v after %+v", before, after)
	}
}

func TestSessionVersions(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := newSession(t, s, "a")
	b := newSession(t, s, "b")
	if a.Version != 1 || b.Version != 2 {
		t.Fatalf("create versions %d, %d", a.Version, b.Version)
	}
	appendOK(t, s, AppendRequest{Session: "a", Entries: []Entry{{Payload: proto.Thinking{}}}}) // 3
	title := "t"
	if _, err := s.UpdateSession(ctx, "b", SessionUpdate{Title: &title}); err != nil { // 4
		t.Fatal(err)
	}
	if err := s.ArchiveSession(ctx, "a", t0); err != nil { // 5
		t.Fatal(err)
	}
	if v, err := s.CatalogVersion(ctx); err != nil || v != 5 {
		t.Fatalf("catalog version %d, %v; want 5", v, err)
	}
	since, err := s.SessionsSince(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 2 || since[0].ID != "b" || since[0].Version != 4 || since[1].ID != "a" || since[1].Version != 5 {
		t.Fatalf("sessions since 2: %+v", since)
	}
}

func TestUpdateSession(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	native, title := "native-1", "Fix sync"
	got, err := s.UpdateSession(ctx, "s", SessionUpdate{NativeID: &native, Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness.NativeID != "native-1" || got.Title != "Fix sync" || got.Version != 2 {
		t.Fatalf("updated %+v", got)
	}
	got, err = s.UpdateSession(ctx, "s", SessionUpdate{Title: &native})
	if err != nil || got.Harness.NativeID != "native-1" || got.Title != "native-1" {
		t.Fatalf("partial update %+v, %v", got, err)
	}
	if _, err := s.UpdateSession(ctx, "missing", SessionUpdate{Title: &title}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestPruneReceipts(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Time: t0, ReceiptKey: "old", Entries: []Entry{{Payload: proto.Thinking{}}}})
	appendOK(t, s, AppendRequest{Session: "s", Time: t0.Add(2 * time.Hour), ReceiptKey: "new", Entries: []Entry{{Payload: proto.Thinking{}}}})
	n, err := s.PruneReceipts(ctx, t0.Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v; want 1", n, err)
	}
	if res := appendOK(t, s, AppendRequest{Session: "s", ReceiptKey: "new", Entries: []Entry{{Payload: proto.Thinking{}}}}); !res.Duplicate {
		t.Fatal("recent receipt was pruned")
	}
	if res := appendOK(t, s, AppendRequest{Session: "s", ReceiptKey: "old", Entries: []Entry{{Payload: proto.Thinking{}}}}); res.Duplicate {
		t.Fatal("pruned receipt still recognized")
	}
}

func TestReadHelpers(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	if _, err := s.Events(ctx, "missing", 0, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Events unknown session: %v", err)
	}
	if _, err := s.EventsBefore(ctx, "missing", 10, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("EventsBefore unknown session: %v", err)
	}
	if _, _, err := s.GapSize(ctx, "missing", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GapSize unknown session: %v", err)
	}
	for range 5 {
		appendOK(t, s, AppendRequest{Session: "s", Entries: []Entry{{Payload: proto.Thinking{Text: "é"}}}})
	}
	before, err := s.EventsBefore(ctx, "s", 5, 2)
	if err != nil || len(before) != 2 || before[0].Seq != 3 || before[1].Seq != 4 {
		t.Fatalf("EventsBefore: %+v, %v", before, err)
	}
	if _, err := s.EventsBefore(ctx, "s", 5, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("EventsBefore limit 0: %v", err)
	}
	count, size, err := s.GapSize(ctx, "s", 2)
	if err != nil || count != 3 {
		t.Fatalf("GapSize count %d, %v", count, err)
	}
	evs, _ := s.Events(ctx, "s", 2, 100)
	want := int64(0)
	for _, ev := range evs {
		want += int64(len(ev.Payload))
	}
	if size != want {
		t.Fatalf("GapSize bytes %d, want %d (byte length, not characters)", size, want)
	}
}

func TestSnapshot(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Entries: []Entry{{Payload: proto.Thinking{Text: "before any turn"}}}}) // 1
	for n := uint32(1); n <= 3; n++ {
		appendOK(t, s, AppendRequest{Session: "s", Turn: n, Entries: []Entry{
			{Payload: proto.TurnStarted{}}, {Payload: proto.Thinking{Text: "t"}}, {Payload: proto.TurnEnded{Reason: proto.TurnCompleted}},
		}}) // 2-4, 5-7, 8-10
	}
	appendOK(t, s, AppendRequest{Session: "s", Entries: []Entry{{Payload: proto.Thinking{Text: "after, no turn"}}}}) // 11
	snap, err := s.Snapshot(ctx, "s", 2)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Session.ID != "s" || snap.Session.LastSeq != 11 {
		t.Fatalf("session %+v", snap.Session)
	}
	if len(snap.Events) != 7 || snap.Events[0].Seq != 5 || snap.Events[6].Seq != 11 {
		t.Fatalf("events from seq %d, %d of them", snap.Events[0].Seq, len(snap.Events))
	}
	all, err := s.Snapshot(ctx, "s", 10)
	if err != nil || len(all.Events) != 11 {
		t.Fatalf("fewer turns than asked: %d events, %v", len(all.Events), err)
	}
	if _, err := s.Snapshot(ctx, "missing", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}
	appendOK(t, s, AppendRequest{Session: "s", Turn: 4, Entries: []Entry{{Payload: approvalRequest("a1")}}})
	snap, _ = s.Snapshot(ctx, "s", 1)
	if len(snap.Pending) != 1 || snap.Pending[0].RequestSeq != 12 || snap.Pending[0].Request.ID != "a1" {
		t.Fatalf("pending %+v", snap.Pending)
	}
}
