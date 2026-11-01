package workspace

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestDiscover(t *testing.T) {
	g := newGit(t)
	root := t.TempDir()
	for _, r := range []string{"api", "web", "shared", "api/tools/gen", "a/b/c", "a/b/c/d/e", "node_modules/dep", "vendor/lib", "notes/old/repo"} {
		initRepo(t, g, filepath.Join(root, r), nil)
	}
	write(t, root, "notes/todo.txt", "x")
	other := t.TempDir()
	initRepo(t, g, filepath.Join(other, "linked"), nil)
	if err := os.Symlink(filepath.Join(other, "linked"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "locked"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "locked"), 0o755) })

	defer guard(t, g, filepath.Join(root, "api"), filepath.Join(root, "a", "b", "c"))()
	repos, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	parents := map[string]string{}
	for _, r := range repos {
		got = append(got, r.Path)
		parents[r.Path] = r.Parent
		if r.Abs != filepath.Join(root, filepath.FromSlash(r.Path)) {
			t.Errorf("abs path %s for %s", r.Abs, r.Path)
		}
	}
	want := []string{"a/b/c", "api", "api/tools/gen", "notes/old/repo", "shared", "web"}
	if !slices.Equal(got, want) {
		t.Fatalf("repos %v, want %v", got, want)
	}
	if parents["api/tools/gen"] != "api" || parents["api"] != "" {
		t.Fatalf("parents %v", parents)
	}

	// The directory itself being a repository comes first and parents the rest.
	initRepo(t, g, root, nil)
	repos, err = Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if repos[0].Path != "." || repos[0].Parent != "" || repos[1].Path != "a/b/c" || repos[1].Parent != "." {
		t.Fatalf("repos %+v", repos[:2])
	}
	if _, err := Discover(context.Background(), filepath.Join(root, "missing")); err == nil {
		t.Fatal("discovered a missing directory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, root); err == nil {
		t.Fatal("discovery ignored a cancelled context")
	}
}

func TestEnclosingRepositoryIsFoundFromASubdirectory(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"top.txt": "t\n", "services/api/main.go": "package main\n"})
	dir := filepath.Join(repo, "services", "api")
	initRepo(t, g, filepath.Join(dir, "vendored"), map[string]string{"v.txt": "v\n"})
	found, err := g.Discover(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].Path != "../.." || found[0].Parent != "" || found[1].Path != "vendored" || found[1].Parent != "../.." {
		t.Fatalf("repos %+v", found)
	}
	if real, _ := filepath.EvalSymlinks(repo); found[0].Abs != real {
		t.Fatalf("abs %s, want %s", found[0].Abs, real)
	}
	f := newFixture(t, g)
	defer guard(t, g, repo, filepath.Join(dir, "vendored"))()
	f.baseline(t, dir)
	write(t, repo, "top.txt", "t2\n") // outside the directory, inside the repository
	write(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	d := f.diff(t, dir, proto.DiffParams{})
	expectFiles(t, repoByPath(t, d, "../..").Files, "modified:services/api/main.go", "modified:top.txt")
	if repoByPath(t, d, "vendored").State != proto.RepoOK {
		t.Fatalf("diff %+v", d)
	}
	// At the top of the repository nothing is added.
	if top, err := g.Discover(context.Background(), repo); err != nil || top[0].Path != "." || len(top) != 2 {
		t.Fatalf("at the top: %+v %v", top, err)
	}
}

func TestInvalidDotGitIsNotARepository(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	if err := os.MkdirAll(filepath.Join(repo, "x", ".git"), 0o755); err != nil { // empty: not a git dir
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "z"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "z", ".git"), []byte("gitdir: /nonexistent/elsewhere\n"), 0o644); err != nil { // broken gitfile
		t.Fatal(err)
	}
	found, err := g.Discover(context.Background(), repo)
	if err != nil || len(found) != 1 || found[0].Path != "." {
		t.Fatalf("repos %+v %v", found, err)
	}
	// An embedded directory like that is no commit either.
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	write(t, repo, "a", "2\n")
	expectFiles(t, f.diff(t, repo, proto.DiffParams{}).Repos[0].Files, "modified:a")
}
