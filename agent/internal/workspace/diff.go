package workspace

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yangflow/hoplet/agent/internal/locate"
	"github.com/yangflow/hoplet/agent/internal/provider"
)

// Diff is one changed path's diff against HEAD (or, for DiffPage.Base, the commit the session's worktree
// started from): Kind "text" with one page of the hunks (git's unified format from the first "@@", at most
// the page's limit, cut at a line end) and Size, the bytes of all of them; or Kind "binary" with the sizes
// of the sides that exist. Truncated says the page ends before the hunks do; Next is the cursor of the page
// after it, absent when there is none (a single line longer than the limit is cut and the page after it
// starts at the next line).
type Diff struct {
	Status    string
	Kind      string
	Text      string
	Truncated bool
	Next      *int64
	Size      int64
	OldSize   *int64
	NewSize   *int64
}

// DiffPage selects what ReadDiffPage reads. The zero value is the first maxDiffBytes of the diff against HEAD.
type DiffPage struct {
	// Base compares with the commit the session's worktree started from instead of HEAD.
	Base bool
	// From is the cursor: the byte offset in the hunks where the page starts, 0 or a Next of an earlier page.
	From int64
	// Limit is the most bytes of hunks in the page; 0 is maxDiffBytes, and it is held between minDiffPage and
	// maxDiffBytes.
	Limit int
}

const minDiffPage = 1024

// errInvalidCursor is the reason for a cursor that is not the start of a line of the diff.
var errInvalidCursor = errors.New("invalid-cursor")

// ReadDiff returns the first page of the diff of one changed path of the session directory against HEAD.
func ReadDiff(ctx context.Context, finders []provider.Finder, sessionID, rel string) (Diff, error) {
	return ReadDiffPage(ctx, finders, sessionID, rel, DiffPage{})
}

// ReadDiffPage returns one page of the diff of one changed path of the session directory. An untracked file
// is all added lines, read through os.Root like any worktree read (ADR 0010). The cursor counts bytes of the
// hunks as they are now: a file that changes between two pages can make the second page not fit the first.
func ReadDiffPage(ctx context.Context, finders []provider.Finder, sessionID, rel string, page DiffPage) (Diff, error) {
	if page.From < 0 {
		return Diff{}, errInvalidCursor
	}
	if page.Limit <= 0 || page.Limit > maxDiffBytes {
		page.Limit = maxDiffBytes
	}
	page.Limit = max(page.Limit, minDiffPage)
	sess, err := locate.Locate(sessionID, finders...)
	if err != nil {
		return Diff{}, err
	}
	name, err := requestPath(sess.Cwd, rel)
	if err != nil {
		return Diff{}, err
	}
	if headCommit(ctx, sess.Cwd) == "" {
		return Diff{}, errNoRepository
	}
	rev := "HEAD"
	if page.Base {
		scope, err := changeScope(ctx, sess.Cwd, sessionID)
		if err != nil {
			return Diff{}, err
		}
		rev = scope.rev
	}
	status, err := pathStatus(ctx, sess.Cwd, rev, name)
	if err != nil {
		return Diff{}, err
	}
	if status == "untracked" {
		return untrackedDiff(sess.Cwd, name, page)
	}
	return trackedDiff(ctx, sess.Cwd, rev, name, status, page)
}

// literal makes a git command take its path arguments as names, never as patterns.
func literal(cmd *exec.Cmd) *exec.Cmd {
	cmd.Env = append(cmd.Env, "GIT_LITERAL_PATHSPECS=1")
	return cmd
}

// pathStatus is the change status of name against rev, or errNotChanged.
func pathStatus(ctx context.Context, dir, rev, name string) (string, error) {
	out, err := literal(gitCommand(ctx, dir, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff",
		"--relative", rev, "--", name)).Output()
	if err != nil {
		return "", wrap("diff", err, nil)
	}
	fields := splitZ(out)
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i+1] == name {
			return changeStatus(fields[i]), nil
		}
	}
	out, err = literal(gitCommand(ctx, dir, "ls-files", "-z", "--others", "--exclude-standard", "--", name)).Output()
	if err != nil {
		return "", wrap("ls-files", err, nil)
	}
	for _, f := range splitZ(out) {
		if f == name {
			return "untracked", nil
		}
	}
	return "", errNotChanged
}

// hunkWriter keeps one page of the hunks of a diff: it drops the header lines before the first "@@", notes a
// "Binary files … differ" line, keeps the bytes from page.From to page.From+page.Limit and counts everything.
type hunkWriter struct {
	page      DiffPage
	started   bool
	binary    bool
	lineStart bool
	keep      []byte
	size      int64 // bytes of hunks seen so far, which is also the offset of the next piece
	cursorOK  bool  // the cursor is the start of a line
	firstEnd  int64 // the end of the first line that ends after the cursor, 0 while none did
	lastEnd   int64 // the end of the last line that ends within the page, 0 while none did
	prevNL    bool  // the last hunk byte seen was a line end
}

func newHunkWriter(page DiffPage) *hunkWriter {
	return &hunkWriter{page: page, lineStart: true, cursorOK: page.From == 0}
}

// fragment takes the next piece of the diff; a piece never spans a line end.
func (h *hunkWriter) fragment(p []byte) {
	if len(p) == 0 {
		return
	}
	atStart := h.lineStart
	h.lineStart = p[len(p)-1] == '\n'
	if !h.started {
		if atStart && bytes.HasPrefix(p, []byte("Binary files ")) {
			h.binary = true
		}
		if !atStart || !bytes.HasPrefix(p, []byte("@@")) {
			return
		}
		h.started = true
	}
	from, end := h.page.From, h.page.From+int64(h.page.Limit)
	start, stop := h.size, h.size+int64(len(p))
	if start == from {
		h.cursorOK = h.size == 0 || h.prevNL
	}
	h.size = stop
	h.prevNL = p[len(p)-1] == '\n'
	if stop <= from {
		return
	}
	if p[len(p)-1] == '\n' {
		if h.firstEnd == 0 {
			h.firstEnd = stop
		}
		if stop <= end {
			h.lastEnd = stop
		}
	}
	lo, hi := max(start, from), min(stop, end)
	if lo < hi {
		h.keep = append(h.keep, p[lo-start:hi-start]...)
	}
}

// page is what the writer kept: the text, cut back to the last line end when the diff goes on past the
// page, and the cursor of the page after it. It is errInvalidCursor when the cursor is not the start of a
// line of the hunks or lies beyond them.
func (h *hunkWriter) result() (text string, truncated bool, next *int64, err error) {
	if h.page.From == h.size && h.size > 0 {
		h.cursorOK = h.prevNL
	}
	if !h.cursorOK || h.page.From > h.size {
		return "", false, nil, errInvalidCursor
	}
	if h.size <= h.page.From+int64(h.page.Limit) {
		return string(h.keep), false, nil, nil
	}
	if h.lastEnd > h.page.From {
		n := h.lastEnd
		return string(h.keep[:n-h.page.From]), true, &n, nil
	}
	// One line is longer than the page: the page is a piece of it, cut at a character, and the next page
	// starts after the line.
	keep := trimPartialRune(h.keep, true)
	if h.firstEnd > 0 && h.firstEnd < h.size {
		n := h.firstEnd
		return string(keep), true, &n, nil
	}
	return string(keep), true, nil, nil
}

// feed passes r to h line by line, in pieces of at most the reader's buffer.
func (h *hunkWriter) feed(r io.Reader) error {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		piece, err := br.ReadSlice('\n')
		h.fragment(piece)
		if err == nil || errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err == io.EOF {
			return nil
		}
		return err
	}
}

func trackedDiff(ctx context.Context, dir, rev, name, status string, page DiffPage) (Diff, error) {
	cmd := literal(gitCommand(ctx, dir, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames",
		"-U3", "--relative", rev, "--", name))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Diff{}, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Diff{}, err
	}
	h := newHunkWriter(page)
	feedErr := h.feed(out)
	if err := cmd.Wait(); err != nil {
		return Diff{}, fmt.Errorf("git diff: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if feedErr != nil {
		return Diff{}, feedErr
	}
	if !h.binary {
		return h.diff(status)
	}
	d := Diff{Status: status, Kind: "binary"}
	if status != "added" {
		if size, err := blobSize(ctx, dir, rev+":./"+name); err == nil {
			d.OldSize = &size
		}
	}
	if status != "deleted" {
		if root, err := os.OpenRoot(dir); err == nil {
			if info, err := root.Stat(filepath.FromSlash(name)); err == nil {
				size := info.Size()
				d.NewSize = &size
			}
			root.Close()
		}
	}
	return d, nil
}

// untrackedDiff synthesizes the diff git shows for a new file: one hunk of added lines, or binary when a
// NUL byte is in what git looks at.
func untrackedDiff(dir, name string, page DiffPage) (Diff, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Diff{}, err
	}
	defer root.Close()
	file, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return Diff{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Diff{}, err
	}
	if !info.Mode().IsRegular() {
		return Diff{}, errNotRegular
	}
	head, err := readPrefix(file, binarySniffBytes)
	if err != nil {
		return Diff{}, err
	}
	if bytes.IndexByte(head, 0) >= 0 {
		size := info.Size()
		return Diff{Status: "untracked", Kind: "binary", NewSize: &size}, nil
	}
	// The hunk header names the line count, so the lines are counted before they are written.
	lines, last, err := scanLines(io.MultiReader(bytes.NewReader(head), file))
	if err != nil {
		return Diff{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Diff{}, err
	}
	h := newHunkWriter(page)
	if lines == 0 {
		if page.From > 0 {
			return Diff{}, errInvalidCursor
		}
		return Diff{Status: "untracked", Kind: "text"}, nil
	}
	if lines == 1 {
		h.fragment([]byte("@@ -0,0 +1 @@\n"))
	} else {
		h.fragment(fmt.Appendf(nil, "@@ -0,0 +1,%d @@\n", lines))
	}
	br := bufio.NewReaderSize(io.LimitReader(file, last), 64<<10)
	lineStart := true
	for {
		piece, err := br.ReadSlice('\n')
		if len(piece) > 0 {
			if lineStart {
				h.fragment([]byte{'+'})
			}
			h.fragment(piece)
			lineStart = piece[len(piece)-1] == '\n'
		}
		if err == nil || errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != io.EOF {
			return Diff{}, err
		}
		break
	}
	if !lineStart {
		h.fragment([]byte("\n\\ No newline at end of file\n"))
	}
	return h.diff("untracked")
}

// diff is the text diff the writer kept, for a path with status.
func (h *hunkWriter) diff(status string) (Diff, error) {
	text, truncated, next, err := h.result()
	if err != nil {
		return Diff{}, err
	}
	return Diff{Status: status, Kind: "text", Text: text, Truncated: truncated, Next: next, Size: h.size}, nil
}

// scanLines counts the lines of r as git does and returns how many bytes they span, so the second pass
// reads exactly what was counted even if the file grows meanwhile.
func scanLines(r io.Reader) (lines int, size int64, err error) {
	buf := make([]byte, 64<<10)
	var lastByte byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			lines += bytes.Count(buf[:n], []byte{'\n'})
			size += int64(n)
			lastByte = buf[n-1]
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, err
		}
	}
	if size > 0 && lastByte != '\n' {
		lines++
	}
	return lines, size, nil
}
