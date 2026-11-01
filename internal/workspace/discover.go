package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// maxDepth is how many directory levels below the root Discover looks at.
const maxDepth = 3

// Repo is a git repository found under a working directory.
type Repo struct {
	Path   string // relative to the working directory, slash-separated; "." for the directory itself
	Abs    string // absolute worktree path
	Parent string // Path of the enclosing repo for a submodule or nested repo; "" otherwise
	// Err, when set, says why the repository is reported as an error and not
	// examined: git refuses it, or its layout is not supported.
	Err string
	// Ceiling, for a repository found below the working directory, is the
	// directory above it, which git must not search: a .git there that is
	// not a valid git dir must not make git use an enclosing repository.
	Ceiling string
}

// gitEnv keeps git from looking above the repository.
func (r Repo) gitEnv() []string {
	if r.Ceiling == "" {
		return nil
	}
	return []string{"GIT_CEILING_DIRECTORIES=" + r.Ceiling}
}

// Discover finds the git repositories at root and in the directories up to
// three levels below it, sorted by Path. A directory holding .git (a
// directory or a file) is a repository, and the search goes on inside it for
// submodules and nested repositories. node_modules, vendor, .git and symlinked
// directories are skipped; directories that cannot be read are skipped
// silently. Parent is left empty; Git.Discover assigns it once it knows which
// directories are repositories.
func Discover(ctx context.Context, root string) ([]Repo, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("workspace: resolve %s: %w", root, err)
	}
	fi, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("workspace: %s is not a directory", root)
	}
	var repos []Repo
	if err := walk(ctx, root, ".", 0, &repos); err != nil {
		return nil, err
	}
	slices.SortFunc(repos, func(a, b Repo) int { return strings.Compare(a.Path, b.Path) })
	return repos, nil
}

func walk(ctx context.Context, root, rel string, depth int, repos *[]Repo) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("workspace: discover: %w", err)
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := os.Lstat(filepath.Join(abs, ".git")); err == nil {
		*repos = append(*repos, Repo{Path: rel, Abs: abs})
	}
	if depth == maxDepth {
		return nil
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil // unreadable: skipped
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || name == ".git" || name == "node_modules" || name == "vendor" {
			continue // IsDir is false for a symlink to a directory
		}
		child := name
		if rel != "." {
			child = rel + "/" + name
		}
		if err := walk(ctx, root, child, depth+1, repos); err != nil {
			return err
		}
	}
	return nil
}

// Discover is Discover plus the repository that contains root, when root
// lies inside one but not at its top (a session started in a subdirectory of
// a repository): its Path is the relative path from root to its top, such as
// "../..", the whole repository is snapshotted and diffed, and the
// repositories found below root become its children. A repository whose top
// is the user's home directory or above it is not adopted this way.
//
// A directory counts as a repository only if its own .git is a valid git
// dir, and git is told not to look above it; otherwise an invalid .git (an
// empty directory, a broken gitfile) would make git use an enclosing
// repository. Such directories are skipped. A repository git refuses (the
// owner differs, say) or whose layout is not supported (see unsupported) is
// returned with Err set.
func (g Git) Discover(ctx context.Context, root string) ([]Repo, error) {
	repos, err := Discover(ctx, root)
	if err != nil {
		return nil, err
	}
	valid := repos[:0]
	for _, r := range repos {
		if parent, err := filepath.EvalSymlinks(filepath.Dir(r.Abs)); err == nil {
			r.Ceiling = parent
		}
		own, refusal := g.ownsGitDir(ctx, r)
		switch {
		case refusal != "":
			r.Err = refusal
		case !own:
			continue
		default:
			r.Err = g.unsupported(ctx, r)
		}
		valid = append(valid, r)
	}
	repos = assignParents(valid)

	abs, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("workspace: resolve %s: %w", root, err)
	}
	out, terr := g.output(ctx, runOpts{Dir: abs}, "rev-parse", "--show-toplevel")
	top, failure := strings.TrimRight(string(out), "\n"), ""
	if terr != nil {
		var ee *exec.ExitError
		if !errors.As(terr, &ee) {
			return nil, terr
		}
		if strings.Contains(terr.Error(), "not a git repository") {
			return repos, nil
		}
		// git refuses the repository: it still encloses root, but only its
		// error can be shown.
		if top = findTop(abs); top == "" {
			return repos, nil
		}
		failure = terr.Error()
	}
	rel, err := filepath.Rel(abs, top)
	// A home repository (dotfiles) that merely encloses the session is not
	// the session's project, and every personal session lives in it.
	if err != nil || rel == "." || g.isHomeOrAbove(top) {
		return repos, nil
	}
	enclosing := Repo{Path: filepath.ToSlash(rel), Abs: top, Err: failure}
	if failure == "" {
		enclosing.Err = g.unsupported(ctx, enclosing)
	}
	for i := range repos {
		if repos[i].Parent == "" {
			repos[i].Parent = enclosing.Path
		}
	}
	repos = append(repos, enclosing)
	slices.SortFunc(repos, func(a, b Repo) int { return strings.Compare(a.Path, b.Path) })
	return repos, nil
}

// unsupported says why r is not supported, or returns "". Layouts that need
// special handling to stay safe are refused rather than handled:
//   - a .git that is a symlink, and a worktree configured elsewhere than next
//     to .git (core.worktree);
//   - a partial clone, where reading history can fetch into the repository;
//   - a repository whose top is the user's home directory or one of its
//     ancestors (a dotfiles repository), which would snapshot the whole home.
func (g Git) unsupported(ctx context.Context, r Repo) string {
	const prefix = "unsupported repository layout: "
	if fi, err := os.Lstat(filepath.Join(r.Abs, ".git")); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return prefix + ".git is a symlink"
	}
	if g.isHomeOrAbove(r.Abs) {
		return prefix + "its top is your home directory or above it"
	}
	out, _ := g.output(ctx, runOpts{Dir: r.Abs, Env: r.gitEnv()}, "config", "-z", "--get-regexp", `^(core\.worktree|extensions\.partialclone|remote\..*\.promisor)$`)
	for _, kv := range strings.Split(string(out), "\x00") {
		switch key, val, _ := strings.Cut(kv, "\n"); {
		case key == "core.worktree":
			// A submodule has it, pointing at the directory its .git file is in.
			if top, err := g.output(ctx, runOpts{Dir: r.Abs, Env: r.gitEnv()}, "rev-parse", "--show-toplevel"); err != nil || !sameDir(strings.TrimSpace(string(top)), r.Abs) {
				return prefix + "core.worktree puts the worktree elsewhere"
			}
		case key == "extensions.partialclone", strings.HasSuffix(key, ".promisor") && val == "true":
			return prefix + "it is a partial clone"
		}
	}
	return ""
}

// sameDir reports whether a and b are the same directory.
func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// findTop finds the nearest directory at or above dir that holds .git.
func findTop(dir string) string {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// isHomeOrAbove reports whether top is the user's home directory ($HOME of
// the environment git runs with) or one of its ancestors.
func (g Git) isHomeOrAbove(top string) bool {
	home := ""
	for _, kv := range g.Env {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = v
		}
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	home, err1 := filepath.EvalSymlinks(home)
	top, err2 := filepath.EvalSymlinks(top)
	return err1 == nil && err2 == nil && (home == top || strings.HasPrefix(home, strings.TrimSuffix(top, "/")+"/"))
}

// ownsGitDir reports whether the .git in r.Abs is a valid git dir that git
// resolves to: the directory itself, or the target of a gitfile. When git
// fails for a reason other than "not a git repository", it returns git's
// message instead.
func (g Git) ownsGitDir(ctx context.Context, r Repo) (own bool, refusal string) {
	out, err := g.output(ctx, runOpts{Dir: r.Abs, Env: r.gitEnv()}, "rev-parse", "--absolute-git-dir")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && !strings.Contains(err.Error(), "not a git repository") {
			return false, err.Error()
		}
		return false, ""
	}
	want := filepath.Join(r.Abs, ".git")
	if fi, err := os.Stat(want); err == nil && !fi.IsDir() { // a gitfile
		b, err := os.ReadFile(filepath.Clean(want))
		target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
		if err != nil || !ok {
			return false, ""
		}
		want = strings.TrimSpace(target)
		if !filepath.IsAbs(want) {
			want = filepath.Join(r.Abs, want)
		}
	}
	return sameDir(strings.TrimRight(string(out), "\n"), want), ""
}

// assignParents sets each repository's Parent to the nearest enclosing one
// of the list.
func assignParents(repos []Repo) []Repo {
	for i := range repos {
		repos[i].Parent = ""
		for _, p := range repos {
			encloses := p.Path == "." || strings.HasPrefix(repos[i].Path, p.Path+"/")
			if p.Path != repos[i].Path && encloses && len(p.Path) > len(repos[i].Parent) {
				repos[i].Parent = p.Path
			}
		}
	}
	return repos
}
