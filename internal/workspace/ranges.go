package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yangflow/hoplet/proto"
)

const (
	// maxBranchCommits is the most commits listed for a branch, and
	// maxRecentCommits for the latest commits of HEAD.
	maxBranchCommits = 200
	maxRecentCommits = 50
)

// errNoDefaultBranch says a repository has no default branch to compare
// with.
var errNoDefaultBranch = errors.New("no default branch")

// rangeOf is p's range, DiffRangeSession when unset.
func rangeOf(r proto.DiffRange) proto.DiffRange {
	if r == "" {
		return proto.DiffRangeSession
	}
	return r
}

// checkRange rejects a range that is unknown, or a commit range without a
// repository or a commit id. Only object ids are accepted for a commit, so
// nothing a client sends is read as an option or a revision expression.
func checkRange(rng proto.DiffRange, repo, commit string) error {
	switch rangeOf(rng) {
	case proto.DiffRangeSession, proto.DiffRangeUncommitted, proto.DiffRangeBranch:
		return nil
	case proto.DiffRangeCommit:
		if repo == "" {
			return fmt.Errorf("%w: a commit range needs a repository", ErrInvalidPath)
		}
		if !isObjectID(commit) {
			return fmt.Errorf("%w: %q is not a commit id", ErrInvalidPath, commit)
		}
		return nil
	}
	return fmt.Errorf("%w: unknown diff range %q", ErrInvalidPath, rng)
}

// isObjectID reports whether s is a full or abbreviated object id.
func isObjectID(s string) bool {
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// userObjects is the user's object directory of r, for
// GIT_ALTERNATE_OBJECT_DIRECTORIES. A path that the variable cannot hold
// as it is (a colon or a leading quote) is not supported.
func (g Git) userObjects(ctx context.Context, r Repo) (string, error) {
	info, err := g.repoInfo(ctx, r)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(info.commonDir, "objects")
	if strings.ContainsRune(dir, filepath.ListSeparator) || strings.HasPrefix(dir, `"`) {
		return "", fmt.Errorf("%w: the git directory path %s holds a %q", ErrUnsupported, info.commonDir, filepath.ListSeparator)
	}
	return dir, nil
}

// revParse resolves rev in the user's repository; ok is false when it names
// nothing.
func (g Git) revParse(ctx context.Context, r Repo, rev string) (id string, ok bool, err error) {
	out, err := g.output(ctx, runOpts{Dir: r.Abs, Env: r.gitEnv()}, "rev-parse", "-q", "--verify", "--end-of-options", rev)
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

// defaultBranch finds the branch r's work is compared with: the one
// origin/HEAD points to, else main, else master. name drops "origin/".
func (g Git) defaultBranch(ctx context.Context, r Repo) (ref, name string, err error) {
	out, err := g.output(ctx, runOpts{Dir: r.Abs, Env: r.gitEnv()}, "symbolic-ref", "-q", "refs/remotes/origin/HEAD")
	var ee *exec.ExitError
	switch {
	case err == nil:
		ref = strings.TrimSpace(string(out))
		return ref, strings.TrimPrefix(ref, "refs/remotes/origin/"), nil
	case !errors.As(err, &ee) || ee.ExitCode() != 1:
		return "", "", err
	}
	for _, b := range []string{"main", "master"} {
		if _, ok, err := g.revParse(ctx, r, "refs/heads/"+b+"^{commit}"); err != nil {
			return "", "", err
		} else if ok {
			return "refs/heads/" + b, b, nil
		}
	}
	return "", "", errNoDefaultBranch
}

// sides are the trees a range compares in one repository. snapshot says the
// new side is the worktree now, which a snapshot records; otherwise it is
// newTree.
type sides struct {
	oldTree, newTree string
	snapshot         bool
	base             string // the default branch, for a branch range
}

// rangeSides works out what a range other than the session's compares in
// r. empty is the empty tree's id.
func (g Git) rangeSides(ctx context.Context, r Repo, rng proto.DiffRange, commit, empty string) (sides, error) {
	switch rng {
	case proto.DiffRangeUncommitted:
		tree, ok, err := g.revParse(ctx, r, "HEAD^{tree}")
		if err != nil {
			return sides{}, err
		}
		if !ok {
			tree = empty // no commit yet
		}
		return sides{oldTree: tree, snapshot: true}, nil
	case proto.DiffRangeBranch:
		ref, name, err := g.defaultBranch(ctx, r)
		if err != nil {
			return sides{}, err
		}
		out, err := g.output(ctx, runOpts{Dir: r.Abs, Env: r.gitEnv()}, "merge-base", "HEAD", ref)
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return sides{}, fmt.Errorf("no history in common with %s", name)
		}
		if err != nil {
			return sides{}, err
		}
		tree, ok, err := g.revParse(ctx, r, strings.TrimSpace(string(out))+"^{tree}")
		if err != nil {
			return sides{}, err
		}
		if !ok {
			return sides{}, fmt.Errorf("the fork point with %s cannot be read", name)
		}
		return sides{oldTree: tree, snapshot: true, base: name}, nil
	case proto.DiffRangeCommit:
		id, ok, err := g.revParse(ctx, r, commit+"^{commit}")
		if err != nil {
			return sides{}, err
		}
		if !ok {
			return sides{}, fmt.Errorf("commit %s is not in this repository", commit)
		}
		tree, _, err := g.revParse(ctx, r, id+"^{tree}")
		if err != nil {
			return sides{}, err
		}
		parent, ok, err := g.revParse(ctx, r, id+"^1^{tree}")
		if err != nil {
			return sides{}, err
		}
		if !ok {
			parent = empty // a root commit
		}
		return sides{oldTree: parent, newTree: tree}, nil
	}
	return sides{}, fmt.Errorf("%w: unknown diff range %q", ErrInvalidPath, rng)
}

// rangeDiff is diff for a range other than the session's: it needs no
// baseline and lists the repositories there are now.
func (s *Service) rangeDiff(ctx context.Context, session proto.SessionID, dir string, p proto.DiffParams) (proto.SessionDiff, error) {
	out := proto.SessionDiff{Session: session, Repos: []proto.RepoDiff{}}
	found, err := s.Git.Discover(ctx, dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return proto.SessionDiff{}, err
	}
	budget, filesLeft := orDefault(s.maxBytes, maxResponseBytes), orDefault(s.maxTotalFiles, maxResponseTotalFiles)
	for _, r := range found {
		if p.Repo != "" && p.Repo != r.Path {
			continue
		}
		if err := ctx.Err(); err != nil {
			return proto.SessionDiff{}, fmt.Errorf("workspace: diff: %w", err)
		}
		rd := s.rangeRepoDiff(ctx, r, p, budget, min(orDefault(s.maxFiles, maxResponseFiles), filesLeft))
		size := fit(&rd, budget)
		sealSHA(&rd, p.Patch)
		budget -= size
		filesLeft -= len(rd.Files)
		out.Repos = append(out.Repos, rd)
	}
	return out, nil
}

// rangeRepoDiff diffs one repository over p's range; budget and fileLimit
// are as for repoDiff.
func (s *Service) rangeRepoDiff(ctx context.Context, r Repo, p proto.DiffParams, budget, fileLimit int) proto.RepoDiff {
	rd := proto.RepoDiff{Path: r.Path, Parent: r.Parent, State: proto.RepoOK, Files: []proto.FileDiff{}}
	fail := func(err error) proto.RepoDiff {
		rd.State, rd.Error = proto.RepoError, err.Error()
		return rd
	}
	if r.Err != "" {
		return fail(errors.New(r.Err))
	}
	objects, err := ObjectDir(ctx, s.Home, r, s.Git)
	if err != nil {
		return fail(err)
	}
	g := s.Git
	if g.alt, err = g.userObjects(ctx, r); err != nil {
		return fail(err)
	}
	empty, err := g.EmptyTree(ctx, r, objects)
	if err != nil {
		return fail(err)
	}
	sd, err := g.rangeSides(ctx, r, p.Range, p.Commit, empty)
	if err != nil {
		return fail(err)
	}
	rd.Base = sd.base
	if sd.snapshot {
		snap, err := s.snapshot(ctx, r)
		if err != nil {
			return fail(err)
		}
		if s.afterSnapshot != nil {
			s.afterSnapshot()
		}
		sd.newTree, rd.Skipped = snap.Tree, snap.Skipped
	}
	g.MaxPatch = min(g.maxPatch(), max(budget, 0))
	switch {
	case p.Patch && budget > 0:
		rd.Files, rd.Patch, rd.Truncated, err = g.Diff(ctx, r, objects, sd.oldTree, sd.newTree)
	default:
		rd.Files, err = g.DiffFiles(ctx, r, objects, sd.oldTree, sd.newTree)
		rd.Truncated = p.Patch && len(rd.Files) > 0
	}
	if err != nil {
		return fail(err)
	}
	if len(rd.Files) > fileLimit {
		rd.Files, rd.FilesTruncated = rd.Files[:fileLimit], true
	}
	if len(rd.Skipped) > maxResponseSkipped {
		rd.Skipped, rd.SkippedTruncated = rd.Skipped[:maxResponseSkipped], true
	}
	rd.Branch = g.branch(ctx, r)
	h := sha256.New()
	for _, part := range []string{string(p.Range), sd.oldTree, sd.newTree, rd.Branch, rd.Base, string(rd.State), skippedKey(rd.Skipped)} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	rd.SHA = hex.EncodeToString(h.Sum(nil))
	return rd
}

// readRange reads rel on one side of a range other than the session's: the
// old side when old is set, else the new side, which is the file on disk
// except for a commit.
func (s *Service) readRange(ctx context.Context, dir, rel string, p proto.FilesReadParams, out proto.FileContent) (proto.FileContent, error) {
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	repos, err := s.Git.Discover(ctx, dir)
	if err != nil {
		return proto.FileContent{}, err
	}
	r, ok := innermost(filepath.Dir(abs), repos)
	if !ok {
		return proto.FileContent{}, fmt.Errorf("%w: %s is not in a git repository", ErrNoBaseline, rel)
	}
	if r.Err != "" {
		return proto.FileContent{}, fmt.Errorf("workspace: %s", r.Err)
	}
	objects, err := ObjectDir(ctx, s.Home, r, s.Git)
	if err != nil {
		return proto.FileContent{}, err
	}
	empty, err := s.Git.EmptyTree(ctx, r, objects)
	if err != nil {
		return proto.FileContent{}, err
	}
	sd, err := s.Git.rangeSides(ctx, r, p.Range, p.Commit, empty)
	if err != nil {
		return proto.FileContent{}, fmt.Errorf("workspace: %w", err)
	}
	tree := sd.newTree
	if p.Baseline {
		tree = sd.oldTree
	}
	if tree == empty {
		out.Missing = true
		return out, nil
	}
	inRepo, err := filepath.Rel(r.Abs, abs)
	if err != nil {
		return proto.FileContent{}, fmt.Errorf("workspace: %w", err)
	}
	// The trees and their files are the user's objects: read them in place.
	return s.readTree(ctx, runOpts{Dir: r.Abs, Env: r.gitEnv()}, tree, filepath.ToSlash(inRepo), out)
}

// Commits lists, per repository under dir, the commits a commit range can
// show: those of the current branch that the default branch does not have,
// or, on the default branch, when there are none, or when there is no
// default branch, the latest commits of HEAD.
func (s *Service) Commits(ctx context.Context, session proto.SessionID, dir string, p proto.CommitsParams) (proto.SessionCommits, error) {
	if s.Git.Bin == "" {
		return proto.SessionCommits{}, ErrNoGit
	}
	ctx, cancel := context.WithTimeout(ctx, orDefault(s.diffTimeout, defaultDiffTimeout))
	defer cancel()
	out := proto.SessionCommits{Session: session, Repos: []proto.RepoCommits{}}
	found, err := s.Git.Discover(ctx, dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return proto.SessionCommits{}, err
	}
	for _, r := range found {
		if p.Repo != "" && p.Repo != r.Path {
			continue
		}
		rc := proto.RepoCommits{Path: r.Path, Commits: []proto.Commit{}}
		if err := s.Git.commits(ctx, r, &rc); err != nil {
			if ctx.Err() != nil {
				return proto.SessionCommits{}, fmt.Errorf("workspace: commits: %w", ctx.Err())
			}
			rc.Commits, rc.Truncated, rc.Error = []proto.Commit{}, false, err.Error()
		}
		out.Repos = append(out.Repos, rc)
	}
	return out, nil
}

// commits fills rc for r.
func (g Git) commits(ctx context.Context, r Repo, rc *proto.RepoCommits) error {
	if r.Err != "" {
		return errors.New(r.Err)
	}
	if _, ok, err := g.revParse(ctx, r, "HEAD^{commit}"); err != nil || !ok {
		return err // no commit yet: nothing to list
	}
	ref, name, err := g.defaultBranch(ctx, r)
	switch {
	case errors.Is(err, errNoDefaultBranch):
	case err != nil:
		return err
	default:
		rc.Base = name
		if g.branch(ctx, r) != name {
			if rc.Commits, rc.Truncated, err = g.log(ctx, r, maxBranchCommits, ref+"..HEAD"); err != nil || len(rc.Commits) > 0 {
				return err
			}
		}
	}
	rc.Commits, rc.Truncated, err = g.log(ctx, r, maxRecentCommits, "HEAD")
	return err
}

// log lists at most n commits of revs, newest first.
func (g Git) log(ctx context.Context, r Repo, n int, revs string) ([]proto.Commit, bool, error) {
	out, err := g.output(ctx, runOpts{Dir: r.Abs, Env: r.gitEnv()}, "log", "-z", "--no-show-signature", "--no-color",
		"--format=%H%x1f%s", fmt.Sprintf("--max-count=%d", n+1), "--end-of-options", revs)
	if err != nil {
		return nil, false, err
	}
	list := []proto.Commit{}
	for _, entry := range strings.Split(string(out), "\x00") {
		id, subject, ok := strings.Cut(entry, "\x1f")
		if !ok {
			continue
		}
		list = append(list, proto.Commit{SHA: strings.TrimSpace(id), Subject: strings.ToValidUTF8(subject, "�")})
	}
	if len(list) > n {
		return list[:n], true, nil
	}
	return list, false, nil
}
