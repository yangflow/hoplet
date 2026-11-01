package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func TestHooksNeverRun(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	marker := filepath.Join(repo, ".git", "hook-ran")
	hook := "#!/bin/sh\ntouch '" + marker + "'\n"
	for _, name := range []string{"post-index-change", "post-checkout", "reference-transaction"} {
		if err := os.WriteFile(filepath.Join(repo, ".git", "hooks", name), []byte(hook), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	write(t, repo, "a", "2\n")
	write(t, repo, "b", "new\n")
	f.diff(t, repo, proto.DiffParams{Patch: true})
	if exists(marker) {
		t.Fatal("a hook ran")
	}
}

// TestFiltersNeverRunOnRacyEntries checkouts files and snapshots right away:
// their times are not older than the index, so git compares their content
// whenever it writes the index, which would run a clean filter.
func TestFiltersNeverRunOnRacyEntries(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{".gitattributes": "*.dat filter=x\n", "a.dat": "one\n", "b.txt": "b\n"})
	git(t, g, repo, "config", "filter.x.clean", "sh -c 'touch .git/filter-ran; cat'")
	git(t, g, repo, "config", "filter.x.smudge", "sh -c 'touch .git/filter-ran; cat'")
	git(t, g, repo, "config", "filter.x.process", "sh -c 'touch .git/filter-ran; cat'")
	future := time.Now().Add(time.Hour)
	for _, n := range []string{"a.dat", "b.txt"} {
		if err := os.Chtimes(filepath.Join(repo, n), future, future); err != nil {
			t.Fatal(err)
		}
	}
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	write(t, repo, "b.txt", "b2\n")
	write(t, repo, "c.dat", "new\n")
	if err := os.Chtimes(filepath.Join(repo, "c.dat"), future, future); err != nil {
		t.Fatal(err)
	}
	f.diff(t, repo, proto.DiffParams{Patch: true})
	if exists(filepath.Join(repo, ".git", "filter-ran")) {
		t.Fatal("a filter ran")
	}
}

func TestFiltersNeverRun(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{".gitattributes": "*.dat filter=x\n", "data.dat": "one\n", "plain.txt": "p\n"})
	// The filter exists only now, so committing did not run it.
	git(t, g, repo, "config", "filter.x.clean", "sh -c 'touch .git/filter-ran; cat'")
	git(t, g, repo, "config", "filter.x.smudge", "sh -c 'touch .git/filter-ran; cat'")
	git(t, g, repo, "config", "filter.x.required", "true")
	// Make git doubt the file is clean, so that it would compare content.
	old := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(repo, "data.dat"), old, old); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, g)
	check := guard(t, g, repo)
	f.baseline(t, repo)
	check()

	write(t, repo, "data.dat", "two\n")
	write(t, repo, "new.dat", "fresh\n")
	write(t, repo, "plain.txt", "p2\n")
	check = guard(t, g, repo)
	d := f.diff(t, repo, proto.DiffParams{Patch: true})
	check()
	if _, err := os.Stat(filepath.Join(repo, ".git", "filter-ran")); err == nil {
		t.Fatal("a clean filter ran")
	}
	r := d.Repos[0]
	expectFiles(t, r.Files, "modified:data.dat", "added:new.dat", "modified:plain.txt")
	for _, file := range r.Files {
		want := ""
		if strings.HasSuffix(file.Path, ".dat") {
			want = "x"
		}
		if file.Filter != want {
			t.Errorf("%s: filter %q, want %q", file.Path, file.Filter, want)
		}
	}
	if strings.Contains(r.Patch, "dat") || !strings.Contains(r.Patch, "+p2") {
		t.Errorf("patch:\n%s", r.Patch)
	}
	if r.Files[0].Additions != 1 {
		t.Errorf("line counts of a filtered file: %+v", r.Files[0])
	}
}

func TestSplitIndexIsUntouched(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n", "b": "2\n"})
	git(t, g, repo, "config", "core.splitIndex", "true")
	git(t, g, repo, "update-index", "--split-index")
	write(t, repo, "a", "changed\n")
	git(t, g, repo, "update-index", "--add", "a")
	if m, _ := filepath.Glob(filepath.Join(repo, ".git", "sharedindex.*")); len(m) == 0 {
		t.Skip("git did not make a split index")
	}
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	write(t, repo, "b", "changed\n")
	d := f.diff(t, repo, proto.DiffParams{Patch: true})
	expectFiles(t, d.Repos[0].Files, "modified:b")
}

// TestUserIndexIsNeverRead: Hoplet works from the worktree and HEAD only.
func TestUserIndexIsNeverRead(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	if err := os.WriteFile(filepath.Join(repo, ".git", "index"), []byte("not an index"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	write(t, repo, "a", "2\n")
	expectFiles(t, f.diff(t, repo, proto.DiffParams{}).Repos[0].Files, "modified:a")
}

// TestUserGcDoesNotBreakDiffs: everything a Hoplet tree names is in
// Hoplet's own objects, including content only the user's index held.
func TestUserGcDoesNotBreakDiffs(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	write(t, repo, "staged.txt", "only in the index\n")
	git(t, g, repo, "add", "staged.txt")
	f := newFixture(t, g)
	check := guard(t, g, repo)
	f.baseline(t, repo)
	check()
	git(t, g, repo, "reset", "-q")
	if err := os.Remove(filepath.Join(repo, "staged.txt")); err != nil {
		t.Fatal(err)
	}
	git(t, g, repo, "reflog", "expire", "--expire=now", "--all")
	git(t, g, repo, "gc", "-q", "--prune=now")
	d := f.diff(t, repo, proto.DiffParams{Patch: true})
	r := d.Repos[0]
	expectFiles(t, r.Files, "deleted:staged.txt")
	if r.State != proto.RepoOK || !strings.Contains(r.Patch, "-only in the index") {
		t.Fatalf("diff after gc: %+v", r)
	}
}
