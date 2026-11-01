package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestSingleRepoAtTheRoot(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	body := strings.Repeat("a line of text that is long enough\n", 20)
	initRepo(t, g, repo, map[string]string{
		"keep.txt": "keep\n", "edit.txt": "one\ntwo\n", "gone.txt": "bye\n", "move-me.txt": body,
		".gitignore": "*.log\nbuild/\n", "run.sh": "#!/bin/sh\n",
	})
	f := newFixture(t, g)
	check := guard(t, g, repo)
	f.baseline(t, repo)
	check()

	// The session's changes.
	write(t, repo, "edit.txt", "one\ntwo\nthree\n")
	if err := os.Remove(filepath.Join(repo, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repo, "move-me.txt"), filepath.Join(repo, "moved.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "new/dir/file.txt", "fresh\n")
	write(t, repo, "debug.log", "ignored\n")
	write(t, repo, "build/out.o", "ignored\n")
	write(t, repo, "with space.txt", "x\n")
	write(t, repo, "\"quoted\".txt", "q\n")
	write(t, repo, "line\nbreak.txt", "nl\n")
	write(t, repo, "image.bin", "\x00\x01\x02\x03binary")
	write(t, repo, "tool.sh", "#!/bin/sh\necho hi\n")
	if err := os.Chmod(filepath.Join(repo, "tool.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(repo, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("keep.txt", filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}

	check = guard(t, g, repo)
	d := f.diff(t, repo, proto.DiffParams{Patch: true})
	check()
	if d.BaselineAt == nil || d.BaselineError != "" || len(d.Repos) != 1 {
		t.Fatalf("diff %+v", d)
	}
	r := d.Repos[0]
	if r.Path != "." || r.State != proto.RepoOK || r.Branch != "main" || r.Error != "" || r.SHA == "" || r.Truncated {
		t.Fatalf("repo %+v", r)
	}
	expectFiles(t, r.Files,
		"added:\"quoted\".txt", "modified:edit.txt", "deleted:gone.txt", "added:image.bin", "added:line\nbreak.txt",
		"added:link", "renamed:moved.txt<-move-me.txt", "added:new/dir/file.txt", "modified:run.sh", "added:tool.sh",
		"added:with space.txt")
	byPath := map[string]proto.FileDiff{}
	for _, f := range r.Files {
		byPath[f.Path] = f
	}
	if e := byPath["edit.txt"]; e.Additions != 1 || e.Deletions != 0 || e.Binary {
		t.Errorf("edit.txt %+v", e)
	}
	if !byPath["image.bin"].Binary {
		t.Errorf("image.bin %+v", byPath["image.bin"])
	}
	for _, want := range []string{
		"+three", "new file mode 100755", "new mode 100755", "new file mode 120000", "\n+keep.txt",
		"rename from move-me.txt", "deleted file mode 100644", "Binary files /dev/null and b/image.bin differ",
	} {
		if !strings.Contains(r.Patch, want) {
			t.Errorf("patch lacks %q:\n%s", want, r.Patch)
		}
	}
	if strings.Contains(r.Patch, "debug.log") || strings.Contains(r.Patch, "out.o") {
		t.Errorf("ignored files in the patch:\n%s", r.Patch)
	}

	// Without a patch the files are the same and the SHA does not change.
	nop := f.diff(t, repo, proto.DiffParams{}).Repos[0]
	if nop.Patch != "" || nop.SHA != r.SHA || len(nop.Files) != len(r.Files) {
		t.Errorf("files only: %+v", nop)
	}
	// A change shows up in the SHA, and undoing it brings the old one back.
	write(t, repo, "edit.txt", "one\ntwo\nthree\nfour\n")
	if again := f.diff(t, repo, proto.DiffParams{}).Repos[0]; again.SHA == r.SHA {
		t.Error("SHA did not change with the content")
	}
	write(t, repo, "edit.txt", "one\ntwo\nthree\n")
	if again := f.diff(t, repo, proto.DiffParams{}).Repos[0]; again.SHA != r.SHA {
		t.Error("SHA changed although the content did not")
	}
}

func TestNoChangesAndUnbornRepo(t *testing.T) {
	g := newGit(t)
	root := t.TempDir()
	initRepo(t, g, filepath.Join(root, "clean"), map[string]string{"a": "1\n"})
	initRepo(t, g, filepath.Join(root, "unborn"), nil)
	write(t, filepath.Join(root, "unborn"), "pre.txt", "before\n")
	f := newFixture(t, g)
	check := guard(t, g, filepath.Join(root, "clean"), filepath.Join(root, "unborn"))
	f.baseline(t, root)
	d := f.diff(t, root, proto.DiffParams{Patch: true})
	check()
	for _, r := range d.Repos {
		if r.State != proto.RepoOK || len(r.Files) != 0 || r.Patch != "" {
			t.Errorf("unchanged repo %+v", r)
		}
	}
	write(t, filepath.Join(root, "unborn"), "post.txt", "after\n")
	d = f.diff(t, root, proto.DiffParams{})
	u := repoByPath(t, d, "unborn")
	expectFiles(t, u.Files, "added:post.txt")
	if u.Branch != "main" {
		t.Errorf("branch of an unborn repository: %q", u.Branch)
	}
	git(t, g, filepath.Join(root, "clean"), "checkout", "-q", "--detach")
	if b := repoByPath(t, f.diff(t, root, proto.DiffParams{}), "clean").Branch; b != "HEAD" {
		t.Errorf("branch of a detached HEAD: %q", b)
	}
}

func TestSeveralRepos(t *testing.T) {
	g := newGit(t)
	root := t.TempDir()
	for _, n := range []string{"api", "web", "shared"} {
		initRepo(t, g, filepath.Join(root, n), map[string]string{n + ".txt": n + "\n"})
	}
	write(t, root, "notes/todo.txt", "not in a repo\n")
	f := newFixture(t, g)
	repos := []string{"api", "web", "shared"}
	var abs []string
	for _, n := range repos {
		abs = append(abs, filepath.Join(root, n))
	}
	check := guard(t, g, abs...)
	f.baseline(t, root)
	check()

	write(t, root, "api/api.txt", "changed\n")
	write(t, root, "shared/new.go", "package shared\n")
	write(t, root, "notes/todo.txt", "edited, but outside any repo\n")
	// A repo that appears and one that goes away.
	initRepo(t, g, filepath.Join(root, "fresh"), map[string]string{"f.txt": "f\n"})
	if err := os.RemoveAll(filepath.Join(root, "web")); err != nil {
		t.Fatal(err)
	}
	check = guard(t, g, filepath.Join(root, "api"), filepath.Join(root, "shared"), filepath.Join(root, "fresh"))
	d := f.diff(t, root, proto.DiffParams{})
	check()
	var seen []string
	for _, r := range d.Repos {
		seen = append(seen, r.Path+":"+string(r.State))
	}
	if strings.Join(seen, " ") != "api:ok fresh:new shared:ok web:missing" {
		t.Fatalf("repos %v", seen)
	}
	expectFiles(t, repoByPath(t, d, "api").Files, "modified:api.txt")
	expectFiles(t, repoByPath(t, d, "shared").Files, "added:new.go")
	expectFiles(t, repoByPath(t, d, "fresh").Files, "added:f.txt")
	if w := repoByPath(t, d, "web"); len(w.Files) != 0 || w.Error != "" {
		t.Errorf("missing repo %+v", w)
	}

	// Asking for one repository.
	one := f.diff(t, root, proto.DiffParams{Repo: "shared"})
	if len(one.Repos) != 1 || one.Repos[0].Path != "shared" {
		t.Fatalf("one repo: %+v", one.Repos)
	}
	if none := f.diff(t, root, proto.DiffParams{Repo: "nope"}); len(none.Repos) != 0 {
		t.Fatalf("unknown repo: %+v", none.Repos)
	}
	// The working directory itself gone: every repository is missing.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	gone := f.diff(t, root, proto.DiffParams{})
	for _, r := range gone.Repos {
		if r.State != proto.RepoMissing {
			t.Errorf("after removing the directory: %+v", r)
		}
	}
}

func TestPatchIsCutAtALine(t *testing.T) {
	g := newGit(t)
	g.MaxPatch = 300
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "x\n"})
	f := newFixture(t, g)
	f.baseline(t, repo)
	write(t, repo, "big.txt", strings.Repeat("0123456789 0123456789\n", 100))
	check := guard(t, g, repo)
	r := f.diff(t, repo, proto.DiffParams{Patch: true}).Repos[0]
	check()
	if !r.Truncated || len(r.Patch) == 0 || len(r.Patch) > 300 || !strings.HasSuffix(r.Patch, "\n") {
		t.Fatalf("patch of %d bytes, truncated %v:\n%s", len(r.Patch), r.Truncated, r.Patch)
	}
	if len(r.Files) != 1 || r.Files[0].Additions != 100 {
		t.Fatalf("files %+v", r.Files)
	}
	// One line longer than the cap leaves no patch at all.
	g.MaxPatch = 10
	f.svc.Git = g
	write(t, repo, "long.txt", strings.Repeat("x", 1000)+"\n")
	if r := f.diff(t, repo, proto.DiffParams{Patch: true}).Repos[0]; !r.Truncated || r.Patch != "" {
		t.Fatalf("patch of a line longer than the cap: truncated %v, %d bytes", r.Truncated, len(r.Patch))
	}
	if err := os.Remove(filepath.Join(repo, "long.txt")); err != nil {
		t.Fatal(err)
	}
	// A patch that fits is not marked.
	g.MaxPatch = 0
	f.svc.Git = g
	if r := f.diff(t, repo, proto.DiffParams{Patch: true}).Repos[0]; r.Truncated {
		t.Fatal("patch marked truncated")
	}
}

func TestNestedRepoAndSubmodule(t *testing.T) {
	g := newGit(t)
	root := t.TempDir()
	lib := filepath.Join(t.TempDir(), "lib")
	initRepo(t, g, lib, map[string]string{"lib.go": "package lib\n"})
	parent := filepath.Join(root, "app")
	initRepo(t, g, parent, map[string]string{"main.go": "package main\n"})
	git(t, g, parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "deps/lib")
	git(t, g, parent, "commit", "-q", "-m", "add submodule")
	// An embedded repository the parent does not track.
	initRepo(t, g, filepath.Join(parent, "scratch"), map[string]string{"s.txt": "s\n"})

	sub := filepath.Join(parent, "deps", "lib")
	f := newFixture(t, g)
	check := guard(t, g, parent, sub, filepath.Join(parent, "scratch"))
	f.baseline(t, root)
	check()
	b, _, _ := f.st.Baseline(context.Background(), f.id)
	if len(b.Repos) != 3 {
		t.Fatalf("baseline repos %+v", b.Repos)
	}

	// The submodule moves to a new commit; the parent also changes a file.
	write(t, sub, "lib.go", "package lib\n\nfunc F() {}\n")
	git(t, g, sub, "commit", "-q", "-am", "work in the submodule")
	write(t, sub, "extra.go", "package lib\n")
	write(t, parent, "main.go", "package main\n\nfunc main() {}\n")
	write(t, parent, "scratch/s.txt", "s2\n")

	check = guard(t, g, parent, sub, filepath.Join(parent, "scratch"))
	d := f.diff(t, root, proto.DiffParams{Patch: true})
	check()
	app := repoByPath(t, d, "app")
	expectFiles(t, app.Files, "modified:deps/lib", "modified:main.go")
	if !strings.Contains(app.Patch, "Subproject commit") {
		t.Errorf("no submodule change in the patch:\n%s", app.Patch)
	}
	if l := repoByPath(t, d, "app/deps/lib"); l.Parent != "app" {
		t.Errorf("submodule parent %q", l.Parent)
	} else {
		expectFiles(t, l.Files, "added:extra.go", "modified:lib.go")
	}
	if s := repoByPath(t, d, "app/scratch"); s.Parent != "app" {
		t.Errorf("nested repo parent %q", s.Parent)
	} else {
		expectFiles(t, s.Files, "modified:s.txt")
	}
	// Nothing of the nested repositories leaks into the parent's files.
	for _, file := range app.Files {
		if strings.HasPrefix(file.Path, "scratch") {
			t.Errorf("nested repository content in the parent: %+v", file)
		}
	}
}

func TestResponseIsBounded(t *testing.T) {
	g := newGit(t)
	root := t.TempDir()
	for _, n := range []string{"r1", "r2", "r3"} {
		initRepo(t, g, filepath.Join(root, n), map[string]string{"base": "x\n"})
	}
	f := newFixture(t, g)
	f.svc.maxBytes = 6000
	f.svc.maxFiles = 4
	defer guard(t, g, filepath.Join(root, "r1"), filepath.Join(root, "r2"), filepath.Join(root, "r3"))()
	f.baseline(t, root)
	for _, n := range []string{"r1", "r2", "r3"} {
		for i := 0; i < 10; i++ {
			write(t, filepath.Join(root, n), "f"+string(rune('a'+i)), strings.Repeat("line of text\n", 100))
		}
	}
	d := f.diff(t, root, proto.DiffParams{Patch: true})
	for _, r := range d.Repos {
		if len(r.Files) > 4 || !r.FilesTruncated || !r.Truncated {
			t.Errorf("%s: %d files, files truncated %v, truncated %v", r.Path, len(r.Files), r.FilesTruncated, r.Truncated)
		}
	}
	if d.Repos[0].Patch == "" || d.Repos[2].Patch != "" {
		t.Fatalf("budget not spent in order: %d %d %d bytes", len(d.Repos[0].Patch), len(d.Repos[1].Patch), len(d.Repos[2].Patch))
	}
	if b, _ := json.Marshal(d); len(b) > 6000+600 {
		t.Fatalf("response of %d bytes", len(b))
	}
}

// TestResponseBoundCountsEncodedBytes: <, > and & take six bytes each in JSON.
func TestResponseBoundCountsEncodedBytes(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"base": "x\n"})
	f := newFixture(t, g) // the real limits
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	write(t, repo, "jsx.txt", strings.Repeat("<div className=\"a\">&&</div>\n", 200000)) // about 5 MB
	d := f.diff(t, repo, proto.DiffParams{Patch: true})
	b, _ := json.Marshal(d)
	if len(b) > maxResponseBytes+4096 {
		t.Fatalf("response of %d bytes, limit %d", len(b), maxResponseBytes)
	}
	r := d.Repos[0]
	if !r.Truncated || r.Patch == "" || !strings.HasSuffix(r.Patch, "\n") {
		t.Fatalf("truncated %v, %d bytes of patch", r.Truncated, len(r.Patch))
	}
}

func TestSkippedFilesAreCapped(t *testing.T) {
	g := newGit(t)
	g.MaxFile = 1
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"base": "x\n"})
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	for i := 0; i < maxResponseSkipped+5; i++ {
		write(t, repo, "big/"+strconv.Itoa(i), "too big\n")
	}
	f.baseline(t, repo)
	r := f.diff(t, repo, proto.DiffParams{}).Repos[0]
	if len(r.Skipped) != maxResponseSkipped || !r.SkippedTruncated {
		t.Fatalf("%d skipped, truncated %v", len(r.Skipped), r.SkippedTruncated)
	}
}

func TestTotalFilesInAResponseAreCapped(t *testing.T) {
	g := newGit(t)
	root := t.TempDir()
	for _, n := range []string{"r1", "r2", "r3"} {
		initRepo(t, g, filepath.Join(root, n), map[string]string{"base": "x\n"})
	}
	f := newFixture(t, g)
	f.svc.maxFiles = 5
	f.svc.maxTotalFiles = 7
	defer guard(t, g, filepath.Join(root, "r1"), filepath.Join(root, "r2"), filepath.Join(root, "r3"))()
	f.baseline(t, root)
	for _, n := range []string{"r1", "r2", "r3"} {
		for i := 0; i < 6; i++ {
			write(t, filepath.Join(root, n), "f"+string(rune('a'+i)), "x\n")
		}
	}
	d := f.diff(t, root, proto.DiffParams{})
	got := []int{len(d.Repos[0].Files), len(d.Repos[1].Files), len(d.Repos[2].Files)}
	if got[0] != 5 || got[1] != 2 || got[2] != 0 {
		t.Fatalf("files listed per repo: %v", got)
	}
	for _, r := range d.Repos {
		if !r.FilesTruncated {
			t.Errorf("%s not marked files_truncated", r.Path)
		}
	}
}

func TestHardResponseCapLeavesMinimalEntries(t *testing.T) {
	g := newGit(t)
	g.MaxFile = 100
	root := t.TempDir()
	const repos = 5
	var paths []string
	for i := 0; i < repos; i++ {
		r := filepath.Join(root, "r"+strconv.Itoa(i))
		initRepo(t, g, r, map[string]string{"base": "x\n"})
		paths = append(paths, r)
	}
	f := newFixture(t, g)
	f.svc.maxBytes = 20000
	defer guard(t, g, paths...)()
	f.baseline(t, root)
	for _, r := range paths {
		for i := 0; i < maxResponseSkipped+5; i++ {
			write(t, r, "big/"+strconv.Itoa(i), strings.Repeat("too big\n", 20))
		}
		write(t, r, "plain", strings.Repeat("line\n", 10))
	}
	d := f.diff(t, root, proto.DiffParams{Patch: true})
	b, _ := json.Marshal(d)
	if limit := 20000 + 512*repos; len(b) > limit {
		t.Fatalf("response of %d bytes, limit %d", len(b), limit)
	}
	last := d.Repos[repos-1]
	if len(last.Files) != 0 || last.Patch != "" || len(last.Skipped) != 0 || !last.SkippedTruncated || !last.FilesTruncated || last.SHA == "" || last.Branch == "" {
		t.Fatalf("last entry is not minimal: %+v", last)
	}
}
