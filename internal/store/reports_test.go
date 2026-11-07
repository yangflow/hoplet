package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func child(id, parent proto.SessionID) proto.Session {
	sess := personal(id)
	sess.Owner, sess.Parent = proto.OwnerDelegated, parent
	return sess
}

// oldSession stores sess as a schema-12 build wrote it: this build's
// CreateSession knows columns schema 12 lacks.
func oldSession(t *testing.T, s *Store, sess proto.Session) {
	t.Helper()
	var archived any
	if sess.ArchivedAt != nil {
		archived = sess.ArchivedAt.UnixMilli()
	}
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO sessions (id, mode, harness_kind, native_id, directory, isolation,
		owner, origin, status, title, created_at, updated_at, archived_at, version, permission)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'idle', ?, ?, ?, ?, 1, 'confirm')`,
		sess.ID, sess.Mode, sess.Harness.Kind, sess.Harness.NativeID, sess.Directory, sess.Isolation,
		sess.Owner, sess.Origin, sess.Title, sess.CreatedAt.UnixMilli(), sess.CreatedAt.UnixMilli(), archived)
	if err != nil {
		t.Fatal(err)
	}
}

func codeSession(id proto.SessionID) proto.Session {
	return proto.Session{ID: id, Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"},
		Directory: "/work", Isolation: proto.InPlace, Owner: proto.OwnerMe, Origin: proto.OriginMe, CreatedAt: t0}
}

func TestSessionParent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "p")
	got, err := s.CreateSession(ctx, child("c", "p"))
	if err != nil || got.Parent != "p" {
		t.Fatalf("create child %+v %v", got, err)
	}
	if got, err := s.Session(ctx, "c"); err != nil || got.Parent != "p" || got.Owner != proto.OwnerDelegated {
		t.Fatalf("read child %+v %v", got, err)
	}
	if _, err := s.CreateSession(ctx, child("orphan", "missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing parent: %v", err)
	}
	if _, err := s.CreateSession(ctx, child("self", "self")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("own parent: %v", err)
	}
	if _, err := s.Session(ctx, "orphan"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan stored: %v", err)
	}
}

func TestReportQueuesAKeyOnce(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "p")
	if _, err := s.CreateSession(ctx, child("c", "p")); err != nil {
		t.Fatal(err)
	}
	m, queued, err := s.Report(ctx, "p", "c", "r1", "found it")
	if err != nil || !queued || m.From != "c" || m.Text != "found it" {
		t.Fatalf("report %+v %v %v", m, queued, err)
	}
	q, err := s.Queue(ctx, "p")
	if err != nil || len(q) != 1 || q[0].From != "c" || q[0].Key != "r1" {
		t.Fatalf("queue %+v %v", q, err)
	}
	if _, queued, err := s.Report(ctx, "p", "c", "r1", "again"); err != nil || queued {
		t.Fatalf("report again: %v %v", queued, err)
	}
	// An edit keeps the source.
	if err := s.EditQueued(ctx, "p", "r1", "edited"); err != nil {
		t.Fatal(err)
	}
	head, _, ok, err := s.QueueHead(ctx, "p")
	if err != nil || !ok || head.From != "c" || head.Text != "edited" {
		t.Fatalf("head %+v %v %v", head, ok, err)
	}
	// A deleted report does not come back.
	if err := s.DeleteQueued(ctx, "p", "r1"); err != nil {
		t.Fatal(err)
	}
	if _, queued, err := s.Report(ctx, "p", "c", "r1", "again"); err != nil || queued {
		t.Fatalf("report after delete: %v %v", queued, err)
	}
	if got := queueKeys(t, s, "p"); len(got) != 0 {
		t.Fatalf("queue %v", got)
	}
	// Other keys and the user's messages queue as before.
	enqueue(t, s, "p", "mine")
	if _, queued, err := s.Report(ctx, "p", "c", "r2", "more"); err != nil || !queued {
		t.Fatalf("second report: %v %v", queued, err)
	}
	if got := queueKeys(t, s, "p"); !slices.Equal(got, []string{"mine", "r2"}) {
		t.Fatalf("queue %v", got)
	}
	if q, _ := s.Queue(ctx, "p"); q[0].From != "" {
		t.Fatalf("user message from %q", q[0].From)
	}
}

func TestReportFailures(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "p")
	if _, err := s.CreateSession(ctx, child("c", "p")); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		parent, from proto.SessionID
		key, text    string
		want         error
	}{
		"missing parent": {"missing", "c", "k", "t", ErrNotFound},
		"no source":      {"p", "", "k", "t", ErrInvalid},
		"no key":         {"p", "c", "", "t", ErrInvalid},
		"hash in key":    {"p", "c", "a#1", "t", ErrInvalid},
		"no text":        {"p", "c", "k", "", ErrInvalid},
	} {
		if _, _, err := s.Report(ctx, tc.parent, tc.from, tc.key, tc.text); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	// A failed report leaves its key free.
	if _, queued, err := s.Report(ctx, "p", "c", "k", "t"); err != nil || !queued {
		t.Fatalf("report %v %v", queued, err)
	}
	if err := s.ArchiveSession(ctx, "p", t0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Report(ctx, "p", "c", "k2", "t"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("archived parent: %v", err)
	}
}

func TestReportFullQueue(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "p")
	for i := range MaxQueue {
		enqueue(t, s, "p", string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if _, _, err := s.Report(ctx, "p", "c", "k", "t"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("full queue: %v", err)
	}
	// The key was not used up by the failed report.
	if err := s.DeleteQueued(ctx, "p", "aa"); err != nil {
		t.Fatal(err)
	}
	if _, queued, err := s.Report(ctx, "p", "c", "k", "t"); err != nil || !queued {
		t.Fatalf("report after room: %v %v", queued, err)
	}
}

// TestDelegationMigration checks migration 13 on a schema-12 store:
// sessions, events and the queue keep their data, with no parent and no
// source.
func TestDelegationMigration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := open(ctx, dir, migrationsUpTo(t, 12))
	if err != nil {
		t.Fatal(err)
	}
	oldSession(t, s, codeSession("code"))
	appendOK(t, s, AppendRequest{Session: "code", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}, {Payload: proto.TurnEnded{}}}})
	if _, err := s.db.ExecContext(ctx, `INSERT INTO queue (session_id, key, text, position, created_at) VALUES ('code', 'k', 'hi', 0, 1)`); err != nil {
		t.Fatal(err)
	}
	const queueRows = `SELECT * FROM queue ORDER BY session_id, key`
	sessions, events, queue := dumpRows(t, s, "sessions"), dumpRows(t, s, "events"), dumpQuery(t, s, queueRows)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// The new column is last and empty.
	withEmpty := func(rows []string) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = strings.TrimSuffix(r, "]") + " ]"
		}
		return out
	}
	if got := dumpRows(t, s, "sessions"); !slices.Equal(got, withEmpty(sessions)) {
		t.Fatalf("sessions changed:\n%v\nwant\n%v", got, sessions)
	}
	if got := dumpRows(t, s, "events"); !slices.Equal(got, events) || len(got) == 0 {
		t.Fatalf("events changed:\n%v\nwant\n%v", got, events)
	}
	if got := dumpQuery(t, s, queueRows); !slices.Equal(got, withEmpty(queue)) || len(got) == 0 {
		t.Fatalf("queue changed:\n%v\nwant\n%v", got, queue)
	}
	if sess, err := s.Session(ctx, "code"); err != nil || sess.Parent != "" {
		t.Fatalf("migrated session %+v %v", sess, err)
	}
	if q, err := s.Queue(ctx, "code"); err != nil || len(q) != 1 || q[0].From != "" || q[0].Text != "hi" {
		t.Fatalf("migrated queue %+v %v", q, err)
	}
}
