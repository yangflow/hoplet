package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func memOp(id, entry string, version int, hash string) MemoryOp {
	return MemoryOp{ID: id, EntryID: entry, ExpectedVersion: version - 1, NewVersion: version, Actor: "user", Reason: "r", Content: "c", Hash: hash}
}

func memRow(id, category, status, title, body string, at int64) MemoryRow {
	return MemoryRow{ID: id, Version: 1, Category: category, Status: status, ScopeKind: "global", Title: title, Body: body, Hash: "h", UpdatedAt: time.UnixMilli(at).UTC()}
}

func commitOp(t *testing.T, s *Store, op MemoryOp, row MemoryRow) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.BeginMemoryOp(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishMemoryOp(ctx, op.ID, row); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryMigrationKeepsSessions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	old, err := open(ctx, dir, migrationsUpTo(t, 15))
	if err != nil {
		t.Fatal(err)
	}
	newSession(t, old, "s1")
	sessions, events := dumpRows(t, old, "sessions"), dumpRows(t, old, "events")
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if got := dumpRows(t, s, "sessions"); !reflect.DeepEqual(got, sessions) {
		t.Fatalf("sessions changed: %v, want %v", got, sessions)
	}
	if got := dumpRows(t, s, "events"); !reflect.DeepEqual(got, events) {
		t.Fatalf("events changed: %v, want %v", got, events)
	}
	if got, err := s.SearchMemoryIndex(ctx, MemoryQuery{}); err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestBeginMemoryOpDuplicate(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.BeginMemoryOp(ctx, memOp("o1", "e", 1, "h1")); err != nil {
		t.Fatal(err)
	}
	got, err := s.BeginMemoryOp(ctx, memOp("o1", "e", 1, "h1"))
	if !errors.Is(err, ErrExists) || got.ID != "o1" || got.State != MemoryOpPending {
		t.Fatalf("same hash: %+v, %v", got, err)
	}
	if _, err := s.BeginMemoryOp(ctx, memOp("o1", "e", 1, "h2")); !errors.Is(err, ErrConflict) {
		t.Fatalf("other hash: %v", err)
	}
}

func TestSecondCommittedVersionRejected(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	commitOp(t, s, memOp("o1", "e", 1, "h1"), memRow("e", "fact", "active", "t", "b", 1))
	if _, err := s.BeginMemoryOp(ctx, memOp("o2", "e", 1, "h2")); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishMemoryOp(ctx, "o2", memRow("e", "fact", "active", "t", "b", 2)); !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v", err)
	}
	pending, err := s.PendingMemoryOps(ctx)
	if err != nil || len(pending) != 1 || pending[0].ID != "o2" {
		t.Fatalf("got %+v, %v", pending, err)
	}
}

func TestFailMemoryOp(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.BeginMemoryOp(ctx, memOp("o1", "e", 1, "h1")); err != nil {
		t.Fatal(err)
	}
	if err := s.FailMemoryOp(ctx, "o1"); err != nil {
		t.Fatal(err)
	}
	op, err := s.MemoryOpByID(ctx, "o1")
	if err != nil || op.State != MemoryOpFailed || op.FinishedAt == nil {
		t.Fatalf("got %+v, %v", op, err)
	}
	if err := s.FailMemoryOp(ctx, "o1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second fail: %v", err)
	}
	if ops, err := s.MemoryOps(ctx, "e"); err != nil || len(ops) != 0 {
		t.Fatalf("failed op listed: %+v, %v", ops, err)
	}
}

func TestMemoryOpsNewestFirst(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	commitOp(t, s, memOp("o1", "e", 1, "h1"), memRow("e", "fact", "active", "t", "b", 1))
	commitOp(t, s, memOp("o2", "e", 2, "h2"), memRow("e", "fact", "active", "t", "b", 2))
	ops, err := s.MemoryOps(ctx, "e")
	if err != nil || len(ops) != 2 || ops[0].NewVersion != 2 {
		t.Fatalf("got %+v, %v", ops, err)
	}
}

func TestReplaceMemoryIndexKeepsOps(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	commitOp(t, s, memOp("o1", "e", 1, "h1"), memRow("e", "fact", "active", "t", "b", 1))
	ops := dumpRows(t, s, "memory_ops")
	want := []MemoryProblem{{Path: "fact/x.md", Err: "bad"}}
	if err := s.ReplaceMemoryIndex(ctx, []MemoryRow{memRow("f", "fact", "active", "t", "b", 1)}, want); err != nil {
		t.Fatal(err)
	}
	if got := dumpRows(t, s, "memory_ops"); !reflect.DeepEqual(got, ops) {
		t.Fatalf("ops changed: %v", got)
	}
	if _, err := s.MemoryIndexEntry(ctx, "e"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old row: %v", err)
	}
	if _, err := s.MemoryIndexEntry(ctx, "f"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.MemoryProblems(ctx); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestSearchMemoryIndex(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	repo := memRow("d", "convention", "active", "scoped", "x", 5)
	repo.ScopeKind, repo.ScopeValue = "repo", "a/b"
	rows := []MemoryRow{
		memRow("a", "fact", "active", "直达 flight", "100% sure", 1),
		memRow("b", "fact", "stale", "under_score", "plain", 2),
		memRow("c", "preference", "active", "back\\slash", "body 咖啡 text", 3),
		repo,
	}
	if err := s.ReplaceMemoryIndex(ctx, rows, nil); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		q    MemoryQuery
		want []string
	}{
		{"all newest first", MemoryQuery{}, []string{"d", "c", "b", "a"}},
		{"limit", MemoryQuery{Limit: 1}, []string{"d"}},
		{"category", MemoryQuery{Category: "fact"}, []string{"b", "a"}},
		{"category and status", MemoryQuery{Category: "fact", Status: "active"}, []string{"a"}},
		{"scope", MemoryQuery{ScopeKind: "repo", ScopeValue: "a/b"}, []string{"d"}},
		{"scope other value", MemoryQuery{ScopeKind: "repo", ScopeValue: "x"}, nil},
		{"two chinese characters in title", MemoryQuery{Text: "直达"}, []string{"a"}},
		{"two chinese characters in body", MemoryQuery{Text: "咖啡"}, []string{"c"}},
		{"literal percent", MemoryQuery{Text: "0%"}, []string{"a"}},
		{"percent is not a wildcard", MemoryQuery{Text: "1%s"}, nil},
		{"literal underscore", MemoryQuery{Text: "r_s"}, []string{"b"}},
		{"underscore is not a wildcard", MemoryQuery{Text: "unde_"}, nil},
		{"literal backslash", MemoryQuery{Text: `k\s`}, []string{"c"}},
		{"text and filter", MemoryQuery{Text: "plain", Status: "active"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.SearchMemoryIndex(ctx, tt.q)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, r := range got {
				ids = append(ids, r.ID)
			}
			if !reflect.DeepEqual(ids, tt.want) {
				t.Fatalf("got %v, want %v", ids, tt.want)
			}
		})
	}
}
