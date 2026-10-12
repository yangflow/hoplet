package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yangflow/hoplet/agent/internal/locate"
	"github.com/yangflow/hoplet/agent/internal/provider"
)

// Workspace reads (docs/spec.md#workspace). Every path they take and return is slash separated and relative
// to the session's working directory, and only what is inside that directory is listed.
const (
	maxPaths      = 400
	maxFileBytes  = 256 << 10
	maxImageBytes = 4 << 20
	maxDiffBytes  = 256 << 10
)

// Errors a workspace read reports to the core as its reason.
var (
	errPathEscape   = errors.New("path-escape")
	errNotRegular   = errors.New("not-regular-file")
	errNotInHead    = errors.New("not-in-head")
	errNotChanged   = errors.New("not-changed")
	errInvalidAt    = errors.New("invalid-at")
	errNoRepository = errors.New("not-a-git-repository")
)

// Listing is the working tree of the session directory: tracked and untracked files that are not ignored,
// at most maxPaths of them, and every changed path. Git is false, with nothing else set, when the directory
// is not inside a repository with a commit.
type Listing struct {
	Git       bool
	Commit    string
	Origin    string
	Paths     []string
	Truncated bool
	Changes   []Change
}

// List finds the session on this machine and lists its working tree.
func List(ctx context.Context, finders []provider.Finder, sessionID string) (Listing, error) {
	sess, err := locate.Locate(sessionID, finders...)
	if err != nil {
		return Listing{}, err
	}
	return listTree(ctx, sess.Cwd)
}

// headCommit is the commit HEAD names in dir, or "" when dir is not inside a repository with a commit (or
// git is not installed).
func headCommit(ctx context.Context, dir string) string {
	if _, err := exec.LookPath("git"); err != nil {
		return ""
	}
	commit, err := gitLine(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return ""
	}
	return commit
}

func listTree(ctx context.Context, dir string) (Listing, error) {
	commit := headCommit(ctx, dir)
	if commit == "" {
		return Listing{}, nil
	}
	changes, err := listChanges(ctx, dir)
	if err != nil {
		return Listing{}, err
	}
	// Index and untracked files that are not ignored, relative to dir.
	out, err := git(ctx, dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return Listing{}, err
	}
	paths, truncated := pickPaths(splitZ(out), changes)
	return Listing{Git: true, Commit: commit, Origin: originURL(ctx, dir), Paths: paths, Truncated: truncated, Changes: changes}, nil
}

// pickPaths keeps at most maxPaths of names, without the ones deleted from the working tree: every changed
// path that still exists first, so the tree shows
// what changed however large it is, then the others shallowest first and taking turns between directories, so a
// cut keeps the top of the tree. Truncated reports that names were left out.
func pickPaths(names []string, changes []Change) (paths []string, truncated bool) {
	deleted := map[string]bool{}
	for _, c := range changes {
		if c.Status == "deleted" {
			deleted[c.Path] = true
		}
	}
	exists := map[string]bool{}
	var rest []string
	for _, name := range names {
		if !exists[name] && !deleted[name] {
			exists[name] = true
			rest = append(rest, name)
		}
	}
	picked := map[string]bool{}
	for _, c := range changes {
		if c.Status != "deleted" && exists[c.Path] && !picked[c.Path] && len(paths) < maxPaths {
			picked[c.Path] = true
			paths = append(paths, c.Path)
		}
	}
	// Shallowest first; within a depth every directory gives its first entry before any gives its second, so
	// one large directory cannot crowd out its siblings.
	sort.Strings(rest)
	rank := map[string]int{}
	seenIn := map[string]int{}
	for _, name := range rest {
		dir := path.Dir(name)
		rank[name] = seenIn[dir]
		seenIn[dir]++
	}
	sort.SliceStable(rest, func(i, j int) bool {
		di, dj := strings.Count(rest[i], "/"), strings.Count(rest[j], "/")
		if di != dj {
			return di < dj
		}
		if rank[rest[i]] != rank[rest[j]] {
			return rank[rest[i]] < rank[rest[j]]
		}
		return rest[i] < rest[j]
	})
	for _, name := range rest {
		if picked[name] {
			continue
		}
		if len(paths) >= maxPaths {
			return paths, true
		}
		paths = append(paths, name)
	}
	return paths, false
}

// File is one file of the working tree or of HEAD, classified: Kind "text" with Text (at most
// maxFileBytes, Truncated beyond), "image" with MediaType and the whole file in Data, or "binary". Size is
// the file's size in bytes.
type File struct {
	Kind      string
	Size      int64
	Text      string
	Truncated bool
	MediaType string
	Data      []byte
}

// Read returns one file of the session directory: at "" or "worktree" as it is on disk, opened through
// os.Root (ADR 0010); at "head" as HEAD has it. The web never writes it.
func Read(ctx context.Context, finders []provider.Finder, sessionID, rel, at string) (File, error) {
	sess, err := locate.Locate(sessionID, finders...)
	if err != nil {
		return File{}, err
	}
	name, err := requestPath(sess.Cwd, rel)
	if err != nil {
		return File{}, err
	}
	switch at {
	case "", "worktree":
		return readWorktree(sess.Cwd, name)
	case "head":
		return readHead(ctx, sess.Cwd, name)
	}
	return File{}, errInvalidAt
}

func readWorktree(dir, name string) (File, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return File{}, err
	}
	defer root.Close()
	// Root.Open resolves links and opens beneath the same directory handle.
	file, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return File{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return File{}, err
	}
	if !info.Mode().IsRegular() {
		return File{}, errNotRegular
	}
	// Bytes are taken from the same descriptor that supplied the size; a lookahead byte detects
	// truncation even if the file grows after Stat.
	raw, err := readPrefix(file, readLimit(name, info.Size()))
	if err != nil {
		return File{}, err
	}
	return classifyFile(name, info.Size(), raw), nil
}

func readHead(ctx context.Context, dir, name string) (File, error) {
	if headCommit(ctx, dir) == "" {
		return File{}, errNoRepository
	}
	// "HEAD:./<path>" resolves the path against the working directory, not the repository root.
	spec := "HEAD:./" + name
	kind, err := gitLine(ctx, dir, "cat-file", "-t", spec)
	if err != nil {
		return File{}, errNotInHead
	}
	if kind != "blob" {
		return File{}, errNotRegular
	}
	size, err := blobSize(ctx, dir, spec)
	if err != nil {
		return File{}, err
	}
	raw, err := gitPrefix(ctx, dir, readLimit(name, size), "cat-file", "blob", spec)
	if err != nil {
		return File{}, err
	}
	return classifyFile(name, size, raw), nil
}

// requestPath checks a path that came from the network: relative, inside dir, and never reaching git's
// metadata. It returns the path cleaned and slash separated.
func requestPath(dir, rel string) (string, error) {
	if _, err := confinedPath(dir, rel); err != nil {
		return "", err
	}
	name := filepath.ToSlash(filepath.Clean(rel))
	if !localPath(name) {
		return "", errPathEscape
	}
	return name, nil
}

func confinedPath(root, rel string) (string, error) {
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", errPathEscape
	}
	full := filepath.Join(root, clean)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	sep := string(filepath.Separator)
	if fullAbs != rootAbs && !strings.HasPrefix(fullAbs, rootAbs+sep) {
		return "", errPathEscape
	}
	return fullAbs, nil
}
