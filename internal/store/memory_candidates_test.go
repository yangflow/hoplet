package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestMemoryCandidatesMigrationKeepsOps(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	old, err := open(ctx, dir, migrationsUpTo(t, 16))
	if err != nil {
		t.Fatal(err)
	}
	commitOp(t, old, memOp("o1", "e", 1, "h1"), memRow("e", "fact", "active", "t", "b", 1))
	want := dumpRows(t, old, "memory_ops")
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := open(ctx, dir, migrationsUpTo(t, 17))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if got := dumpRows(t, s, "memory_ops"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRecordMemoryRejection(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	op := MemoryOp{ID: "r1", EntryID: "e", Actor: "companion", Reason: "credential: looks like a key"}
	if err := s.RecordMemoryRejection(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMemoryRejection(ctx, op); !errors.Is(err, ErrExists) {
		t.Fatalf("second: %v", err)
	}
	got, err := s.MemoryOpByID(ctx, "r1")
	if err != nil || got.State != MemoryOpRejected || got.Reason != op.Reason {
		t.Fatalf("%+v, %v", got, err)
	}
	if ops, err := s.MemoryOps(ctx, "e"); err != nil || len(ops) != 0 {
		t.Fatalf("%+v, %v", ops, err)
	}
	if ops, err := s.AllCommittedMemoryOps(ctx); err != nil || len(ops) != 0 {
		t.Fatalf("%+v, %v", ops, err)
	}
}

func TestMemoryCandidateDecidedOnce(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	c := MemoryCandidate{EntryID: "c1", Action: "update", TargetID: "e", ExpectedVersion: 2, Actor: "extraction", Reason: "r"}
	if err := s.AddMemoryCandidate(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMemoryCandidate(ctx, c); !errors.Is(err, ErrExists) {
		t.Fatalf("second add: %v", err)
	}
	if err := s.DecideMemoryCandidate(ctx, "c1", "open"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad state: %v", err)
	}
	open, err := s.OpenMemoryCandidates(ctx)
	if err != nil || len(open) != 1 || open[0].TargetID != "e" || open[0].State != MemoryCandidateOpen {
		t.Fatalf("%+v, %v", open, err)
	}
	if err := s.ReplaceMemoryIndex(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideMemoryCandidate(ctx, "c1", MemoryCandidateRejected); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideMemoryCandidate(ctx, "c1", MemoryCandidateAccepted); !errors.Is(err, ErrConflict) {
		t.Fatalf("second decision: %v", err)
	}
	got, err := s.MemoryCandidateByEntry(ctx, "c1")
	if err != nil || got.State != MemoryCandidateRejected || got.DecidedAt == nil {
		t.Fatalf("%+v, %v", got, err)
	}
	if open, err := s.OpenMemoryCandidates(ctx); err != nil || len(open) != 0 {
		t.Fatalf("%+v, %v", open, err)
	}
}

func TestEventBySeq(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s1")
	appendOK(t, s, AppendRequest{Session: "s1", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	evs, err := s.Events(ctx, "s1", 0, 10)
	if err != nil || len(evs) == 0 {
		t.Fatalf("%+v, %v", evs, err)
	}
	got, err := s.EventBySeq(ctx, "s1", evs[0].Seq)
	if err != nil || got.Seq != evs[0].Seq {
		t.Fatalf("%+v, %v", got, err)
	}
	if _, err := s.EventBySeq(ctx, "s1", 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown seq: %v", err)
	}
	if _, err := s.EventBySeq(ctx, "nope", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}
}
