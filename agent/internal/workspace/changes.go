package workspace

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Untracked files have no diff to count from, so their lines are counted here. A file larger than
// maxCountBytes, or any file once maxCountTotal bytes were read for one listing, is listed without counts
// (only checked for being binary), so a large untracked tree costs a bounded read.
const (
	maxCountBytes = 1 << 20
	maxCountTotal = 16 << 20
)

// Change is one changed path against HEAD. Added and Removed are nil for a binary file and for an
// untracked file whose lines were not counted.
type Change struct {
	Path    string
	Status  string // modified | added | deleted | untracked
	Added   *int
	Removed *int
	Binary  bool
}

// listChanges reports every path inside dir that differs from HEAD, tracked or untracked and not ignored,
// sorted by path. Renames are a deletion and an addition.
func listChanges(ctx context.Context, dir string) ([]Change, error) {
	statuses, err := git(ctx, dir, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff", "--relative", "HEAD")
	if err != nil {
		return nil, err
	}
	numstat, err := git(ctx, dir, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", "--relative", "HEAD")
	if err != nil {
		return nil, err
	}
	byPath := map[string]*Change{}
	var changes []*Change
	fields := splitZ(statuses)
	for i := 0; i+1 < len(fields); i += 2 {
		c := &Change{Path: fields[i+1], Status: changeStatus(fields[i])}
		byPath[c.Path] = c
		changes = append(changes, c)
	}
	// Each numstat record is "<added>\t<removed>\t<path>", "-" for both counts of a binary file.
	for _, record := range splitZ(numstat) {
		parts := strings.SplitN(record, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		c := byPath[parts[2]]
		if c == nil {
			continue
		}
		if parts[0] == "-" {
			c.Binary = true
			continue
		}
		added, errA := strconv.Atoi(parts[0])
		removed, errR := strconv.Atoi(parts[1])
		if errA == nil && errR == nil {
			c.Added, c.Removed = &added, &removed
		}
	}
	untracked, err := git(ctx, dir, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	budget := int64(maxCountTotal)
	for _, name := range splitZ(untracked) {
		if strings.HasSuffix(name, "/") || byPath[name] != nil {
			continue // a nested repository
		}
		c := &Change{Path: name, Status: "untracked"}
		countUntracked(root, c, &budget)
		byPath[name] = c
		changes = append(changes, c)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	out := make([]Change, len(changes))
	for i, c := range changes {
		out[i] = *c
	}
	return out, nil
}

func changeStatus(letter string) string {
	switch letter {
	case "A":
		return "added"
	case "D":
		return "deleted"
	}
	return "modified" // M, and T (type change) and U (unmerged) read as modified
}

// countUntracked sets the line counts of an untracked regular file, or Binary when a NUL byte is in what
// git looks at. Links and files past the budget are left without counts.
func countUntracked(root *os.Root, c *Change, budget *int64) {
	info, err := root.Lstat(filepath.FromSlash(c.Path))
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	f, err := root.Open(filepath.FromSlash(c.Path))
	if err != nil {
		return
	}
	defer f.Close()
	if info.Size() > maxCountBytes || info.Size() > *budget {
		head, err := readPrefix(f, binarySniffBytes)
		if err == nil && bytes.IndexByte(head, 0) >= 0 {
			c.Binary = true
		}
		return
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxCountBytes))
	if err != nil {
		return
	}
	*budget -= int64(len(raw))
	if bytes.IndexByte(raw[:min(len(raw), binarySniffBytes)], 0) >= 0 {
		c.Binary = true
		return
	}
	added, removed := countLines(raw), 0
	c.Added, c.Removed = &added, &removed
}

// countLines counts lines as git does: a last line without a newline counts.
func countLines(b []byte) int {
	n := bytes.Count(b, []byte{'\n'})
	if len(b) > 0 && b[len(b)-1] != '\n' {
		n++
	}
	return n
}
