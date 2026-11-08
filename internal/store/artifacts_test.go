package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func version(session proto.SessionID, id proto.ArtifactID, name string, kind proto.ArtifactKind, n int) proto.Artifact {
	return proto.Artifact{ID: id, Session: session, Name: name, Kind: kind, Version: n,
		Entry: "index.html", By: proto.ArtifactByHarness, Files: 2, Size: 1234}
}

func TestPublishArtifactVersions(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "a")
	newSession(t, s, "b")
	v1, err := s.PublishArtifact(ctx, version("a", "art1", "report", proto.ArtifactHTML, 1))
	if err != nil || v1.Version != 1 || v1.Files != 2 || v1.Size != 1234 || v1.CreatedAt.IsZero() || !v1.UpdatedAt.Equal(v1.CreatedAt) {
		t.Fatalf("v1 %+v %v", v1, err)
	}
	v2 := version("a", "art1", "report", proto.ArtifactHTML, 2)
	v2.By, v2.Entry = proto.ArtifactByCLI, "page.html"
	got, err := s.PublishArtifact(ctx, v2)
	if err != nil || got.Version != 2 || got.By != proto.ArtifactByCLI || got.Entry != "page.html" || !got.CreatedAt.Equal(v1.CreatedAt) {
		t.Fatalf("v2 %+v %v", got, err)
	}
	// The same name in another session is another artifact.
	if _, err := s.PublishArtifact(ctx, version("b", "art2", "report", proto.ArtifactMarkdown, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishArtifact(ctx, version("a", "art3", "chart", proto.ArtifactImage, 1)); err != nil {
		t.Fatal(err)
	}

	list, err := s.Artifacts(ctx, "a")
	if err != nil || len(list) != 2 {
		t.Fatalf("list %+v %v", list, err)
	}
	names := []string{list[0].Name, list[1].Name}
	if !slices.Equal(names, []string{"chart", "report"}) || list[1].Version != 2 {
		t.Fatalf("list %+v", list)
	}
	if old, err := s.ArtifactVersion(ctx, "art1", 1); err != nil || old.Entry != "index.html" || old.By != proto.ArtifactByHarness {
		t.Fatalf("v1 after v2 %+v %v", old, err)
	}
	if latest, err := s.ArtifactVersion(ctx, "art1", 0); err != nil || latest.Version != 2 {
		t.Fatalf("latest %+v %v", latest, err)
	}
	if byName, err := s.ArtifactByName(ctx, "b", "report"); err != nil || byName.ID != "art2" {
		t.Fatalf("by name %+v %v", byName, err)
	}
	if _, err := s.ArtifactVersion(ctx, "art1", 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing version: %v", err)
	}
	if _, err := s.ArtifactVersion(ctx, "nope", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing artifact: %v", err)
	}
	versions, err := s.ArtifactVersions(ctx, "a")
	if err != nil || !slices.Equal(versions["art1"], []int{1, 2}) || !slices.Equal(versions["art3"], []int{1}) || len(versions) != 2 {
		t.Fatalf("versions %v %v", versions, err)
	}
}

func TestPublishArtifactFailures(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "a")
	if _, err := s.PublishArtifact(ctx, version("a", "art1", "report", proto.ArtifactHTML, 1)); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		v    proto.Artifact
		want error
	}{
		"missing session": {version("nope", "x", "report", proto.ArtifactHTML, 1), ErrNotFound},
		"kind changes":    {version("a", "art1", "report", proto.ArtifactMarkdown, 2), ErrInvalid},
		"unknown kind":    {version("a", "art1", "report", "svg", 2), ErrInvalid},
		"no name":         {version("a", "art1", "", proto.ArtifactHTML, 2), ErrInvalid},
		"version skipped": {version("a", "art1", "report", proto.ArtifactHTML, 3), ErrConflict},
		"version reused":  {version("a", "art1", "report", proto.ArtifactHTML, 1), ErrConflict},
		"other ID":        {version("a", "other", "report", proto.ArtifactHTML, 2), ErrConflict},
		"new at 2":        {version("a", "new", "fresh", proto.ArtifactHTML, 2), ErrConflict},
		"ID taken":        {version("a", "art1", "fresh", proto.ArtifactHTML, 1), ErrExists},
	} {
		if _, err := s.PublishArtifact(ctx, tc.v); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	if list, _ := s.Artifacts(ctx, "a"); len(list) != 1 || list[0].Version != 1 {
		t.Fatalf("failures left rows: %+v", list)
	}
	if err := s.ArchiveSession(ctx, "a", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishArtifact(ctx, version("a", "art1", "report", proto.ArtifactHTML, 2)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("archived session: %v", err)
	}
}

func TestPublishArtifactEvent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "a")
	// No turn is open: the event is at turn 0.
	if _, err := s.PublishArtifact(ctx, version("a", "art1", "report", proto.ArtifactHTML, 1)); err != nil {
		t.Fatal(err)
	}
	appendOK(t, s, AppendRequest{Session: "a", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	if _, err := s.PublishArtifact(ctx, version("a", "art1", "report", proto.ArtifactHTML, 2)); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, "a", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var got []proto.Event
	for _, ev := range events {
		if ev.Type == proto.TypeArtifactPublished {
			got = append(got, ev)
		}
	}
	if len(got) != 2 || got[0].Turn != 0 || got[1].Turn != 1 {
		t.Fatalf("events %+v", got)
	}
	p, err := got[1].Decode()
	if err != nil {
		t.Fatal(err)
	}
	want := proto.ArtifactPublished{Artifact: "art1", Name: "report", Kind: proto.ArtifactHTML, Version: 2, By: proto.ArtifactByHarness}
	if p != want {
		t.Fatalf("payload %+v", p)
	}
	if sess, err := s.Session(ctx, "a"); err != nil || sess.LastSeq != got[1].Seq {
		t.Fatalf("session %+v %v", sess, err)
	}
}

// TestArtifactsMigration checks migration 14 on a schema-13 store:
// sessions and events stay as they were, with no artifacts.
func TestArtifactsMigration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := open(ctx, dir, migrationsUpTo(t, 13))
	if err != nil {
		t.Fatal(err)
	}
	newSession(t, s, "code")
	appendOK(t, s, AppendRequest{Session: "code", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}, {Payload: proto.TurnEnded{}}}})
	sessions, events := dumpRows(t, s, "sessions"), dumpRows(t, s, "events")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := dumpRows(t, s, "sessions"); !slices.Equal(got, sessions) || len(got) == 0 {
		t.Fatalf("sessions changed:\n%v\nwant\n%v", got, sessions)
	}
	if got := dumpRows(t, s, "events"); !slices.Equal(got, events) || len(got) == 0 {
		t.Fatalf("events changed:\n%v\nwant\n%v", got, events)
	}
	if list, err := s.Artifacts(ctx, "code"); err != nil || len(list) != 0 {
		t.Fatalf("artifacts %+v %v", list, err)
	}
	if _, err := s.PublishArtifact(ctx, version("code", "art1", "report", proto.ArtifactHTML, 1)); err != nil {
		t.Fatal(err)
	}
}
