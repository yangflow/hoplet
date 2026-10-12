package workspace

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoClone means the machine has no clone of the snapshot's repository to put the worktree next to.
var ErrNoClone = errors.New("no-clone")

// normalizeOrigin reduces a remote URL to host and path, so that the spellings of one repository compare
// equal: `git@host:org/repo.git`, `ssh://git@host:22/org/repo` and `https://host/org/repo/`. Local paths and
// file URLs reduce to the path. The empty string stays empty.
func normalizeOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	tidy := func(path string) string {
		path = strings.TrimRight(path, "/")
		return strings.TrimSuffix(path, ".git")
	}
	if strings.HasPrefix(raw, "/") {
		return tidy(filepath.Clean(raw))
	}
	if !strings.Contains(raw, "://") {
		// scp-like: [user@]host:path
		if host, path, ok := strings.Cut(raw, ":"); ok && !strings.Contains(host, "/") {
			if at := strings.LastIndex(host, "@"); at >= 0 {
				host = host[at+1:]
			}
			return strings.ToLower(host) + "/" + tidy(strings.TrimLeft(path, "/"))
		}
		return tidy(filepath.Clean(raw))
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Scheme == "file" || u.Host == "" {
		return tidy(filepath.Clean(u.Path))
	}
	return strings.ToLower(u.Hostname()) + "/" + tidy(strings.TrimLeft(u.Path, "/"))
}

// mainWorktree is the main working tree of the repository that contains dir (dir may itself be a linked
// worktree), as a real path. It fails when dir is not in a working tree.
func mainWorktree(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			return filepath.EvalSymlinks(path)
		}
	}
	return "", ErrNotGitRepo
}

// validDirectory reports whether p is an absolute path to an existing directory, free of NUL.
func validDirectory(p string) bool {
	if p == "" || len(p) > 4096 || strings.ContainsRune(p, 0) || !filepath.IsAbs(p) {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// FindClone is the clone a handoff of a snapshot with this origin would use without a target path: the main
// working tree of a watched project whose `origin` is origin, or "" when there is none.
func FindClone(ctx context.Context, watched []string, origin string) string {
	clone, err := ResolveClone(ctx, watched, "", origin)
	if err != nil {
		return ""
	}
	return clone
}

// ResolveClone finds the clone a handed-off worktree is added to: the main working tree of a watched
// project whose `origin` is the snapshot's, else the repository at targetPath, else ErrNoClone.
func ResolveClone(ctx context.Context, watched []string, targetPath, origin string) (string, error) {
	if want := normalizeOrigin(origin); want != "" {
		seen := map[string]bool{}
		for _, p := range watched {
			if !validDirectory(p) {
				continue
			}
			main, err := mainWorktree(ctx, p)
			if err != nil || seen[main] {
				continue
			}
			seen[main] = true
			if have, err := gitLine(ctx, main, "config", "--get", "remote.origin.url"); err == nil && normalizeOrigin(have) == want {
				return main, nil
			}
		}
	}
	if targetPath != "" {
		if !validDirectory(targetPath) {
			return "", ErrNoClone
		}
		main, err := mainWorktree(ctx, targetPath)
		if err != nil {
			return "", ErrNoClone
		}
		return main, nil
	}
	return "", ErrNoClone
}
