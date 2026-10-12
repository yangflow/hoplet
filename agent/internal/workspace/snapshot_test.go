package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// output runs a command and returns its standard output.
func output(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TEMPLATE_DIR=", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name string, data []byte, perm os.FileMode) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(full, perm); err != nil {
		t.Fatal(err)
	}
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// fixture is a bare origin, the clone a session works in and another clone on a second machine.
type fixture struct {
	origin string
	source string // the session's working tree
	target string // a clone of the same origin elsewhere
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{origin: filepath.Join(root, "origin.git"), source: filepath.Join(root, "a", "repo"), target: filepath.Join(root, "b", "repo")}
	run(t, root, "git", "init", "--bare", "-b", "main", f.origin)
	seed := filepath.Join(root, "seed")
	run(t, root, "git", "clone", f.origin, seed)
	run(t, seed, "git", "config", "user.email", "t@t.test")
	run(t, seed, "git", "config", "user.name", "t")
	write(t, seed, "README.md", []byte("hello\n"), 0o644)
	write(t, seed, "src/main.txt", []byte("one\ntwo\nthree\n"), 0o644)
	write(t, seed, "bin/tool.sh", []byte("#!/bin/sh\necho tool\n"), 0o755)
	write(t, seed, "data/blob.bin", randomBytes(t, 4096), 0o644)
	write(t, seed, "gone.txt", []byte("to be deleted\n"), 0o644)
	write(t, seed, ".gitignore", []byte("*.log\nbuild/\n"), 0o644)
	run(t, seed, "git", "add", ".")
	run(t, seed, "git", "commit", "-m", "seed")
	run(t, seed, "git", "push", "origin", "HEAD:main")
	for _, dir := range []string{f.source, f.target} {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			t.Fatal(err)
		}
		run(t, root, "git", "clone", f.origin, dir)
		run(t, dir, "git", "config", "user.email", "t@t.test")
		run(t, dir, "git", "config", "user.name", "t")
	}
	return f
}

// dirty makes the source tree look like a session in progress: an unpushed commit, staged and unstaged
// edits, a binary edit, a mode change, a deletion, untracked text, binary, executable and link files, and
// ignored files.
func (f fixture) dirty(t *testing.T) {
	t.Helper()
	write(t, f.source, "committed.txt", []byte("local commit\n"), 0o644)
	run(t, f.source, "git", "add", "committed.txt")
	run(t, f.source, "git", "commit", "-m", "local")
	write(t, f.source, "src/main.txt", []byte("one\nTWO\nthree\nfour\n"), 0o644)
	write(t, f.source, "README.md", []byte("hello\nstaged\n"), 0o644)
	run(t, f.source, "git", "add", "README.md")
	write(t, f.source, "data/blob.bin", randomBytes(t, 5000), 0o644)
	if err := os.Chmod(filepath.Join(f.source, "bin/tool.sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.source, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, f.source, "notes/todo.txt", []byte("untracked 字\n"), 0o644)
	write(t, f.source, "notes/blob.dat", append([]byte{0, 1, 2, 0xff, 0xfe, 0}, randomBytes(t, 3000)...), 0o600)
	write(t, f.source, "run.sh", []byte("#!/bin/sh\necho run\n"), 0o755)
	if err := os.Symlink("notes/todo.txt", filepath.Join(f.source, "todo-link")); err != nil {
		t.Fatal(err)
	}
	write(t, f.source, "debug.log", []byte("ignored\n"), 0o644)
	write(t, f.source, "build/out.o", []byte("ignored\n"), 0o644)
}

// carried lists the files of a tree that a snapshot carries: tracked and untracked, not ignored.
func carried(t *testing.T, dir string) []string {
	t.Helper()
	out := output(t, dir, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	var files []string
	seen := map[string]bool{}
	for _, name := range strings.Split(out, "\x00") {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	return files
}

func sameTrees(t *testing.T, a, b string) {
	t.Helper()
	fa, fb := carried(t, a), carried(t, b)
	if strings.Join(fa, "\n") != strings.Join(fb, "\n") {
		t.Fatalf("file lists differ:\n%v\n%v", fa, fb)
	}
	for _, name := range fa {
		ia, err := os.Lstat(filepath.Join(a, name))
		if err != nil {
			t.Fatal(err)
		}
		ib, err := os.Lstat(filepath.Join(b, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ia.Mode().Type() != ib.Mode().Type() || (ia.Mode().Perm()&0o111 != 0) != (ib.Mode().Perm()&0o111 != 0) {
			t.Fatalf("%s: mode %v vs %v", name, ia.Mode(), ib.Mode())
		}
		if ia.Mode()&os.ModeSymlink != 0 {
			la, _ := os.Readlink(filepath.Join(a, name))
			lb, _ := os.Readlink(filepath.Join(b, name))
			if la != lb {
				t.Fatalf("%s: link %q vs %q", name, la, lb)
			}
			continue
		}
		da, _ := os.ReadFile(filepath.Join(a, name))
		db, _ := os.ReadFile(filepath.Join(b, name))
		if !bytes.Equal(da, db) {
			t.Fatalf("%s: content differs", name)
		}
	}
}

func open(t *testing.T, s *Snapshot) *Archive {
	t.Helper()
	a, err := OpenArchive(s.Path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSnapshotRestoresAnUncommittedWorkingTreeIntoAnotherClone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.dirty(t)
	ctx := context.Background()

	snap, err := CreateSnapshot(ctx, f.source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	head := output(t, f.source, "git", "rev-parse", "HEAD")
	if snap.Meta.BaseCommit != head || snap.Meta.OriginURL != f.origin || snap.Meta.Subdir != "" {
		t.Fatalf("meta = %+v", snap.Meta)
	}
	if snap.Size <= 0 || snap.Size > MaxSnapshotCompressed || len(snap.SHA256) != 64 {
		t.Fatalf("snapshot = %+v", snap)
	}
	a := open(t, snap)
	if a.BundlePath == "" {
		t.Fatal("the unpushed commit must travel in a bundle")
	}
	names := map[string]ArchiveFile{}
	for _, file := range a.Files {
		names[file.Name] = file
	}
	if _, ok := names["debug.log"]; ok {
		t.Fatal("an ignored file was carried")
	}
	if _, ok := names["build/out.o"]; ok {
		t.Fatal("an ignored directory was carried")
	}
	for _, want := range []string{"notes/todo.txt", "notes/blob.dat", "run.sh", "todo-link"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("%s is not in the archive: %v", want, names)
		}
	}
	if names["run.sh"].Perm&0o111 == 0 || names["notes/blob.dat"].Perm != 0o600 || names["todo-link"].Link != "notes/todo.txt" {
		t.Fatalf("modes or links lost: %+v", names)
	}

	before := output(t, f.target, "git", "status", "--porcelain=v1", "--branch")
	worktree := filepath.Join(filepath.Dir(f.target), "repo-abcd1234")
	spec := RestoreSpec{Clone: f.target, Worktree: worktree, Branch: "hoplet/abcd1234"}
	if err := Restore(ctx, a, spec); err != nil {
		t.Fatal(err)
	}
	sameTrees(t, f.source, worktree)
	if got := output(t, worktree, "git", "rev-parse", "HEAD"); got != head {
		t.Fatalf("worktree HEAD = %s, want %s", got, head)
	}
	if got := output(t, worktree, "git", "rev-parse", "--abbrev-ref", "HEAD"); got != "hoplet/abcd1234" {
		t.Fatalf("branch = %s", got)
	}
	got, err := TreeHash(ctx, worktree)
	if err != nil || got != snap.Meta.TreeHash {
		t.Fatalf("tree hash %s (%v), want %s", got, err, snap.Meta.TreeHash)
	}
	if after := output(t, f.target, "git", "status", "--porcelain=v1", "--branch"); after != before {
		t.Fatalf("the clone changed:\n%s\n%s", before, after)
	}
	if refs := output(t, f.target, "git", "for-each-ref", "refs/hoplet"); refs != "" {
		t.Fatalf("temporary refs left behind: %s", refs)
	}
	if _, err := os.Stat(filepath.Join(worktree, "debug.log")); !os.IsNotExist(err) {
		t.Fatal("an ignored file appeared in the worktree")
	}
}

func TestSnapshotWithoutUnpushedCommitsHasNoBundle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	write(t, f.source, "src/main.txt", []byte("changed\n"), 0o644)
	snap, err := CreateSnapshot(context.Background(), f.source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := open(t, snap)
	if a.BundlePath != "" || a.DiffPath == "" || len(a.Files) != 0 {
		t.Fatalf("archive = %+v", a)
	}
	worktree := filepath.Join(filepath.Dir(f.target), "repo-00000001")
	if err := Restore(context.Background(), a, RestoreSpec{Clone: f.target, Worktree: worktree, Branch: "hoplet/00000001"}); err != nil {
		t.Fatal(err)
	}
	sameTrees(t, f.source, worktree)
}

func TestSnapshotOfACleanTreeIsEmpty(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	snap, err := CreateSnapshot(context.Background(), f.source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := open(t, snap)
	if a.DiffPath != "" || a.BundlePath != "" || len(a.Files) != 0 {
		t.Fatalf("archive = %+v", a)
	}
	worktree := filepath.Join(filepath.Dir(f.target), "repo-00000002")
	if err := Restore(context.Background(), a, RestoreSpec{Clone: f.target, Worktree: worktree, Branch: "hoplet/00000002"}); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotOfASubdirectoryRecordsIt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	snap, err := CreateSnapshot(context.Background(), filepath.Join(f.source, "src"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.Subdir != "src" {
		t.Fatalf("subdir = %q", snap.Meta.Subdir)
	}
}

func TestSnapshotRefusesWhatIsNotAnInspectableRepository(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := CreateSnapshot(ctx, t.TempDir(), t.TempDir()); !errors.Is(err, ErrNotGitRepo) {
		t.Fatalf("plain directory: %v", err)
	}
	empty := t.TempDir()
	run(t, empty, "git", "init", "-b", "main")
	if _, err := CreateSnapshot(ctx, empty, t.TempDir()); !errors.Is(err, ErrNoCommit) {
		t.Fatalf("repository without a commit: %v", err)
	}
}

func TestTreeHashFollowsContentAndModesOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	clean, err := TreeHash(ctx, f.source)
	if err != nil {
		t.Fatal(err)
	}
	if want := output(t, f.source, "git", "rev-parse", "HEAD^{tree}"); clean != want {
		t.Fatalf("clean tree hash %s, want HEAD's tree %s", clean, want)
	}
	other, _ := TreeHash(ctx, f.target)
	if other != clean {
		t.Fatal("two clones of the same commit differ")
	}
	write(t, f.source, "debug.log", []byte("ignored\n"), 0o644)
	if got, _ := TreeHash(ctx, f.source); got != clean {
		t.Fatal("an ignored file changed the hash")
	}
	write(t, f.source, "new.txt", []byte("x"), 0o644)
	withFile, _ := TreeHash(ctx, f.source)
	if withFile == clean {
		t.Fatal("an untracked file did not change the hash")
	}
	if err := os.Chmod(filepath.Join(f.source, "new.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := TreeHash(ctx, f.source); got == withFile {
		t.Fatal("a mode change did not change the hash")
	}
	if err := os.Remove(filepath.Join(f.source, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if got, _ := TreeHash(ctx, f.source); got != clean {
		t.Fatal("removing the file did not restore the hash")
	}
	if status := output(t, f.source, "git", "status", "--porcelain=v1"); strings.Contains(status, "new.txt") {
		t.Fatalf("hashing touched the index: %s", status)
	}
}

func TestSnapshotOfAnUnsafeLinkLeavesItOut(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if err := os.Symlink("/etc/hosts", filepath.Join(f.source, "abs-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", filepath.Join(f.source, "up-link")); err != nil {
		t.Fatal(err)
	}
	snap, err := CreateSnapshot(context.Background(), f.source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if a := open(t, snap); len(a.Files) != 0 {
		t.Fatalf("unsafe links were carried: %+v", a.Files)
	}
	worktree := filepath.Join(filepath.Dir(f.target), "repo-00000003")
	a := open(t, snap)
	if err := Restore(context.Background(), a, RestoreSpec{Clone: f.target, Worktree: worktree, Branch: "hoplet/00000003"}); err != nil {
		t.Fatalf("the hash must ignore what the archive leaves out: %v", err)
	}
}

func TestSnapshotTooLargeWhenUncompressedPastTheLimit(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("writes a 257 MiB sparse file")
	}
	f := newFixture(t)
	big, err := os.Create(filepath.Join(f.source, "zeros.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(MaxSnapshotUncompressed + 1); err != nil {
		t.Fatal(err)
	}
	big.Close()
	out := t.TempDir()
	if _, err := CreateSnapshot(context.Background(), f.source, out); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func TestSnapshotTooLargeWhenCompressedPastTheLimit(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("writes 65 MiB of random data")
	}
	f := newFixture(t)
	write(t, f.source, "random.bin", randomBytes(t, int(MaxSnapshotCompressed)+(1<<20)), 0o644)
	out := t.TempDir()
	if _, err := CreateSnapshot(context.Background(), f.source, out); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func TestRestoreRefusesAnExistingWorktreeOrBranchAndTouchesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	write(t, f.source, "a.txt", []byte("a"), 0o644)
	snap, err := CreateSnapshot(context.Background(), f.source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := open(t, snap)

	taken := filepath.Join(filepath.Dir(f.target), "taken")
	write(t, taken, "mine.txt", []byte("keep"), 0o644)
	err = Restore(context.Background(), a, RestoreSpec{Clone: f.target, Worktree: taken, Branch: "hoplet/fresh"})
	if !errors.Is(err, ErrTargetExists) {
		t.Fatalf("existing directory: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(taken, "mine.txt")); string(got) != "keep" {
		t.Fatal("the existing directory was touched")
	}

	run(t, f.target, "git", "branch", "hoplet/used")
	err = Restore(context.Background(), a, RestoreSpec{Clone: f.target, Worktree: filepath.Join(filepath.Dir(f.target), "wt"), Branch: "hoplet/used"})
	if !errors.Is(err, ErrTargetExists) {
		t.Fatalf("existing branch: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(f.target), "wt")); !os.IsNotExist(statErr) {
		t.Fatal("a worktree was created")
	}
	if branches := output(t, f.target, "git", "branch", "--list", "hoplet/used"); !strings.Contains(branches, "hoplet/used") {
		t.Fatal("the existing branch was removed")
	}
}

func TestRestoreWithoutTheBaseCommitFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// A commit on a clone with no origin refs: no bundle, and the target cannot get it from origin.
	write(t, f.source, "x.txt", []byte("x"), 0o644)
	run(t, f.source, "git", "add", "x.txt")
	run(t, f.source, "git", "commit", "-m", "unreachable")
	run(t, f.source, "git", "update-ref", "-d", "refs/remotes/origin/main")
	run(t, f.source, "git", "update-ref", "-d", "refs/remotes/origin/HEAD")
	snap, err := CreateSnapshot(context.Background(), f.source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := open(t, snap)
	if a.BundlePath != "" {
		t.Fatal("without origin refs there is nothing to compare against")
	}
	worktree := filepath.Join(filepath.Dir(f.target), "repo-nobase")
	err = Restore(context.Background(), a, RestoreSpec{Clone: f.target, Worktree: worktree, Branch: "hoplet/nobase"})
	if !errors.Is(err, ErrBaseUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(worktree); !os.IsNotExist(statErr) {
		t.Fatal("a worktree was left behind")
	}
}

func TestRestoreFailureRemovesTheWorktreeAndBranch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	write(t, f.source, "src/main.txt", []byte("changed\n"), 0o644)
	write(t, f.source, "new.txt", []byte("new\n"), 0o644)
	snap, err := CreateSnapshot(context.Background(), f.source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := open(t, snap)
	a.Meta.TreeHash = strings.Repeat("0", 40) // the hash the snapshot claims is not the tree it holds
	worktree := filepath.Join(filepath.Dir(f.target), "repo-bad")
	err = Restore(context.Background(), a, RestoreSpec{Clone: f.target, Worktree: worktree, Branch: "hoplet/bad"})
	if !errors.Is(err, ErrTreeMismatch) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(worktree); !os.IsNotExist(statErr) {
		t.Fatal("the worktree stayed")
	}
	if got := output(t, f.target, "git", "branch", "--list", "hoplet/bad"); got != "" {
		t.Fatalf("the branch stayed: %s", got)
	}
	if got := output(t, f.target, "git", "worktree", "list", "--porcelain"); strings.Count(got, "worktree ") != 1 {
		t.Fatalf("worktrees: %s", got)
	}
}

func TestRestoreFailsWhenAnUntrackedFileCollidesWithATrackedOne(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	write(t, f.source, "new.txt", []byte("new\n"), 0o644)
	snap, err := CreateSnapshot(context.Background(), f.source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The target's clone has this path tracked at the base commit: exclusive creation must refuse it.
	write(t, f.target, "new.txt", []byte("theirs\n"), 0o644)
	run(t, f.target, "git", "add", "new.txt")
	run(t, f.target, "git", "commit", "-m", "diverged")
	a := open(t, snap)
	a.Meta.BaseCommit = output(t, f.target, "git", "rev-parse", "HEAD")
	worktree := filepath.Join(filepath.Dir(f.target), "repo-collide")
	if err := Restore(context.Background(), a, RestoreSpec{Clone: f.target, Worktree: worktree, Branch: "hoplet/collide"}); err == nil {
		t.Fatal("collision accepted")
	}
	if _, statErr := os.Stat(worktree); !os.IsNotExist(statErr) {
		t.Fatal("the worktree stayed")
	}
}
