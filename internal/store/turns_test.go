package store

import (
	"context"
	"errors"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestTurnState(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	check := func(wantLast uint32, wantOpen bool) {
		t.Helper()
		last, open, err := s.TurnState(ctx, "s")
		if err != nil {
			t.Fatal(err)
		}
		if last != wantLast || open != wantOpen {
			t.Fatalf("turn state (%d, %v), want (%d, %v)", last, open, wantLast, wantOpen)
		}
	}
	check(0, false)
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	check(1, true)
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnEnded{Reason: proto.TurnCompleted}}}})
	check(1, false)
	appendOK(t, s, AppendRequest{Session: "s", Turn: 2, Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	check(2, true)
	if _, _, err := s.TurnState(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
}

func TestValidateEntry(t *testing.T) {
	for name, tc := range map[string]struct {
		turn  uint32
		entry Entry
		ok    bool
	}{
		"message":          {0, Entry{Payload: proto.Message{ID: "m", Role: proto.RoleUser, Text: "x"}}, true},
		"turn start no n":  {0, Entry{Payload: proto.TurnStarted{}}, false},
		"turn end no n":    {0, Entry{Payload: proto.TurnEnded{Reason: proto.TurnCompleted}}, false},
		"turn start":       {1, Entry{Payload: proto.TurnStarted{}}, true},
		"nil payload":      {1, Entry{}, false},
		"bad outcome":      {1, Entry{Payload: proto.ApprovalResolved{ID: "a", Outcome: "maybe"}}, false},
		"error with empty": {0, Entry{Payload: proto.Error{Message: "x"}}, true},
	} {
		err := ValidateEntry(tc.turn, tc.entry)
		if tc.ok && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestSecondTurnEndChangesNothing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnEnded{Reason: proto.TurnCompleted}}}})
	// A late approval request, then a stray second end for the ended turn.
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.ApprovalRequested{ID: "a", Kind: proto.ApprovalCommand, Title: "t",
		Options: []proto.ApprovalOption{{ID: "o", Label: "ok", Scope: proto.ScopeOnce}}}}}})
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnEnded{Reason: proto.TurnFailed}}}})
	pending, err := s.PendingApprovals(ctx, "s")
	if err != nil || len(pending) != 1 {
		t.Fatalf("stray turn end expired the approval: %v %v", pending, err)
	}
	var reason string
	if err := s.db.QueryRowContext(ctx, `SELECT reason FROM turns WHERE session_id = 's' AND n = 1`).Scan(&reason); err != nil || reason != string(proto.TurnCompleted) {
		t.Fatalf("turn reason %q, %v; the first end must stand", reason, err)
	}
}

func TestReceipt(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	if _, found, err := s.Receipt(ctx, "s", "k"); err != nil || found {
		t.Fatalf("unexpected receipt: %v %v", found, err)
	}
	res := appendOK(t, s, AppendRequest{Session: "s", Turn: 1, ReceiptKey: "k", Entries: []Entry{{Payload: proto.Message{ID: "m", Role: proto.RoleUser, Text: "x"}}}})
	got, found, err := s.Receipt(ctx, "s", "k")
	if err != nil || !found || got.FirstSeq != res.FirstSeq || got.LastSeq != res.LastSeq || !got.Duplicate {
		t.Fatalf("receipt %+v %v %v, want %+v", got, found, err, res)
	}
	if _, _, err := s.Receipt(ctx, "missing", "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
}

func TestChangesWakesOnWrites(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	wake := func(what string, write func()) {
		t.Helper()
		ch := s.Changes()
		select {
		case <-ch:
			t.Fatalf("%s: channel closed before the write", what)
		default:
		}
		write()
		select {
		case <-ch:
		default:
			t.Fatalf("%s: waiter not woken", what)
		}
	}
	wake("create", func() { newSession(t, s, "s") })
	wake("append", func() {
		appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	})
	title := "t"
	wake("update", func() {
		if _, err := s.UpdateSession(ctx, "s", SessionUpdate{Title: &title}); err != nil {
			t.Fatal(err)
		}
	})
}
