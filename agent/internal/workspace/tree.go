package workspace

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// localPath reports whether name is a relative slash-separated path that stays inside the directory it is
// resolved against and never touches git's own metadata.
func localPath(name string) bool {
	if name == "" || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return false
	}
	if !filepath.IsLocal(filepath.FromSlash(name)) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}

// safeLink reports whether a symbolic link at name may point at target: relative, and resolving inside the
// tree it is part of.
func safeLink(name, target string) bool {
	if target == "" || strings.ContainsRune(target, 0) || strings.HasPrefix(target, "/") {
		return false
	}
	resolved := path.Clean(path.Join(path.Dir(name), target))
	if resolved == "." {
		return true
	}
	return localPath(resolved)
}

// untrackedFile is one untracked, non-ignored entry of a working tree that a snapshot carries.
type untrackedFile struct {
	Path string // slash separated, relative to the repository root
	Link string // target of a symbolic link, empty for a regular file
	Perm fs.FileMode
	Size int64
}

// untrackedFiles lists the untracked files that are not ignored, in git's order. It leaves out what a
// snapshot cannot carry safely: nested repositories, special files, and symbolic links that are absolute or
// leave the tree. The same list feeds the archive and the tree hash, so both always agree.
func untrackedFiles(ctx context.Context, top string) ([]untrackedFile, error) {
	out, err := git(ctx, top, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(top)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var files []untrackedFile
	for _, name := range splitZ(out) {
		if strings.HasSuffix(name, "/") || !localPath(name) {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil {
			continue // removed since the listing
		}
		switch {
		case info.Mode().IsRegular():
			files = append(files, untrackedFile{Path: name, Perm: info.Mode().Perm(), Size: info.Size()})
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := root.Readlink(name)
			if err != nil || !safeLink(name, link) {
				continue
			}
			files = append(files, untrackedFile{Path: name, Link: link})
		}
	}
	return files, nil
}

// TreeHash is the git tree object id of the working tree at top: HEAD with every tracked change and every
// untracked file that is not ignored applied, as `git add -A` followed by `git write-tree` would record it,
// but computed in a private index so the repository's index, objects and refs stay as they are. Two working
// trees with the same files and executable bits have the same hash on every machine.
func TreeHash(ctx context.Context, top string) (string, error) {
	untracked, err := untrackedFiles(ctx, top)
	if err != nil {
		return "", err
	}
	changed, err := git(ctx, top, "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", "HEAD")
	if err != nil {
		return "", err
	}
	var paths bytes.Buffer
	seen := map[string]bool{}
	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		paths.WriteString(name)
		paths.WriteByte(0)
	}
	for _, name := range splitZ(changed) {
		add(name)
	}
	for _, f := range untracked {
		add(f.Path)
	}
	dir, err := os.MkdirTemp("", "hoplet-tree-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	index := filepath.Join(dir, "index")
	read := gitCommand(ctx, top, "read-tree", "HEAD")
	read.Env = append(read.Env, "GIT_INDEX_FILE="+index)
	if out, err := read.CombinedOutput(); err != nil {
		return "", wrap("read-tree", err, out)
	}
	if paths.Len() > 0 {
		update := gitCommand(ctx, top, "-c", "core.autocrlf=false", "-c", "core.safecrlf=false",
			"update-index", "--add", "--remove", "--replace", "--info-only", "-z", "--stdin")
		update.Env = append(update.Env, "GIT_INDEX_FILE="+index)
		update.Stdin = &paths
		if out, err := update.CombinedOutput(); err != nil {
			return "", wrap("update-index", err, out)
		}
	}
	write := gitCommand(ctx, top, "write-tree", "--missing-ok")
	write.Env = append(write.Env, "GIT_INDEX_FILE="+index)
	out, err := write.Output()
	if err != nil {
		return "", wrap("write-tree", err, nil)
	}
	return strings.TrimSpace(string(out)), nil
}
