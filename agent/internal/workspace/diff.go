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

// Diff is one changed path's diff against HEAD: Kind "text" with the hunks (git's unified format from the
// first "@@", at most maxDiffBytes and cut at a line end, Truncated beyond) and Size, the bytes of all of
// them; or Kind "binary" with the sizes of the sides that exist.
type Diff struct {
	Status    string
	Kind      string
	Text      string
	Truncated bool
	Size      int64
	OldSize   *int64
	NewSize   *int64
}

// ReadDiff returns the diff of one changed path of the session directory against HEAD. An untracked file
// is all added lines, read through os.Root like any worktree read (ADR 0010).
func ReadDiff(ctx context.Context, finders []provider.Finder, sessionID, rel string) (Diff, error) {
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
	status, err := pathStatus(ctx, sess.Cwd, name)
	if err != nil {
		return Diff{}, err
	}
	if status == "untracked" {
		return untrackedDiff(sess.Cwd, name)
	}
	return trackedDiff(ctx, sess.Cwd, name, status)
}

// literal makes a git command take its path arguments as names, never as patterns.
func literal(cmd *exec.Cmd) *exec.Cmd {
	cmd.Env = append(cmd.Env, "GIT_LITERAL_PATHSPECS=1")
	return cmd
}

// pathStatus is the change status of name against HEAD, or errNotChanged.
func pathStatus(ctx context.Context, dir, name string) (string, error) {
	out, err := literal(gitCommand(ctx, dir, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff",
		"--relative", "HEAD", "--", name)).Output()
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

// hunkWriter keeps the hunks of a diff: it drops the header lines before the first "@@", notes a
// "Binary files … differ" line, keeps at most maxDiffBytes and counts everything.
type hunkWriter struct {
	started   bool
	binary    bool
	lineStart bool
	keep      []byte
	size      int64
	truncated bool
}

func newHunkWriter() *hunkWriter { return &hunkWriter{lineStart: true} }

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
	h.size += int64(len(p))
	if h.truncated {
		return
	}
	if len(h.keep)+len(p) > maxDiffBytes {
		h.truncated = true
		return
	}
	h.keep = append(h.keep, p...)
}

// text is what was kept, cut back to the last line end when the diff was truncated.
func (h *hunkWriter) text() string {
	if h.truncated {
		if i := bytes.LastIndexByte(h.keep, '\n'); i >= 0 {
			return string(h.keep[:i+1])
		}
	}
	return string(h.keep)
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

func trackedDiff(ctx context.Context, dir, name, status string) (Diff, error) {
	cmd := literal(gitCommand(ctx, dir, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames",
		"-U3", "--relative", "HEAD", "--", name))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Diff{}, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Diff{}, err
	}
	h := newHunkWriter()
	feedErr := h.feed(out)
	if err := cmd.Wait(); err != nil {
		return Diff{}, fmt.Errorf("git diff: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if feedErr != nil {
		return Diff{}, feedErr
	}
	if !h.binary {
		return Diff{Status: status, Kind: "text", Text: h.text(), Truncated: h.truncated, Size: h.size}, nil
	}
	d := Diff{Status: status, Kind: "binary"}
	if status != "added" {
		if size, err := blobSize(ctx, dir, "HEAD:./"+name); err == nil {
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
func untrackedDiff(dir, name string) (Diff, error) {
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
	h := newHunkWriter()
	if lines == 0 {
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
	return Diff{Status: "untracked", Kind: "text", Text: h.text(), Truncated: h.truncated, Size: h.size}, nil
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
