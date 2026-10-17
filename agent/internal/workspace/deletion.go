package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SessionWorktree is the worktree and branch a handoff made for a session, found again from the session ID
// alone (the installation record is gone once the handoff finishes). Present says whether the worktree
// directory is still there; when it is not, only the branch may be left.
type SessionWorktree struct {
	Clone     string
	Path      string
	Branch    string
	Present   bool
	HasBranch bool
}

// Dirty counts uncommitted work in a worktree.
type Dirty struct {
	Modified  int
	Untracked int
}

// installNames names an installation's worktree and branch the way Install does.
func installNames(clone, sessionID string) (worktree, branch string) {
	short := sessionID
	if len(short) > 8 {
		short = short[:8]
	}
	return filepath.Join(filepath.Dir(clone), filepath.Base(clone)+"-"+short), "hoplet/" + short
}

// FindSessionWorktree looks for what a handoff made for sessionID in the clones reachable from cwd (the
// directory the session file records) and the watched projects. It returns a worktree only when the
// worktree is registered in the clone on the branch the installation names (the check of a rollback), or
// the branch alone when the directory is gone.
func FindSessionWorktree(ctx context.Context, cwd string, watched []string, sessionID string) (SessionWorktree, bool) {
	seen := map[string]bool{}
	dirs := append([]string{cwd}, watched...)
	for _, d := range dirs {
		if !validDirectory(d) {
			continue
		}
		clone, err := mainWorktree(ctx, d)
		if err != nil || seen[clone] {
			continue
		}
		seen[clone] = true
		wt, branch := installNames(clone, sessionID)
		rec := Record{Clone: clone, Worktree: wt, Branch: branch}
		if _, err := os.Lstat(wt); err == nil {
			if worktreeIsOurs(ctx, rec) {
				return SessionWorktree{Clone: clone, Path: wt, Branch: branch, Present: true, HasBranch: BranchExists(ctx, clone, branch)}, true
			}
			continue
		}
		if BranchExists(ctx, clone, branch) {
			return SessionWorktree{Clone: clone, Path: wt, Branch: branch, HasBranch: true}, true
		}
	}
	return SessionWorktree{}, false
}

// IsOurs re-checks, right before a removal, that the worktree is still the one the installation made.
func (w SessionWorktree) IsOurs(ctx context.Context) bool {
	return worktreeIsOurs(ctx, Record{Clone: w.Clone, Worktree: w.Path, Branch: w.Branch})
}

// InspectWorktree counts the uncommitted files of the worktree and the commits only its branch has: on no
// remote and on no other local branch, so deleting the branch loses them.
func InspectWorktree(ctx context.Context, w SessionWorktree) (Dirty, int, error) {
	var d Dirty
	if !w.Present {
		return d, 0, nil
	}
	out, err := git(ctx, w.Path, "status", "--porcelain", "--untracked-files=all", "-z")
	if err != nil {
		return d, 0, err
	}
	for _, e := range splitZ(out) {
		switch {
		case strings.HasPrefix(e, "??"):
			d.Untracked++
		case len(e) > 2 && e[2] == ' ':
			d.Modified++
		}
	}
	unpushed := 0
	if n, err := gitLine(ctx, w.Clone, "rev-list", "--count", w.Branch, "--not", "--remotes", "--exclude="+w.Branch, "--branches"); err == nil {
		unpushed, _ = strconv.Atoi(n)
	}
	return d, unpushed, nil
}
