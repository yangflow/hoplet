// Package claude is the provider of Claude Code, a native vendor (ADR 0012, ADR 0014): it serves sessions from
// Claude Code's append-only JSONL files under its projects store and installs them there during handoff.
package claude

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// readStep is how far Read extends its window while looking for the LF of an oversized first line.
const readStep int64 = 1 << 20

// Provider serves session sources from Claude Code's append-only JSONL files. It keeps one source per file
// across scans: a file whose size, mtime and inode did not change is not read again, and its cached title
// and cwd are reused. The state is disposable; a fresh Provider rebuilds it from disk.
type Provider struct {
	root    string
	exclude []string

	mu       sync.Mutex
	sources  map[string]*claudeSource // by path; only files the latest scan saw
	dirNames map[string]string        // DirName by project path, computed once per scan

	hints func() []string
}

// SetProjectHints gives Find the watched project paths. A session handed off from another machine is
// installed in the store of its new worktree, but the working directories its lines record are the old
// machine's; the watched paths say where it lives now.
func (c *Provider) SetProjectHints(hints func() []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hints = hints
}

// New serves the sessions under root, Claude Code's projects store, except those whose store directory
// decodes to a path under one of the exclude prefixes.
func New(root string, exclude []string) *Provider {
	return &Provider{root: root, exclude: exclude}
}

var (
	_ provider.Provider = (*Provider)(nil)
	_ provider.Store    = (*Provider)(nil)
)

func (c *Provider) Name() string { return "claude" }

// SessionFile is where Claude Code writes the session sessionID it runs in dir.
func (c *Provider) SessionFile(dir, sessionID string) string {
	return filepath.Join(c.root, DirName(dir), sessionID+".jsonl")
}

func (c *Provider) Install(cwd, sessionID, staged string) (string, error) {
	return installSession(c.root, cwd, sessionID, staged)
}

func (c *Provider) Remove(path string) { removeSession(c.root, path) }

// dirName is DirName(path), resolved once per scan for every source that asks.
func (c *Provider) dirName(path string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d, ok := c.dirNames[path]; ok {
		return d
	}
	if c.dirNames == nil {
		c.dirNames = map[string]string{}
	}
	d := DirName(path)
	c.dirNames[path] = d
	return d
}

// Find locates the session's file. A store can hold the same session twice: agents of two configurations on
// one machine share it, and after a handoff the session is in the store directory of the old working
// directory and of the new worktree. The copy in the store directory of a watched project is this agent's.
func (c *Provider) Find(sessionID string) (provider.Found, bool) {
	c.mu.Lock()
	hints := c.hints
	c.mu.Unlock()
	watched := map[string]bool{}
	if hints != nil {
		for _, p := range hints() {
			watched[DirName(p)] = true
		}
	}
	var best *sessionFile
	for _, f := range scanFiles(c.root, c.exclude) {
		if f.SessionID != sessionID {
			continue
		}
		f := f
		if watched[f.Dir] {
			best = &f
			break
		}
		if best == nil {
			best = &f
		}
	}
	if best == nil {
		return provider.Found{}, false
	}
	return provider.Found{Cwd: c.workingDir(*best), NativePath: best.Path}, true
}

// workingDir is the directory Claude Code must run in to resume the session: one whose store directory is
// the one the file is in. It is the directory the file records, the latest one it records, or a watched
// project; a directory that is none of them is the first one recorded.
func (c *Provider) workingDir(f sessionFile) string {
	first := firstCwd(f.Path)
	candidates := []string{first, lastCwd(f.Path)}
	c.mu.Lock()
	hints := c.hints
	c.mu.Unlock()
	if hints != nil {
		candidates = append(candidates, hints()...)
	}
	for _, dir := range candidates {
		if dir == "" || DirName(dir) != f.Dir {
			continue
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	if first == "" {
		return strings.ReplaceAll(f.Dir, "-", "/")
	}
	return first
}

func (c *Provider) Scan(_ context.Context) ([]provider.Source, error) {
	files := scanFiles(c.root, c.exclude)
	c.mu.Lock()
	defer c.mu.Unlock()
	next := make(map[string]*claudeSource, len(files))
	out := make([]provider.Source, 0, len(files))
	for _, f := range files {
		s := c.sources[f.Path]
		if s == nil {
			s = &claudeSource{file: f, owner: c}
		} else {
			s.update(f)
		}
		next[f.Path] = s
		out = append(out, s)
	}
	c.sources = next
	c.dirNames = nil
	return out, nil
}

type claudeSource struct {
	owner *Provider

	mu   sync.Mutex
	file sessionFile // the last scan's stat

	title   string
	titleOK bool
	cwd     string
	cwdOK   bool

	// idle remembers that Read found no complete line at idleOffset while the file had idleSize bytes.
	idle       bool
	idleOffset int64
	idleSize   int64
}

// update installs the stat of a new scan and drops the cached values the change can have made stale. The
// file is append-only, so growth keeps the cwd once found and keeps the title once the title window was
// full; a shrink, a different mtime at the same size, or a different inode means the file was rewritten.
func (s *claudeSource) update(f sessionFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.file
	s.file = f
	if prev.info != nil && f.info != nil && os.SameFile(prev.info, f.info) &&
		f.Size == prev.Size && f.ModTime.Equal(prev.ModTime) {
		return
	}
	rewritten := prev.info == nil || f.info == nil || !os.SameFile(prev.info, f.info) ||
		f.Size < prev.Size || (f.Size == prev.Size && !f.ModTime.Equal(prev.ModTime))
	s.idle = false
	if rewritten {
		s.titleOK, s.cwdOK = false, false
		return
	}
	if prev.Size < titlePeek {
		s.titleOK = false
	}
	if s.cwd == "" {
		s.cwdOK = false
	}
}

func (s *claudeSource) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.SessionID
}

func (s *claudeSource) Dir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Dir
}

// Cwd is the first cwd recorded in the file; it reads the file head only until one is found. A file that
// records none runs in the directory its store directory name encodes ('-' stands for '/'), which is lossy
// for names with hyphens.
func (s *claudeSource) Cwd() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cwdOK {
		s.cwd = strings.TrimSpace(firstCwd(s.file.Path))
		s.cwdOK = true
	}
	if s.cwd != "" {
		return s.cwd
	}
	return strings.ReplaceAll(s.file.Dir, "-", "/")
}

// InProject: the session runs in path, or its file is in path's store directory, where a session handed off
// from another machine is installed.
func (s *claudeSource) InProject(path string) bool {
	if filepath.Clean(s.Cwd()) == path {
		return true
	}
	return s.owner != nil && s.Dir() == s.owner.dirName(path)
}

// Title is PeekTitle's value, recomputed only after the file changed in a way that can alter it.
func (s *claudeSource) Title(_ context.Context) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.titleOK {
		data, _ := readWindow(s.file.Path, 0, titlePeek)
		s.title = titleFromData(data)
		s.titleOK = true
	}
	return s.title
}

// Size is the file size at the last scan; a source that no scan has stat'ed reads it now.
func (s *claudeSource) Size(_ context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file.info != nil {
		return s.file.Size, nil
	}
	fi, err := os.Stat(s.file.Path)
	if err != nil {
		return -1, nil // unreadable now: let Read surface the error or report nothing new
	}
	return fi.Size(), nil
}

func (s *claudeSource) Read(_ context.Context, offset int64, max int64) ([]byte, error) {
	s.mu.Lock()
	path := s.file.Path
	if s.idle && s.idleOffset == offset && s.file.info != nil && s.idleSize == s.file.Size {
		s.mu.Unlock()
		return nil, nil
	}
	s.mu.Unlock()

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if offset > size {
		return nil, provider.ErrBeyondEnd
	}
	if offset >= size || max <= 0 {
		return nil, nil
	}
	max = min(max, provider.MaxLineBytes)
	buf := make([]byte, min(size-offset, max))
	n, err := file.ReadAt(buf, offset)
	sessionBytesRead.Add(int64(n))
	if n <= 0 {
		return nil, err
	}
	chunk := buf[:n]
	if nl := lastByte(chunk, '\n'); nl >= 0 {
		return chunk[:nl+1], nil
	}
	// The first line is longer than the window or unfinished. Extend one step at a time up to the line limit.
	for int64(len(chunk)) < provider.MaxLineBytes && offset+int64(len(chunk)) < size {
		step := min(readStep, provider.MaxLineBytes-int64(len(chunk)), size-offset-int64(len(chunk)))
		old := len(chunk)
		chunk = append(chunk, make([]byte, step)...)
		n, _ := file.ReadAt(chunk[old:], offset+int64(old))
		sessionBytesRead.Add(int64(n))
		chunk = chunk[:old+n]
		if nl := bytes.IndexByte(chunk[old:], '\n'); nl >= 0 {
			return chunk[:old+nl+1], nil
		}
		if n < int(step) {
			break
		}
	}
	if int64(len(chunk)) >= provider.MaxLineBytes {
		return nil, &provider.LineTooLargeError{Offset: offset}
	}
	// An unfinished tail: nothing to read until the file grows.
	s.mu.Lock()
	s.idle, s.idleOffset, s.idleSize = true, offset, size
	s.mu.Unlock()
	return nil, nil
}

// readWindow returns the bytes in [offset, offset+max) cut at the last LF, without extending the window.
func readWindow(path string, offset, max int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if offset >= fi.Size() {
		return nil, nil
	}
	buf := make([]byte, min(fi.Size()-offset, max))
	n, _ := file.ReadAt(buf, offset)
	sessionBytesRead.Add(int64(n))
	buf = buf[:max0(n)]
	nl := lastByte(buf, '\n')
	if nl < 0 {
		return nil, nil
	}
	return buf[:nl+1], nil
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func lastByte(buf []byte, b byte) int {
	for i := len(buf) - 1; i >= 0; i-- {
		if buf[i] == b {
			return i
		}
	}
	return -1
}
