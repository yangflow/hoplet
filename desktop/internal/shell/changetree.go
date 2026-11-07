package shell

import (
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui/editor"

	"github.com/yangflow/hoplet/proto"
)

// changeKind is what a row of the changes column shows.
type changeKind int

// Kinds of rows.
const (
	changeRepo changeKind = iota // a repository's heading
	changeDir                    // a directory of changed files
	changeFile                   // a changed file
	changeNote                   // why a repository shows no files
)

// changeRow is a row of the changes column. Key is stable across reads:
// the repository's path for its heading, the directory or file path
// relative to the working directory otherwise.
type changeRow struct {
	kind     changeKind
	depth    int
	key      string
	label    string
	repo     *proto.RepoDiff
	file     proto.FileDiff
	add, del int
	clean    bool   // a repository without changes
	open     bool   // a directory showing its files
	dir      string // a flat list's file's directory in its repository
}

// changeNode is a directory of a repository's changes. A directory
// holding one directory and no files is joined with it, as
// "internal/harness/hermes".
type changeNode struct {
	name  string
	path  string // relative to the repository
	dirs  []*changeNode
	files []proto.FileDiff
}

// changeTree arranges a repository's changed files by directory:
// directories first, then files, each by name.
func changeTree(files []proto.FileDiff) *changeNode {
	root := &changeNode{}
	for _, f := range files {
		n := root
		dir := path.Dir(f.Path)
		if dir != "." {
			for _, part := range strings.Split(dir, "/") {
				n = n.child(part)
			}
		}
		n.files = append(n.files, f)
	}
	root.settle()
	return root
}

// child returns the directory name under n, made on first use.
func (n *changeNode) child(name string) *changeNode {
	for _, d := range n.dirs {
		if d.name == name {
			return d
		}
	}
	d := &changeNode{name: name, path: path.Join(n.path, name)}
	n.dirs = append(n.dirs, d)
	return d
}

// settle joins single-directory chains and sorts what n holds.
func (n *changeNode) settle() {
	for i, d := range n.dirs {
		for len(d.files) == 0 && len(d.dirs) == 1 {
			only := d.dirs[0]
			only.name = d.name + "/" + only.name
			d = only
		}
		n.dirs[i] = d
		d.settle()
	}
	sort.Slice(n.dirs, func(i, j int) bool { return n.dirs[i].name < n.dirs[j].name })
	sort.Slice(n.files, func(i, j int) bool { return path.Base(n.files[i].Path) < path.Base(n.files[j].Path) })
}

// inRepo is a path of a repository relative to the working directory.
func inRepo(repo, p string) string {
	if repo == "." || repo == "" {
		return p
	}
	return repo + "/" + p
}

// repoLabel names a repository: the working directory's own by the
// directory's name.
func repoLabel(r proto.RepoDiff, dir string) string {
	if r.Path == "." || r.Path == "" {
		return filepath.Base(dir)
	}
	return r.Path
}

// changeTotals counts the files changed and their lines, over every
// repository.
func changeTotals(d *proto.SessionDiff) (files, add, del int) {
	if d == nil {
		return 0, 0, 0
	}
	for _, r := range d.Repos {
		for _, f := range r.Files {
			files++
			add += f.Additions
			del += f.Deletions
		}
	}
	return files, add, del
}

// changesNote is the line the changes column shows in place of its list,
// "" when it lists files: no repository, no baseline, nothing changed.
func changesNote(d *proto.SessionDiff) string {
	if d == nil {
		return ""
	}
	if d.BaselineError != "" {
		return "没有基线：" + d.BaselineError
	}
	if len(d.Repos) == 0 {
		return "这个目录不是 git 仓库，看不到改动"
	}
	for _, r := range d.Repos {
		if len(r.Files) > 0 || r.State == proto.RepoError || r.State == proto.RepoMissing {
			return ""
		}
	}
	return "没有改动"
}

// repoNote says why a repository lists no files.
func repoNote(r proto.RepoDiff) string {
	switch r.State {
	case proto.RepoMissing:
		return "仓库不见了"
	case proto.RepoError:
		return "读不了改动：" + r.Error
	}
	return ""
}

// changeRows lays out the changes column: each repository's heading,
// then its changed files by directory. closed holds the directories
// folded away.
func changeRows(d *proto.SessionDiff, dir string, closed map[string]bool) []changeRow {
	return changeList(d, dir, closed, false)
}

// changeList lays out the changes column by directory, or flat: each
// repository's files by path, one level under its heading.
func changeList(d *proto.SessionDiff, dir string, closed map[string]bool, flat bool) []changeRow {
	if d == nil || changesNote(d) != "" {
		return nil
	}
	var rows []changeRow
	for i := range d.Repos {
		r := &d.Repos[i]
		head := changeRow{kind: changeRepo, key: r.Path, label: repoLabel(*r, dir), repo: r, clean: len(r.Files) == 0}
		for _, f := range r.Files {
			head.add += f.Additions
			head.del += f.Deletions
		}
		if note := repoNote(*r); note != "" {
			rows = append(rows, head, changeRow{kind: changeNote, depth: 1, key: r.Path + "\x00note", label: note, repo: r})
			continue
		}
		rows = append(rows, head)
		if flat {
			rows = appendFlat(rows, r)
			continue
		}
		rows = appendNode(rows, changeTree(r.Files), r, 1, closed)
	}
	return rows
}

func appendNode(rows []changeRow, n *changeNode, r *proto.RepoDiff, depth int, closed map[string]bool) []changeRow {
	for _, d := range n.dirs {
		key := inRepo(r.Path, d.path)
		open := !closed[key]
		rows = append(rows, changeRow{kind: changeDir, depth: depth, key: key, label: d.name, repo: r, open: open})
		if open {
			rows = appendNode(rows, d, r, depth+1, closed)
		}
	}
	for _, f := range n.files {
		rows = append(rows, changeRow{kind: changeFile, depth: depth, key: inRepo(r.Path, f.Path), label: path.Base(f.Path),
			repo: r, file: f, add: f.Additions, del: f.Deletions})
	}
	return rows
}

// appendFlat lists a repository's files by path, each with its directory.
func appendFlat(rows []changeRow, r *proto.RepoDiff) []changeRow {
	files := slices.Clone(r.Files)
	slices.SortFunc(files, func(a, b proto.FileDiff) int { return strings.Compare(a.Path, b.Path) })
	for _, f := range files {
		dir := path.Dir(f.Path)
		if dir == "." {
			dir = ""
		}
		rows = append(rows, changeRow{kind: changeFile, depth: 1, key: inRepo(r.Path, f.Path), label: path.Base(f.Path),
			repo: r, file: f, add: f.Additions, del: f.Deletions, dir: dir})
	}
	return rows
}

// changedFiles lists every changed file in the column's order, closed
// directories or not: the changes tab shows them all.
func changedFiles(d *proto.SessionDiff, dir string) []changeRow {
	var out []changeRow
	for _, row := range changeRows(d, dir, nil) {
		if row.kind == changeFile {
			out = append(out, row)
		}
	}
	return out
}

// statusMark is a file's status as one letter: M, A, D or R.
func statusMark(s proto.FileStatus) string {
	switch s {
	case proto.FileStatusAdded:
		return "A"
	case proto.FileStatusDeleted:
		return "D"
	case proto.FileStatusRenamed:
		return "R"
	}
	return "M"
}

// unchangedBefore is how many unchanged lines lie between hunk i and the
// one before it, or the start of the file for the first. A hunk of no
// lines on a side starts after the line its header names.
func unchangedBefore(hunks []editor.PatchHunk, i int) int {
	start := func(h editor.PatchHunk) int {
		if h.NewLines == 0 {
			return h.NewStart + 1
		}
		return h.NewStart
	}
	prevEnd := 1
	if i > 0 {
		p := hunks[i-1]
		prevEnd = start(p) + p.NewLines
	}
	return max(0, start(hunks[i])-prevEnd)
}
