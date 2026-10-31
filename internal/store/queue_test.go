package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func queueKeys(t *testing.T, s *Store, id proto.SessionID) []string {
	t.Helper()
	msgs, err := s.Queue(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for i, m := range msgs {
		if m.Position != i {
			t.Fatalf("message %s at position %d, want %d", m.Key, m.Position, i)
		}
		keys = append(keys, m.Key)
	}
	return keys
}

func enqueue(t *testing.T, s *Store, id proto.SessionID, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, _, err := s.Enqueue(context.Background(), id, k, "text "+k); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnqueueIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	m, queued, err := s.Enqueue(ctx, "s", "a", "first")
	if err != nil || !queued || m.Position != 0 || m.Text != "first" {
		t.Fatalf("enqueue: %+v %v %v", m, queued, err)
	}
	again, queued, err := s.Enqueue(ctx, "s", "a", "changed")
	if err != nil || queued || again.Text != "first" || !again.CreatedAt.Equal(m.CreatedAt) {
		t.Fatalf("enqueue again: %+v %v %v", again, queued, err)
	}
	if got := queueKeys(t, s, "s"); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("queue %v", got)
	}
	if _, _, err := s.Enqueue(ctx, "missing", "a", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	if _, _, err := s.Enqueue(ctx, "s", "", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty key: %v", err)
	}
	if _, _, err := s.Enqueue(ctx, "s", "b", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty text: %v", err)
	}
	if _, _, err := s.Enqueue(ctx, "s", strings.Repeat("k", MaxQueueKey+1), "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("key too long: %v", err)
	}
	if _, queued, err := s.Enqueue(ctx, "s", strings.Repeat("k", MaxQueueKey), "x"); err != nil || !queued {
		t.Fatalf("key at the limit: %v %v", queued, err)
	}
}

func TestQueueIsBounded(t *testing.T) {
	s := newStore(t)
	newSession(t, s, "s")
	for i := range MaxQueue {
		enqueue(t, s, "s", fmt.Sprint(i))
	}
	if _, _, err := s.Enqueue(context.Background(), "s", "over", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over the bound: %v", err)
	}
	if _, queued, err := s.Enqueue(context.Background(), "s", "0", "again"); err != nil || queued {
		t.Fatalf("existing key while full: queued %v, err %v", queued, err)
	}
}

func TestQueueEditMoveDelete(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	newSession(t, s, "other")
	enqueue(t, s, "s", "a", "b", "c", "d")
	enqueue(t, s, "other", "x")

	if err := s.MoveQueued(ctx, "s", "c", 0); err != nil {
		t.Fatal(err)
	}
	if got := queueKeys(t, s, "s"); !slices.Equal(got, []string{"c", "a", "b", "d"}) {
		t.Fatalf("after move to front: %v", got)
	}
	if err := s.MoveQueued(ctx, "s", "c", 99); err != nil {
		t.Fatal(err)
	}
	if got := queueKeys(t, s, "s"); !slices.Equal(got, []string{"a", "b", "d", "c"}) {
		t.Fatalf("after move past the end: %v", got)
	}
	if err := s.MoveQueued(ctx, "s", "a", -3); err != nil {
		t.Fatal(err)
	}
	if got := queueKeys(t, s, "s"); !slices.Equal(got, []string{"a", "b", "d", "c"}) {
		t.Fatalf("after clamped move: %v", got)
	}
	if err := s.DeleteQueued(ctx, "s", "b"); err != nil {
		t.Fatal(err)
	}
	if got := queueKeys(t, s, "s"); !slices.Equal(got, []string{"a", "d", "c"}) {
		t.Fatalf("after delete: %v", got)
	}
	enqueue(t, s, "s", "e")
	if got := queueKeys(t, s, "s"); !slices.Equal(got, []string{"a", "d", "c", "e"}) {
		t.Fatalf("after enqueue: %v", got)
	}

	if err := s.FailQueued(ctx, "s", "a", "harness is gone"); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.Queue(ctx, "s")
	if msgs[0].Error != "harness is gone" {
		t.Fatalf("failure not recorded: %+v", msgs[0])
	}
	if err := s.EditQueued(ctx, "s", "a", "new text"); err != nil {
		t.Fatal(err)
	}
	msgs, _ = s.Queue(ctx, "s")
	if msgs[0].Error != "" || msgs[0].Text != "new text" {
		t.Fatalf("edit: %+v", msgs[0])
	}
	if err := s.FailQueued(ctx, "s", "a", "again"); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveQueued(ctx, "s", "a", 0); err != nil {
		t.Fatal(err)
	}
	msgs, _ = s.Queue(ctx, "s")
	if msgs[0].Error != "" {
		t.Fatalf("move kept the error: %+v", msgs[0])
	}

	for name, err := range map[string]error{
		"edit":       s.EditQueued(ctx, "s", "nope", "x"),
		"fail":       s.FailQueued(ctx, "s", "nope", "x"),
		"delete":     s.DeleteQueued(ctx, "s", "nope"),
		"move":       s.MoveQueued(ctx, "s", "nope", 0),
		"other":      s.DeleteQueued(ctx, "other", "a"),
		"no session": s.DeleteQueued(ctx, "missing", "a"),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, want ErrNotFound", name, err)
		}
	}
	if err := s.FailQueued(ctx, "s", "a", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty reason: %v", err)
	}
	if got := queueKeys(t, s, "other"); !slices.Equal(got, []string{"x"}) {
		t.Fatalf("other session's queue changed: %v", got)
	}
	if _, err := s.Queue(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("queue of missing session: %v", err)
	}
}

func TestQueuedSessions(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "a")
	newSession(t, s, "b")
	newSession(t, s, "c")
	enqueue(t, s, "c", "1", "2")
	enqueue(t, s, "a", "1")
	ids, err := s.QueuedSessions(ctx)
	if err != nil || !slices.Equal(ids, []proto.SessionID{"a", "c"}) {
		t.Fatalf("queued sessions %v %v", ids, err)
	}
	if err := s.DeleteQueued(ctx, "a", "1"); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.QueuedSessions(ctx); !slices.Equal(ids, []proto.SessionID{"c"}) {
		t.Fatalf("after delete %v", ids)
	}
}

func TestQueueWritesSignalChanges(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	enqueue(t, s, "s", "a", "b")
	for _, op := range []struct {
		name string
		run  func() error
	}{
		{"enqueue", func() error { _, _, err := s.Enqueue(ctx, "s", "c", "x"); return err }},
		{"edit", func() error { return s.EditQueued(ctx, "s", "a", "y") }},
		{"fail", func() error { return s.FailQueued(ctx, "s", "a", "reason") }},
		{"move", func() error { return s.MoveQueued(ctx, "s", "b", 0) }},
		{"delete", func() error { return s.DeleteQueued(ctx, "s", "c") }},
	} {
		ch := s.Changes()
		if err := op.run(); err != nil {
			t.Fatalf("%s: %v", op.name, err)
		}
		select {
		case <-ch:
		default:
			t.Fatalf("%s did not signal a change", op.name)
		}
	}
}

func TestQueueSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	newSession(t, s, "s")
	enqueue(t, s, "s", "a", "b")
	if err := s.MoveQueued(ctx, "s", "b", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.FailQueued(ctx, "s", "a", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	msgs, err := s.Queue(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Key != "b" || msgs[1].Key != "a" || msgs[1].Error != "x" {
		t.Fatalf("after reopen: %+v", msgs)
	}
}

func TestQueueHeadSendKeyChangesWithEdits(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	if _, _, ok, err := s.QueueHead(ctx, "s"); err != nil || ok {
		t.Fatalf("head of an empty queue: ok %v, %v", ok, err)
	}
	enqueue(t, s, "s", "k", "other")
	head := func() (string, string) {
		m, sendKey, ok, err := s.QueueHead(ctx, "s")
		if err != nil || !ok {
			t.Fatalf("head: ok %v, %v", ok, err)
		}
		return m.Key, sendKey
	}
	if k, sk := head(); k != "k" || sk != "k" {
		t.Fatalf("head %s sent as %s, want k as k", k, sk)
	}
	for range 2 {
		if err := s.EditQueued(ctx, "s", "k", "again"); err != nil {
			t.Fatal(err)
		}
	}
	if k, sk := head(); k != "k" || sk != "k#2" {
		t.Fatalf("edited head %s sent as %s, want k as k#2", k, sk)
	}
	// Moving does not change the attempt.
	if err := s.MoveQueued(ctx, "s", "other", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveQueued(ctx, "s", "k", 0); err != nil {
		t.Fatal(err)
	}
	if k, sk := head(); k != "k" || sk != "k#2" {
		t.Fatalf("moved head %s sent as %s, want k as k#2", k, sk)
	}
	if _, _, _, err := s.QueueHead(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("head of an unknown session: %v", err)
	}
}

func TestQueueRefusesEmptyEditsAndReservedKeys(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	enqueue(t, s, "s", "a")
	if err := s.EditQueued(ctx, "s", "a", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty edit: %v", err)
	}
	if msgs, err := s.Queue(ctx, "s"); err != nil || msgs[0].Text != "text a" {
		t.Fatalf("queue after a refused edit: %+v %v", msgs, err)
	}
	if _, _, err := s.Enqueue(ctx, "s", "b#1", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("key with #: %v", err)
	}
}
