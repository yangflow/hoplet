package workspace

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/yangflow/hoplet/proto"
)

const (
	// defaultChunkFiles and defaultChunkBytes bound one round of hashing.
	defaultChunkFiles = 10000
	defaultChunkBytes = 256 << 20
	// defaultMaxSnapshotBytes is the most content one snapshot reads.
	defaultMaxSnapshotBytes = 4 << 30
)

// ErrTooLarge is returned for a repository whose worktree is too large to
// snapshot.
var ErrTooLarge = errors.New("repository too large to snapshot")

// Index entry modes.
const (
	modeFile    = "100644"
	modeExec    = "100755"
	modeSymlink = "120000"
	modeGitlink = "160000"
)

// Reasons a file is left out of a snapshot.
const (
	ReasonTooLarge   = "too large"
	ReasonUnreadable = "unreadable"
)

// Snapshot is a repo's content at one moment, as a tree in Hoplet's objects.
type Snapshot struct {
	Tree    string              // tree id
	Skipped []proto.SkippedFile // files left out, keeping their previously recorded version
}

// repoInfo is what git says about a repository's layout.
type repoInfo struct {
	commonDir string
	format    string // sha1 or sha256
}

func (g Git) repoInfo(ctx context.Context, r Repo) (repoInfo, error) {
	out, err := g.output(ctx, runOpts{Dir: r.Abs, Env: r.gitEnv()}, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-object-format")
	if err != nil {
		return repoInfo{}, err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 2 {
		return repoInfo{}, fmt.Errorf("workspace: unexpected rev-parse output for %s", r.Path)
	}
	return repoInfo{commonDir: lines[0], format: lines[1]}, nil
}

func (i repoInfo) zeroID() string {
	if i.format == "sha256" {
		return strings.Repeat("0", 64)
	}
	return strings.Repeat("0", 40)
}

// ObjectDir returns Hoplet's object directory for the repository, shared by
// every session that works in it, and creates it. It holds the objects of
// every snapshot and Hoplet's own index of the repository. It is named after
// the real path of the repository's common git dir.
func ObjectDir(ctx context.Context, home string, r Repo, g Git) (string, error) {
	info, err := g.repoInfo(ctx, r)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(info.commonDir)
	if err != nil {
		return "", fmt.Errorf("workspace: resolve %s: %w", info.commonDir, err)
	}
	sum := sha256.Sum256([]byte(real))
	dir, err := filepath.Abs(filepath.Join(home, "objects", hex.EncodeToString(sum[:8])))
	if err != nil {
		return "", fmt.Errorf("workspace: resolve object dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("workspace: create object dir: %w", err)
	}
	// A repository deleted and created again at the same path with the other
	// object format gets a fresh directory: what is in it is of the wrong
	// format. The import repository tells the format it was made with.
	if old := importFormat(dir); old != "" && old != info.format {
		if err := os.RemoveAll(dir); err != nil {
			return "", fmt.Errorf("workspace: reset object dir: %w", err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("workspace: create object dir: %w", err)
		}
	}
	return dir, nil
}

// importFormat is the object format Hoplet's import repository in an object
// directory was made with, or "" if there is none. git records a format other
// than sha1 in its config as "objectformat = sha256".
func importFormat(objects string) string {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(objects, "fi.git", "config")))
	if err != nil {
		return ""
	}
	format := "sha1"
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(line)), "objectformat"); ok {
			format = strings.Trim(v, " \t=")
		}
	}
	return format
}

// objectEnv points git at Hoplet's object directory only: the repository's
// own objects are never reachable, so nothing in the repository is read or
// refreshed through them and nothing Hoplet stores depends on them. With
// index, git also uses that Hoplet index (see indexPath).
func objectEnv(objects, index string) []string {
	env := []string{"GIT_OBJECT_DIRECTORY=" + objects}
	if index != "" {
		env = append(env, "GIT_INDEX_FILE="+index)
	}
	return env
}

// indexPath is Hoplet's index of one worktree. Linked worktrees of a
// repository share its object directory but each has its own index, named
// after the worktree's real path, so their stat caches do not clash.
func indexPath(objects string, r Repo) string {
	abs := r.Abs
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(objects, "index-"+hex.EncodeToString(sum[:8]))
}

// EmptyTree returns the id of the empty tree in the repository's object
// format (sha1 or sha256), and makes sure it exists in objects.
func (g Git) EmptyTree(ctx context.Context, r Repo, objects string) (string, error) {
	o, err := g.repoOpts(ctx, r, objectEnv(objects, "")...)
	if err != nil {
		return "", err
	}
	o.Stdin = []byte{}
	out, err := g.output(ctx, o, "hash-object", "-t", "tree", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// indexError says Hoplet's index could not be read.
type indexError struct{ err error }

func (e *indexError) Error() string { return "workspace: read index: " + e.err.Error() }
func (e *indexError) Unwrap() error { return e.err }

// indexEntry is an entry of Hoplet's index.
type indexEntry struct{ mode, id string }

// change is one path's new state in a snapshot.
type change struct {
	path string
	mode string // "" for a removal
	id   string
}

// item is a file to store, or a symlink whose text is stored.
type item struct {
	path string // in the repository
	text string // a symlink's text
	link bool
	size int64
	mode string
}

// Snapshot records the repository's worktree as a tree in objects: every file
// tracked at HEAD, plus every untracked file that is not ignored (none, with
// status.showUntrackedFiles=no), as raw bytes (no clean or smudge filter, no
// eol, ident or encoding conversion), with a submodule or embedded repository
// as its commit. A path that lies behind a symlinked directory counts as
// absent; the symlink is recorded as a symlink. It uses Hoplet's own
// persistent index for the worktree (see indexPath) as a stat cache and saves
// it after every round of up to 10000 files or 256 MiB, so a snapshot that is
// cut short keeps what it hashed and only the first snapshot of a large
// repository reads every file. The user's index and objects are never read.
// Callers serialize snapshots of one index. Files larger than MaxFile and
// unreadable files keep the version recorded before and are reported in
// Skipped. A worktree needing more than 4 GiB to be read is
// ErrTooLarge.
func (g Git) Snapshot(ctx context.Context, r Repo, objects string) (Snapshot, error) {
	info, err := g.repoInfo(ctx, r)
	if err != nil {
		return Snapshot{}, err
	}
	// o reads and writes Hoplet's objects and index; plain reads the
	// repository, which only config, rev-parse and ls-tree do.
	o, err := g.repoOpts(ctx, r, objectEnv(objects, indexPath(objects, r))...)
	if err != nil {
		return Snapshot{}, err
	}
	plain := runOpts{Dir: r.Abs, Cfg: o.Cfg, Env: r.gitEnv()}

	idx, err := g.readIndex(ctx, o)
	if err != nil {
		if ctx.Err() != nil {
			return Snapshot{}, err
		}
		return Snapshot{}, &indexError{err}
	}
	paths, stale, err := g.candidates(ctx, o, plain, idx)
	if err != nil {
		return Snapshot{}, err
	}
	pl, err := g.classify(ctx, r, idx, paths)
	if err != nil {
		return Snapshot{}, err
	}
	for _, p := range stale {
		pl.changes = append(pl.changes, change{path: p})
	}
	var total int64
	for _, it := range pl.items {
		total += it.size
	}
	if total > orDefault(g.snapshotLimit, defaultMaxSnapshotBytes) {
		return Snapshot{}, ErrTooLarge
	}
	if len(pl.changes) > 0 {
		if err := g.apply(ctx, o, info, pl.changes); err != nil {
			return Snapshot{}, err
		}
	}
	if err := os.MkdirAll(filepath.Join(objects, "tmp"), 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("workspace: create temporary dir: %w", err)
	}
	tmp, err := os.MkdirTemp(filepath.Join(objects, "tmp"), "snap-")
	if err != nil {
		return Snapshot{}, fmt.Errorf("workspace: create temporary dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	importDir := ""
	skipped := pl.skipped
	for round := 1; len(pl.items) > 0; round++ {
		var n int
		var bytes int64
		for n < len(pl.items) && n < orDefault(g.filesPerChunk, defaultChunkFiles) && (n == 0 || bytes < defaultChunkBytes) {
			bytes += pl.items[n].size
			n++
		}
		if importDir == "" {
			if importDir, err = g.importRepo(ctx, objects, info); err != nil {
				return Snapshot{}, err
			}
		}
		changes, unreadable, err := g.hashItems(ctx, o, objects, importDir, tmp, idx, pl.items[:n])
		if err != nil {
			return Snapshot{}, err
		}
		skipped = append(skipped, unreadable...)
		pl.items = pl.items[n:]
		if len(changes) > 0 {
			if err := g.apply(ctx, o, info, changes); err != nil {
				return Snapshot{}, err
			}
		}
		if g.afterChunk != nil {
			g.afterChunk(round)
		}
	}
	out, err := g.output(ctx, o, "write-tree")
	if err != nil {
		return Snapshot{}, err
	}
	slices.SortFunc(skipped, func(a, b proto.SkippedFile) int { return strings.Compare(a.Path, b.Path) })
	return Snapshot{Tree: strings.TrimSpace(string(out)), Skipped: skipped}, nil
}

// apply writes changes into Hoplet's index: removals first, so a file
// replaced by a directory (or the other way round) never conflicts.
func (g Git) apply(ctx context.Context, o runOpts, info repoInfo, changes []change) error {
	slices.SortStableFunc(changes, func(a, b change) int {
		if (a.mode == "") != (b.mode == "") {
			if a.mode == "" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.path, b.path)
	})
	var buf bytes.Buffer
	for _, c := range changes {
		if c.mode == "" {
			fmt.Fprintf(&buf, "0 %s\t%s\x00", info.zeroID(), c.path)
		} else {
			fmt.Fprintf(&buf, "%s %s\t%s\x00", c.mode, c.id, c.path)
		}
	}
	w := o
	w.Stdin = buf.Bytes()
	if _, err := g.output(ctx, w, "update-index", "-z", "--index-info"); err != nil {
		return err
	}
	// Entries written this way carry no stat data; refreshing them is what
	// makes the next snapshot cheap. Files that differ from their entry make
	// it exit 1, which is not an error here.
	if _, err := g.output(ctx, o, "update-index", "-q", "--refresh"); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 {
			return err
		}
	}
	return nil
}

// readIndex lists Hoplet's index.
func (g Git) readIndex(ctx context.Context, o runOpts) (map[string]indexEntry, error) {
	out, err := g.output(ctx, o, "ls-files", "-s", "-z")
	if err != nil {
		return nil, err
	}
	idx := map[string]indexEntry{}
	for _, e := range strings.Split(string(out), "\x00") {
		meta, path, ok := strings.Cut(e, "\t")
		f := strings.Fields(meta)
		if ok && len(f) == 3 {
			idx[path] = indexEntry{mode: f[0], id: f[1]}
		}
	}
	return idx, nil
}

// candidates lists the paths that may differ from Hoplet's index: those git
// reports as modified, deleted or untracked-and-not-ignored (the last unless
// status.showUntrackedFiles is no), and those tracked at HEAD that the index
// does not have yet (which includes ignored ones).
func (g Git) candidates(ctx context.Context, o, plain runOpts, idx map[string]indexEntry) (paths, stale []string, err error) {
	args := []string{"ls-files", "-z", "--modified", "--deleted"}
	if v, err := g.output(ctx, plain, "config", "--get", "status.showUntrackedFiles"); err != nil || !isNo(string(v)) {
		args = append(args, "--others", "--exclude-standard")
	}
	out, err := g.output(ctx, o, args...)
	if err != nil {
		return nil, nil, err
	}
	set := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		// A trailing slash marks an embedded repository.
		if p = strings.TrimSuffix(p, "/"); p != "" {
			set[p] = true
		}
	}
	// An unborn branch has no HEAD.
	if _, err := g.output(ctx, plain, "rev-parse", "-q", "--verify", "HEAD"); err == nil {
		head, err := g.output(ctx, plain, "ls-tree", "-r", "-z", "--name-only", "HEAD")
		if err != nil {
			return nil, nil, err
		}
		for _, p := range strings.Split(string(head), "\x00") {
			if _, ok := idx[p]; !ok && p != "" {
				set[p] = true
			}
		}
	}
	// Where the filesystem ignores case, git finds a file under any spelling:
	// after a committed case-only rename the old spelling stays in Hoplet's
	// index and is never reported deleted. Remove it when the new one is added.
	if v, err := g.output(ctx, plain, "config", "--type=bool", "--get", "core.ignorecase"); err == nil && strings.TrimSpace(string(v)) == "true" {
		lower := make(map[string]string, len(idx))
		for p := range idx {
			lower[strings.ToLower(p)] = p
		}
		for p := range set {
			if old, ok := lower[strings.ToLower(p)]; ok && old != p && idx[p] == (indexEntry{}) {
				stale = append(stale, old)
				delete(set, old)
			}
		}
	}
	paths = slices.Sorted(maps.Keys(set))
	return paths, stale, nil
}

// isNo reports whether a git config value spells "no" (git accepts these).
func isNo(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "no", "false", "off", "0":
		return true
	}
	return false
}

// plan is what a snapshot has to do: index changes that need no hashing,
// and files to hash.
type plan struct {
	changes []change
	items   []item
	skipped []proto.SkippedFile
}

// classify looks at each candidate on disk: removal, symlink, repository
// commit, or file to hash. Directories are looked at once per snapshot.
func (g Git) classify(ctx context.Context, r Repo, idx map[string]indexEntry, paths []string) (plan, error) {
	var pl plan
	dirs := map[string]bool{} // directory (relative) -> is a real directory
	for _, p := range paths {
		abs := filepath.Join(r.Abs, filepath.FromSlash(p))
		prev, tracked := idx[p]
		remove := func() {
			if tracked {
				pl.changes = append(pl.changes, change{path: p})
			}
		}
		if behindSymlink(r.Abs, p, dirs) {
			remove()
			continue
		}
		fi, err := os.Lstat(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
			remove()
		case err != nil:
			pl.skipped = append(pl.skipped, proto.SkippedFile{Path: p, Reason: ReasonUnreadable})
		case fi.Mode()&fs.ModeSymlink != 0:
			// hash-object follows a symlink given by path; the link's text
			// is the blob, so it is hashed from a file holding the text.
			target, err := os.Readlink(abs)
			if err != nil {
				remove()
				continue
			}
			pl.items = append(pl.items, item{path: p, text: target, link: true, size: int64(len(target)), mode: modeSymlink})
		case fi.IsDir():
			c, ok, err := g.repoChange(ctx, abs, p, prev, tracked)
			if err != nil {
				return plan{}, err
			}
			if ok {
				pl.changes = append(pl.changes, c)
			}
		case !fi.Mode().IsRegular():
			// Sockets, devices and the like have no content to record.
		case fi.Size() > g.maxFile():
			pl.skipped = append(pl.skipped, proto.SkippedFile{Path: p, Size: fi.Size(), Reason: ReasonTooLarge})
		default:
			mode := modeFile // git uses the owner's execute bit
			if fi.Mode().Perm()&0o100 != 0 {
				mode = modeExec
			}
			pl.items = append(pl.items, item{path: p, size: fi.Size(), mode: mode})
		}
	}
	return pl, nil
}

// behindSymlink reports whether a parent directory of p, within the
// repository, is a symlink (or something other than a directory). dirs
// caches the answer per directory.
func behindSymlink(root, p string, dirs map[string]bool) bool {
	rel := ""
	for rest := p; ; {
		name, tail, more := strings.Cut(rest, "/")
		if !more {
			return false
		}
		if rel != "" {
			rel += "/"
		}
		rel += name
		real, seen := dirs[rel]
		if !seen {
			fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
			real = err != nil || fi.IsDir() // a missing parent is handled by the file's own lstat
			dirs[rel] = real
		}
		if !real {
			return true
		}
		rest = tail
	}
}

// importRepo returns Hoplet's own bare repository next to its objects, made
// on first use. fast-import runs in it, so that nothing it keeps beside the
// objects (it writes a crash report next to the git dir) lands in the user's
// repository.
func (g Git) importRepo(ctx context.Context, objects string, info repoInfo) (string, error) {
	dir := filepath.Join(objects, "fi.git")
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		return dir, nil
	}
	if _, err := g.output(ctx, runOpts{}, "init", "--bare", "-q", "--template=", "--object-format="+info.format, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// errChanged says a file stopped being what the snapshot looked at.
var errChanged = errors.New("workspace: file changed while being read")

// errTooBig says a file grew past the size limit while the snapshot ran.
var errTooBig = errors.New("workspace: file grew too large while being read")

// readRaw reads a regular file, up to max bytes. It never follows a symlink
// at the end of the path and never blocks on a special file: the file is
// opened non-blocking, so a FIFO swapped in meanwhile opens at once and is
// rejected by the check that it is a regular file.
func readRaw(path string, max int64) ([]byte, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, errChanged
	}
	if fi.Size() > max {
		return nil, errTooBig
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errTooBig
	}
	return data, nil
}

// hashItems writes the raw content of items into the object directory, as a
// pack (git fast-import: far cheaper than a loose object per file), and
// returns their index changes. A file that disappears or turns into
// something else meanwhile is removed from the index if it was there; one
// that cannot be read is returned in unreadable. Files are opened without
// following a symlink.
func (g Git) hashItems(ctx context.Context, o runOpts, objects, importDir, tmp string, idx map[string]indexEntry, items []item) (changes []change, unreadable []proto.SkippedFile, err error) {
	marks := filepath.Join(tmp, "marks")
	pr, pw := io.Pipe()
	type produced struct {
		stored, gone, unreadable, tooBig []int // item numbers
		err                              error
	}
	done := make(chan produced, 1)
	go func() {
		var res produced
		w := bufio.NewWriterSize(pw, 1<<20)
	files:
		for i, it := range items {
			if ctx.Err() != nil {
				break
			}
			data := []byte(it.text)
			if !it.link {
				var rerr error
				data, rerr = readRaw(filepath.Join(o.Dir, filepath.FromSlash(it.path)), g.maxFile())
				switch {
				case rerr == nil:
				case errors.Is(rerr, errTooBig):
					res.tooBig = append(res.tooBig, i)
					continue
				case errors.Is(rerr, fs.ErrNotExist), errors.Is(rerr, syscall.ENOTDIR), errors.Is(rerr, syscall.ELOOP), errors.Is(rerr, errChanged):
					res.gone = append(res.gone, i)
					continue
				default:
					res.unreadable = append(res.unreadable, i)
					continue
				}
			}
			// A failed write (git has gone) ends the reading of files.
			if _, err := fmt.Fprintf(w, "blob\nmark :%d\ndata %d\n", i+1, len(data)); err != nil {
				res.err = err
				break files
			}
			if _, err := w.Write(data); err != nil {
				res.err = err
				break files
			}
			if err := w.WriteByte(0x0a); err != nil {
				res.err = err
				break files
			}
			res.stored = append(res.stored, i)
		}
		if res.err == nil {
			fmt.Fprint(w, "done\n")
			res.err = w.Flush()
		}
		pw.CloseWithError(res.err)
		done <- res
	}()
	// fast-import runs in Hoplet's own bare repository, on Hoplet's objects.
	fi := runOpts{Dir: objects, StdinReader: pr, Cfg: []string{"-c", "fastimport.unpackLimit=100"}, Env: []string{"GIT_DIR=" + importDir, "GIT_OBJECT_DIRECTORY=" + objects}}
	_, runErr := g.output(ctx, fi, "fast-import", "--done", "--quiet", "--export-marks="+marks)
	pr.CloseWithError(runErr) // frees the reader of files if git failed
	res := <-done
	if runErr != nil {
		return nil, nil, runErr
	}
	if res.err != nil {
		return nil, nil, fmt.Errorf("workspace: feed fast-import: %w", res.err)
	}
	ids, err := readMarks(marks)
	if err != nil {
		return nil, nil, err
	}
	var stored []string
	for _, i := range res.stored {
		id, ok := ids[i+1]
		if !ok {
			return nil, nil, fmt.Errorf("workspace: fast-import stored no object for %s", items[i].path)
		}
		changes = append(changes, change{path: items[i].path, mode: items[i].mode, id: id})
		stored = append(stored, items[i].path)
	}
	if g.hashed != nil {
		g.hashed(stored)
	}
	for _, i := range res.gone {
		if _, tracked := idx[items[i].path]; tracked {
			changes = append(changes, change{path: items[i].path})
		}
	}
	for _, i := range res.unreadable {
		unreadable = append(unreadable, proto.SkippedFile{Path: items[i].path, Size: items[i].size, Reason: ReasonUnreadable})
	}
	// A file that grew past the limit meanwhile keeps its recorded version.
	for _, i := range res.tooBig {
		unreadable = append(unreadable, proto.SkippedFile{Path: items[i].path, Reason: ReasonTooLarge}) // its size now is not known
	}
	return changes, unreadable, nil
}

// readMarks reads the marks file fast-import exports: ":N id" per line.
func readMarks(path string) (map[int]string, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("workspace: read fast-import marks: %w", err)
	}
	ids := map[int]string{}
	for _, line := range strings.Split(string(b), "\n") {
		mark, id, ok := strings.Cut(line, " ")
		if n, err := strconv.Atoi(strings.TrimPrefix(mark, ":")); ok && err == nil {
			ids[n] = id
		}
	}
	return ids, nil
}

// repoChange handles a directory among the candidates. A directory with a
// repository of its own (a submodule or an embedded repository) is recorded
// as that repository's HEAD commit; one without commits is skipped. Any
// other directory removes the file or repository entry it replaced.
func (g Git) repoChange(ctx context.Context, abs, p string, prev indexEntry, tracked bool) (change, bool, error) {
	if _, err := os.Lstat(filepath.Join(abs, ".git")); err != nil {
		return change{path: p}, tracked, nil
	}
	// An invalid .git there must not make git use the enclosing repository.
	env := []string(nil)
	if parent, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		env = []string{"GIT_CEILING_DIRECTORIES=" + parent}
	}
	head, err := g.output(ctx, runOpts{Dir: abs, Env: env}, "rev-parse", "-q", "--verify", "HEAD^{commit}")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return change{}, false, nil // no commits yet, or not usable
		}
		return change{}, false, err
	}
	id := strings.TrimSpace(string(head))
	if tracked && prev.mode == modeGitlink && prev.id == id {
		return change{}, false, nil
	}
	return change{path: p, mode: modeGitlink, id: id}, true, nil
}

// packCount is how many packs Hoplet's object directory holds.
func packCount(objects string) int {
	packs, _ := filepath.Glob(filepath.Join(objects, "pack", "*.pack"))
	return len(packs)
}

// consolidate packs Hoplet's packs into one (each round that stores more than
// 100 blobs adds one). The import repository has no refs, so everything in it
// counts as unreachable: --keep-unreachable (-k) is what keeps it.
func (g Git) consolidate(ctx context.Context, objects string) error {
	dir := filepath.Join(objects, "fi.git")
	run := runOpts{Dir: objects, Env: []string{"GIT_DIR=" + dir, "GIT_OBJECT_DIRECTORY=" + objects}}
	_, err := g.output(ctx, run, "repack", "-a", "-d", "-k", "-q")
	return err
}

// sweepTemp removes what a killed import or repack leaves in an object
// directory: temporary packs and crash reports. Callers make sure nothing is
// writing there.
func sweepTemp(objects string) {
	for _, pattern := range []string{"pack/tmp_pack_*", "pack/tmp_idx_*", "pack/.tmp-*", "fi.git/fast_import_crash_*"} {
		files, _ := filepath.Glob(filepath.Join(objects, pattern))
		for _, f := range files {
			_ = os.Remove(f)
		}
	}
}
