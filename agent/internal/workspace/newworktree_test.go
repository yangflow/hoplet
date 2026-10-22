package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const newWorktreeSession = "5d1c7a90-1111-4222-8333-444455556666"

// repoWithCommit is a repository with one commit; the paths are symlink-free.
func repoWithCommit(t *testing.T) (root, clone string) {
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
	run(t, clone, "git", "commit", "-q", "--allow-empty", "-m", "init")
	return root, clone
}

func TestValidBranch(t *testing.T) {
	for _, ok := range []string{"feature", "feature/x", "fix-1.2", "a_b", "hoplet/0b7e1c52", "ü"} {
		if err := ValidBranch(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	bad := []string{"", "-x", "--force", "@", "HEAD", "a..b", "a@{b", "a//b", "/a", "a/", "a.", "a b", "a~b", "a^b", "a:b",
		"a?b", "a*b", "a[b", "a\\b", "a\x01b", ".hidden", "a/.b", "a.lock", "a/b.lock/c", strings.Repeat("a", MaxBranchName+1)}
	for _, b := range bad {
		if !errors.Is(ValidBranch(b), ErrNewWorktreeBranchBad) {
			t.Errorf("%q accepted", b)
		}
	}
}

func TestCreateNewWorktreeNamedAndGenerated(t *testing.T) {
	root, clone := repoWithCommit(t)
	ctx := context.Background()
	w, err := CreateNewWorktree(ctx, clone, "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	if w.Clone != clone || w.Branch != "feature/x" || w.Path != filepath.Join(root, "repo-feature-x") || w.Dir != w.Path {
		t.Fatalf("%+v", w)
	}
	if !BranchExists(ctx, clone, "feature/x") {
		t.Fatal("branch missing")
	}
	g, err := CreateNewWorktree(ctx, clone, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(g.Branch, "hoplet/") || len(g.Branch) != len("hoplet/")+8 {
		t.Fatalf("generated %q", g.Branch)
	}
	if _, err := os.Stat(g.Path); err != nil {
		t.Fatal(err)
	}
}

func TestCreateNewWorktreeRefusals(t *testing.T) {
	ctx := context.Background()
	root, clone := repoWithCommit(t)

	plain := filepath.Join(root, "plain")
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNewWorktree(ctx, plain, ""); !errors.Is(err, ErrNewWorktreeNotRepo) {
		t.Errorf("not a repo: %v", err)
	}
	if _, err := CreateNewWorktree(ctx, clone, "-x"); !errors.Is(err, ErrNewWorktreeBranchBad) {
		t.Errorf("bad branch: %v", err)
	}
	if _, err := CreateNewWorktree(ctx, clone, "main"); !errors.Is(err, ErrNewWorktreeBranchExists) {
		t.Errorf("existing branch: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "repo-taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNewWorktree(ctx, clone, "taken"); !errors.Is(err, ErrNewWorktreePathExists) {
		t.Errorf("existing path: %v", err)
	}
	if BranchExists(ctx, clone, "taken") {
		t.Error("a refused worktree left its branch")
	}

	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, empty, "git", "init", "-q", "-b", "main")
	if _, err := CreateNewWorktree(ctx, empty, ""); !errors.Is(err, ErrNewWorktreeNoCommit) {
		t.Errorf("no commit: %v", err)
	}
}

func TestCreateNewWorktreeOfASubdirectoryAndOfALinkedWorktree(t *testing.T) {
	ctx := context.Background()
	root, clone := repoWithCommit(t)
	sub := filepath.Join(clone, "pkg", "a")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, clone, "git", "add", "pkg")
	run(t, clone, "git", "commit", "-q", "-m", "sub")
	w, err := CreateNewWorktree(ctx, sub, "sub")
	if err != nil {
		t.Fatal(err)
	}
	if w.Path != filepath.Join(root, "repo-sub") || w.Dir != filepath.Join(w.Path, "pkg", "a") {
		t.Fatalf("%+v", w)
	}
	if _, err := os.Stat(filepath.Join(w.Dir, "f.txt")); err != nil {
		t.Fatal(err)
	}
	// From a linked worktree the new one still sits next to the main working tree.
	w2, err := CreateNewWorktree(ctx, w.Path, "again")
	if err != nil {
		t.Fatal(err)
	}
	if w2.Clone != clone || w2.Path != filepath.Join(root, "repo-again") {
		t.Fatalf("%+v", w2)
	}
}

func TestNewWorktreeMarkFindAndRemove(t *testing.T) {
	ctx := context.Background()
	_, clone := repoWithCommit(t)
	w, err := CreateNewWorktree(ctx, clone, "mark")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Mark(ctx, newWorktreeSession); err != nil {
		t.Fatal(err)
	}
	found, ok := FindSessionWorktree(ctx, w.Dir, nil, newWorktreeSession)
	if !ok || !found.Present || !found.HasBranch || found.Clone != clone || found.Path != w.Path || found.Branch != "mark" {
		t.Fatalf("found %+v %v", found, ok)
	}
	if !found.IsOurs(ctx) {
		t.Fatal("not ours")
	}
	if _, ok := FindSessionWorktree(ctx, w.Dir, nil, "another-session"); ok {
		t.Fatal("found for another session")
	}

	// The directory is gone: the branch alone is offered.
	RemoveWorktreeKeepBranch(ctx, clone, w.Path)
	found, ok = FindSessionWorktree(ctx, clone, nil, newWorktreeSession)
	if !ok || found.Present || !found.HasBranch || found.Branch != "mark" {
		t.Fatalf("branch only %+v %v", found, ok)
	}

	w3, err := CreateNewWorktree(ctx, clone, "gone")
	if err != nil {
		t.Fatal(err)
	}
	if err := w3.Mark(ctx, "s3"); err != nil {
		t.Fatal(err)
	}
	w3.Remove(ctx)
	if _, err := os.Stat(w3.Path); err == nil || BranchExists(ctx, clone, "gone") {
		t.Fatal("not removed")
	}
	if _, ok := FindSessionWorktree(ctx, clone, nil, "s3"); ok {
		t.Fatal("markers survived the removal")
	}
}

func TestMarkedWorktreeNeverTheMainWorkingTree(t *testing.T) {
	ctx := context.Background()
	_, clone := repoWithCommit(t)
	// A forged marker on the branch checked out in the main working tree.
	run(t, clone, "git", "config", "--local", "branch.main.hopletsession", newWorktreeSession)
	found, ok := FindSessionWorktree(ctx, clone, nil, newWorktreeSession)
	if ok && found.IsOurs(ctx) {
		t.Fatalf("the main working tree is offered: %+v", found)
	}
}
