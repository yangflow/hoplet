package store

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/yangflow/hoplet/proto"
)

// TestReadOnlyBecomesPlanMode checks migration 4 on a store that has a
// session in the retired read_only mode.
func TestReadOnlyBecomesPlanMode(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	upTo := func(n int) fstest.MapFS {
		out := fstest.MapFS{}
		names, err := fs.Glob(migrations, "migrations/*.sql")
		if err != nil {
			t.Fatal(err)
		}
		for i, name := range names {
			if i >= n {
				break
			}
			b, err := fs.ReadFile(migrations, name)
			if err != nil {
				t.Fatal(err)
			}
			out[name[len("migrations/"):]] = &fstest.MapFile{Data: b}
		}
		return out
	}
	s, err := open(ctx, dir, upTo(3))
	if err != nil {
		t.Fatal(err)
	}
	// Written as a schema-3 build wrote them: this build's CreateSession
	// knows columns schema 3 lacks.
	for id, perm := range map[string]string{"old": "read_only", "kept": "confirm"} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id, mode, harness_kind, owner, origin, status,
			created_at, updated_at, version, permission) VALUES (?, 'code', 'claude', 'me', 'me', 'idle', 0, 0, 1, ?)`, id, perm); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old, err := s.Session(ctx, "old")
	if err != nil || old.Permission != proto.ModeConfirm || !old.Plan || old.Effort != "" {
		t.Fatalf("migrated session %+v %v", old, err)
	}
	if kept, _ := s.Session(ctx, "kept"); kept.Permission != proto.ModeConfirm || kept.Plan {
		t.Fatalf("untouched session %+v", kept)
	}
}

func TestSessionPlanAndEffort(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	sess, err := s.CreateSession(ctx, proto.Session{
		ID: "p", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"}, Directory: "/work",
		Isolation: proto.InPlace, Owner: proto.OwnerMe, Origin: proto.OriginMe, CreatedAt: t0,
		Permission: proto.ModeAutoReview, Plan: true, Fast: true, Effort: proto.EffortHigh,
	})
	if err != nil || sess.Permission != proto.ModeAutoReview || !sess.Plan || !sess.Fast || sess.Effort != proto.EffortHigh {
		t.Fatalf("create %+v %v", sess, err)
	}
	off, low := false, proto.EffortLow
	got, err := s.UpdateSession(ctx, "p", SessionUpdate{Plan: &off, Fast: &off, Effort: &low})
	if err != nil || got.Plan || got.Fast || got.Effort != proto.EffortLow || got.Version <= sess.Version {
		t.Fatalf("update %+v %v", got, err)
	}
	none := proto.Effort("")
	if got, err := s.UpdateSession(ctx, "p", SessionUpdate{Effort: &none}); err != nil || got.Effort != "" {
		t.Fatalf("restore default effort %+v %v", got, err)
	}
	bogus := proto.Effort("huge")
	if _, err := s.UpdateSession(ctx, "p", SessionUpdate{Effort: &bogus}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bogus effort: %v", err)
	}
	if _, err := s.CreateSession(ctx, proto.Session{
		ID: "q", Mode: proto.ModePersonal, Harness: proto.HarnessRef{Kind: "x"}, Owner: proto.OwnerMe, Origin: proto.OriginMe, Effort: bogus,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("create with bogus effort: %v", err)
	}
	old := proto.PermissionMode("read_only")
	if _, err := s.UpdateSession(ctx, "p", SessionUpdate{Permission: &old}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("retired mode accepted: %v", err)
	}
}

func TestModeChangedUpdatesTheSession(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	on := true
	before, err := s.UpdateSession(ctx, "s", SessionUpdate{Plan: &on})
	if err != nil {
		t.Fatal(err)
	}
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{
		{Payload: proto.TurnStarted{}},
		{Payload: proto.ModeChanged{Permission: proto.ModeAutoEdit, Plan: false}},
	}})
	after, err := s.Session(ctx, "s")
	if err != nil || after.Permission != proto.ModeAutoEdit || after.Plan || after.Version <= before.Version {
		t.Fatalf("after the change %+v %v", after, err)
	}
}
