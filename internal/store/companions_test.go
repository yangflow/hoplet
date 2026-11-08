package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yangflow/hoplet/proto"
)

// migrationsUpTo is the first n migrations.
func migrationsUpTo(t *testing.T, n int) fstest.MapFS {
	t.Helper()
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for _, name := range names[:n] {
		b, err := fs.ReadFile(migrations, name)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimPrefix(name, "migrations/")] = &fstest.MapFile{Data: b}
	}
	return out
}

// dumpRows renders every row of table, in rowid order.
func dumpRows(t *testing.T, s *Store, table string) []string {
	t.Helper()
	return dumpQuery(t, s, `SELECT * FROM `+table+` ORDER BY rowid`)
}

// dumpQuery is every row query returns, formatted.
func dumpQuery(t *testing.T, s *Store, query string) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%v", vals))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func personal(id proto.SessionID) proto.Session {
	return proto.Session{ID: id, Mode: proto.ModePersonal, Harness: proto.HarnessRef{Kind: "hermes"},
		Owner: proto.OwnerMe, Origin: proto.OriginMe, Title: "Ada " + string(id), CreatedAt: t0}
}

// TestCompanionsMigration checks migration 12 on a schema-11 store: every
// Personal session, archived or not, becomes exactly one companion's main
// session, and sessions and events stay as they were.
func TestCompanionsMigration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := open(ctx, dir, migrationsUpTo(t, 11))
	if err != nil {
		t.Fatal(err)
	}
	oldSession(t, s, codeSession("code"))
	for _, id := range []proto.SessionID{"fresh", "native", "gone"} {
		sess := personal(id)
		switch id {
		case "native":
			sess.Harness.NativeID = "n1"
		case "gone":
			sess.ArchivedAt = &t0
		}
		oldSession(t, s, sess)
	}
	for _, id := range []proto.SessionID{"code", "native"} {
		appendOK(t, s, AppendRequest{Session: id, Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}, {Payload: proto.TurnEnded{}}}})
	}
	sessions, events := dumpRows(t, s, "sessions"), dumpRows(t, s, "events")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = open(ctx, dir, migrationsUpTo(t, 12))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := dumpRows(t, s, "sessions"); !slices.Equal(got, sessions) {
		t.Fatalf("sessions changed:\n%v\nwant\n%v", got, sessions)
	}
	if got := dumpRows(t, s, "events"); !slices.Equal(got, events) || len(got) == 0 {
		t.Fatalf("events changed:\n%v\nwant\n%v", got, events)
	}
	cs, err := s.Companions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mains := map[proto.SessionID]Companion{}
	for _, c := range cs {
		mains[c.Main] = c
		if len(c.ID) != 16 || strings.Trim(string(c.ID), "0123456789abcdef") != "" {
			t.Errorf("companion ID %q", c.ID)
		}
		if !c.CreatedAt.Equal(t0) {
			t.Errorf("companion of %s created %v", c.Main, c.CreatedAt)
		}
	}
	if len(cs) != 3 || len(mains) != 3 {
		t.Fatalf("companions %+v", cs)
	}
	if _, ok := mains["code"]; ok {
		t.Fatal("a code session became a companion")
	}
	if c := mains["native"]; !c.InSessionDir || c.Harness.NativeID != "n1" || c.Name != "Ada native" {
		t.Errorf("migrated native session %+v", c)
	}
	if c := mains["fresh"]; c.InSessionDir || c.ArchivedAt != nil {
		t.Errorf("migrated fresh session %+v", c)
	}
	if c := mains["gone"]; c.ArchivedAt == nil || !c.ArchivedAt.Equal(t0) {
		t.Errorf("migrated archived session %+v", c)
	}
}

func TestCreateCompanion(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	sess, c, err := s.CreateCompanion(ctx, personal("main"), "c0ffee0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID != "main" || sess.Version == 0 {
		t.Fatalf("session %+v", sess)
	}
	want := Companion{Companion: proto.Companion{ID: "c0ffee0123456789", Name: "Ada main", Harness: proto.HarnessRef{Kind: "hermes"}, Main: "main", CreatedAt: t0}}
	if fmt.Sprint(c) != fmt.Sprint(want) {
		t.Fatalf("companion %+v, want %+v", c, want)
	}
	for _, arg := range []string{"c0ffee0123456789", "c0ff"} {
		if got, err := s.Companion(ctx, arg); err != nil || got.ID != c.ID {
			t.Errorf("Companion(%q) = %+v %v", arg, got, err)
		}
	}
	if got, err := s.CompanionByMain(ctx, "main"); err != nil || got.ID != c.ID {
		t.Errorf("by main %+v %v", got, err)
	}
	if _, _, err := s.CreateCompanion(ctx, personal("other"), "c0ffee9999999999"); err != nil {
		t.Fatal(err)
	}
	for arg, want := range map[string]error{
		"c0f":               ErrNotFound, // too short for a prefix
		"c0ffee":            ErrInvalid,  // two match
		"beef":              ErrNotFound,
		"c0ffee0123456789x": ErrNotFound,
	} {
		if _, err := s.Companion(ctx, arg); !errors.Is(err, want) {
			t.Errorf("Companion(%q): %v, want %v", arg, err, want)
		}
	}
	if _, err := s.CompanionByMain(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("by unknown main: %v", err)
	}

	// Archiving the main session archives the companion.
	if err := s.ArchiveSession(ctx, "main", t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Companion(ctx, "c0ffee0123456789"); got.ArchivedAt == nil {
		t.Errorf("archived companion %+v", got)
	}
	// A rename of the main session renames the companion.
	title := "Grace"
	if _, err := s.UpdateSession(ctx, "other", SessionUpdate{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.CompanionByMain(ctx, "other"); got.Name != "Grace" {
		t.Errorf("renamed companion %+v", got)
	}
}

func TestCreateCompanionAllOrNothing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, _, err := s.CreateCompanion(ctx, personal("a"), "c1"); err != nil {
		t.Fatal(err)
	}
	// The session exists: no companion row either.
	if _, _, err := s.CreateCompanion(ctx, personal("a"), "c2"); !errors.Is(err, ErrExists) {
		t.Fatalf("existing session: %v", err)
	}
	// The companion exists: no session either.
	if _, _, err := s.CreateCompanion(ctx, personal("b"), "c1"); !errors.Is(err, ErrExists) {
		t.Fatalf("existing companion: %v", err)
	}
	if _, err := s.Session(ctx, "b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session of a failed companion: %v", err)
	}
	if cs, err := s.Companions(ctx); err != nil || len(cs) != 1 || cs[0].ID != "c1" {
		t.Fatalf("companions %+v %v", cs, err)
	}
	// One main session, one companion.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO companions (id, main_session, created_at) VALUES ('c3', 'a', 0)`); err == nil {
		t.Fatal("a second companion of one main session")
	}
	code := personal("code")
	code.Mode = proto.ModeCode
	if _, _, err := s.CreateCompanion(ctx, code, "c4"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("code main session: %v", err)
	}
}
