package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeOrigin(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git@github.com:acme/app.git", "github.com/acme/app"},
		{"https://github.com/acme/app.git", "github.com/acme/app"},
		{"https://user:secret@GitHub.com/acme/app/", "github.com/acme/app"},
		{"ssh://git@github.com:22/acme/app.git", "github.com/acme/app"},
		{"github.com:acme/app", "github.com/acme/app"},
		{"/srv/git/app.git", ""},
		{"../app.git", ""},
		{"file:///srv/git/app.git", ""},
		{"", ""},
	}
	for _, c := range cases {
		got, ok := normalizeOrigin(c.in)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%q: %q %v", c.in, got, ok)
		}
	}
}

func TestRepoIdentity(t *testing.T) {
	git, err := FindGit(os.Environ())
	if err != nil {
		t.Skip(err)
	}
	ctx := context.Background()
	if _, ok := git.RepoIdentity(ctx, t.TempDir(), "mac"); ok {
		t.Error("a plain directory has an identity")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := git.output(ctx, runOpts{Dir: dir}, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	if id, ok := git.RepoIdentity(ctx, dir, "mac"); !ok || id != "mac:"+dir {
		t.Errorf("no origin: %q %v", id, ok)
	}
	if _, err := git.output(ctx, runOpts{Dir: dir}, "remote", "add", "origin", "git@github.com:acme/app.git"); err != nil {
		t.Fatal(err)
	}
	if id, ok := git.RepoIdentity(ctx, dir, "mac"); !ok || id != "github.com/acme/app" {
		t.Errorf("origin: %q %v", id, ok)
	}
}
