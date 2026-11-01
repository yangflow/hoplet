package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestRefusedEnclosingRepositoryIsAnErrorRow(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"sub/a": "1\n"})
	// A git that refuses to say where the worktree is, as it does for a
	// repository owned by someone else.
	script := filepath.Join(t.TempDir(), "git")
	body := "#!/bin/sh\ncase \"$*\" in *--show-toplevel*) echo 'fatal: detected dubious ownership in repository' >&2; exit 128;; esac\nexec '" + g.Bin + "' \"$@\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	refusing := g
	refusing.Bin = script
	dir := filepath.Join(repo, "sub")
	found, err := refusing.Discover(context.Background(), dir)
	if err != nil || len(found) != 1 || found[0].Path != ".." || !strings.Contains(found[0].Err, "dubious ownership") {
		t.Fatalf("repos %+v %v", found, err)
	}
	f := newFixture(t, g)
	f.svc.Git = refusing
	defer guard(t, g, repo)()
	f.baseline(t, dir)
	d := f.diff(t, dir, proto.DiffParams{})
	if r := d.Repos[0]; r.State != proto.RepoError || !strings.Contains(r.Error, "dubious ownership") {
		t.Fatalf("repo %+v", r)
	}
}

func TestRefusedNestedRepositoryIsAnErrorRow(t *testing.T) {
	g := newGit(t)
	root := t.TempDir()
	initRepo(t, g, filepath.Join(root, "sub"), map[string]string{"a": "1\n"})
	if err := os.MkdirAll(filepath.Join(root, "invalid", ".git"), 0o755); err != nil { // skipped silently
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "git")
	body := "#!/bin/sh\ncase \"$*\" in *--absolute-git-dir*) echo 'fatal: detected dubious ownership in repository' >&2; exit 128;; esac\nexec '" + g.Bin + "' \"$@\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	refusing := g
	refusing.Bin = script
	found, err := refusing.Discover(context.Background(), root)
	if err != nil || len(found) != 2 || found[0].Err == "" || found[1].Err == "" {
		t.Fatalf("repos %+v %v", found, err)
	}
	f := newFixture(t, g)
	f.svc.Git = refusing
	defer guard(t, g, filepath.Join(root, "sub"))()
	f.baseline(t, root)
	d := f.diff(t, root, proto.DiffParams{})
	sub := repoByPath(t, d, "sub")
	if sub.State != proto.RepoError || !strings.Contains(sub.Error, "dubious ownership") {
		t.Fatalf("repo %+v", sub)
	}
	// With the real git, the invalid directory is no repository and sub is fine.
	real, err := g.Discover(context.Background(), root)
	if err != nil || len(real) != 1 || real[0].Path != "sub" || real[0].Err != "" {
		t.Fatalf("repos %+v %v", real, err)
	}
}

// layoutCase builds an unsupported layout under root and returns the git
// dirs to guard and the path the repository is found at.
type layoutCase struct {
	name string
	// build makes the layout and returns the working directory, the path
	// Discover reports, and the repositories to guard.
	build func(t *testing.T, g Git) (dir, path string, guarded []string)
	why   string
}

func TestUnsupportedLayoutsAreErrorRows(t *testing.T) {
	g := newGit(t)
	for _, tc := range []layoutCase{
		{"symlinked .git", func(t *testing.T, g Git) (string, string, []string) {
			store, root := t.TempDir(), t.TempDir()
			initRepo(t, g, store, map[string]string{"a": "1\n"})
			if err := os.MkdirAll(filepath.Join(root, "x"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(store, ".git"), filepath.Join(root, "x", ".git")); err != nil {
				t.Fatal(err)
			}
			return root, "x", []string{store}
		}, ".git is a symlink"},
		{"core.worktree", func(t *testing.T, g Git) (string, string, []string) {
			root, wt := t.TempDir(), t.TempDir()
			gitdir := filepath.Join(root, "r")
			if err := os.MkdirAll(gitdir, 0o755); err != nil {
				t.Fatal(err)
			}
			git(t, g, gitdir, "init", "-q", "-b", "main")
			git(t, g, gitdir, "config", "core.worktree", wt)
			write(t, wt, "o", "in the worktree\n")
			return root, "r", []string{gitdir}
		}, "core.worktree"},
		{"partial clone", func(t *testing.T, g Git) (string, string, []string) {
			repo := t.TempDir()
			initRepo(t, g, repo, map[string]string{"a": "1\n"})
			git(t, g, repo, "config", "extensions.partialClone", "origin")
			return repo, ".", []string{repo}
		}, "partial clone"},
		{"promisor remote", func(t *testing.T, g Git) (string, string, []string) {
			repo := t.TempDir()
			initRepo(t, g, repo, map[string]string{"a": "1\n"})
			git(t, g, repo, "config", "remote.origin.url", "https://example.invalid/x.git")
			git(t, g, repo, "config", "remote.origin.promisor", "true")
			return repo, ".", []string{repo}
		}, "partial clone"},
		{"home as the session directory", func(t *testing.T, g Git) (string, string, []string) {
			home := t.TempDir()
			initRepo(t, g, home, map[string]string{"private/secret": "TOP SECRET\n"})
			return home, ".", []string{home}
		}, "home directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, path, guarded := tc.build(t, g)
			h := g
			if tc.name == "home as the session directory" {
				h.Env = append(append([]string(nil), g.Env...), "HOME="+dir)
			}
			defer guard(t, g, guarded...)()
			f := newFixture(t, h)
			assertUnsupported(t, h, f, dir, path, tc.why)
		})
	}
	t.Run("home found from above; as an enclosing repository it is ignored", func(t *testing.T) {
		outer := t.TempDir()
		home := filepath.Join(outer, "home")
		initRepo(t, g, home, map[string]string{"private/secret": "TOP SECRET\n", "proj/readme": "r\n"})
		h := g
		h.Env = append(append([]string(nil), g.Env...), "HOME="+home)
		defer guard(t, g, home)()
		assertUnsupported(t, h, newFixture(t, h), outer, "home", "home directory")
		if found, err := h.Discover(context.Background(), filepath.Join(home, "proj")); err != nil || len(found) != 0 {
			t.Fatalf("a session inside the home repository: %+v %v", found, err)
		}
	})
}

// assertUnsupported checks that Discover finds exactly the repository at path
// with an unsupported-layout error, that a baseline and a diff report it as
// an error row, and that nothing of it is stored.
func assertUnsupported(t *testing.T, g Git, f fixture, dir, path, why string) {
	t.Helper()
	found, err := g.Discover(context.Background(), dir)
	if err != nil || len(found) != 1 || found[0].Path != path || !strings.HasPrefix(found[0].Err, "unsupported repository layout: ") || !strings.Contains(found[0].Err, why) {
		t.Fatalf("repos %+v %v, want %s: %s", found, err, path, why)
	}
	f.baseline(t, dir)
	r := repoByPath(t, f.diff(t, dir, proto.DiffParams{Patch: true}), path)
	if r.State != proto.RepoError || !strings.Contains(r.Error, why) || r.Patch != "" || len(r.Files) != 0 {
		t.Fatalf("repo %+v", r)
	}
	if objs, _ := filepath.Glob(filepath.Join(f.home, "objects", "*")); len(objs) != 0 {
		t.Fatalf("something was stored: %v", objs)
	}
}
