package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func TestFindGit(t *testing.T) {
	g := newGit(t)
	v, err := g.Version(context.Background())
	if err != nil || !versionAtLeast(v, 38) {
		t.Fatalf("version %q %v", v, err)
	}
	if _, err := FindGit([]string{"PATH=" + t.TempDir()}); !errors.Is(err, ErrNoGit) {
		t.Fatalf("git on an empty PATH: %v", err)
	}
	if _, err := FindGit(nil); !errors.Is(err, ErrNoGit) {
		t.Fatalf("git without PATH: %v", err)
	}
	// An old git is refused.
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'git version 2.30.1'\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := FindGit([]string{"PATH=" + dir}); !errors.Is(err, ErrNoGit) || !strings.Contains(err.Error(), "2.30.1") {
		t.Fatalf("old git: %v", err)
	}
	if _, err := (Git{}).Version(context.Background()); !errors.Is(err, ErrNoGit) {
		t.Fatalf("zero Git: %v", err)
	}
}

func TestVersionAtLeast(t *testing.T) {
	for v, want := range map[string]bool{
		"2.38.0": true, "2.54.0": true, "2.39.5.windows.1": true, "3.0.0": true, "10.1": true,
		"2.37.9": false, "1.99.0": false, "2": false, "x.y.z": false, "": false,
	} {
		if got := versionAtLeast(v, 38); got != want {
			t.Errorf("versionAtLeast(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestEnvironmentVariablesOfGitAreIgnored(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	defer guard(t, g, repo)()
	g.Env = append(g.Env, "GIT_DIR=/nonexistent", "GIT_INDEX_FILE=/nonexistent/index", "GIT_WORK_TREE=/nonexistent", "GIT_OBJECT_DIRECTORY=/nonexistent")
	f := newFixture(t, g)
	f.baseline(t, repo)
	write(t, repo, "a", "2\n")
	d := f.diff(t, repo, proto.DiffParams{})
	expectFiles(t, repoByPath(t, d, ".").Files, "modified:a")
}

func TestEmptyTree(t *testing.T) {
	g := newGit(t)
	for format, want := range map[string]string{
		"sha1":   "4b825dc642cb6eb9a060e54bf8d69288fbee4904",
		"sha256": "6ef19b41225c5369f1c104d45d8d85efa9b057b53b14b4b9b939dd74decc5321",
	} {
		repo := t.TempDir()
		if out, err := tryInit(g, repo, format); err != nil {
			t.Skipf("git cannot make a %s repository: %v %s", format, err, out)
		}
		r := Repo{Path: ".", Abs: repo}
		check := guard(t, g, repo)
		objects, err := ObjectDir(context.Background(), t.TempDir(), r, g)
		if err != nil {
			t.Fatal(err)
		}
		got, err := g.EmptyTree(context.Background(), r, objects)
		check()
		if err != nil || got != want {
			t.Errorf("%s empty tree %q %v, want %s", format, got, err, want)
		}
	}
}

func TestSnapshotProgressSurvivesCancellation(t *testing.T) {
	g := newGit(t)
	files := map[string]string{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		files[n] = n + "\n"
	}
	repo := t.TempDir()
	initRepo(t, g, repo, files)
	r := Repo{Path: ".", Abs: repo}
	objects, err := ObjectDir(context.Background(), t.TempDir(), r, g)
	if err != nil {
		t.Fatal(err)
	}
	defer guard(t, g, repo)()

	g.filesPerChunk = 2
	ctx, cancel := context.WithCancel(context.Background())
	var first []string
	g.hashed = func(p []string) { first = append(first, p...) }
	g.afterChunk = func(n int) {
		if n == 1 {
			cancel()
		}
	}
	if _, err := g.Snapshot(ctx, r, objects); err == nil {
		t.Fatal("a cancelled snapshot succeeded")
	}
	if len(first) != 2 {
		t.Fatalf("first attempt hashed %v", first)
	}
	var second []string
	g.hashed = func(p []string) { second = append(second, p...) }
	g.afterChunk = nil
	snap, err := g.Snapshot(context.Background(), r, objects)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 5 {
		t.Fatalf("second attempt hashed %v, want the 5 files the first did not reach", second)
	}
	for _, p := range second {
		if p == first[0] || p == first[1] {
			t.Fatalf("%s was hashed twice", p)
		}
	}
	// Chunking does not change the tree.
	g.filesPerChunk = 0
	whole, err := g.Snapshot(context.Background(), r, t.TempDir())
	if err == nil && whole.Tree != "" && whole.Tree != snap.Tree {
		t.Fatalf("trees differ: %s %s", whole.Tree, snap.Tree)
	}
}

func TestRepositoryTooLargeToSnapshot(t *testing.T) {
	g := newGit(t)
	g.snapshotLimit = 50
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"small": "s\n"})
	for i := 0; i < 10; i++ {
		write(t, repo, "data"+string(rune('a'+i)), strings.Repeat("x", 20))
	}
	r := Repo{Path: ".", Abs: repo}
	objects, err := ObjectDir(context.Background(), t.TempDir(), r, g)
	if err != nil {
		t.Fatal(err)
	}
	defer guard(t, g, repo)()
	if _, err := g.Snapshot(context.Background(), r, objects); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("snapshot: %v", err)
	}
	f := newFixture(t, g)
	f.baseline(t, repo)
	d := f.diff(t, repo, proto.DiffParams{})
	if rd := d.Repos[0]; rd.State != proto.RepoError || !strings.Contains(rd.Error, "too large to snapshot") {
		t.Fatalf("repo %+v", rd)
	}
}

func TestFileGrownPastTheLimitIsSkippedNotDeleted(t *testing.T) {
	g := newGit(t)
	g.MaxFile = 5
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	write(t, repo, "a", "grown past the limit\n")
	defer guard(t, g, repo)()
	r := Repo{Path: ".", Abs: repo}
	objects, err := ObjectDir(context.Background(), t.TempDir(), r, g)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := g.repoInfo(context.Background(), r)
	importDir, err := g.importRepo(context.Background(), objects, info)
	if err != nil {
		t.Fatal(err)
	}
	// classify saw it small; by the time it is read it is not.
	ch, skipped, err := g.hashItems(context.Background(), runOpts{Dir: repo}, objects, importDir, t.TempDir(),
		map[string]indexEntry{"a": {mode: modeFile}}, []item{{path: "a", size: 2, mode: modeFile}})
	if err != nil || len(ch) != 0 || len(skipped) != 1 || skipped[0].Reason != ReasonTooLarge || skipped[0].Size != 0 {
		t.Fatalf("changes %+v, skipped %+v, error %v", ch, skipped, err)
	}
}

func TestAFifoNeverBlocksTheSnapshot(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"ok": "fine\n"})
	if err := syscall.Mkfifo(filepath.Join(repo, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Repo{Path: ".", Abs: repo}
	objects, err := ObjectDir(context.Background(), t.TempDir(), r, g)
	if err != nil {
		t.Fatal(err)
	}
	defer guard(t, g, repo)()
	info, err := g.repoInfo(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	importDir, err := g.importRepo(context.Background(), objects, info)
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	type result struct {
		changes []change
		err     error
	}
	done := make(chan result, 1)
	go func() {
		// The classification saw regular files; one became a FIFO since.
		items := []item{{path: "pipe", size: 1, mode: modeFile}, {path: "ok", size: 5, mode: modeFile}}
		o := runOpts{Dir: repo}
		ch, _, err := g.hashItems(context.Background(), o, objects, importDir, tmp, map[string]indexEntry{"pipe": {mode: modeFile}}, items)
		done <- result{ch, err}
	}()
	select {
	case res := <-done:
		if res.err != nil || len(res.changes) != 2 {
			t.Fatalf("changes %+v, error %v", res.changes, res.err)
		}
		var stored, removed int
		for _, c := range res.changes {
			if c.path == "ok" && c.id != "" {
				stored++
			}
			if c.path == "pipe" && c.mode == "" {
				removed++
			}
		}
		if stored != 1 || removed != 1 {
			t.Fatalf("changes %+v", res.changes)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("hashing a FIFO blocked")
	}
}

func TestObjectDirStartsAfreshWhenTheFormatChanges(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	home := t.TempDir()
	r := Repo{Path: ".", Abs: repo}
	objects, err := ObjectDir(context.Background(), home, r, g)
	if err != nil {
		t.Fatal(err)
	}
	check := guard(t, g, repo)
	first, err := g.Snapshot(context.Background(), r, objects)
	check()
	if err != nil || len(first.Tree) != 40 {
		t.Fatalf("sha1 snapshot %+v %v", first, err)
	}
	// The repository is deleted and created again with the other format.
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tryInit(g, repo, "sha256"); err != nil {
		t.Skipf("git cannot make a sha256 repository: %v %s", err, out)
	}
	write(t, repo, "a", "1\n")
	again, err := ObjectDir(context.Background(), home, r, g)
	if err != nil || again != objects {
		t.Fatalf("object dir %s %v", again, err)
	}
	defer guard(t, g, repo)()
	second, err := g.Snapshot(context.Background(), r, objects)
	if err != nil || len(second.Tree) != 64 {
		t.Fatalf("sha256 snapshot %+v %v", second, err)
	}
}

func TestObjectDirIsSharedPerRepository(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	other := t.TempDir()
	initRepo(t, g, other, map[string]string{"a": "1\n"})
	home := t.TempDir()
	ctx := context.Background()
	r := Repo{Path: ".", Abs: repo}
	a, err := ObjectDir(ctx, home, r, g)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ObjectDir(ctx, home, Repo{Path: "elsewhere", Abs: repo}, g)
	c, _ := ObjectDir(ctx, home, Repo{Path: ".", Abs: other}, g)
	if a != b || a == c || filepath.Dir(a) != filepath.Join(home, "objects") || len(filepath.Base(a)) != 16 {
		t.Fatalf("object dirs %s %s %s", a, b, c)
	}
	if fi, err := os.Stat(a); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("object dir: %v %v", fi, err)
	}
	// A linked worktree shares its repository's object directory.
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, g, repo, "worktree", "add", "-q", "-b", "other", wt)
	d, err := ObjectDir(ctx, home, Repo{Path: "wt", Abs: wt}, g)
	if err != nil || d != a {
		t.Fatalf("worktree object dir %s %v, want %s", d, err, a)
	}
	// The worktree's own index and git dir are left alone by snapshots.
	defer guard(t, g, wt)()
	write(t, wt, "w.txt", "w\n")
	if _, err := g.Snapshot(ctx, Repo{Path: "wt", Abs: wt}, d); err != nil {
		t.Fatal(err)
	}
}

func TestSymlinkBlobIsTheLinkText(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	if err := os.Symlink("../outside/target", filepath.Join(repo, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a", filepath.Join(repo, "ok")); err != nil {
		t.Fatal(err)
	}
	r := Repo{Path: ".", Abs: repo}
	objects, err := ObjectDir(context.Background(), t.TempDir(), r, g)
	if err != nil {
		t.Fatal(err)
	}
	defer guard(t, g, repo)()
	snap, err := g.Snapshot(context.Background(), r, objects)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := g.EmptyTree(context.Background(), r, objects)
	if err != nil {
		t.Fatal(err)
	}
	_, patch, _, err := g.Diff(context.Background(), r, objects, empty, snap.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "+../outside/target") || !strings.Contains(patch, "new file mode 120000") {
		t.Fatalf("patch:\n%s", patch)
	}
}

// TestRacilyCleanEditIsSeen covers a file edited without a change in its
// size or modification time, while that time is not older than Hoplet's
// index: git must compare content, not trust the stat data.
func TestRacilyCleanEditIsSeen(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	git(t, g, repo, "config", "core.trustctime", "false")
	future := time.Now().Add(time.Hour)
	a := filepath.Join(repo, "a")
	if err := os.Chtimes(a, future, future); err != nil {
		t.Fatal(err)
	}
	r := Repo{Path: ".", Abs: repo}
	objects, err := ObjectDir(context.Background(), t.TempDir(), r, g)
	if err != nil {
		t.Fatal(err)
	}
	defer assertGitUntouched(t, g, repo)()
	before, err := g.Snapshot(context.Background(), r, objects)
	if err != nil {
		t.Fatal(err)
	}
	write(t, repo, "a", "2\n")
	if err := os.Chtimes(a, future, future); err != nil {
		t.Fatal(err)
	}
	after, err := g.Snapshot(context.Background(), r, objects)
	if err != nil {
		t.Fatal(err)
	}
	if before.Tree == after.Tree {
		t.Fatal("an edit that kept the file's size and time was missed")
	}
}

func TestSymlinkedDirectoryIsNeverReadThrough(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "f", "SECRET OUTSIDE\n")
	initRepo(t, g, repo, map[string]string{"d/f": "inside\n", "keep": "k\n"})
	f := newFixture(t, g)
	f.baseline(t, repo)
	if err := os.RemoveAll(filepath.Join(repo, "d")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "d")); err != nil {
		t.Fatal(err)
	}
	defer guard(t, g, repo)()
	r := f.diff(t, repo, proto.DiffParams{Patch: true}).Repos[0]
	expectFiles(t, r.Files, "added:d", "deleted:d/f")
	if strings.Contains(r.Patch, "SECRET") {
		t.Fatalf("content from outside the repository:\n%s", r.Patch)
	}
	// Edits behind the link are not the session's.
	write(t, outside, "f", "changed\n")
	again := f.diff(t, repo, proto.DiffParams{Patch: true}).Repos[0]
	if again.SHA != r.SHA {
		t.Fatal("a change outside the repository changed the diff")
	}
	// The same from the very first snapshot.
	f2 := newFixture(t, g)
	f2.baseline(t, repo)
	if r := f2.diff(t, repo, proto.DiffParams{Patch: true}).Repos[0]; len(r.Files) != 0 || strings.Contains(r.Patch, "changed") {
		t.Fatalf("link in place before the baseline: %+v", r)
	}
}

func TestSHA256Repo(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	if out, err := tryInit(g, repo, "sha256"); err != nil {
		t.Skipf("git cannot make a sha256 repository: %v %s", err, out)
	}
	write(t, repo, "a.txt", "a\n")
	git(t, g, repo, "add", "-A")
	git(t, g, repo, "commit", "-q", "-m", "init")
	f := newFixture(t, g)
	check := guard(t, g, repo)
	f.baseline(t, repo)
	write(t, repo, "a.txt", "b\n")
	write(t, repo, "c.txt", "c\n")
	if err := os.Remove(filepath.Join(repo, "a.txt")); err != nil {
		t.Fatal(err)
	}
	d := f.diff(t, repo, proto.DiffParams{Patch: true})
	check()
	r := d.Repos[0]
	expectFiles(t, r.Files, "deleted:a.txt", "added:c.txt")
	if len(r.SHA) != 64 || !strings.Contains(r.Patch, "c.txt") {
		t.Fatalf("repo %+v", r)
	}
}

func TestShowUntrackedFilesNoSnapshotsTrackedFilesOnly(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"tracked": "1\n"})
	git(t, g, repo, "config", "status.showUntrackedFiles", "no")
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	write(t, repo, "tracked", "2\n")
	write(t, repo, "private.txt", "not mine to copy\n")
	expectFiles(t, f.diff(t, repo, proto.DiffParams{}).Repos[0].Files, "modified:tracked")
}

func TestShowUntrackedFilesSpellings(t *testing.T) {
	for v, no := range map[string]bool{"no": true, "No": true, "false": true, "OFF": true, "0": true, "normal": false, "all": false, "yes": false} {
		if isNo(v) != no {
			t.Errorf("isNo(%q) = %v", v, !no)
		}
	}
}

func TestIgnoredButTrackedFilesAreIncluded(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{".gitignore": "*.gen\n", "x.txt": "x\n"})
	write(t, repo, "kept.gen", "1\n")
	git(t, g, repo, "add", "-f", "kept.gen")
	git(t, g, repo, "commit", "-q", "-m", "tracked despite the ignore")
	write(t, repo, "loose.gen", "ignored\n")
	f := newFixture(t, g)
	f.baseline(t, repo)
	write(t, repo, "kept.gen", "2\n")
	write(t, repo, "loose.gen", "still ignored\n")
	defer guard(t, g, repo)()
	expectFiles(t, f.diff(t, repo, proto.DiffParams{}).Repos[0].Files, "modified:kept.gen")
}

func TestExecBitIsTheOwnersBit(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a.sh": "x\n"})
	f := newFixture(t, g)
	f.baseline(t, repo)
	if err := os.Chmod(filepath.Join(repo, "a.sh"), 0o654); err != nil { // group may execute, the owner may not
		t.Fatal(err)
	}
	check := guard(t, g, repo)
	if files := f.diff(t, repo, proto.DiffParams{}).Repos[0].Files; len(files) != 0 {
		t.Fatalf("0654 shows a change: %+v", files)
	}
	check()
}

func TestUnreadableFileIsSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	write(t, repo, "secret", "hidden\n")
	if err := os.Chmod(filepath.Join(repo, "secret"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(repo, "secret"), 0o644) })
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	write(t, repo, "a", "2\n")
	r := f.diff(t, repo, proto.DiffParams{}).Repos[0]
	expectFiles(t, r.Files, "modified:a")
	if r.State != proto.RepoOK || len(r.Skipped) != 1 || r.Skipped[0].Path != "secret" || r.Skipped[0].Reason != ReasonUnreadable {
		t.Fatalf("repo %+v", r)
	}
}

func TestLargeFilesAreSkipped(t *testing.T) {
	g := newGit(t)
	g.MaxFile = 100
	repo := t.TempDir()
	big := strings.Repeat("x", 150)
	initRepo(t, g, repo, map[string]string{"small.txt": "s\n", "tracked.bin": big})
	f := newFixture(t, g)
	f.baseline(t, repo)
	b, _, _ := f.st.Baseline(context.Background(), f.id)
	if sk := b.Repos[0].Skipped; len(sk) != 1 || sk[0].Path != "tracked.bin" || sk[0].Reason != ReasonTooLarge || sk[0].Size != 150 {
		t.Fatalf("baseline skipped files: %+v", sk)
	}
	write(t, repo, "tracked.bin", big+big) // changed, and too large
	write(t, repo, "untracked.bin", big)
	write(t, repo, "small.txt", "s2\n")
	check := guard(t, g, repo)
	d := f.diff(t, repo, proto.DiffParams{Patch: true})
	check()
	r := d.Repos[0]
	expectFiles(t, r.Files, "modified:small.txt")
	if len(r.Skipped) != 2 || r.Skipped[0].Path != "tracked.bin" || r.Skipped[0].Size != 300 || r.Skipped[0].Reason != ReasonTooLarge || r.Skipped[1].Path != "untracked.bin" {
		t.Fatalf("skipped %+v", r.Skipped)
	}
	if strings.Contains(r.Patch, "bin") {
		t.Errorf("skipped files in the patch:\n%s", r.Patch)
	}
	// The skipped list is part of the SHA.
	write(t, repo, "other.bin", big)
	if again := f.diff(t, repo, proto.DiffParams{}).Repos[0]; again.SHA == r.SHA || len(again.Skipped) != 3 {
		t.Errorf("SHA ignores skipped files: %+v", again)
	}
}

func TestEmbeddedRepositoryIsRecordedAsItsCommit(t *testing.T) {
	g := newGit(t)
	root := t.TempDir()
	initRepo(t, g, root, map[string]string{"a": "1\n"})
	emb := filepath.Join(root, "emb")
	initRepo(t, g, emb, map[string]string{"e": "1\n"})
	initRepo(t, g, filepath.Join(root, "empty"), nil) // no commits: skipped
	f := newFixture(t, g)
	f.baseline(t, root)
	write(t, emb, "e", "2\n")
	git(t, g, emb, "commit", "-q", "-am", "next")
	defer guard(t, g, root, emb)()
	d := f.diff(t, root, proto.DiffParams{})
	expectFiles(t, repoByPath(t, d, ".").Files, "modified:emb")
	expectFiles(t, repoByPath(t, d, "emb").Files, "modified:e")
}

func TestCRLFEditIsOneLine(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{".gitattributes": "* text=auto\n"})
	write(t, repo, "w.txt", "one\r\ntwo\r\nthree\r\n")
	git(t, g, repo, "add", "-A")
	git(t, g, repo, "commit", "-q", "-m", "crlf")
	f := newFixture(t, g)
	f.baseline(t, repo)
	write(t, repo, "w.txt", "one\r\nTWO\r\nthree\r\n")
	defer guard(t, g, repo)()
	files := f.diff(t, repo, proto.DiffParams{}).Repos[0].Files
	if len(files) != 1 || files[0].Additions != 1 || files[0].Deletions != 1 {
		t.Fatalf("files %+v", files)
	}
}

func TestFilteredFilesAreComparedAsRawBytes(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{".gitattributes": "*.u filter=up\n", "f.u": "text\n"})
	git(t, g, repo, "config", "filter.up.clean", "tr a-z A-Z")
	git(t, g, repo, "config", "filter.up.smudge", "tr A-Z a-z")
	f := newFixture(t, g)
	f.baseline(t, repo)
	// A touch changes the stat data and nothing else.
	later := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(repo, "f.u"), later, later); err != nil {
		t.Fatal(err)
	}
	defer guard(t, g, repo)()
	if files := f.diff(t, repo, proto.DiffParams{}).Repos[0].Files; len(files) != 0 {
		t.Fatalf("touch-only change: %+v", files)
	}
	write(t, repo, "f.u", "text2\n")
	files := f.diff(t, repo, proto.DiffParams{}).Repos[0].Files
	if len(files) != 1 || files[0].Filter != "up" {
		t.Fatalf("edit: %+v", files)
	}
}

func TestLinkedWorktreesHaveTheirOwnIndex(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, g, repo, "worktree", "add", "-q", "-b", "other", wt)
	write(t, wt, "a", "other branch edit\n")
	f := newFixture(t, g)
	defer guard(t, g, repo, wt)()
	// Two sessions, one per worktree, working on the same repository.
	f2 := newFixture(t, g)
	f2.svc.Home = f.home
	f.baseline(t, repo)
	f2.baseline(t, wt)
	write(t, repo, "main-only", "m\n")
	write(t, wt, "wt-only", "w\n")
	main := f.diff(t, repo, proto.DiffParams{}).Repos[0]
	linked := f2.diff(t, wt, proto.DiffParams{}).Repos[0]
	expectFiles(t, main.Files, "added:main-only")
	expectFiles(t, linked.Files, "added:wt-only")
	objs, _ := filepath.Glob(filepath.Join(f.home, "objects", "*"))
	idx, _ := filepath.Glob(filepath.Join(f.home, "objects", "*", "index-*"))
	if len(objs) != 1 || len(idx) != 2 {
		t.Fatalf("object dirs %v, indexes %v", objs, idx)
	}
	if indexPath(objs[0], Repo{Abs: repo}) == indexPath(objs[0], Repo{Abs: wt}) {
		t.Fatal("worktrees share an index")
	}
}

func TestIgnoreStatInTheUsersConfigDoesNotHideEdits(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	initRepo(t, g, repo, map[string]string{"a": "1\n"})
	git(t, g, repo, "config", "core.ignoreStat", "true")
	f := newFixture(t, g)
	defer guard(t, g, repo)()
	f.baseline(t, repo)
	write(t, repo, "a", "2\n")
	write(t, repo, "b", "new\n")
	expectFiles(t, f.diff(t, repo, proto.DiffParams{}).Repos[0].Files, "modified:a", "added:b")
}

// TestCommittedCaseOnlyRenameIsARename: where the filesystem ignores case,
// git finds a file under either spelling, so the old spelling must be
// removed from Hoplet's index when the new one is added.
func TestCommittedCaseOnlyRenameIsARename(t *testing.T) {
	g := newGit(t)
	repo := t.TempDir()
	body := strings.Repeat("a line of the readme\n", 20)
	initRepo(t, g, repo, map[string]string{"README": body})
	if _, err := os.Stat(filepath.Join(repo, "readme")); err != nil {
		t.Skip("the filesystem is case-sensitive")
	}
	f := newFixture(t, g)
	f.baseline(t, repo)
	if err := os.Rename(filepath.Join(repo, "README"), filepath.Join(repo, "readme")); err != nil {
		t.Fatal(err)
	}
	git(t, g, repo, "rm", "-q", "--cached", "README")
	git(t, g, repo, "add", "readme")
	git(t, g, repo, "commit", "-q", "-m", "rename")
	write(t, repo, "readme", body+"one more line\n")
	defer guard(t, g, repo)()
	expectFiles(t, f.diff(t, repo, proto.DiffParams{}).Repos[0].Files, "renamed:readme<-README")
}
