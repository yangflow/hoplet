package store

import (
	"context"
	"errors"
	"testing"
)

func TestSessionTokens(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	newSession(t, s, "s1")
	newSession(t, s, "s2")
	if ok, err := s.HasSessionToken(ctx, "s1"); err != nil || ok {
		t.Fatalf("token before any: %v %v", ok, err)
	}
	if err := s.SetSessionToken(ctx, "s1", []byte("h1"), t0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionToken(ctx, "s2", []byte("h2"), t0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionToken(ctx, "s2", nil, t0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty hash: %v", err)
	}
	for hash, want := range map[string]string{"h1": "s1", "h2": "s2"} {
		if id, err := s.SessionByToken(ctx, []byte(hash)); err != nil || string(id) != want {
			t.Fatalf("%s: %q %v", hash, id, err)
		}
	}

	// A new token replaces the old one.
	if err := s.SetSessionToken(ctx, "s1", []byte("h1b"), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByToken(ctx, []byte("h1")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replaced token: %v", err)
	}

	// Tokens outlive a restart.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(ctx, dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if id, err := s.SessionByToken(ctx, []byte("h1b")); err != nil || id != "s1" {
		t.Fatalf("after reopen: %q %v", id, err)
	}

	// Archiving drops the token.
	if err := s.ArchiveSession(ctx, "s1", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByToken(ctx, []byte("h1b")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived session's token: %v", err)
	}
	if ok, err := s.HasSessionToken(ctx, "s1"); err != nil || ok {
		t.Fatalf("archived session has a token: %v %v", ok, err)
	}
	if id, err := s.SessionByToken(ctx, []byte("h2")); err != nil || id != "s2" {
		t.Fatalf("other session: %q %v", id, err)
	}
}
