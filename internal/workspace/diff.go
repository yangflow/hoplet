package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/yangflow/hoplet/proto"
)

// diffFlags make diff-tree output independent of the user's configuration;
// patchFlags do the same for the text of a patch.
var (
	diffFlags  = []string{"-r", "-M", "--no-ext-diff", "--no-textconv", "--no-color"}
	patchFlags = []string{"-p", "--src-prefix=a/", "--dst-prefix=b/", "-U3"}
)

// DiffFiles lists the files that differ between the trees base and now, by
// path, with their line counts. A file whose attributes select a git filter
// carries the filter's name in Filter.
func (g Git) DiffFiles(ctx context.Context, r Repo, objects, base, now string) ([]proto.FileDiff, error) {
	o, err := g.repoOpts(ctx, r, objectEnv(objects, indexPath(objects, r))...)
	if err != nil {
		return nil, err
	}
	diffTree := func(format string) ([]byte, error) {
		return g.output(ctx, o, append(append([]string{"diff-tree"}, diffFlags...), "-z", format, base, now)...)
	}
	raw, err := diffTree("--raw")
	if err != nil {
		return nil, err
	}
	files, err := parseRaw(raw)
	if err != nil || len(files) == 0 {
		return files, err
	}
	num, err := diffTree("--numstat")
	if err != nil {
		return nil, err
	}
	if err := applyNumstat(files, num); err != nil {
		return nil, err
	}
	if err := g.applyFilters(ctx, o, files); err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b proto.FileDiff) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// Diff lists the files that differ between the trees base and now and
// returns their patch, cut at a line boundary, with truncated set, when it
// is longer than MaxPatch (when no line end lies within the cap, the patch is
// empty). The patch leaves out files that have a filter. Bytes that are not
// valid UTF-8 are replaced by U+FFFD, as JSON would do anyway.
func (g Git) Diff(ctx context.Context, r Repo, objects, base, now string) (files []proto.FileDiff, patch string, truncated bool, err error) {
	files, err = g.DiffFiles(ctx, r, objects, base, now)
	if err != nil || len(files) == 0 {
		return files, "", false, err
	}
	o, err := g.repoOpts(ctx, r, objectEnv(objects, indexPath(objects, r))...)
	if err != nil {
		return nil, "", false, err
	}
	args := append(append(append([]string{"diff-tree"}, diffFlags...), patchFlags...), base, now)
	var excluded []string
	for _, f := range files {
		if f.Filter != "" {
			excluded = append(excluded, ":(exclude,literal)"+f.Path)
			if f.OldPath != "" {
				excluded = append(excluded, ":(exclude,literal)"+f.OldPath)
			}
		}
	}
	if len(excluded) > 0 {
		args = append(append(args, "--"), excluded...)
	}
	out, more, err := g.run(ctx, o, int64(g.maxPatch()), args...)
	if err != nil {
		return nil, "", false, err
	}
	if more && !bytes.HasSuffix(out, []byte("\n")) {
		// Cut at the last line end; a single line longer than the cap
		// leaves no patch.
		out = out[:bytes.LastIndexByte(out, '\n')+1]
	}
	return files, strings.ToValidUTF8(string(out), "\uFFFD"), more, nil
}

// parseRaw reads diff-tree --raw -z output.
func parseRaw(raw []byte) ([]proto.FileDiff, error) {
	toks := strings.Split(string(raw), "\x00")
	files := []proto.FileDiff{}
	for i := 0; i < len(toks); i++ {
		meta := toks[i]
		if meta == "" {
			continue
		}
		f := strings.Fields(strings.TrimPrefix(meta, ":"))
		if !strings.HasPrefix(meta, ":") || len(f) != 5 {
			return nil, fmt.Errorf("workspace: unexpected diff-tree output %q", meta)
		}
		status := f[4][0]
		n := 1 // paths that follow
		if status == 'R' || status == 'C' {
			n = 2
		}
		if i+n >= len(toks) {
			return nil, errors.New("workspace: truncated diff-tree output")
		}
		d := proto.FileDiff{Status: statusOf(status), Path: toks[i+n]}
		if status == 'R' {
			d.OldPath = toks[i+1]
		}
		i += n
		files = append(files, d)
	}
	return files, nil
}

func statusOf(c byte) proto.FileStatus {
	switch c {
	case 'A', 'C':
		return proto.FileStatusAdded
	case 'D':
		return proto.FileStatusDeleted
	case 'R':
		return proto.FileStatusRenamed
	case 'T':
		return proto.FileStatusTypeChanged
	}
	return proto.FileStatusModified
}

// applyNumstat fills in line counts from diff-tree --numstat -z output.
func applyNumstat(files []proto.FileDiff, num []byte) error {
	byPath := make(map[string]*proto.FileDiff, len(files))
	for i := range files {
		byPath[files[i].Path] = &files[i]
	}
	toks := strings.Split(string(num), "\x00")
	for i := 0; i < len(toks); i++ {
		if toks[i] == "" {
			continue
		}
		parts := strings.SplitN(toks[i], "\t", 3)
		if len(parts) != 3 {
			return fmt.Errorf("workspace: unexpected numstat output %q", toks[i])
		}
		path := parts[2]
		if path == "" { // a rename: the old and the new path follow
			if i+2 >= len(toks) {
				return errors.New("workspace: truncated numstat output")
			}
			path = toks[i+2]
			i += 2
		}
		f := byPath[path]
		if f == nil {
			continue
		}
		if parts[0] == "-" || parts[1] == "-" {
			f.Binary = true
			continue
		}
		var err1, err2 error
		f.Additions, err1 = strconv.Atoi(parts[0])
		f.Deletions, err2 = strconv.Atoi(parts[1])
		if err := errors.Join(err1, err2); err != nil {
			return fmt.Errorf("workspace: numstat line %q: %w", toks[i], err)
		}
	}
	return nil
}

// applyFilters marks the files whose attributes select a git filter.
func (g Git) applyFilters(ctx context.Context, o runOpts, files []proto.FileDiff) error {
	var in bytes.Buffer
	for _, f := range files {
		in.WriteString(f.Path)
		in.WriteByte(0)
	}
	o.Stdin = in.Bytes()
	out, err := g.output(ctx, o, "check-attr", "-z", "--stdin", "filter")
	if err != nil {
		return err
	}
	toks := strings.Split(string(out), "\x00")
	filters := map[string]string{}
	for i := 0; i+2 < len(toks); i += 3 {
		switch v := toks[i+2]; v {
		case "unspecified", "unset", "set":
		default:
			filters[toks[i]] = v
		}
	}
	for i := range files {
		files[i].Filter = filters[files[i].Path]
	}
	return nil
}

// hasTree reports whether the tree id is in Hoplet's objects.
func (g Git) hasTree(ctx context.Context, r Repo, objects, id string) bool {
	o, err := g.repoOpts(ctx, r, objectEnv(objects, "")...)
	if err != nil {
		return true // do not blame the store for another failure
	}
	_, err = g.output(ctx, o, "cat-file", "-e", id+"^{tree}")
	return err == nil
}
