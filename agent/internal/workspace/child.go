package workspace

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/yangflow/hoplet/agent/internal/locate"
	"github.com/yangflow/hoplet/agent/internal/provider"
)

// MaxChangeFiles is the most files one Changes answer lists (ADR 0035): a session that touched more says so in
// Total and leaves the rest to the diff of the paths it names.
const MaxChangeFiles = 200

// scope is what a session's changes are counted against.
type scope struct {
	rev    string // the commit, or HEAD
	branch string // "" for a detached HEAD
	base   string // the commit the changes are counted against
	shared bool   // the session works in a directory no worktree was made for
}

// changeScope finds out what the changes of the session whose directory is cwd are counted against: the commit
// its own worktree started from, or HEAD when it has none or the worktree does not record one. The session is
// said to share its directory unless a worktree made for it is the directory.
func changeScope(ctx context.Context, cwd, sessionID string) (scope, error) {
	head := headCommit(ctx, cwd)
	if head == "" {
		return scope{}, errNoRepository
	}
	s := scope{rev: "HEAD", base: head, shared: true}
	if wt, ok := FindSessionWorktree(ctx, cwd, nil, sessionID); ok && wt.Present && inside(wt.Path, cwd) {
		s.shared = false
		s.branch = wt.Branch
		if base, err := gitLine(ctx, wt.Clone, "config", "--local", "--get", "branch."+wt.Branch+"."+markerBase); err == nil && validCommit(ctx, cwd, base) {
			s.rev, s.base = base, base
		}
	}
	if s.branch == "" {
		if b, err := gitLine(ctx, cwd, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
			s.branch = b
		}
	}
	return s, nil
}

// inside reports whether dir is the directory root or below it.
func inside(root, dir string) bool {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return sameFile(root, dir) || strings.HasPrefix(dir, strings.TrimRight(root, "/")+"/")
}

// validCommit reports whether rev is a full object name of a commit dir's repository has.
func validCommit(ctx context.Context, dir, rev string) bool {
	if len(rev) < 40 || strings.Trim(rev, "0123456789abcdef") != "" {
		return false
	}
	_, err := gitLine(ctx, dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	return err == nil
}

// Changes is what a session changed in its working tree: Files are the paths that differ from Base (at most
// MaxChangeFiles, by path) and Total how many there are.
type Changes struct {
	Branch string
	Base   string
	Shared bool
	Files  []Change
	Total  int
}

// ReadChanges lists the files the session changed, committed or not, without any content. A session in a worktree
// Hoplet made for it is compared with the commit the worktree started from; any other is compared with HEAD.
func ReadChanges(ctx context.Context, finders []provider.Finder, sessionID string) (Changes, error) {
	sess, err := locate.Locate(sessionID, finders...)
	if err != nil {
		return Changes{}, err
	}
	s, err := changeScope(ctx, sess.Cwd, sessionID)
	if err != nil {
		return Changes{}, err
	}
	files, err := listChangesAgainst(ctx, sess.Cwd, s.rev)
	if err != nil {
		return Changes{}, err
	}
	total := len(files)
	if total > MaxChangeFiles {
		files = files[:MaxChangeFiles]
	}
	return Changes{Branch: s.branch, Base: s.base, Shared: s.shared, Files: files, Total: total}, nil
}
