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
