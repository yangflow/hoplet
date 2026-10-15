package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBranch(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", "--template=", "-b", "main")
	run(t, dir, "git", "config", "user.email", "t@t.test")
	run(t, dir, "git", "config", "user.name", "t")
	if got := Branch(dir); got != "main" {
		t.Fatalf("a repository without a commit: got %q, want main", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", "a.txt")
	run(t, dir, "git", "commit", "-m", "a")
	run(t, dir, "git", "checkout", "-b", "feat/10-branch-chip")

	sub := filepath.Join(dir, "web", "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Branch(sub); got != "feat/10-branch-chip" {
		t.Fatalf("a directory inside the repository: got %q", got)
	}

	// A linked worktree has its own HEAD behind its `.git` file.
	wt := filepath.Join(t.TempDir(), "wt")
	run(t, dir, "git", "worktree", "add", "-b", "hoplet/11111111", wt)
	if got := Branch(wt); got != "hoplet/11111111" {
		t.Fatalf("a linked worktree: got %q", got)
	}

	// A detached HEAD reads as the commit's first 7 hex digits.
	run(t, dir, "git", "checkout", "--detach")
	got := Branch(dir)
	if len(got) != 7 || strings.Trim(got, "0123456789abcdef") != "" {
		t.Fatalf("a detached HEAD: got %q", got)
	}

	if got := Branch(t.TempDir()); got != "" {
		t.Fatalf("outside any repository: got %q, want empty", got)
	}
}
