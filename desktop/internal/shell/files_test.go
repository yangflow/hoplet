package shell

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/proto"
)

// treeText writes rows as "depth path" or "depth (note)", one per row.
func treeText(rows []treeRow) []string {
	var out []string
	for _, r := range rows {
		s := strings.Repeat(" ", r.depth)
		if r.note != "" {
			s += "(" + r.note + ")"
		} else {
			s += r.entry.Path
		}
		out = append(out, s)
	}
	return out
}

func dirEntry(p string) proto.FileEntry  { return proto.FileEntry{Path: p, Kind: proto.FileKindDir} }
func fileEntry(p string) proto.FileEntry { return proto.FileEntry{Path: p, Kind: proto.FileKindFile} }

func TestTreeRows(t *testing.T) {
	tree := &fileTree{
		dirs: map[string]*dirListing{
			"":         {loaded: true, entries: []proto.FileEntry{dirEntry("cmd"), dirEntry("empty"), dirEntry("internal"), fileEntry("go.mod")}},
			"empty":    {loaded: true},
			"internal": {loaded: true, truncated: true, entries: []proto.FileEntry{fileEntry("internal/a.go")}},
			"cmd":      {loading: true},
		},
		open: map[string]bool{"empty": true, "internal": true, "cmd": true},
	}
	got := treeText(treeRows(tree))
	want := []string{"cmd", "empty", " (空目录)", "internal", " internal/a.go", " (只列出前 1 项)", "go.mod"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if r := treeRows(tree)[0]; !r.loading {
		t.Error("an open directory being read shows no spinner")
	}
}

func TestMatchRows(t *testing.T) {
	all := []proto.FileEntry{fileEntry("go.mod"), fileEntry("internal/harness/bridge.go"), fileEntry("internal/harness/gateway.go"), fileEntry("cmd/bridge/main.go")}
	got := treeText(matchRows(all, "Bridge"))
	want := []string{"cmd", " cmd/bridge", "  cmd/bridge/main.go", "internal", " internal/harness", "  internal/harness/bridge.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if rows := matchRows(all, "nothing"); len(rows) != 0 {
		t.Errorf("no match: %q", treeText(rows))
	}
}

// openTree opens s1's panel on the file tree, its root and internal
// directory listed.
func openTree(t *testing.T) (*App, *host, *ui.Tester) {
	t.Helper()
	a, h, tt, _ := newApp(t)
	h.diffs = map[proto.SessionID]proto.SessionDiff{"s1": changedDiff("a1")}
	h.lists = map[string]proto.FileList{
		"":         {Entries: []proto.FileEntry{dirEntry("internal"), fileEntry("notes.md"), fileEntry("logo.png"), fileEntry("big.log")}},
		"internal": {Entries: []proto.FileEntry{fileEntry("internal/bridge.go")}},
	}
	h.contents = map[string]proto.FileContent{
		"internal/bridge.go":      {Path: "internal/bridge.go", Text: "package internal\n\nfunc silent() bool { return true }\n"},
		"base:internal/bridge.go": {Path: "internal/bridge.go", Text: "package internal\n\nfunc silent() bool { return false }\n"},
		"notes.md":                {Path: "notes.md", Text: "# Notes\n"},
		"plain.txt":               {Path: "plain.txt", Text: "unchanged\n"},
		"logo.png":                {Path: "logo.png", Binary: true, Size: 2048},
		"big.log":                 {Path: "big.log", TooLarge: true, Size: 3 << 20},
	}
	tt.SetSize(1700, 900)
	a.Select("s1")
	settle(tt)
	a.ShowTree()
	settle(tt)
	return a, h, tt
}

func TestFileTreeReadsADirectoryAsItOpens(t *testing.T) {
	_, h, tt := openTree(t)
	if len(h.listed) != 1 || h.listed[0].Path != "" {
		t.Fatalf("listed %+v, want the root alone", h.listed)
	}
	for _, want := range []string{"internal", "notes.md", "1"} { // internal holds one changed file
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("internal"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if len(h.listed) != 2 || h.listed[1].Path != "internal" {
		t.Fatalf("listed %+v after opening internal", h.listed)
	}
	if !tt.HasText("bridge.go") || !tt.HasText("M") {
		t.Errorf("internal's file and its mark do not show: %q", tt.Texts())
	}
}

func TestFileTreeShowsIgnored(t *testing.T) {
	_, h, tt := openTree(t)
	if err := tt.Click("显示被忽略的文件"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	last := h.listed[len(h.listed)-1]
	if !last.Ignored || last.Path != "" {
		t.Errorf("listed %+v after showing ignored files", h.listed)
	}
	if err := tt.Click("隐藏被忽略的文件"); err != nil {
		t.Fatalf("the button does not turn to hiding them: %v", err)
	}
	settle(tt)
	if _, ok := tt.Find("显示被忽略的文件"); !ok {
		t.Error("the button does not turn back to showing them")
	}
}

func TestFileTreeOpensAFileOnce(t *testing.T) {
	a, h, tt := openTree(t)
	for range 2 {
		if err := tt.Click("notes.md"); err != nil {
			t.Fatal(err)
		}
		settle(tt)
	}
	ps := a.panelFor("s1")
	if len(ps.tabs) != 1 || ps.tabs[0].path != "notes.md" {
		t.Errorf("tabs %+v", ps.tabs)
	}
	if len(h.read) != 1 || h.read[0].Path != "notes.md" || h.read[0].Baseline {
		t.Errorf("read %+v", h.read)
	}
}

func TestFileTabShowsChanges(t *testing.T) {
	a, h, tt := openTree(t)
	ps := a.panelFor("s1")
	a.openFile(ps, "internal/bridge.go")
	settle(tt)
	for _, want := range []string{"hoplet", "internal", "bridge.go", "显示更改", "左右对比", "在 Finder 中显示", "打开方式"} {
		if !tt.HasText(want) {
			t.Errorf("no %q on the path bar: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("显示更改"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if last := h.read[len(h.read)-1]; !last.Baseline || last.Path != "internal/bridge.go" {
		t.Errorf("read %+v after asking for the changes", h.read)
	}
	if v := ps.viewFor("internal/bridge.go"); v.show != showChanges || v.base == nil {
		t.Errorf("show %v, baseline %v", v.show, v.base)
	}
	// A file without changes offers neither.
	a.openFile(ps, "plain.txt")
	settle(tt)
	if tt.HasText("显示更改") {
		t.Error("an unchanged file offers its changes")
	}
}

func TestFileTabNotes(t *testing.T) {
	a, _, tt := openTree(t)
	ps := a.panelFor("s1")
	for p, want := range map[string]string{"logo.png": "二进制文件，不显示内容", "big.log": "文件超过 2 MiB，不显示内容"} {
		a.openFile(ps, p)
		settle(tt)
		if !tt.HasText(want) {
			t.Errorf("%s: no %q in %q", p, want, tt.Texts())
		}
	}
}

func TestFileTabOpensElsewhere(t *testing.T) {
	a, h, tt := openTree(t)
	a.openFile(a.panelFor("s1"), "notes.md")
	settle(tt)
	for _, item := range []string{"用默认应用打开", "复制路径"} {
		if err := tt.Click("打开方式"); err != nil {
			t.Fatal(err)
		}
		settle(tt)
		if err := tt.Click(item); err != nil {
			t.Fatal(err)
		}
		settle(tt)
	}
	a.CopyPath()
	want := []any{"open /w/hoplet/notes.md", "copy /w/hoplet/notes.md", "copy /w/hoplet/notes.md"}
	if got := h.calls[len(h.calls)-3:]; !reflect.DeepEqual(got, want) {
		t.Errorf("calls %q, want %q", got, want)
	}
}

// TestFileTreeReadsAgainAfterChange lists the open directories and reads
// the open file again once the session changes a file.
func TestFileTreeReadsAgainAfterChange(t *testing.T) {
	a, h, tt := openTree(t)
	ps := a.panelFor("s1")
	a.openDir(proto.Session{ID: "s1"}, &ps.tree, "internal")
	a.openFile(ps, "notes.md")
	settle(tt)
	listed, read := len(h.listed), len(h.read)
	payload, err := json.Marshal(proto.FileChange{Path: "/w/hoplet/notes.md", Op: proto.FileModified})
	if err != nil {
		t.Fatal(err)
	}
	a.ApplySession("s1", proto.SessionItem{Events: []proto.Event{{Session: "s1", Seq: 9, Type: proto.TypeFileChange, V: 1, Payload: payload}}})
	ps.column = columnTree
	settle(tt)
	if len(h.read) != read+1 {
		t.Errorf("read %d more times after a change, want once", len(h.read)-read)
	}
	// The tree is not shown while the file tab is, so it reads again when
	// it shows.
	a.ShowTree()
	settle(tt)
	if got := len(h.listed) - listed; got != 2 {
		t.Errorf("listed %d directories again, want the root and internal", got)
	}
}

// TestFileTreeRetriesOnReconnect reads again what could not be read once
// the daemon is reached again.
func TestFileTreeRetriesOnReconnect(t *testing.T) {
	a, h, tt := openTree(t)
	ps := a.panelFor("s1")
	ps.tree.dirs = map[string]*dirListing{"": {err: "method not found"}}
	settle(tt)
	listed := len(h.listed)
	a.Connected("studio")
	settle(tt)
	if len(h.listed) != listed+1 || !tt.HasText("notes.md") {
		t.Errorf("listed %d more times after reconnecting, texts %q", len(h.listed)-listed, tt.Texts())
	}
}

func TestMarkdownPreview(t *testing.T) {
	a, _, tt := openTree(t)
	ps := a.panelFor("s1")
	a.openFile(ps, "notes.md")
	settle(tt)
	v := ps.viewFor("notes.md")
	if !tt.HasText("显示源码") || tt.HasText("显示预览") || !tt.HasText("Notes") {
		t.Fatalf("no preview: %q", tt.Texts())
	}
	if err := tt.Click("显示源码"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if !v.source || !tt.HasText("显示预览") {
		t.Fatalf("no source: %q", tt.Texts())
	}
	if err := tt.Click("显示预览"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if v.source || !tt.HasText("显示源码") {
		t.Fatalf("no preview again: %q", tt.Texts())
	}
	// Its changes show in its source, which it keeps to after.
	if err := tt.Click("显示更改"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if v.show != showChanges || !v.source || !tt.HasText("显示预览") {
		t.Fatalf("show %v, source %v: %q", v.show, v.source, tt.Texts())
	}
	if err := tt.Click("显示预览"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if v.show != showText || v.source {
		t.Errorf("show %v, source %v", v.show, v.source)
	}
	// Other files have no preview.
	a.openFile(ps, "internal/bridge.go")
	settle(tt)
	if tt.HasText("显示源码") || tt.HasText("显示预览") {
		t.Errorf("a preview toggle on a Go file: %q", tt.Texts())
	}
}
