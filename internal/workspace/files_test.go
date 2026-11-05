package workspace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// entryPaths are the paths of a listing, with a mark for kinds and ignored
// entries: "dir/", "link@", "file!" (ignored).
func entryPaths(l proto.FileList) []string {
	var out []string
	for _, e := range l.Entries {
		p := e.Path
		switch e.Kind {
		case proto.FileKindDir:
			p += "/"
		case proto.FileKindSymlink:
			p += "@"
		}
		if e.Ignored {
			p += "!"
		}
		out = append(out, p)
	}
	return out
}

// filesFixture is a working directory holding a repository with an ignored
// directory, a symbolic link and a nested repository, beside a directory
// that is not in git.
func filesFixture(t *testing.T, g Git) (fixture, string) {
	t.Helper()
	f := newFixture(t, g)
	dir := t.TempDir()
	repo := filepath.Join(dir, "app")
	initRepo(t, g, repo, map[string]string{
		".gitignore": "node_modules/\n*.log\n",
		"main.go":    "package main\n",
		"lib/a.go":   "package lib\n",
	})
	write(t, repo, "node_modules/x/index.js", "x")
	write(t, repo, "debug.log", "log")
	write(t, repo, "new.txt", "new")
	if err := os.Symlink("lib", filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}
	initRepo(t, g, filepath.Join(repo, "vendored"), map[string]string{"v.go": "package v\n"})
	write(t, dir, "notes/todo.md", "todo")
	return f, dir
}

func TestListOneLevel(t *testing.T) {
	g := newGit(t)
	f, dir := filesFixture(t, g)
	defer guard(t, g, filepath.Join(dir, "app"))()
	ctx := context.Background()
	for name, tc := range map[string]struct {
		p    proto.FilesListParams
		want []string
	}{
		"root":         {proto.FilesListParams{}, []string{"app/", "notes/"}},
		"repo":         {proto.FilesListParams{Path: "app"}, []string{"app/lib/", "app/vendored/", "app/.gitignore", "app/link@", "app/main.go", "app/new.txt"}},
		"with ignored": {proto.FilesListParams{Path: "app", Ignored: true}, []string{"app/lib/", "app/node_modules/!", "app/vendored/", "app/.gitignore", "app/debug.log!", "app/link@", "app/main.go", "app/new.txt"}},
		"cleaned":      {proto.FilesListParams{Path: "./app/lib/"}, []string{"app/lib/a.go"}},
		"not in git":   {proto.FilesListParams{Path: "notes"}, []string{"notes/todo.md"}},
	} {
		got, err := f.svc.List(ctx, dir, tc.p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !slices.Equal(entryPaths(got), tc.want) {
			t.Errorf("%s: %q, want %q", name, entryPaths(got), tc.want)
		}
	}
	got, err := f.svc.List(ctx, dir, proto.FilesListParams{Path: "app"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got.Entries {
		if e.Path == "app/main.go" && e.Size != int64(len("package main\n")) {
			t.Errorf("size of main.go: %d", e.Size)
		}
	}
}

func TestListAll(t *testing.T) {
	g := newGit(t)
	f, dir := filesFixture(t, g)
	defer guard(t, g, filepath.Join(dir, "app"))()
	ctx := context.Background()
	got, err := f.svc.List(ctx, dir, proto.FilesListParams{All: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"app/.gitignore", "app/lib/a.go", "app/main.go", "app/new.txt", "app/vendored/v.go", "notes/todo.md"}
	if !slices.Equal(entryPaths(got), want) || got.Truncated {
		t.Errorf("all: %q (truncated %v), want %q", entryPaths(got), got.Truncated, want)
	}
	got, err = f.svc.List(ctx, dir, proto.FilesListParams{Path: "app/lib", All: true})
	if err != nil || !slices.Equal(entryPaths(got), []string{"app/lib/a.go"}) {
		t.Errorf("all under a directory of the repository: %q, %v", entryPaths(got), err)
	}
	f.svc.maxList = 2
	got, err = f.svc.List(ctx, dir, proto.FilesListParams{All: true})
	if err != nil || len(got.Entries) != 2 || !got.Truncated {
		t.Errorf("over the limit: %q truncated %v, %v", entryPaths(got), got.Truncated, err)
	}
	if _, err := f.svc.List(ctx, dir, proto.FilesListParams{All: true, Ignored: true}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("every file with the ignored ones: %v, want ErrUnsupported", err)
	}
}

func TestListRefusesPathsOut(t *testing.T) {
	g := newGit(t)
	f, dir := filesFixture(t, g)
	ctx := context.Background()
	for _, p := range []string{"..", "../x", "app/../..", "/etc", "app/main.go"} {
		if _, err := f.svc.List(ctx, dir, proto.FilesListParams{Path: p}); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%q: %v, want ErrInvalidPath", p, err)
		}
	}
	// Through a symbolic link to a directory: as if it were not there.
	if _, err := f.svc.List(ctx, dir, proto.FilesListParams{Path: "app/link"}); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("the link itself: %v, want ErrInvalidPath", err)
	}
	if _, err := f.svc.List(ctx, dir, proto.FilesListParams{Path: "app/link/sub"}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("through the link: %v, want not exist", err)
	}
	if _, err := f.svc.List(ctx, dir, proto.FilesListParams{Path: "gone"}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing directory: %v, want not exist", err)
	}
}

func TestReadNow(t *testing.T) {
	g := newGit(t)
	f, dir := filesFixture(t, g)
	ctx := context.Background()
	write(t, dir, "notes/bin.dat", "a\x00b")
	write(t, dir, "notes/latin1.txt", "caf\xe9")
	write(t, dir, "notes/big.txt", strings.Repeat("x", maxReadBytes+1))
	if err := syscall.Mkfifo(filepath.Join(dir, "notes", "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		path string
		want proto.FileContent
	}{
		"text":        {"app/main.go", proto.FileContent{Path: "app/main.go", Text: "package main\n", Size: 13}},
		"binary":      {"notes/bin.dat", proto.FileContent{Path: "notes/bin.dat", Size: 3, Binary: true}},
		"not utf-8":   {"notes/latin1.txt", proto.FileContent{Path: "notes/latin1.txt", Size: 4, Binary: true}},
		"too large":   {"notes/big.txt", proto.FileContent{Path: "notes/big.txt", Size: maxReadBytes + 1, TooLarge: true}},
		"missing":     {"notes/gone.md", proto.FileContent{Path: "notes/gone.md", Missing: true}},
		"behind link": {"app/link/a.go", proto.FileContent{Path: "app/link/a.go", Missing: true}},
	} {
		got, err := f.svc.Read(ctx, f.id, dir, proto.FilesReadParams{Path: tc.path})
		tc.want.Session = f.id
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v, %v; want %+v", name, got, err, tc.want)
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.svc.Read(ctx, f.id, dir, proto.FilesReadParams{Path: "notes/pipe"})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrInvalidPath) {
			t.Errorf("a FIFO: %v, want ErrInvalidPath", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a FIFO blocked")
	}
	for _, p := range []string{"app/link", "", "..", "/etc/hosts"} {
		if _, err := f.svc.Read(ctx, f.id, dir, proto.FilesReadParams{Path: p}); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%q: %v, want ErrInvalidPath", p, err)
		}
	}
}

func TestReadBaseline(t *testing.T) {
	g := newGit(t)
	f, dir := filesFixture(t, g)
	ctx := context.Background()
	repo := filepath.Join(dir, "app")
	f.baseline(t, dir)
	defer guard(t, g, repo)()
	write(t, repo, "main.go", "package main\n\nfunc main() {}\n")
	write(t, repo, "added.go", "package main\n")
	for name, tc := range map[string]struct {
		path string
		want proto.FileContent
	}{
		"changed":   {"app/main.go", proto.FileContent{Path: "app/main.go", Text: "package main\n", Size: 13}},
		"untracked": {"app/new.txt", proto.FileContent{Path: "app/new.txt", Text: "new", Size: 3}},
		"added":     {"app/added.go", proto.FileContent{Path: "app/added.go", Missing: true}},
		"nested":    {"app/vendored/v.go", proto.FileContent{Path: "app/vendored/v.go", Text: "package v\n", Size: 10}},
	} {
		got, err := f.svc.Read(ctx, f.id, dir, proto.FilesReadParams{Path: tc.path, Baseline: true})
		tc.want.Session = f.id
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v, %v; want %+v", name, got, err, tc.want)
		}
	}
	if _, err := f.svc.Read(ctx, f.id, dir, proto.FilesReadParams{Path: "notes/todo.md", Baseline: true}); !errors.Is(err, ErrNoBaseline) {
		t.Errorf("outside git: %v, want ErrNoBaseline", err)
	}
	if _, err := f.st.CreateSession(ctx, proto.Session{
		ID: "s2", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "fake"}, Directory: "/work",
		Isolation: proto.InPlace, Owner: proto.OwnerMe, Origin: proto.OriginMe,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Read(ctx, "s2", dir, proto.FilesReadParams{Path: "app/main.go", Baseline: true}); !errors.Is(err, ErrNoBaseline) {
		t.Errorf("a session without a baseline: %v, want ErrNoBaseline", err)
	}
}
