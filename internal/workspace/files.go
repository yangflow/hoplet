package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yangflow/hoplet/proto"
)

const (
	// maxReadBytes is the largest file files.read returns the text of.
	maxReadBytes = 2 << 20
	// maxListEntries bounds a listing, one level or every file.
	maxListEntries = 20000
	// binarySniff is how much of a file is looked at for a NUL byte, as git
	// does to tell a binary file.
	binarySniff = 8000
	// maxListOutput bounds what git ls-files may print for one repository.
	maxListOutput = 16 << 20
)

// ErrInvalidPath says a path names nothing inside the working directory: it
// is absolute, climbs out with "..", or is not a regular file to read.
var ErrInvalidPath = errors.New("workspace: invalid path")

// ErrNoBaseline says a file has no baseline to read: it is not in a git
// repository, or the session has no baseline of that repository.
var ErrNoBaseline = errors.New("workspace: no baseline for this file")

// ErrUnsupported says a request asks for something files.list does not do.
var ErrUnsupported = errors.New("workspace: unsupported")

// cleanRel turns a client's path into a clean slash-separated path relative
// to the working directory, "" for the directory itself.
func cleanRel(p string) (string, error) {
	p = filepath.ToSlash(p)
	if path.IsAbs(p) || filepath.IsAbs(p) {
		return "", fmt.Errorf("%w: %s is absolute", ErrInvalidPath, p)
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("%w: %s is outside the working directory", ErrInvalidPath, p)
	}
	if c == "." {
		return "", nil
	}
	return c, nil
}

// resolve returns the absolute path of rel under dir. Like a snapshot, it
// treats a path whose parent directory is a symbolic link (or not a
// directory) as missing, so nothing is reached through a link.
func resolve(dir, rel string) (string, error) {
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if rel == "" {
		return abs, nil
	}
	parts := strings.Split(rel, "/")
	cur := dir
	for _, name := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, name)
		fi, err := os.Lstat(cur)
		if err != nil {
			return "", err
		}
		if !fi.IsDir() {
			return "", fmt.Errorf("workspace: %s: %w", rel, fs.ErrNotExist)
		}
	}
	return abs, nil
}

// dsStore is the file Finder leaves in the directories it shows: never
// listed, whatever .gitignore says.
const dsStore = ".DS_Store"

// List lists a session's working directory dir: one directory's entries,
// directories first and then by name, or with All every file under it. It
// leaves out .git, .DS_Store and, unless Ignored, what .gitignore ignores; symbolic
// links are listed and never followed.
func (s *Service) List(ctx context.Context, dir string, p proto.FilesListParams) (proto.FileList, error) {
	rel, err := cleanRel(p.Path)
	if err != nil {
		return proto.FileList{}, err
	}
	out := proto.FileList{Session: p.Session, Path: rel, Entries: []proto.FileEntry{}}
	abs, err := resolve(dir, rel)
	if err != nil {
		return proto.FileList{}, err
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return proto.FileList{}, fmt.Errorf("workspace: %w", err)
	}
	if !fi.IsDir() {
		return proto.FileList{}, fmt.Errorf("%w: %s is not a directory", ErrInvalidPath, p.Path)
	}
	repos, err := s.Git.Discover(ctx, dir)
	if err != nil && !errors.Is(err, ErrNoGit) {
		return proto.FileList{}, err
	}
	if p.All {
		if p.Ignored {
			return proto.FileList{}, fmt.Errorf("%w: listing every file with the ignored ones", ErrUnsupported)
		}
		out.Entries, out.Truncated, err = s.listAll(ctx, dir, abs, repos)
		return out, err
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		return proto.FileList{}, fmt.Errorf("workspace: %w", err)
	}
	var entries []proto.FileEntry
	for _, de := range des {
		if de.Name() == ".git" || de.Name() == dsStore {
			continue
		}
		e := proto.FileEntry{Path: path.Join(rel, de.Name())}
		switch t := de.Type(); {
		case t&fs.ModeSymlink != 0:
			e.Kind = proto.FileKindSymlink
		case t.IsDir():
			e.Kind = proto.FileKindDir
		case t.IsRegular():
			e.Kind = proto.FileKindFile
			if info, err := de.Info(); err == nil {
				e.Size = info.Size()
			}
		default:
			continue // devices, sockets and FIFOs are not files to open
		}
		entries = append(entries, e)
	}
	ignored := s.ignoredIn(ctx, abs, repos, entries)
	for _, e := range entries {
		if ignored[e.Path] {
			if !p.Ignored {
				continue
			}
			e.Ignored = true
		}
		out.Entries = append(out.Entries, e)
	}
	slices.SortFunc(out.Entries, func(a, b proto.FileEntry) int {
		if ad, bd := a.Kind == proto.FileKindDir, b.Kind == proto.FileKindDir; ad != bd {
			if ad {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	if limit := orDefault(s.maxList, maxListEntries); len(out.Entries) > limit {
		out.Entries, out.Truncated = out.Entries[:limit], true
	}
	return out, nil
}

// innermost is the repository that holds abs, the deepest one when they
// nest; ok is false when abs is in none or the repository cannot be used.
func innermost(abs string, repos []Repo) (Repo, bool) {
	var best Repo
	found := false
	for _, r := range repos {
		if r.Err != "" || !within(abs, r.Abs) {
			continue
		}
		if !found || len(r.Abs) > len(best.Abs) {
			best, found = r, true
		}
	}
	return best, found
}

// within reports whether p is dir or lies under it.
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

// ignoredIn asks git which of the entries of the directory abs .gitignore
// ignores. It never reads the repository's index (--no-index), so a file
// tracked but matching a pattern counts as ignored too. Without git, or
// outside a repository, nothing is ignored.
func (s *Service) ignoredIn(ctx context.Context, abs string, repos []Repo, entries []proto.FileEntry) map[string]bool {
	r, ok := innermost(abs, repos)
	if !ok || len(entries) == 0 {
		return nil
	}
	var in bytes.Buffer
	byRepo := map[string]string{}
	for _, e := range entries {
		rp, err := filepath.Rel(r.Abs, filepath.Join(abs, filepath.Base(filepath.FromSlash(e.Path))))
		if err != nil {
			continue
		}
		rp = filepath.ToSlash(rp)
		if e.Kind == proto.FileKindDir {
			rp += "/"
		}
		byRepo[rp] = e.Path
		in.WriteString(rp)
		in.WriteByte(0)
	}
	out, err := s.Git.output(ctx, runOpts{Dir: r.Abs, Env: r.gitEnv(), Stdin: in.Bytes()}, "check-ignore", "--no-index", "-z", "--stdin")
	var ee *exec.ExitError
	if err != nil && (!errors.As(err, &ee) || ee.ExitCode() != 1) {
		return nil // an error ignores nothing rather than hiding files
	}
	ignored := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if rel, ok := byRepo[p]; ok {
			ignored[rel] = true
		}
	}
	return ignored
}

// listAll lists every file under abs, in the working directory dir:
// inside a repository what git ls-files reports as not ignored, without
// reading the repository's index; outside any repository every file. It
// skips .git, .DS_Store and symbolic links, and stops at maxListEntries.
func (s *Service) listAll(ctx context.Context, dir, abs string, repos []Repo) ([]proto.FileEntry, bool, error) {
	var entries []proto.FileEntry
	add := func(p string) {
		if path.Base(p) == dsStore {
			return
		}
		fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil || !fi.Mode().IsRegular() {
			return
		}
		entries = append(entries, proto.FileEntry{Path: p, Kind: proto.FileKindFile, Size: fi.Size()})
	}
	// The repositories whose files come from git: the one holding abs, and
	// every one below it.
	var tops []Repo
	if r, ok := innermost(abs, repos); ok {
		tops = append(tops, Repo{Path: r.Path, Abs: abs, Ceiling: r.Ceiling})
	}
	for _, r := range repos {
		if r.Err == "" && r.Abs != abs && within(r.Abs, abs) {
			tops = append(tops, r)
		}
	}
	for _, r := range tops {
		files, err := s.lsFiles(ctx, r)
		if err != nil {
			return nil, false, err
		}
		base, err := filepath.Rel(dir, r.Abs)
		if err != nil {
			return nil, false, fmt.Errorf("workspace: %w", err)
		}
		for _, f := range files {
			add(path.Join(filepath.ToSlash(base), f))
		}
	}
	if len(tops) == 0 || tops[0].Abs != abs {
		// Outside any repository: walk, leaving the repositories to git.
		isTop := map[string]bool{}
		for _, r := range tops {
			isTop[r.Abs] = true
		}
		err := filepath.WalkDir(abs, func(p string, de fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return nil // an unreadable directory is skipped
			case de.Type()&fs.ModeSymlink != 0:
				return nil
			case de.IsDir() && (de.Name() == ".git" || (p != abs && isTop[p])):
				return filepath.SkipDir
			case de.IsDir():
				return ctx.Err()
			}
			if r, err := filepath.Rel(dir, p); err == nil {
				add(filepath.ToSlash(r))
			}
			if len(entries) > orDefault(s.maxList, maxListEntries) {
				return filepath.SkipAll
			}
			return nil
		})
		if err != nil {
			return nil, false, fmt.Errorf("workspace: %w", err)
		}
	}
	slices.SortFunc(entries, func(a, b proto.FileEntry) int { return strings.Compare(a.Path, b.Path) })
	entries = slices.CompactFunc(entries, func(a, b proto.FileEntry) bool { return a.Path == b.Path })
	if limit := orDefault(s.maxList, maxListEntries); len(entries) > limit {
		return entries[:limit], true, nil
	}
	return entries, false, nil
}

// lsFiles lists the files git sees in r.Abs that .gitignore does not ignore,
// relative to it. It points git at an index that does not exist, so it
// never reads the repository's own: every file is untracked to it. A nested
// repository shows as its directory, which is skipped (it is listed on its
// own).
func (s *Service) lsFiles(ctx context.Context, r Repo) ([]string, error) {
	noIndex := filepath.Join(s.Home, "no-index")
	o := runOpts{Dir: r.Abs, Env: append(r.gitEnv(), "GIT_INDEX_FILE="+noIndex)}
	out, more, err := s.Git.run(ctx, o, maxListOutput, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	if more {
		out = out[:bytes.LastIndexByte(out, 0)+1]
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" && !strings.HasSuffix(f, "/") {
			files = append(files, f)
		}
	}
	return files, nil
}

// Read reads a file of the session's working directory dir as it is now,
// or with Baseline as the session's baseline recorded it; with another
// Range, a side of that range (see proto.FilesReadParams). A file larger
// than maxReadBytes, binary (a NUL in its first bytes, or not UTF-8) or
// missing comes back without text, saying which.
func (s *Service) Read(ctx context.Context, session proto.SessionID, dir string, p proto.FilesReadParams) (proto.FileContent, error) {
	rel, err := cleanRel(p.Path)
	if err != nil {
		return proto.FileContent{}, err
	}
	if rel == "" {
		return proto.FileContent{}, fmt.Errorf("%w: no file named", ErrInvalidPath)
	}
	out := proto.FileContent{Session: session, Path: rel}
	if err := checkRange(p.Range, ".", p.Commit); err != nil {
		return proto.FileContent{}, err
	}
	if rng := rangeOf(p.Range); rng != proto.DiffRangeSession && (p.Baseline || rng == proto.DiffRangeCommit) {
		return s.readRange(ctx, dir, rel, p, out)
	}
	if p.Baseline {
		return s.readBaseline(ctx, session, dir, rel, out)
	}
	abs, err := resolve(dir, rel)
	if errors.Is(err, fs.ErrNotExist) {
		out.Missing = true
		return out, nil
	}
	if err != nil {
		return proto.FileContent{}, fmt.Errorf("workspace: %w", err)
	}
	fi, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		out.Missing = true
		return out, nil
	case err != nil:
		return proto.FileContent{}, fmt.Errorf("workspace: %w", err)
	case !fi.Mode().IsRegular():
		return proto.FileContent{}, fmt.Errorf("%w: %s is not a regular file", ErrInvalidPath, rel)
	}
	out.Size = fi.Size()
	if fi.Size() > maxReadBytes {
		out.TooLarge = true
		return out, nil
	}
	data, err := readRaw(abs, maxReadBytes)
	switch {
	case errors.Is(err, errTooBig):
		out.TooLarge = true
		return out, nil
	case errors.Is(err, fs.ErrNotExist):
		out.Missing = true
		return out, nil
	case err != nil:
		return proto.FileContent{}, fmt.Errorf("workspace: read %s: %w", rel, err)
	}
	return withText(out, data), nil
}

// withText sets the text of c from data, or marks it binary.
func withText(c proto.FileContent, data []byte) proto.FileContent {
	c.Size = int64(len(data))
	if bytes.IndexByte(data[:min(len(data), binarySniff)], 0) >= 0 || !utf8.Valid(data) {
		c.Binary = true
		return c
	}
	c.Text = string(data)
	return c
}

// readBaseline reads rel from the tree the session's baseline recorded for
// the repository holding it, out of Hoplet's own object directory.
func (s *Service) readBaseline(ctx context.Context, session proto.SessionID, dir, rel string, out proto.FileContent) (proto.FileContent, error) {
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	repos, err := s.Git.Discover(ctx, dir)
	if err != nil {
		return proto.FileContent{}, err
	}
	r, ok := innermost(filepath.Dir(abs), repos)
	if !ok {
		return proto.FileContent{}, fmt.Errorf("%w: %s is not in a git repository", ErrNoBaseline, rel)
	}
	b, found, err := s.Store.Baseline(ctx, session)
	if err != nil {
		return proto.FileContent{}, err
	}
	var tree string
	for _, row := range b.Repos {
		if row.Path == r.Path && row.Error == "" {
			tree = row.Tree
		}
	}
	if !found || tree == "" {
		return proto.FileContent{}, fmt.Errorf("%w: %s", ErrNoBaseline, rel)
	}
	objects, err := ObjectDir(ctx, s.Home, r, s.Git)
	if err != nil {
		return proto.FileContent{}, err
	}
	inRepo, err := filepath.Rel(r.Abs, abs)
	if err != nil {
		return proto.FileContent{}, fmt.Errorf("workspace: %w", err)
	}
	return s.readTree(ctx, runOpts{Dir: r.Abs, Env: append(r.gitEnv(), objectEnv(objects, "")...)}, tree, filepath.ToSlash(inRepo), out)
}

// readTree reads the file at path (relative to the repository) in tree,
// with the objects o reaches.
func (s *Service) readTree(ctx context.Context, o runOpts, tree, path string, out proto.FileContent) (proto.FileContent, error) {
	line, err := s.Git.output(ctx, o, "ls-tree", "-l", "-z", tree, "--", path)
	if err != nil {
		return proto.FileContent{}, err
	}
	// "<mode> <type> <id> <size>\t<path>"
	meta, _, _ := strings.Cut(strings.TrimSuffix(string(line), "\x00"), "\t")
	f := strings.Fields(meta)
	if len(f) != 4 {
		out.Missing = true
		return out, nil
	}
	if f[1] != "blob" || f[0] == "120000" {
		out.Binary = true // a submodule or a symbolic link has no text to show
		return out, nil
	}
	var size int64
	if _, err := fmt.Sscan(f[3], &size); err == nil && size > maxReadBytes {
		out.Size, out.TooLarge = size, true
		return out, nil
	}
	data, err := s.Git.output(ctx, o, "cat-file", "blob", f[2])
	if err != nil {
		return proto.FileContent{}, err
	}
	return withText(out, data), nil
}
