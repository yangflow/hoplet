package workspace

import (
	"context"
	"fmt"
	"os"
)

// RestoreSpec says where a snapshot is restored.
type RestoreSpec struct {
	Clone    string // working tree of the clone the new worktree is added to
	Worktree string // the new worktree's directory; it must not exist
	Branch   string // the new branch, created at the snapshot's base commit; it must not exist
}

// Restore adds a git worktree to the clone on a new branch at the snapshot's base commit, applies the
// tracked diff and writes the untracked files, then checks that the worktree's tree hash is the snapshot's.
// The archive must have been accepted by OpenArchive. On any failure Restore removes the worktree and the
// branch it created and leaves the clone as it was; a worktree or branch that already existed is never
// touched.
func Restore(ctx context.Context, a *Archive, spec RestoreSpec) (err error) {
	if _, statErr := os.Lstat(spec.Worktree); statErr == nil {
		return ErrTargetExists
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if gitCommand(ctx, spec.Clone, "show-ref", "--verify", "--quiet", "refs/heads/"+spec.Branch).Run() == nil {
		return ErrTargetExists
	}
	incoming := "refs/hoplet/incoming/" + a.Meta.BaseCommit
	defer func() {
		_, _ = git(context.WithoutCancel(ctx), spec.Clone, "update-ref", "-d", incoming)
	}()
	if err := ensureBase(ctx, spec.Clone, a, incoming); err != nil {
		return err
	}

	created := false
	defer func() {
		if err != nil && created {
			RemoveWorktree(context.WithoutCancel(ctx), spec.Clone, spec.Worktree, spec.Branch)
		}
	}()
	if _, err = git(ctx, spec.Clone, "worktree", "add", "--quiet", "-b", spec.Branch, spec.Worktree, a.Meta.BaseCommit); err != nil {
		// A failed `worktree add` can leave a half-made directory it created itself.
		RemoveWorktree(context.WithoutCancel(ctx), spec.Clone, spec.Worktree, spec.Branch)
		return err
	}
	created = true
	if a.DiffPath != "" {
		if _, err = git(ctx, spec.Worktree, "apply", "--binary", "--whitespace=nowarn", a.DiffPath); err != nil {
			return err
		}
	}
	root, err := os.OpenRoot(spec.Worktree)
	if err != nil {
		return err
	}
	err = a.ExtractUntracked(root)
	root.Close()
	if err != nil {
		return err
	}
	tree, err := TreeHash(ctx, spec.Worktree)
	if err != nil {
		return err
	}
	if tree != a.Meta.TreeHash {
		return ErrTreeMismatch
	}
	return nil
}

func hasCommit(ctx context.Context, clone, commit string) bool {
	return gitCommand(ctx, clone, "cat-file", "-e", commit+"^{commit}").Run() == nil
}

// ensureBase makes the snapshot's base commit available in the clone: from the bundle when the snapshot
// carries one, from the clone's own origin otherwise (or when the bundle needs commits the clone lacks).
func ensureBase(ctx context.Context, clone string, a *Archive, incoming string) error {
	if hasCommit(ctx, clone, a.Meta.BaseCommit) {
		return nil
	}
	if a.BundlePath != "" {
		// The bundle comes from another machine: its objects are checked as they are stored.
		fetch := []string{"-c", "transfer.fsckObjects=true", "-c", "fetch.fsckObjects=true", "fetch", "--quiet", "--no-tags", a.BundlePath, "+HEAD:" + incoming}
		_, err := git(ctx, clone, fetch...)
		if err != nil {
			// The bundle builds on commits that the remote has and this clone has not fetched yet.
			fetchOrigin(ctx, clone)
			_, err = git(ctx, clone, fetch...)
		}
		if err == nil && hasCommit(ctx, clone, a.Meta.BaseCommit) {
			return nil
		}
	} else {
		fetchOrigin(ctx, clone)
		if hasCommit(ctx, clone, a.Meta.BaseCommit) {
			return nil
		}
	}
	return ErrBaseUnavailable
}

// fetchOrigin asks the clone's own remote for new commits. A clone without one, or an unreachable remote,
// is not an error here: the caller checks whether the commit arrived.
func fetchOrigin(ctx context.Context, clone string) {
	_, _ = git(ctx, clone, "fetch", "--quiet", "--no-tags", "origin")
}

// RemoveWorktree removes a worktree a snapshot restore created and its branch. Callers pass only paths
// they created themselves.
func RemoveWorktree(ctx context.Context, clone, worktree, branch string) {
	RemoveWorktreeKeepBranch(ctx, clone, worktree)
	DeleteBranch(ctx, clone, branch)
}

// RemoveWorktreeKeepBranch removes the worktree and its registration and leaves the branch.
func RemoveWorktreeKeepBranch(ctx context.Context, clone, worktree string) {
	if _, err := os.Lstat(worktree); err == nil {
		if _, err := git(ctx, clone, "worktree", "remove", "--force", "--force", worktree); err != nil {
			_ = os.RemoveAll(worktree)
		}
	}
	_, _ = git(ctx, clone, "worktree", "prune")
}

// DeleteBranch deletes a local branch, merged or not; a branch that does not exist is deleted already.
func DeleteBranch(ctx context.Context, clone, branch string) {
	if BranchExists(ctx, clone, branch) {
		_, _ = git(ctx, clone, "branch", "-D", "--quiet", branch)
	}
}

// BranchExists reports whether the clone has the local branch.
func BranchExists(ctx context.Context, clone, branch string) bool {
	return gitCommand(ctx, clone, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

// Stage makes a private directory for the staged parts of an archive under dir.
func Stage(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(dir, "stage-")
	if err != nil {
		return "", fmt.Errorf("stage: %w", err)
	}
	return stage, nil
}
