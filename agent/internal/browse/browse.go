// Package browse lists the directories of a machine under one root (the agent's browse_root). It reads
// directory names only, never file contents, and never leaves the root: the requested path is resolved with
// its symlinks and every later access goes through os.Root.
package browse

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxEntries bounds one listing.
const MaxEntries = 500

// Home is the requested path that means the browse root.
const Home = "~"

// Reasons a request is refused.
var (
	ErrRelative   = errors.New("relative path")
	ErrOutside    = errors.New("outside the browse root")
	ErrNotDir     = errors.New("not a directory")
	ErrUnreadable = errors.New("unreadable")
)

type Entry struct {
	Name string
	Git  bool
}

type Listing struct {
	Path      string
	Parent    *string
	Root      string
	Entries   []Entry
	Truncated bool
}

// List returns the visible directories of path. An empty path or "~" is the root itself.
func List(root, path string) (Listing, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Listing{}, ErrUnreadable
	}
	realRoot = filepath.Clean(realRoot)
	target := realRoot
	if path != "" && path != Home {
		if !filepath.IsAbs(path) {
			return Listing{}, ErrRelative
		}
		target, err = filepath.EvalSymlinks(filepath.Clean(path))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) {
				return Listing{}, ErrNotDir
			}
			return Listing{}, ErrUnreadable
		}
		target = filepath.Clean(target)
	}
	rel, err := filepath.Rel(realRoot, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Listing{}, ErrOutside
	}

	rootFS, err := os.OpenRoot(realRoot)
	if err != nil {
		return Listing{}, ErrUnreadable
	}
	defer rootFS.Close()
	info, err := rootFS.Stat(rel)
	if err != nil {
		return Listing{}, ErrUnreadable
	}
	if !info.IsDir() {
		return Listing{}, ErrNotDir
	}
	dir, err := rootFS.Open(rel)
	if err != nil {
		return Listing{}, ErrUnreadable
	}
	defer dir.Close()
	des, err := dir.ReadDir(-1)
	if err != nil {
		return Listing{}, ErrUnreadable
	}

	var names []string
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if de.Type()&fs.ModeSymlink != 0 {
			// A link counts when it resolves to a directory inside the root; os.Root refuses the rest.
			st, err := rootFS.Stat(filepath.Join(rel, name))
			if err != nil || !st.IsDir() {
				continue
			}
		} else if !de.IsDir() {
			continue
		}
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := strings.ToLower(names[i]), strings.ToLower(names[j])
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
	out := Listing{Path: target, Root: realRoot, Entries: []Entry{}}
	if len(names) > MaxEntries {
		names = names[:MaxEntries]
		out.Truncated = true
	}
	for _, name := range names {
		_, gitErr := rootFS.Lstat(filepath.Join(rel, name, ".git"))
		out.Entries = append(out.Entries, Entry{Name: name, Git: gitErr == nil})
	}
	if target != realRoot {
		parent := filepath.Dir(target)
		out.Parent = &parent
	}
	return out, nil
}
