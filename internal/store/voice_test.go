package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func voiceSession(t *testing.T, s *Store, id proto.SessionID) {
	t.Helper()
	now := time.Now()
	if _, err := s.CreateSession(context.Background(), proto.Session{
		ID: id, Harness: proto.HarnessRef{Kind: "codex"}, Mode: proto.ModeVoice, Owner: proto.OwnerMe, Origin: proto.OriginMe,
		Status: proto.StatusIdle, Permission: proto.ModeConfirm, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func say(t *testing.T, s *Store, id proto.SessionID, role proto.Role, msg, text string) {
	t.Helper()
	if _, err := s.Append(context.Background(), AppendRequest{Session: id, Turn: 1, Entries: []Entry{{Payload: proto.Message{ID: msg, Role: role, Text: text}}}}); err != nil {
		t.Fatal(err)
	}
}

func TestVoiceSessionsStayHidden(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	newSession(t, s, "shown")
	voiceSession(t, s, "call")
	all, err := s.Sessions(ctx)
	if err != nil || len(all) != 1 || all[0].ID != "shown" {
		t.Fatalf("sessions: %+v %v", all, err)
	}
	since, err := s.SessionsSince(ctx, 0)
	if err != nil || len(since) != 1 || since[0].ID != "shown" {
		t.Fatalf("since: %+v %v", since, err)
	}
	if _, err := s.Session(ctx, "call"); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveDeletesOnlyAVoiceTranscript(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	newSession(t, s, "kept")
	voiceSession(t, s, "call")
	say(t, s, "kept", proto.RoleUser, "a", "hello")
	say(t, s, "call", proto.RoleUser, "b", "hello")
	for _, id := range []proto.SessionID{"kept", "call"} {
		if err := s.ArchiveSession(ctx, id, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[proto.SessionID]int{"kept": 1, "call": 0} {
		evs, err := s.Events(ctx, id, 0, 10)
		if err != nil || len(evs) != want {
			t.Fatalf("%s: %d events, %v", id, len(evs), err)
		}
	}
}

func TestClearVoiceTokensLeavesOthers(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	newSession(t, s, "kept")
	voiceSession(t, s, "call")
	for id, token := range map[proto.SessionID]string{"kept": "k", "call": "c"} {
		if err := s.SetSessionToken(ctx, id, TokenHash(token), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ClearVoiceTokens(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByToken(ctx, TokenHash("c")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("voice token: %v", err)
	}
	if id, err := s.SessionByToken(ctx, TokenHash("k")); err != nil || id != "kept" {
		t.Fatalf("other token: %s %v", id, err)
	}
}

func TestLatestReply(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	newSession(t, s, "s")
	if got, err := s.LatestReply(ctx, "s"); err != nil || got != "" {
		t.Fatalf("empty: %q %v", got, err)
	}
	say(t, s, "s", proto.RoleAssistant, "a", "first")
	say(t, s, "s", proto.RoleUser, "b", "mine")
	say(t, s, "s", proto.RoleAssistant, "c", "second")
	say(t, s, "s", proto.RoleAssistant, "d", "  ")
	if got, err := s.LatestReply(ctx, "s"); err != nil || got != "second" {
		t.Fatalf("latest: %q %v", got, err)
	}
}

func TestQueueKeepsVia(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	newSession(t, s, "s")
	if _, _, err := s.EnqueueVia(ctx, "s", "k", "hi", proto.ViaVoice); err != nil {
		t.Fatal(err)
	}
	head, _, ok, err := s.QueueHead(ctx, "s")
	if err != nil || !ok || head.Via != proto.ViaVoice {
		t.Fatalf("head: %+v %v %v", head, ok, err)
	}
	q, err := s.Queue(ctx, "s")
	if err != nil || q[0].Via != proto.ViaVoice {
		t.Fatalf("queue: %+v %v", q, err)
	}
}
