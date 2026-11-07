package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

// rangeFixture is a working directory with four repositories, and no
// baseline:
//   - app: on branch feat, two commits ahead of main ("change a", "add c"),
//     with b.txt edited and d.txt untracked;
//   - lib: on main, one commit ahead of origin/trunk, which origin/HEAD
//     names;
//   - solo: on main with two commits and no remote;
//   - odd: on trunk, with no main, master or origin.
func rangeFixture(t *testing.T, g Git) (fixture, string, map[string]string) {
	t.Helper()
	f := newFixture(t, g)
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	initRepo(t, g, app, map[string]string{"a.txt": "1\n", "b.txt": "b\n"})
	git(t, g, app, "checkout", "-q", "-b", "feat")
	write(t, app, "a.txt", "2\n")
	git(t, g, app, "commit", "-q", "-am", "change a")
	ids := map[string]string{"change a": strings.TrimSpace(git(t, g, app, "rev-parse", "HEAD"))}
	write(t, app, "c.txt", "c\n")
	git(t, g, app, "add", "c.txt")
	git(t, g, app, "commit", "-q", "-m", "add c")
	ids["add c"] = strings.TrimSpace(git(t, g, app, "rev-parse", "HEAD"))
	ids["init"] = strings.TrimSpace(git(t, g, app, "rev-parse", "main"))
	write(t, app, "b.txt", "B\n")
	write(t, app, "d.txt", "d\n")

	lib := filepath.Join(dir, "lib")
	initRepo(t, g, lib, map[string]string{"x.txt": "x\n"})
	git(t, g, lib, "update-ref", "refs/remotes/origin/trunk", "HEAD")
	git(t, g, lib, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	write(t, lib, "y.txt", "y\n")
	git(t, g, lib, "add", "y.txt")
	git(t, g, lib, "commit", "-q", "-m", "add y")

	solo := filepath.Join(dir, "solo")
	initRepo(t, g, solo, map[string]string{"s.txt": "s\n"})
	write(t, solo, "s.txt", "S\n")
	git(t, g, solo, "commit", "-q", "-am", "edit s")

	odd := filepath.Join(dir, "odd")
	initRepo(t, g, odd, map[string]string{"o.txt": "o\n"})
	git(t, g, odd, "branch", "-q", "-m", "trunk")
	return f, dir, ids
}

func TestDiffRanges(t *testing.T) {
	g := newGit(t)
	f, dir, ids := rangeFixture(t, g)
	defer guard(t, g, filepath.Join(dir, "app"), filepath.Join(dir, "lib"), filepath.Join(dir, "solo"), filepath.Join(dir, "odd"))()

	d := f.diff(t, dir, proto.DiffParams{Range: proto.DiffRangeUncommitted, Patch: true})
	if d.BaselineAt != nil || d.BaselineError != "" {
		t.Errorf("a range other than the session's reports a baseline: %+v", d)
	}
	app := repoByPath(t, d, "app")
	expectFiles(t, app.Files, "modified:b.txt", "added:d.txt")
	if app.Branch != "feat" || !strings.Contains(app.Patch, "+B") || app.Base != "" {
		t.Errorf("uncommitted app: %+v", app)
	}
	expectFiles(t, repoByPath(t, d, "lib").Files)

	d = f.diff(t, dir, proto.DiffParams{Range: proto.DiffRangeBranch})
	app = repoByPath(t, d, "app")
	expectFiles(t, app.Files, "modified:a.txt", "modified:b.txt", "added:c.txt", "added:d.txt")
	if app.Base != "main" {
		t.Errorf("app base %q, want main", app.Base)
	}
	lib := repoByPath(t, d, "lib")
	expectFiles(t, lib.Files, "added:y.txt")
	if lib.Base != "trunk" {
		t.Errorf("lib base %q, want trunk (origin/HEAD)", lib.Base)
	}
	if odd := repoByPath(t, d, "odd"); odd.State != proto.RepoError || odd.Error != "no default branch" {
		t.Errorf("odd: %+v", odd)
	}

	for name, tc := range map[string]struct {
		commit string
		want   []string
	}{
		"a change":    {ids["change a"], []string{"modified:a.txt"}},
		"an addition": {ids["add c"][:12], []string{"added:c.txt"}},
		"a root":      {ids["init"], []string{"added:a.txt", "added:b.txt"}},
	} {
		d := f.diff(t, dir, proto.DiffParams{Range: proto.DiffRangeCommit, Repo: "app", Commit: tc.commit})
		if len(d.Repos) != 1 {
			t.Fatalf("%s: repos %+v, want only app", name, d.Repos)
		}
		if g := fileSummary(d.Repos[0].Files); !slices.Equal(g, tc.want) {
			t.Errorf("%s: files %v, want %v", name, g, tc.want)
		}
	}
	d = f.diff(t, dir, proto.DiffParams{Range: proto.DiffRangeCommit, Repo: "app", Commit: "deadbeef"})
	if r := d.Repos[0]; r.State != proto.RepoError || !strings.Contains(r.Error, "not in this repository") {
		t.Errorf("unknown commit: %+v", r)
	}
}

func TestDiffRangeSHAs(t *testing.T) {
	g := newGit(t)
	f, dir, _ := rangeFixture(t, g)
	f.baseline(t, dir)
	shas := map[string]proto.DiffRange{}
	for _, rng := range []proto.DiffRange{proto.DiffRangeSession, proto.DiffRangeUncommitted, proto.DiffRangeBranch} {
		r := repoByPath(t, f.diff(t, dir, proto.DiffParams{Range: rng}), "solo")
		if other, seen := shas[r.SHA]; seen {
			t.Errorf("%s and %s share the sha %s", rng, other, r.SHA)
		}
		shas[r.SHA] = rng
	}
	before := repoByPath(t, f.diff(t, dir, proto.DiffParams{Range: proto.DiffRangeUncommitted}), "app").SHA
	if again := repoByPath(t, f.diff(t, dir, proto.DiffParams{Range: proto.DiffRangeUncommitted}), "app").SHA; again != before {
		t.Errorf("sha changed without an edit: %s -> %s", before, again)
	}
	write(t, filepath.Join(dir, "app"), "d.txt", "dd\n")
	if after := repoByPath(t, f.diff(t, dir, proto.DiffParams{Range: proto.DiffRangeUncommitted}), "app").SHA; after == before {
		t.Error("sha kept after an edit")
	}
}

func TestDiffRangeRefusesBadParams(t *testing.T) {
	g := newGit(t)
	f, dir, ids := rangeFixture(t, g)
	ctx := context.Background()
	for name, p := range map[string]proto.DiffParams{
		"commit without a repository": {Range: proto.DiffRangeCommit, Commit: ids["add c"]},
		"a revision expression":       {Range: proto.DiffRangeCommit, Repo: "app", Commit: "HEAD~1"},
		"an option":                   {Range: proto.DiffRangeCommit, Repo: "app", Commit: "--output=x"},
		"an unknown range":            {Range: "turn"},
	} {
		if _, err := f.svc.Diff(ctx, f.id, dir, p); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("%s: %v, want ErrInvalidPath", name, err)
		}
	}
}

func TestReadRangeSides(t *testing.T) {
	g := newGit(t)
	f, dir, ids := rangeFixture(t, g)
	ctx := context.Background()
	defer guard(t, g, filepath.Join(dir, "app"))()
	for name, tc := range map[string]struct {
		p    proto.FilesReadParams
		want proto.FileContent
	}{
		"uncommitted old":       {proto.FilesReadParams{Path: "app/b.txt", Range: proto.DiffRangeUncommitted, Baseline: true}, proto.FileContent{Path: "app/b.txt", Text: "b\n", Size: 2}},
		"uncommitted new":       {proto.FilesReadParams{Path: "app/b.txt", Range: proto.DiffRangeUncommitted}, proto.FileContent{Path: "app/b.txt", Text: "B\n", Size: 2}},
		"uncommitted untracked": {proto.FilesReadParams{Path: "app/d.txt", Range: proto.DiffRangeUncommitted, Baseline: true}, proto.FileContent{Path: "app/d.txt", Missing: true}},
		"branch old":            {proto.FilesReadParams{Path: "app/a.txt", Range: proto.DiffRangeBranch, Baseline: true}, proto.FileContent{Path: "app/a.txt", Text: "1\n", Size: 2}},
		"commit old":            {proto.FilesReadParams{Path: "app/a.txt", Range: proto.DiffRangeCommit, Commit: ids["change a"], Baseline: true}, proto.FileContent{Path: "app/a.txt", Text: "1\n", Size: 2}},
		"commit new":            {proto.FilesReadParams{Path: "app/a.txt", Range: proto.DiffRangeCommit, Commit: ids["change a"]}, proto.FileContent{Path: "app/a.txt", Text: "2\n", Size: 2}},
		"commit new, not yet":   {proto.FilesReadParams{Path: "app/c.txt", Range: proto.DiffRangeCommit, Commit: ids["change a"]}, proto.FileContent{Path: "app/c.txt", Missing: true}},
		"root commit old":       {proto.FilesReadParams{Path: "app/a.txt", Range: proto.DiffRangeCommit, Commit: ids["init"], Baseline: true}, proto.FileContent{Path: "app/a.txt", Missing: true}},
	} {
		got, err := f.svc.Read(ctx, f.id, dir, tc.p)
		tc.want.Session = f.id
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v, %v; want %+v", name, got, err, tc.want)
		}
	}
	if _, err := f.svc.Read(ctx, f.id, dir, proto.FilesReadParams{Path: "app/a.txt", Range: proto.DiffRangeCommit, Commit: "HEAD"}); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("a revision for a commit: %v, want ErrInvalidPath", err)
	}
}

func TestCommits(t *testing.T) {
	g := newGit(t)
	f, dir, ids := rangeFixture(t, g)
	defer guard(t, g, filepath.Join(dir, "app"), filepath.Join(dir, "lib"), filepath.Join(dir, "solo"), filepath.Join(dir, "odd"))()
	got, err := f.svc.Commits(context.Background(), f.id, dir, proto.CommitsParams{})
	if err != nil {
		t.Fatal(err)
	}
	subjects := map[string]string{}
	for _, r := range got.Repos {
		var s []string
		for _, c := range r.Commits {
			s = append(s, c.Subject)
		}
		subjects[r.Path] = r.Base + ": " + strings.Join(s, ", ")
	}
	want := map[string]string{
		"app":  "main: add c, change a", // ahead of main
		"lib":  "trunk: add y",          // ahead of origin/HEAD
		"solo": "main: edit s, init",    // on the default branch: the latest
		"odd":  ": init",                // no default branch: the latest
	}
	for path, w := range want {
		if subjects[path] != w {
			t.Errorf("%s: %q, want %q", path, subjects[path], w)
		}
	}
	if c := got.Repos[0].Commits[0]; c.SHA != ids["add c"] {
		t.Errorf("app's newest commit %s, want %s", c.SHA, ids["add c"])
	}
	one, err := f.svc.Commits(context.Background(), f.id, dir, proto.CommitsParams{Repo: "lib"})
	if err != nil || len(one.Repos) != 1 || one.Repos[0].Path != "lib" {
		t.Errorf("one repository: %+v, %v", one, err)
	}
}

func TestCommitsAreCapped(t *testing.T) {
	g := newGit(t)
	f := newFixture(t, g)
	dir := t.TempDir()
	initRepo(t, g, dir, map[string]string{"n": "0"})
	for i := range maxRecentCommits + 1 {
		git(t, g, dir, "commit", "-q", "--allow-empty", "-m", "c"+strings.Repeat("x", i%3))
	}
	got, err := f.svc.Commits(context.Background(), f.id, dir, proto.CommitsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if r := got.Repos[0]; len(r.Commits) != maxRecentCommits || !r.Truncated {
		t.Errorf("%d commits, truncated %v; want %d, true", len(r.Commits), r.Truncated, maxRecentCommits)
	}
}
