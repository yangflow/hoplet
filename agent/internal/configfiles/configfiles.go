// Package configfiles reads a session's own configuration files: the memory file, the skills and the commands
// of its project or of the user (ADR 0036). The paths are fixed by the provider and the kind and are never
// taken from the caller; a name must be a plain `[a-z0-9][a-z0-9-]{0,63}`. Everything is read through
// os.Root, so nothing outside the project directory (or the vendor's directory in the user's home) is
// reachable, and a symlink is refused at every step. The project's memory file is the one the vendor reads
// (memory.go). Claude Code and Codex have a layout each; Codex has no commands, so that kind is "unsupported".
// Writer previews and applies a change the user approved, in the project only (writer.go).
package configfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// PageBytes is the most text one answer carries (ADR 0007).
	PageBytes = 32 * 1024
	// MaxFileBytes is the largest file that is read at all; the hash covers the whole file, so it is read whole.
	MaxFileBytes = 1 << 20
	// MaxEntries is the most skills and commands a list answer carries.
	MaxEntries = 200
	// MaxDescriptionBytes bounds the description of an entry.
	MaxDescriptionBytes = 300
	// maxScan bounds the directory entries one list looks at.
	maxScan = 1000
	// headBytes is how much of a skill or command is looked at for its description.
	headBytes = 4096
)

// The kinds and scopes of a request, as the wire names them.
const (
	KindMemory  = "memory"
	KindSkill   = "skill"
	KindCommand = "command"
	KindList    = "list"

	ScopeProject = "project"
	ScopeUser    = "user"
)

// The codes of an Error, as the wire names them.
const (
	CodeUnsupported = "unsupported"
	CodeInvalid     = "invalid"
	CodeIO          = "io"
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Error is a refusal the core relays: the provider has no such file, the request is malformed, or the read failed.
type Error struct {
	Code   string
	Reason string
}

func (e *Error) Error() string { return e.Code + ": " + e.Reason }

func refuse(code, reason string) error { return &Error{Code: code, Reason: reason} }

// layout is where one provider keeps its configuration. Paths are slash-separated, relative to the project
// directory or to the user's home directory; a path below the home directory starts with the vendor's own top
// directory (.claude, .codex, .agents), which is where a user-scope read is rooted.
type layout struct {
	vendor string // the vendor's name in a refusal
	// projectMemory is the project memory file the writer writes, below cwd (memory.go).
	projectMemory func(cwd, home string) (string, error)
	// shownMemory is the project file a read shows when projectMemory refuses: the one the vendor reads instead.
	shownMemory string
	// userMemory are the candidates for the user's memory file below the home directory; the first present one
	// is the one the vendor reads.
	userMemory []string
	// maxMemory bounds a new project memory file where the vendor reads less than MaxWriteBytes; 0 if it does not.
	maxMemory int
	skillsDir string // below the project and the home directory; a skill is <name>/<skillFile>
	skillFile string
	// skillHeader says a skill must open with front matter that sets name and description, or the vendor skips it.
	skillHeader bool
	commandsDir string // below the project and the home directory; a command is <name><commandExt>; empty: none
	commandExt  string
}

// layouts lists the providers whose files can be read. A provider without an entry answers "unsupported". The
// paths are those of the vendor spike of ADR 0036.
var layouts = map[string]layout{
	"claude": {
		vendor:        "Claude Code",
		projectMemory: claudeProjectMemory,
		shownMemory:   "CLAUDE.md",
		userMemory:    []string{".claude/CLAUDE.md"},
		skillsDir:     ".claude/skills",
		skillFile:     "SKILL.md",
		commandsDir:   ".claude/commands",
		commandExt:    ".md",
	},
	// Codex reads AGENTS.md (in ~/.codex AGENTS.override.md first), at most 32 KiB in all, and skills from
	// .agents/skills of the project and of the home directory, whose front matter needs name and description. It
	// has no command files.
	"codex": {
		vendor:        "Codex",
		projectMemory: codexProjectMemory,
		shownMemory:   codexOverrideFile,
		userMemory:    []string{".codex/" + codexOverrideFile, ".codex/" + agentsFile},
		maxMemory:     codexMemoryBytes,
		skillsDir:     ".agents/skills",
		skillFile:     "SKILL.md",
		skillHeader:   true,
	},
}

// Request is one read.
type Request struct {
	Provider string
	Kind     string
	Scope    string
	Name     string // the skill or command; empty for memory and list
	Offset   int64  // the byte the page starts at
}

// Entry is one skill or command of a list.
type Entry struct {
	Kind        string
	Name        string
	Description string
}

// Page is the answer. A file that does not exist has Exists false and nothing else.
type Page struct {
	Exists     bool
	Size       int64  // of the whole file
	Hash       string // SHA-256 of the whole file, hex
	Offset     int64  // where Text starts
	Text       string
	NextOffset *int64 // set while more follows
	Entries    []Entry
}

// Reader reads configuration files. Home is the user's home directory.
type Reader struct {
	Home string
}

// Read answers req for the session whose working directory is cwd.
func (r Reader) Read(cwd string, req Request) (Page, error) {
	lay, ok := layouts[req.Provider]
	if !ok {
		return Page{}, refuse(CodeUnsupported, "no configuration files for provider "+req.Provider)
	}
	if req.Scope != ScopeProject && req.Scope != ScopeUser {
		return Page{}, refuse(CodeInvalid, "unknown scope")
	}
	if req.Offset < 0 {
		return Page{}, refuse(CodeInvalid, "negative offset")
	}
	switch req.Kind {
	case KindMemory, KindList:
		if req.Name != "" {
			return Page{}, refuse(CodeInvalid, req.Kind+" has no name")
		}
	case KindSkill, KindCommand:
		if !namePattern.MatchString(req.Name) {
			return Page{}, refuse(CodeInvalid, "invalid name")
		}
	default:
		return Page{}, refuse(CodeInvalid, "unknown kind")
	}

	if req.Kind == KindCommand && lay.commandsDir == "" {
		return Page{}, refuse(CodeUnsupported, lay.vendor+" has no command files")
	}
	if req.Kind == KindList {
		return r.list(cwd, req.Scope, lay)
	}

	var file string
	switch req.Kind {
	case KindMemory:
		if req.Scope == ScopeProject {
			// The file the writer would write, else the one the vendor reads instead (a project whose AGENTS.md
			// has no effect shows its CLAUDE.md).
			file = lay.shownMemory
			if resolved, err := lay.projectMemory(cwd, r.Home); err == nil {
				file = resolved
			}
		} else {
			file = lay.userMemory[len(lay.userMemory)-1]
			for _, candidate := range lay.userMemory {
				if r.Home != "" && present(filepath.Join(r.Home, filepath.FromSlash(candidate))) {
					file = candidate
					break
				}
			}
		}
	case KindSkill:
		file = path.Join(lay.skillsDir, req.Name, lay.skillFile)
	default:
		file = path.Join(lay.commandsDir, req.Name+lay.commandExt)
	}
	root, rel, err := r.open(cwd, req.Scope, file)
	if err != nil {
		return Page{}, err
	}
	if root == nil {
		return Page{Entries: []Entry{}}, nil
	}
	defer root.Close()
	return readPage(root, rel, req.Offset)
}

// open roots p of the scope: at the project directory itself, or for the user scope at the vendor's top
// directory of the home directory (which may be a symlink, as a dotfile manager makes it). rel is p below the
// root; a root that does not exist is (nil, "", nil).
func (r Reader) open(cwd, scope, p string) (*os.Root, string, error) {
	var dir, rel string
	if scope == ScopeProject {
		if !filepath.IsAbs(cwd) {
			return nil, "", refuse(CodeIO, "the session has no working directory")
		}
		dir, rel = cwd, p
	} else {
		if r.Home == "" {
			return nil, "", refuse(CodeIO, "no home directory")
		}
		top, below, _ := strings.Cut(p, "/")
		dir, rel = filepath.Join(r.Home, top), below
	}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", refuse(CodeIO, "cannot open "+scope+" directory")
	}
	return root, rel, nil
}

// regular opens rel under root when every component is a real directory and the last is a regular file; a
// path that is not there is (nil, nil).
func regular(root *os.Root, rel string) (*os.File, os.FileInfo, error) {
	parts := strings.Split(rel, "/")
	for i := range parts {
		info, err := root.Lstat(path.Join(parts[:i+1]...))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		if err != nil {
			return nil, nil, refuse(CodeIO, "cannot stat "+rel)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, nil, refuse(CodeInvalid, "symlink in path")
		}
		last := i == len(parts)-1
		if last && !info.Mode().IsRegular() {
			return nil, nil, refuse(CodeInvalid, "not a regular file")
		}
		if !last && !info.IsDir() {
			return nil, nil, refuse(CodeInvalid, "not a directory in path")
		}
	}
	f, err := root.Open(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, refuse(CodeInvalid, "cannot open file")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, refuse(CodeInvalid, "not a regular file")
	}
	return f, info, nil
}

func readPage(root *os.Root, rel string, offset int64) (Page, error) {
	f, info, err := regular(root, rel)
	if err != nil {
		return Page{}, err
	}
	if f == nil {
		return Page{Entries: []Entry{}}, nil
	}
	defer f.Close()
	if info.Size() > MaxFileBytes {
		return Page{}, refuse(CodeInvalid, "file is larger than 1 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return Page{}, refuse(CodeIO, "cannot read file")
	}
	if len(raw) > MaxFileBytes {
		return Page{}, refuse(CodeInvalid, "file is larger than 1 MiB")
	}
	if strings.IndexByte(string(raw), 0) >= 0 || !utf8.Valid(raw) {
		return Page{}, refuse(CodeInvalid, "file is not UTF-8 text")
	}
	sum := sha256.Sum256(raw)
	size := int64(len(raw))
	start := min(offset, size)
	// A page starts on a character: an offset inside one moves to the next.
	for start < size && !utf8.RuneStart(raw[start]) {
		start++
	}
	end := min(start+PageBytes, size)
	for end < size && end > start && !utf8.RuneStart(raw[end]) {
		end--
	}
	page := Page{
		Exists:  true,
		Size:    size,
		Hash:    hex.EncodeToString(sum[:]),
		Offset:  start,
		Text:    string(raw[start:end]),
		Entries: []Entry{},
	}
	if end < size {
		next := end
		page.NextOffset = &next
	}
	return page, nil
}

// list names the skills and, where the vendor has them, the commands of the scope.
func (r Reader) list(cwd, scope string, lay layout) (Page, error) {
	var entries []Entry
	add := func(kind, dir string, dirs bool, ext string) error {
		root, rel, err := r.open(cwd, scope, dir)
		if err != nil || root == nil {
			return err
		}
		defer root.Close()
		found, err := names(root, rel, dirs, ext)
		if err != nil {
			return err
		}
		for _, name := range found {
			file := path.Join(rel, name+ext)
			if dirs {
				file = path.Join(rel, name, lay.skillFile)
			}
			if desc, ok := describe(root, file); ok {
				entries = append(entries, Entry{Kind: kind, Name: name, Description: desc})
			}
		}
		return nil
	}
	if err := add(KindSkill, lay.skillsDir, true, ""); err != nil {
		return Page{}, err
	}
	if lay.commandsDir != "" {
		if err := add(KindCommand, lay.commandsDir, false, lay.commandExt); err != nil {
			return Page{}, err
		}
	}
	if len(entries) > MaxEntries {
		entries = entries[:MaxEntries]
	}
	if entries == nil {
		entries = []Entry{}
	}
	return Page{Exists: true, Entries: entries}, nil
}

// names lists the valid names in a directory: its subdirectories (dirs) or its files ending in ext. A
// symlink and a name that is not a plain one are skipped.
func names(root *os.Root, dir string, dirs bool, ext string) ([]string, error) {
	if !realDir(root, dir) {
		return nil, nil
	}
	d, err := root.Open(dir)
	if err != nil {
		return nil, nil
	}
	defer d.Close()
	items, err := d.ReadDir(maxScan)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, refuse(CodeIO, "cannot list "+dir)
	}
	var out []string
	for _, item := range items {
		name := item.Name()
		if dirs {
			if !item.IsDir() {
				continue
			}
		} else {
			if !item.Type().IsRegular() || !strings.HasSuffix(name, ext) {
				continue
			}
			name = strings.TrimSuffix(name, ext)
		}
		if namePattern.MatchString(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// realDir reports whether every component of dir is a directory and none is a symlink.
func realDir(root *os.Root, dir string) bool {
	parts := strings.Split(dir, "/")
	for i := range parts {
		info, err := root.Lstat(path.Join(parts[:i+1]...))
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// describe is the description of a skill or command and whether the file is there: the `description` of its
// front matter, else its first line of text. A file that cannot be read has no description.
func describe(root *os.Root, rel string) (string, bool) {
	f, _, err := regular(root, rel)
	if err != nil || f == nil {
		return "", false
	}
	defer f.Close()
	head, err := io.ReadAll(io.LimitReader(f, headBytes))
	if err != nil {
		return "", true
	}
	head = head[:len(head)-trailingPartial(head)]
	if !utf8.Valid(head) {
		return "", true
	}
	return clean(descriptionOf(string(head))), true
}

// trailingPartial is the length of an incomplete UTF-8 sequence at the end of b.
func trailingPartial(b []byte) int {
	for i := 1; i <= utf8.UTFMax-1 && i <= len(b); i++ {
		c := b[len(b)-i]
		if c < 0x80 {
			return 0
		}
		if utf8.RuneStart(c) {
			if utf8.FullRune(b[len(b)-i:]) {
				return 0
			}
			return i
		}
	}
	return 0
}

func descriptionOf(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	body := lines
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			line := strings.TrimSpace(lines[i])
			if line == "---" {
				body = lines[i+1:]
				break
			}
			if v, ok := strings.CutPrefix(line, "description:"); ok {
				return strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
	}
	for _, line := range body {
		if line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#")); line != "" {
			return line
		}
	}
	return ""
}

// clean makes a description one short line of printable text.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	for len(s) > MaxDescriptionBytes {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
	}
	return s
}
