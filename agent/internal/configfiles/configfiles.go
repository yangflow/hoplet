// Package configfiles reads a session's own configuration files: the memory file, the skills and the commands
// of its project or of the user (ADR 0036). The paths are fixed by the provider and the kind and are never
// taken from the caller; a name must be a plain `[a-z0-9][a-z0-9-]{0,63}`. Everything is read through
// os.Root, so nothing outside the project directory (or the user's provider directory) is reachable, and a
// symlink is refused at every step. The package only reads; writing is a separate path (ADR 0036, step 3).
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

// layout is where one provider keeps its configuration, relative to the project directory and to the user's
// home directory.
type layout struct {
	projectMemory string // in the project directory
	userMemory    string // in the user's provider directory
	configDir     string // the provider directory, in the project and in the home directory
	skillsDir     string // below the provider directory; a skill is <name>/<skillFile>
	skillFile     string
	commandsDir   string // below the provider directory; a command is <name><commandExt>
	commandExt    string
}

// layouts lists the providers whose files can be read. A provider without an entry answers "unsupported".
var layouts = map[string]layout{
	"claude": {
		projectMemory: "CLAUDE.md",
		userMemory:    "CLAUDE.md",
		configDir:     ".claude",
		skillsDir:     "skills",
		skillFile:     "SKILL.md",
		commandsDir:   "commands",
		commandExt:    ".md",
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

	dir, prefix, err := r.rootOf(cwd, req.Scope, lay)
	if err != nil {
		return Page{}, err
	}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return Page{Entries: []Entry{}}, nil
	}
	if err != nil {
		return Page{}, refuse(CodeIO, "cannot open "+req.Scope+" directory")
	}
	defer root.Close()

	switch req.Kind {
	case KindList:
		return list(root, prefix, lay)
	case KindMemory:
		memory := lay.projectMemory
		if req.Scope == ScopeUser {
			memory = lay.userMemory
		}
		return readPage(root, memory, req.Offset)
	case KindSkill:
		return readPage(root, path.Join(prefix, lay.skillsDir, req.Name, lay.skillFile), req.Offset)
	default:
		return readPage(root, path.Join(prefix, lay.commandsDir, req.Name+lay.commandExt), req.Offset)
	}
}

// rootOf is the directory every path of the scope stays inside and the provider directory below it: the
// project directory itself, or the provider directory of the user's home.
func (r Reader) rootOf(cwd, scope string, lay layout) (dir, prefix string, err error) {
	if scope == ScopeProject {
		if !filepath.IsAbs(cwd) {
			return "", "", refuse(CodeIO, "the session has no working directory")
		}
		return cwd, lay.configDir, nil
	}
	if r.Home == "" {
		return "", "", refuse(CodeIO, "no home directory")
	}
	return path.Join(r.Home, lay.configDir), "", nil
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

func list(root *os.Root, prefix string, lay layout) (Page, error) {
	var entries []Entry
	skills, err := names(root, path.Join(prefix, lay.skillsDir), true, "")
	if err != nil {
		return Page{}, err
	}
	for _, name := range skills {
		if desc, ok := describe(root, path.Join(prefix, lay.skillsDir, name, lay.skillFile)); ok {
			entries = append(entries, Entry{Kind: KindSkill, Name: name, Description: desc})
		}
	}
	commands, err := names(root, path.Join(prefix, lay.commandsDir), false, lay.commandExt)
	if err != nil {
		return Page{}, err
	}
	for _, name := range commands {
		if desc, ok := describe(root, path.Join(prefix, lay.commandsDir, name+lay.commandExt)); ok {
			entries = append(entries, Entry{Kind: KindCommand, Name: name, Description: desc})
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
