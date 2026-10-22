package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// The reasons a new session's worktree is refused. Their text is what the agent reports in
// runtime.start.rejected and the core passes on as the `reason` of an invalid-request
// (shared/src/branch.ts, WORKTREE_REASONS).
var (
	ErrNewWorktreeNotRepo      = errors.New("worktree-not-a-git-repository")
	ErrNewWorktreeNoCommit     = errors.New("worktree-no-commit")
	ErrNewWorktreeBranchBad    = errors.New("worktree-branch-invalid")
	ErrNewWorktreeBranchExists = errors.New("worktree-branch-exists")
	ErrNewWorktreePathExists   = errors.New("worktree-path-exists")
)

// MaxBranchName is the longest branch name a new session's worktree accepts.
const MaxBranchName = 200

// markerSession and markerWorktree are the keys of the git configuration of a branch that tie a worktree made for
// a new session to the session: its ID is known only after the provider created it, so the worktree cannot be
// named from it the way a handoff names its own (FindSessionWorktree reads them back).
const (
	markerSession  = "hopletsession"
	markerWorktree = "hopletworktree"
)

// NewWorktree is a worktree made for a session that has not started yet.
type NewWorktree struct {
	// Clone is the main working tree of the repository.
	Clone string
	// Path is the root of the new worktree and Branch its new branch.
	Path   string
	Branch string
	// Dir is where the session runs: the directory of the worktree that corresponds to the project the session
	// was asked for (Path itself unless the project is a subdirectory of its repository).
	Dir string
}

// ValidBranch applies the rules of `git check-ref-format --branch` to a branch name, and refuses a leading "-" so
// the name can never be read as an option. shared/src/branch.ts holds the same rules.
func ValidBranch(name string) error {
	switch {
	case name == "", len(name) > MaxBranchName, strings.HasPrefix(name, "-"), name == "@", name == "HEAD":
		return ErrNewWorktreeBranchBad
	case strings.Contains(name, ".."), strings.Contains(name, "@{"), strings.Contains(name, "//"):
		return ErrNewWorktreeBranchBad
	case strings.HasPrefix(name, "/"), strings.HasSuffix(name, "/"), strings.HasSuffix(name, "."):
		return ErrNewWorktreeBranchBad
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f || unicode.IsControl(r) || strings.ContainsRune("~^:?*[\\", r) {
			return ErrNewWorktreeBranchBad
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return ErrNewWorktreeBranchBad
		}
	}
	return nil
}

// generatedBranch is a branch name no one chose: "hoplet/" and eight hex digits.
func generatedBranch() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "hoplet/" + hex.EncodeToString(b[:]), nil
}

// worktreeSlug turns a branch name into one path component.
func worktreeSlug(branch string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '-'
	}, branch)
}

// CreateNewWorktree makes a worktree of the repository that contains project, on a new branch started at the
// project's HEAD, next to the repository's main working tree (<clone>-<branch>). branch is the new branch's name,
// "" for a generated one. Nothing is changed when it fails. Uncommitted changes of the project are not carried
// over: the worktree starts from the commit.
func CreateNewWorktree(ctx context.Context, project, branch string) (NewWorktree, error) {
	var w NewWorktree
	if branch != "" {
		if err := ValidBranch(branch); err != nil {
			return w, err
		}
		// Git has the last word on a name.
		if _, err := git(ctx, project, "check-ref-format", "--branch", branch); err != nil {
			if !isGitMissing(err) {
				return w, ErrNewWorktreeBranchBad
			}
		}
	}
	top, err := gitLine(ctx, project, "rev-parse", "--show-toplevel")
	if err != nil {
		return w, notRepo(err)
	}
	top, err = filepath.EvalSymlinks(top)
	if err != nil {
		return w, ErrNewWorktreeNotRepo
	}
	clone, err := mainWorktree(ctx, project)
	if err != nil {
		return w, notRepo(err)
	}
	realProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		return w, ErrNewWorktreeNotRepo
	}
	rel, err := filepath.Rel(top, realProject)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return w, ErrNewWorktreeNotRepo
	}
	commit, err := gitLine(ctx, project, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil || commit == "" {
		return w, ErrNewWorktreeNoCommit
	}

	pathOf := func(b string) string {
		return filepath.Join(filepath.Dir(clone), filepath.Base(clone)+"-"+worktreeSlug(b))
	}
	if branch == "" {
		for range 8 {
			g, err := generatedBranch()
			if err != nil {
				return w, err
			}
			if _, err := os.Lstat(pathOf(g)); err != nil && !BranchExists(ctx, clone, g) {
				branch = g
				break
			}
		}
		if branch == "" {
			return w, ErrNewWorktreePathExists
		}
	} else if BranchExists(ctx, clone, branch) {
		return w, ErrNewWorktreeBranchExists
	}
	path := pathOf(branch)
	if _, err := os.Lstat(path); err == nil {
		return w, ErrNewWorktreePathExists
	}
	if _, err := git(ctx, clone, "worktree", "add", "--quiet", "-b", branch, path, commit); err != nil {
		// A repeat of the checks above that lost a race.
		if BranchExists(ctx, clone, branch) {
			return w, ErrNewWorktreeBranchExists
		}
		return w, fmt.Errorf("create the worktree: %w", err)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		RemoveWorktree(ctx, clone, path, branch)
		return w, fmt.Errorf("create the worktree: %w", err)
	}
	return NewWorktree{Clone: clone, Path: real, Branch: branch, Dir: filepath.Join(real, rel)}, nil
}

// notRepo is the answer for a directory git does not accept as a working tree; a missing git is reported as it is.
func notRepo(err error) error {
	if isGitMissing(err) {
		return err
	}
	return ErrNewWorktreeNotRepo
}

// isGitMissing reports whether git itself could not be run.
func isGitMissing(err error) bool {
	return errors.Is(err, exec.ErrNotFound)
}

// Mark ties the worktree to the session in the repository's configuration so that FindSessionWorktree finds it
// from the session ID alone (ADR 0023).
func (w NewWorktree) Mark(ctx context.Context, sessionID string) error {
	if _, err := git(ctx, w.Clone, "config", "--local", "branch."+w.Branch+"."+markerSession, sessionID); err != nil {
		return err
	}
	_, err := git(ctx, w.Clone, "config", "--local", "branch."+w.Branch+"."+markerWorktree, w.Path)
	return err
}

// Remove undoes CreateNewWorktree: the worktree, its registration and the branch with its markers.
func (w NewWorktree) Remove(ctx context.Context) {
	RemoveWorktree(ctx, w.Clone, w.Path, w.Branch)
	// Deleting the branch removes its section of the configuration; a branch that never existed leaves none.
	_, _ = git(ctx, w.Clone, "config", "--local", "--remove-section", "branch."+w.Branch)
}

// markedWorktree looks in clone for the worktree made for the session by CreateNewWorktree.
func markedWorktree(ctx context.Context, clone, sessionID string) (SessionWorktree, bool) {
	out, err := git(ctx, clone, "config", "--local", "--get-regexp", `^branch\..*\.`+markerSession+`$`)
	if err != nil {
		return SessionWorktree{}, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, " ")
		if !ok || strings.TrimSpace(value) != sessionID {
			continue
		}
		branch := strings.TrimSuffix(strings.TrimPrefix(key, "branch."), "."+markerSession)
		if ValidBranch(branch) != nil {
			continue
		}
		path := registeredWorktree(ctx, clone, branch)
		if path == "" {
			// The directory is gone or was never registered: the branch alone may be left.
			if !BranchExists(ctx, clone, branch) {
				continue
			}
			recorded, _ := gitLine(ctx, clone, "config", "--local", "--get", "branch."+branch+"."+markerWorktree)
			if recorded != "" {
				if _, err := os.Lstat(recorded); err == nil {
					// Something else sits there now; it is not ours to remove.
					continue
				}
			}
			return SessionWorktree{Clone: clone, Path: recorded, Branch: branch, HasBranch: true}, true
		}
		if sameFile(path, clone) {
			continue
		}
		return SessionWorktree{Clone: clone, Path: path, Branch: branch, Present: true, HasBranch: BranchExists(ctx, clone, branch)}, true
	}
	return SessionWorktree{}, false
}

// registeredWorktree is the path of the worktree of clone that has branch checked out, "" when none has.
func registeredWorktree(ctx context.Context, clone, branch string) string {
	out, err := git(ctx, clone, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	var current string
	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			current = p
			if real, err := filepath.EvalSymlinks(p); err == nil {
				current = real
			}
		}
		if b, ok := strings.CutPrefix(line, "branch refs/heads/"); ok && b == branch {
			return current
		}
	}
	return ""
}

// sameFile reports whether a and b name the same directory.
func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}
