package shell

import (
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"github.com/yangflow/hoplet/proto"
)

// rowsText writes rows as "depth kind label", one per row.
func rowsText(rows []changeRow) []string {
	kinds := map[changeKind]string{changeRepo: "repo", changeDir: "dir", changeFile: "file", changeNote: "note"}
	var out []string
	for _, r := range rows {
		s := strings.Repeat(" ", r.depth) + kinds[r.kind] + " " + r.label
		if r.kind == changeFile {
			s += " " + statusMark(r.file.Status)
		}
		out = append(out, s)
	}
	return out
}

func TestChangeRows(t *testing.T) {
	mod := func(p string) proto.FileDiff {
		return proto.FileDiff{Path: p, Status: proto.FileStatusModified, Additions: 2, Deletions: 1}
	}
	for _, tc := range []struct {
		name   string
		diff   *proto.SessionDiff
		closed map[string]bool
		want   []string
		note   string
	}{
		{
			name: "one repository, a chain of directories joined",
			diff: &proto.SessionDiff{Repos: []proto.RepoDiff{{Path: ".", State: proto.RepoOK, Files: []proto.FileDiff{
				mod("internal/harness/hermes/bridge.go"), mod("internal/harness/hermes/bridge_test.go"),
				{Path: "internal/harness/hermes/testdata/silent.jsonl", Status: proto.FileStatusAdded},
				{Path: "docs/old-notes.md", Status: proto.FileStatusDeleted},
				{Path: "README.md", OldPath: "README", Status: proto.FileStatusRenamed},
			}}}},
			want: []string{
				"repo hoplet",
				" dir docs", "  file old-notes.md D",
				" dir internal/harness/hermes", "  dir testdata", "   file silent.jsonl A",
				"  file bridge.go M", "  file bridge_test.go M",
				" file README.md R",
			},
		},
		{
			name:   "a closed directory hides its files",
			diff:   &proto.SessionDiff{Repos: []proto.RepoDiff{{Path: ".", State: proto.RepoOK, Files: []proto.FileDiff{mod("a/b.go"), mod("c.go")}}}},
			closed: map[string]bool{"a": true},
			want:   []string{"repo hoplet", " dir a", " file c.go M"},
		},
		{
			name: "several repositories, one nested, one clean, one failing",
			diff: &proto.SessionDiff{Repos: []proto.RepoDiff{
				{Path: ".", State: proto.RepoOK, Files: []proto.FileDiff{mod("main.go")}},
				{Path: "vendor/lib", Parent: ".", State: proto.RepoOK, Files: []proto.FileDiff{mod("lib.go")}},
				{Path: "site", State: proto.RepoOK},
				{Path: "gone", State: proto.RepoMissing},
				{Path: "bad", State: proto.RepoError, Error: "exit status 128"},
			}},
			want: []string{
				"repo hoplet", " file main.go M",
				"repo vendor/lib", " file lib.go M",
				"repo site",
				"repo gone", " note 仓库不见了",
				"repo bad", " note 读不了改动：exit status 128",
			},
		},
		{
			name: "nothing changed",
			diff: &proto.SessionDiff{Repos: []proto.RepoDiff{{Path: ".", State: proto.RepoOK}, {Path: "site", State: proto.RepoOK}}},
			note: "没有改动",
		},
		{
			name: "no repository",
			diff: &proto.SessionDiff{Repos: []proto.RepoDiff{}},
			note: "这个目录不是 git 仓库，看不到改动",
		},
		{
			name: "no baseline",
			diff: &proto.SessionDiff{BaselineError: "建会话时快照超时"},
			note: "没有基线：建会话时快照超时",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := changesNote(tc.diff); got != tc.note {
				t.Errorf("note %q, want %q", got, tc.note)
			}
			if got := rowsText(changeRows(tc.diff, "/w/hoplet", tc.closed)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("rows\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

func TestChangeTotalsAndKeys(t *testing.T) {
	d := &proto.SessionDiff{Repos: []proto.RepoDiff{
		{Path: ".", State: proto.RepoOK, Files: []proto.FileDiff{{Path: "a/b.go", Additions: 3, Deletions: 1}}},
		{Path: "site", State: proto.RepoOK, Files: []proto.FileDiff{{Path: "index.html", Additions: 2}, {Path: "logo.png", Binary: true}}},
	}}
	if f, add, del := changeTotals(d); f != 3 || add != 5 || del != 1 {
		t.Errorf("totals %d +%d −%d", f, add, del)
	}
	var keys []string
	for _, r := range changedFiles(d, "/w/hoplet") {
		keys = append(keys, r.key)
	}
	if want := []string{"a/b.go", "site/index.html", "site/logo.png"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("file keys %q, want %q", keys, want)
	}
}

func TestUnchangedBefore(t *testing.T) {
	h := func(os, ol, ns, nl int) editor.PatchHunk {
		return editor.PatchHunk{OldStart: os, OldLines: ol, NewStart: ns, NewLines: nl}
	}
	for _, tc := range []struct {
		name  string
		hunks []editor.PatchHunk
		want  []int
	}{
		{"at the top", []editor.PatchHunk{h(1, 4, 1, 5)}, []int{0}},
		{"lower down, then after a gap", []editor.PatchHunk{h(10, 7, 10, 8), h(40, 6, 41, 6)}, []int{9, 23}},
		{"right after the one before", []editor.PatchHunk{h(3, 2, 3, 2), h(5, 1, 5, 1)}, []int{2, 0}},
		{"only deleted lines", []editor.PatchHunk{h(5, 2, 4, 0), h(20, 1, 18, 2)}, []int{4, 13}},
		{"a new file", []editor.PatchHunk{h(0, 0, 1, 12)}, []int{0}},
		{"a deleted file", []editor.PatchHunk{h(1, 12, 0, 0)}, []int{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []int
			for i := range tc.hunks {
				got = append(got, unchangedBefore(tc.hunks, i))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
