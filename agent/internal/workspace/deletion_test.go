package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const deletionSession = "0b7e1c52-3f44-4a63-9d0e-2a6f5e8c1d11"

// deletionFixture is a clone with a worktree and branch named the way a handoff names them.
func deletionFixture(t *testing.T) (clone, worktree string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clone = filepath.Join(root, "repo")
	if err := os.Mkdir(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, clone, "git", "init", "-q", "-b", "main")
	run(t, clone, "git", "config", "user.email", "t@example.com")
	run(t, clone, "git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(clone, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, clone, "git", "add", "a.txt")
	run(t, clone, "git", "commit", "-q", "-m", "init")
	worktree = filepath.Join(root, "repo-0b7e1c52")
	run(t, clone, "git", "worktree", "add", "-q", "-b", "hoplet/0b7e1c52", worktree)
	return clone, worktree
}

func TestFindSessionWorktreeCleanThenDirty(t *testing.T) {
	clone, worktree := deletionFixture(t)
	ctx := context.Background()
	w, ok := FindSessionWorktree(ctx, worktree, nil, deletionSession)
	if !ok || !w.Present || w.Clone != clone || w.Path != worktree || w.Branch != "hoplet/0b7e1c52" {
		t.Fatalf("found %+v %v", w, ok)
	}
	dirty, unpushed, err := InspectWorktree(ctx, w)
	if err != nil || dirty != (Dirty{}) || unpushed != 0 {
		t.Fatalf("clean: %+v %d %v", dirty, unpushed, err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(worktree, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(t, worktree, "git", "commit", "-q", "--allow-empty", "-m", "local")
	dirty, unpushed, err = InspectWorktree(ctx, w)
	if err != nil || dirty != (Dirty{Modified: 1, Untracked: 2}) || unpushed != 1 {
		t.Fatalf("dirty: %+v %d %v", dirty, unpushed, err)
	}
}

func TestSessionWorktreeRemovalKeepsBranchUntilAsked(t *testing.T) {
	clone, worktree := deletionFixture(t)
	ctx := context.Background()
	w, _ := FindSessionWorktree(ctx, worktree, nil, deletionSession)
	if !w.IsOurs(ctx) {
		t.Fatal("the worktree of the installation is ours")
	}
	RemoveWorktreeKeepBranch(ctx, clone, worktree)
	if _, err := os.Lstat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree left: %v", err)
	}
	if !BranchExists(ctx, clone, w.Branch) {
		t.Fatal("the branch must stay")
	}
	// Retry: the directory is gone, the branch is found from the clone alone.
	again, ok := FindSessionWorktree(ctx, "", []string{clone}, deletionSession)
	if !ok || again.Present || !again.HasBranch {
		t.Fatalf("branch only: %+v %v", again, ok)
	}
	DeleteBranch(ctx, clone, w.Branch)
	DeleteBranch(ctx, clone, w.Branch) // idempotent
	if BranchExists(ctx, clone, w.Branch) {
		t.Fatal("branch left")
	}
	if _, ok := FindSessionWorktree(ctx, "", []string{clone}, deletionSession); ok {
		t.Fatal("nothing is left to find")
	}
}

func TestSessionWorktreeNotOursIsNotFound(t *testing.T) {
	clone, worktree := deletionFixture(t)
	ctx := context.Background()
	// The directory the naming points at exists but is no worktree of the clone on that branch.
	run(t, clone, "git", "worktree", "remove", "--force", worktree)
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "mine.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := SessionWorktree{Clone: clone, Path: worktree, Branch: "hoplet/0b7e1c52", Present: true}
	if w.IsOurs(ctx) {
		t.Fatal("an unregistered directory must not be ours")
	}
	if found, ok := FindSessionWorktree(ctx, "", []string{clone}, deletionSession); ok && found.Present {
		t.Fatalf("found a directory that is not ours: %+v", found)
	}
	if _, err := os.Stat(filepath.Join(worktree, "mine.txt")); err != nil {
		t.Fatal(err)
	}
}
