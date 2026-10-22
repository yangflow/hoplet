package daemon

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/projects"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
)

// gitProject makes dir a repository with one commit.
func gitProject(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "t@example.com")
	gitRun(t, dir, "config", "user.name", "t")
	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
}

func TestStartInANewWorktreeThenDeleteIt(t *testing.T) {
	b, _, st, watched, start := newStartEnvFull(t)
	gitProject(t, watched)
	res, err := start(sync.RuntimeStart{
		ProjectPath: watched, Provider: "claude", Text: "say hi", PromptID: "w1",
		Worktree: &sync.NewWorktree{Branch: "feature/x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := res.Worktree
	if w == nil || w.Branch != "feature/x" || w.Clone != projects.Real(watched) || w.Path != w.Clone+"-feature-x" {
		t.Fatalf("worktree = %+v", w)
	}
	// The session runs in the worktree, which is a watched project that imports it.
	if got := importedIn(t, st, w.Path); len(got) != 1 || got[0] != res.SessionID {
		t.Fatalf("imported = %v", got)
	}
	if got := importedIn(t, st, watched); len(got) != 0 {
		t.Fatalf("the original project imported %v", got)
	}
	if _, err := b.locate(res.SessionID); err != nil {
		t.Fatalf("the session cannot be located: %v", err)
	}

	// Deleting the session finds the worktree and the branch.
	ctx := context.Background()
	got, err := sessionInspect(st.cfg)(ctx, res.SessionID)
	if err != nil || got.Worktree == nil || got.Worktree.Path != w.Path || got.Worktree.Branch != "feature/x" {
		t.Fatalf("inspect: %+v %v", got, err)
	}
	steps := sessionDelete(st.cfg, st)(ctx, res.SessionID, true, true, true)
	for _, step := range []string{sync.StepWorktree, sync.StepBranch} {
		if statuses(steps)[step] != sync.StatusDone {
			t.Fatalf("steps %+v", steps)
		}
	}
	if _, err := os.Lstat(w.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree left: %v", err)
	}
	if slices.Contains(st.watchedPaths(), w.Path) {
		t.Fatal("the worktree is still a project")
	}
}

func TestStartRefusesAWorktreeItCannotMake(t *testing.T) {
	_, _, st, watched, start := newStartEnvFull(t)
	req := sync.RuntimeStart{ProjectPath: watched, Provider: "claude", Text: "say x", PromptID: "w2", Worktree: &sync.NewWorktree{}}
	if _, err := start(req); !errors.Is(err, workspace.ErrNewWorktreeNotRepo) {
		t.Fatalf("not a repository: %v", err)
	}
	gitProject(t, watched)
	req.Worktree = &sync.NewWorktree{Branch: "main"}
	if _, err := start(req); !errors.Is(err, workspace.ErrNewWorktreeBranchExists) {
		t.Fatalf("existing branch: %v", err)
	}
	req.Worktree = &sync.NewWorktree{Branch: "--force"}
	if _, err := start(req); !errors.Is(err, workspace.ErrNewWorktreeBranchBad) {
		t.Fatalf("bad branch: %v", err)
	}
	if got := st.watchedPaths(); len(got) != 1 {
		t.Fatalf("projects = %v", got)
	}
}
