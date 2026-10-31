package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func newSession(t testing.TB, s *Store, id proto.SessionID) proto.Session {
	t.Helper()
	sess, err := s.CreateSession(context.Background(), proto.Session{
		ID: id, Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"},
		Directory: "/work", Isolation: proto.InPlace, Owner: proto.OwnerMe, Origin: proto.OriginMe,
		CreatedAt: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := newSession(t, s, "a")
	if a.Status != proto.StatusIdle || !a.UpdatedAt.Equal(t0) {
		t.Fatalf("defaults not applied: %+v", a)
	}
	newSession(t, s, "b")
	got, err := s.Session(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSeq != 0 || got.Harness.Kind != "claude" || got.Directory != "/work" || !got.CreatedAt.Equal(t0) {
		t.Fatalf("session a: %+v", got)
	}
	if err := s.ArchiveSession(ctx, "a", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Session(ctx, "a")
	if got.ArchivedAt == nil || !got.ArchivedAt.Equal(t0.Add(time.Hour)) || !got.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("archive not recorded: %+v", got)
	}
	list, err := s.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "a" {
		t.Fatalf("want a first (most recently updated), got %+v", list)
	}
	if _, err := s.Session(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	if err := s.ArchiveSession(ctx, "missing", t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archive missing: %v", err)
	}
	if _, err := s.CreateSession(ctx, proto.Session{ID: "c"}); err == nil {
		t.Fatal("incomplete session accepted")
	}
}
