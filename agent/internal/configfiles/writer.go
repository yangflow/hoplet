package configfiles

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	gosync "sync"
	"unicode/utf8"
)

// The writer of ADR 0036: it previews and applies a change a session proposed to its own project files and the
// user approved. Every path is fixed by the provider, the kind and the name; everything is done through
// os.Root in the session's working directory, a symlink or a non-regular file anywhere on the way is refused,
// and a change set is written all or nothing.

// The codes a preview or an apply adds to those of a read, as the wire names them.
const (
	CodeUnsafeContent = "unsafe-content"
	CodeStale         = "stale"
	CodeNotFound      = "not-found"
	CodeTooLarge      = "too-large"
)

const (
	// MaxWriteBytes is the largest file a change writes or replaces: a replaced file is kept as the previous
	// version for undo, and the core stores at most this much of it.
	MaxWriteBytes = 64 * 1024
	// MaxObjects is the most objects one change holds.
	MaxObjects = 5
	// MemoryName is the only name of a memory object.
	MemoryName = "project"
)

const (
	newFileMode = 0o644
	newDirMode  = 0o755
)

// Object is one object of a change. Content nil deletes the file. BaseHash and NewHash are only read by Apply:
// the file must still have BaseHash (empty: it must not exist), and NewHash is the hash of Content.
type Object struct {
	Kind     string
	Name     string
	Content  *string
	BaseHash string
	NewHash  string
}

// Change is a preview or an apply for one session.
type Change struct {
	Provider string
	// Tested says the vendor version that runs the session is one the skill and command formats were checked
	// against; skills and commands are "unsupported" otherwise.
	Tested bool
	// Restore marks an undo: the content is what the agent read at preview time, so the content check, the
	// version check and the rule that memory is never deleted do not apply. The hashes and the paths do.
	Restore bool
	Objects []Object
}

// Preview is what applying one object would change.
type Preview struct {
	Kind        string
	Name        string
	CurrentHash string  // empty when the file does not exist
	Previous    *string // the current text
	Diff        string
	// NewDirectories are the directories applying creates, relative to the working directory.
	NewDirectories []string
}

// Applied is one written object and the file's hash now, empty when it was deleted.
type Applied struct {
	Kind string
	Name string
	Hash string
}

// Writer previews and applies changes. Home is the user's home directory, used to resolve `~` in imports.
type Writer struct {
	Home string
	// fail, set by tests, makes the write of rel fail.
	fail func(rel string) error
}

// writes serializes the applies of this process: two sessions of one project must not interleave.
var writes gosync.Mutex

// target is one object resolved against the working directory.
type target struct {
	obj     Object
	rel     string
	current *string
	hash    string
	mode    fs.FileMode
	newDirs []string
}

// Preview checks the objects and says what applying them would change.
func (w Writer) Preview(cwd string, c Change) ([]Preview, error) {
	root, err := openProject(cwd)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	targets, err := w.resolve(root, cwd, c)
	if err != nil {
		return nil, err
	}
	out := make([]Preview, len(targets))
	for i, t := range targets {
		out[i] = Preview{
			Kind:           t.obj.Kind,
			Name:           t.obj.Name,
			CurrentHash:    t.hash,
			Previous:       t.current,
			Diff:           unifiedDiff(t.rel, t.current, t.obj.Content),
			NewDirectories: t.newDirs,
		}
	}
	return out, nil
}

// Apply writes the objects if every file still has its base hash. On a failure the files already written are
// put back as they were read and the directories created are removed.
func (w Writer) Apply(cwd string, c Change) ([]Applied, error) {
	writes.Lock()
	defer writes.Unlock()
	root, err := openProject(cwd)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	targets, err := w.resolve(root, cwd, c)
	if err != nil {
		return nil, err
	}
	for _, t := range targets {
		if t.obj.BaseHash != t.hash {
			return nil, refuse(CodeStale, t.obj.Kind+":"+t.obj.Name)
		}
		want := ""
		if t.obj.Content != nil {
			want = hashOf(*t.obj.Content)
		}
		if t.obj.NewHash != want {
			return nil, refuse(CodeInvalid, "newHash does not match the content of "+t.obj.Kind+":"+t.obj.Name)
		}
	}

	var done []*target
	var created, removed []string
	rollback := func() {
		for i := len(done) - 1; i >= 0; i-- {
			t := done[i]
			if t.current == nil {
				_ = root.Remove(t.rel)
				continue
			}
			if dir := path.Dir(t.rel); dir != "." && slicesContain(removed, dir) {
				_ = root.Mkdir(dir, newDirMode)
			}
			_ = w.atomicWrite(root, t.rel, *t.current, t.mode, false)
		}
		for i := len(created) - 1; i >= 0; i-- {
			_ = root.Remove(created[i])
		}
	}
	out := make([]Applied, len(targets))
	for i := range targets {
		t := &targets[i]
		if t.obj.Content == nil {
			if err := w.remove(root, t); err != nil {
				rollback()
				return nil, err
			}
			done = append(done, t)
			// A skill is its directory: an emptied one goes with its file.
			if t.obj.Kind == KindSkill && root.Remove(path.Dir(t.rel)) == nil {
				removed = append(removed, path.Dir(t.rel))
			}
			out[i] = Applied{Kind: t.obj.Kind, Name: t.obj.Name}
			continue
		}
		for _, dir := range t.newDirs {
			err := root.Mkdir(dir, newDirMode)
			if errors.Is(err, fs.ErrExist) {
				continue // made by another object of this change, or by someone else: not ours to remove
			}
			if err != nil {
				rollback()
				return nil, refuse(CodeIO, "cannot create "+dir)
			}
			created = append(created, dir)
		}
		mode := t.mode
		if t.current == nil {
			mode = newFileMode
		}
		if err := w.atomicWrite(root, t.rel, *t.obj.Content, mode, true); err != nil {
			rollback()
			return nil, err
		}
		done = append(done, t)
		out[i] = Applied{Kind: t.obj.Kind, Name: t.obj.Name, Hash: hashOf(*t.obj.Content)}
	}
	return out, nil
}

func slicesContain(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// resolve validates the change and reads the current state of every object it names.
func (w Writer) resolve(root *os.Root, cwd string, c Change) ([]target, error) {
	lay, ok := layouts[c.Provider]
	if !ok {
		return nil, refuse(CodeUnsupported, "no configuration files for provider "+c.Provider)
	}
	if len(c.Objects) == 0 || len(c.Objects) > MaxObjects {
		return nil, refuse(CodeInvalid, "a change holds one to five objects")
	}
	seen := map[string]bool{}
	targets := make([]target, 0, len(c.Objects))
	for _, o := range c.Objects {
		key := o.Kind + ":" + o.Name
		if seen[key] {
			return nil, refuse(CodeInvalid, key+" appears twice")
		}
		seen[key] = true
		rel, err := w.pathOf(cwd, lay, c, o)
		if err != nil {
			return nil, err
		}
		if o.Content != nil {
			if len(*o.Content) > MaxWriteBytes {
				return nil, refuse(CodeTooLarge, key)
			}
			if strings.IndexByte(*o.Content, 0) >= 0 || !utf8.ValidString(*o.Content) {
				return nil, refuse(CodeInvalid, "the content of "+key+" is not UTF-8 text")
			}
		}
		t, err := readTarget(root, rel)
		if err != nil {
			return nil, err
		}
		t.obj = o
		if t.current != nil && len(*t.current) > MaxWriteBytes {
			return nil, refuse(CodeTooLarge, key)
		}
		if o.Content == nil {
			if t.current == nil {
				return nil, refuse(CodeNotFound, key)
			}
			t.newDirs = nil
		} else if !c.Restore {
			// The vendor reads no more than this of its memory files; the rest would be dropped unseen.
			if o.Kind == KindMemory && lay.maxMemory > 0 && len(*o.Content) > lay.maxMemory {
				return nil, refuse(CodeTooLarge, fmt.Sprintf("%s reads at most %d KiB of its memory files", lay.vendor, lay.maxMemory/1024))
			}
			if err := checkContent(cwd, w.Home, lay, o.Kind, rel, t.current, *o.Content); err != nil {
				return nil, err
			}
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// pathOf maps an object to its file below the working directory.
func (w Writer) pathOf(cwd string, lay layout, c Change, o Object) (string, error) {
	switch o.Kind {
	case KindMemory:
		if o.Name != MemoryName {
			return "", refuse(CodeInvalid, "invalid name")
		}
		if o.Content == nil && !c.Restore {
			return "", refuse(CodeInvalid, "the memory file is not deleted")
		}
		return lay.projectMemory(cwd, w.Home)
	case KindSkill, KindCommand:
		if !namePattern.MatchString(o.Name) {
			return "", refuse(CodeInvalid, "invalid name")
		}
		if o.Kind == KindCommand && lay.commandsDir == "" {
			return "", refuse(CodeUnsupported, lay.vendor+" has no command files")
		}
		if !c.Tested && !c.Restore {
			return "", refuse(CodeUnsupported, "the vendor version is not one the "+o.Kind+" format was checked against")
		}
		if o.Kind == KindSkill {
			return path.Join(lay.skillsDir, o.Name, lay.skillFile), nil
		}
		return path.Join(lay.commandsDir, o.Name+lay.commandExt), nil
	default:
		return "", refuse(CodeInvalid, "unknown kind")
	}
}

func openProject(cwd string) (*os.Root, error) {
	if !filepath.IsAbs(cwd) {
		return nil, refuse(CodeIO, "the session has no working directory")
	}
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return nil, refuse(CodeIO, "cannot open the working directory")
	}
	return root, nil
}

// readTarget reads rel under root: its text, hash and mode when it exists, and the directories above it that do
// not. A symlink, a non-directory on the way or a file that is not regular is refused.
func readTarget(root *os.Root, rel string) (target, error) {
	t := target{rel: rel}
	parts := strings.Split(rel, "/")
	for i := range parts[:len(parts)-1] {
		dir := path.Join(parts[:i+1]...)
		info, err := root.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			t.newDirs = append(t.newDirs, dir)
			continue
		}
		if err != nil {
			return t, refuse(CodeIO, "cannot stat "+dir)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return t, refuse(CodeInvalid, "symlink in path")
		}
		if !info.IsDir() {
			return t, refuse(CodeInvalid, "not a directory in path")
		}
	}
	if len(t.newDirs) > 0 {
		return t, nil
	}
	f, info, err := regular(root, rel)
	if err != nil {
		return t, err
	}
	if f == nil {
		return t, nil
	}
	defer f.Close()
	if info.Size() > MaxFileBytes {
		return t, refuse(CodeTooLarge, rel)
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return t, refuse(CodeIO, "cannot read "+rel)
	}
	if len(raw) > MaxFileBytes {
		return t, refuse(CodeTooLarge, rel)
	}
	text := string(raw)
	t.current = &text
	t.hash = hashOf(text)
	t.mode = info.Mode().Perm()
	return t, nil
}

// atomicWrite replaces rel with text through a temporary file in the same directory and a rename, with mode.
// check refuses a rel that became a symlink or a non-regular file since it was read.
func (w Writer) atomicWrite(root *os.Root, rel, text string, mode fs.FileMode, check bool) error {
	if w.fail != nil {
		if err := w.fail(rel); err != nil {
			return refuse(CodeIO, err.Error())
		}
	}
	if check {
		if info, err := root.Lstat(rel); err == nil && !info.Mode().IsRegular() {
			return refuse(CodeInvalid, "not a regular file")
		}
	}
	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	tmp := path.Join(path.Dir(rel), "."+path.Base(rel)+".hoplet-"+hex.EncodeToString(suffix[:])+".tmp")
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return refuse(CodeIO, "cannot create a temporary file for "+rel)
	}
	_, werr := io.WriteString(f, text)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = root.Chmod(tmp, mode) // the umask may have narrowed it
	}
	if werr == nil {
		werr = root.Rename(tmp, rel)
	}
	if werr != nil {
		_ = root.Remove(tmp)
		return refuse(CodeIO, "cannot write "+rel)
	}
	return nil
}

func (w Writer) remove(root *os.Root, t *target) error {
	if w.fail != nil {
		if err := w.fail(t.rel); err != nil {
			return refuse(CodeIO, err.Error())
		}
	}
	if info, err := root.Lstat(t.rel); err != nil || !info.Mode().IsRegular() {
		return refuse(CodeInvalid, "not a regular file")
	}
	if err := root.Remove(t.rel); err != nil {
		return refuse(CodeIO, "cannot remove "+t.rel)
	}
	return nil
}

func hashOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
