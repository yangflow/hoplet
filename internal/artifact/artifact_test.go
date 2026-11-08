package artifact

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

func newService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, id := range []proto.SessionID{"s1", "s2"} {
		_, err := st.CreateSession(ctx, proto.Session{ID: id, Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"},
			Directory: "/work", Isolation: proto.InPlace, Owner: proto.OwnerMe, Origin: proto.OriginMe, CreatedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
	}
	return &Service{Store: st, Root: filepath.Join(t.TempDir(), "artifacts")}, st
}

// write creates files under dir from rel path to content.
func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// readAll reads a version's file chunk by chunk.
func readAll(t *testing.T, s *Service, id proto.ArtifactID, version int, rel string) ([]byte, string) {
	t.Helper()
	var out []byte
	var ct string
	for {
		c, err := s.Read(context.Background(), proto.ArtifactsReadParams{Artifact: id, Version: version, Path: rel, Offset: int64(len(out))})
		if err != nil {
			t.Fatal(err)
		}
		out, ct = append(out, c.Data...), c.ContentType
		if int64(len(out)) == c.Size {
			return out, ct
		}
		if len(c.Data) == 0 {
			t.Fatalf("no progress at %d of %d", len(out), c.Size)
		}
	}
}

// tree lists every file and directory under root, relative to it.
func tree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel != "." {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// versionFiles lists the files of every version directory under root.
func versionFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, p := range tree(t, root) {
		if parts := strings.Split(p, "/"); len(parts) > 3 {
			out = append(out, p)
		}
	}
	return out
}

func TestPublishSingleFile(t *testing.T) {
	ctx := context.Background()
	s, st := newService(t)
	src := t.TempDir()
	write(t, src, map[string]string{"notes.md": "# Notes\n"})
	a, err := s.Publish(ctx, Request{Session: "s1", Name: "  notes ", Entry: filepath.Join(src, "notes.md"), By: proto.ArtifactByCLI})
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "notes" || a.Kind != proto.ArtifactMarkdown || a.Version != 1 || a.Entry != "notes.md" || a.Files != 1 || a.Size != 8 || a.By != proto.ArtifactByCLI {
		t.Fatalf("artifact %+v", a)
	}
	got, _ := readAll(t, s, a.ID, 0, "")
	if string(got) != "# Notes\n" {
		t.Fatalf("read %q", got)
	}
	if list, err := st.Artifacts(ctx, "s1"); err != nil || len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("list %+v %v", list, err)
	}
}

func TestPublishHTMLWithFilesAndVersions(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)
	src := t.TempDir()
	big := strings.Repeat("0123456789abcdef", proto.MaxArtifactChunk/16*2+5) // over two chunks
	write(t, src, map[string]string{"site/index.html": "<h1>v1</h1>", "site/js/app.js": big, "site/img/logo.svg": "<svg/>"})
	site := filepath.Join(src, "site")
	req := Request{Session: "s1", Name: "site", Entry: filepath.Join(site, "index.html"),
		Files: []string{filepath.Join(site, "js/app.js"), filepath.Join(site, "img/logo.svg")}, By: proto.ArtifactByHarness}
	v1, err := s.Publish(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Kind != proto.ArtifactHTML || v1.Files != 3 || v1.Size != int64(len(big)+len("<h1>v1</h1>")+len("<svg/>")) {
		t.Fatalf("v1 %+v", v1)
	}
	got, ct := readAll(t, s, v1.ID, 1, "js/app.js")
	if string(got) != big || !strings.Contains(ct, "javascript") {
		t.Fatalf("read app.js: %d bytes, %q", len(got), ct)
	}
	if _, ct := readAll(t, s, v1.ID, 1, "img/logo.svg"); ct != "image/svg+xml" {
		t.Fatalf("svg type %q", ct)
	}
	if _, ct := readAll(t, s, v1.ID, 1, ""); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("entry type %q", ct)
	}

	// A new version leaves the old one as it was.
	write(t, src, map[string]string{"site/index.html": "<h1>v2</h1>"})
	req.Files = nil
	v2, err := s.Publish(ctx, req)
	if err != nil || v2.ID != v1.ID || v2.Version != 2 || v2.Files != 1 {
		t.Fatalf("v2 %+v %v", v2, err)
	}
	if got, _ := readAll(t, s, v1.ID, 1, ""); string(got) != "<h1>v1</h1>" {
		t.Fatalf("v1 entry %q", got)
	}
	if got, _ := readAll(t, s, v1.ID, 0, ""); string(got) != "<h1>v2</h1>" {
		t.Fatalf("latest entry %q", got)
	}
	if _, err := s.Read(ctx, proto.ArtifactsReadParams{Artifact: v1.ID, Version: 2, Path: "js/app.js"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("v2 has no app.js: %v", err)
	}
	// Another kind under the same name is refused.
	write(t, src, map[string]string{"site.md": "x"})
	if _, err := s.Publish(ctx, Request{Session: "s1", Name: "site", Entry: filepath.Join(src, "site.md"), By: proto.ArtifactByCLI}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("kind change: %v", err)
	}
	// The same name in another session is another artifact.
	other, err := s.Publish(ctx, Request{Session: "s2", Name: "site", Entry: filepath.Join(site, "index.html"), By: proto.ArtifactByCLI})
	if err != nil || other.ID == v1.ID || other.Version != 1 {
		t.Fatalf("other session %+v %v", other, err)
	}
}

func TestPublishFailuresLeaveNothing(t *testing.T) {
	ctx := context.Background()
	s, st := newService(t)
	src := t.TempDir()
	outside := t.TempDir()
	write(t, src, map[string]string{"index.html": "x", "a.js": "a", "notes.md": "m", "logo.svg": "<svg/>", "big.bin": strings.Repeat("x", MaxSize)})
	write(t, outside, map[string]string{"secret.txt": "s"})
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(src, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(src, "dir.html"), 0o700); err != nil {
		t.Fatal(err)
	}
	many := make([]string, MaxFiles)
	for i := range many {
		many[i] = filepath.Join(src, "a.js")
	}
	entry := filepath.Join(src, "index.html")
	for name, req := range map[string]Request{
		"no name":            {Name: " ", Entry: entry},
		"slash in name":      {Name: "a/b", Entry: entry},
		"control in name":    {Name: "a\nb", Entry: entry},
		"long name":          {Name: strings.Repeat("n", MaxNameLen+1), Entry: entry},
		"svg entry":          {Name: "n", Entry: filepath.Join(src, "logo.svg")},
		"unknown entry":      {Name: "n", Entry: filepath.Join(src, "big.bin")},
		"relative entry":     {Name: "n", Entry: "index.html"},
		"files with md":      {Name: "n", Entry: filepath.Join(src, "notes.md"), Files: []string{filepath.Join(src, "a.js")}},
		"too many files":     {Name: "n", Entry: entry, Files: many},
		"too large":          {Name: "n", Entry: entry, Files: []string{filepath.Join(src, "a.js"), filepath.Join(src, "big.bin")}},
		"file outside":       {Name: "n", Entry: entry, Files: []string{filepath.Join(outside, "secret.txt")}},
		"symlink outside":    {Name: "n", Entry: entry, Files: []string{filepath.Join(src, "link.txt")}},
		"dotdot":             {Name: "n", Entry: entry, Files: []string{src + "/../x/a.js"}},
		"duplicate":          {Name: "n", Entry: entry, Files: []string{filepath.Join(src, "a.js"), filepath.Join(src, "a.js")}},
		"entry again":        {Name: "n", Entry: entry, Files: []string{entry}},
		"directory entry":    {Name: "n", Entry: filepath.Join(src, "dir.html")},
		"missing file":       {Name: "n", Entry: entry, Files: []string{filepath.Join(src, "gone.js")}},
		"relative file":      {Name: "n", Entry: entry, Files: []string{"a.js"}},
		"missing entry":      {Name: "n", Entry: filepath.Join(src, "gone.html")},
		"directory as files": {Name: "n", Entry: entry, Files: []string{filepath.Join(src, "dir.html")}},
	} {
		req.Session, req.By = "s1", proto.ArtifactByCLI
		if _, err := s.Publish(ctx, req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want invalid", name, err)
		}
	}
	if _, err := s.Publish(ctx, Request{Session: "nope", Name: "n", Entry: entry, By: proto.ArtifactByCLI}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing session: %v", err)
	}
	if err := st.ArchiveSession(ctx, "s2", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(ctx, Request{Session: "s2", Name: "n", Entry: entry, By: proto.ArtifactByCLI}); !errors.Is(err, ErrInvalid) {
		t.Errorf("archived session: %v", err)
	}
	if got := versionFiles(t, s.Root); len(got) != 0 {
		t.Fatalf("left behind: %v", got)
	}
	if list, _ := st.Artifacts(ctx, "s1"); len(list) != 0 {
		t.Fatalf("recorded: %+v", list)
	}
}

// failingStore fails to record.
type failingStore struct{ *store.Store }

func (failingStore) PublishArtifact(context.Context, proto.Artifact) (proto.Artifact, error) {
	return proto.Artifact{}, errors.New("disk full")
}

func TestPublishRemovesVersionWhenRecordFails(t *testing.T) {
	ctx := context.Background()
	s, st := newService(t)
	src := t.TempDir()
	write(t, src, map[string]string{"a.txt": "a"})
	s.Store = failingStore{st}
	if _, err := s.Publish(ctx, Request{Session: "s1", Name: "a", Entry: filepath.Join(src, "a.txt"), By: proto.ArtifactByCLI}); err == nil {
		t.Fatal("publish succeeded")
	}
	if got := versionFiles(t, s.Root); len(got) != 0 {
		t.Fatalf("left behind: %v", got)
	}
}

func TestClean(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)
	src := t.TempDir()
	write(t, src, map[string]string{"a.txt": "a"})
	a, err := s.Publish(ctx, Request{Session: "s1", Name: "a", Entry: filepath.Join(src, "a.txt"), By: proto.ArtifactByCLI})
	if err != nil {
		t.Fatal(err)
	}
	adir := filepath.Join(s.Root, "s1", string(a.ID))
	write(t, adir, map[string]string{".tmp-123/a.txt": "half", "v2/a.txt": "unrecorded", "v1x/keep": "k", "notes": "k"})
	write(t, filepath.Join(s.Root, "s2", "ghost"), map[string]string{"v1/a.txt": "unrecorded"})
	if err := s.Clean(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"s1", "s1/" + string(a.ID), "s1/" + string(a.ID) + "/notes", "s1/" + string(a.ID) + "/v1",
		"s1/" + string(a.ID) + "/v1/a.txt", "s1/" + string(a.ID) + "/v1x", "s1/" + string(a.ID) + "/v1x/keep", "s2", "s2/ghost"}
	if got := tree(t, s.Root); !slices.Equal(got, want) {
		t.Fatalf("after clean:\n%v\nwant\n%v", got, want)
	}
	if got, _ := readAll(t, s, a.ID, 1, ""); string(got) != "a" {
		t.Fatalf("recorded version %q", got)
	}
	// A publish after a crash left an unrecorded next version takes its place.
	write(t, adir, map[string]string{"v2/stale.txt": "stale"})
	v2, err := s.Publish(ctx, Request{Session: "s1", Name: "a", Entry: filepath.Join(src, "a.txt"), By: proto.ArtifactByCLI})
	if err != nil || v2.Version != 2 {
		t.Fatalf("v2 %+v %v", v2, err)
	}
	if _, err := os.Stat(filepath.Join(adir, "v2", "stale.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale file: %v", err)
	}
	// A missing root is clean.
	if err := (&Service{Store: s.Store, Root: filepath.Join(t.TempDir(), "none")}).Clean(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRead(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)
	src := t.TempDir()
	write(t, src, map[string]string{"site/index.html": "hello", "site/data.unknownext": "d", "secret": "s"})
	a, err := s.Publish(ctx, Request{Session: "s1", Name: "site", Entry: filepath.Join(src, "site/index.html"),
		Files: []string{filepath.Join(src, "site/data.unknownext")}, By: proto.ArtifactByCLI})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Read(ctx, proto.ArtifactsReadParams{Artifact: a.ID, Offset: 2})
	if err != nil || !bytes.Equal(c.Data, []byte("llo")) || c.Size != 5 {
		t.Fatalf("offset read %+v %v", c, err)
	}
	if c, err := s.Read(ctx, proto.ArtifactsReadParams{Artifact: a.ID, Offset: 5}); err != nil || len(c.Data) != 0 {
		t.Fatalf("read at end %+v %v", c, err)
	}
	if c, err := s.Read(ctx, proto.ArtifactsReadParams{Artifact: a.ID, Path: "data.unknownext"}); err != nil || c.ContentType != "application/octet-stream" {
		t.Fatalf("unknown type %+v %v", c, err)
	}
	if c, err := s.Read(ctx, proto.ArtifactsReadParams{Artifact: a.ID, Path: "/./index.html"}); err != nil || string(c.Data) != "hello" {
		t.Fatalf("cleaned path %+v %v", c, err)
	}
	for name, tc := range map[string]struct {
		p    proto.ArtifactsReadParams
		want error
	}{
		"past end":        {proto.ArtifactsReadParams{Artifact: a.ID, Offset: 6}, ErrInvalid},
		"negative offset": {proto.ArtifactsReadParams{Artifact: a.ID, Offset: -1}, ErrInvalid},
		"escape":          {proto.ArtifactsReadParams{Artifact: a.ID, Path: "../../../../secret"}, ErrNotFound},
		"missing file":    {proto.ArtifactsReadParams{Artifact: a.ID, Path: "nope.js"}, ErrNotFound},
		"version dir":     {proto.ArtifactsReadParams{Artifact: a.ID, Path: "."}, ErrNotFound},
		"missing version": {proto.ArtifactsReadParams{Artifact: a.ID, Version: 2}, store.ErrNotFound},
		"missing id":      {proto.ArtifactsReadParams{Artifact: "nope"}, store.ErrNotFound},
	} {
		if _, err := s.Read(ctx, tc.p); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}
