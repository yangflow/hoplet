package shell

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

// twoRepos is s1's changes in two repositories with a file each.
func twoRepos() proto.SessionDiff {
	return proto.SessionDiff{Session: "s1", Repos: []proto.RepoDiff{
		{Path: "api", Branch: "feat/heartbeat", State: proto.RepoOK, SHA: "a1", Files: []proto.FileDiff{
			{Path: "server.go", Status: proto.FileStatusModified, Additions: 1},
		}},
		{Path: "web", Branch: "main", State: proto.RepoOK, SHA: "w1", Files: []proto.FileDiff{
			{Path: "app.ts", Status: proto.FileStatusModified, Additions: 1},
		}},
	}}
}

// oneRepo is s1's changes in the directory's own repository.
func oneRepo() proto.SessionDiff {
	d := changedDiff("a1")
	d.Repos = d.Repos[:1]
	return d
}

// openTab opens the changes tab from a file of the column.
func openTab(t *testing.T, tt interface{ Click(string) error }, file string) {
	t.Helper()
	if err := tt.Click(file); err != nil {
		t.Fatal(err)
	}
}

func TestScopeMenuPicksRange(t *testing.T) {
	a, h, tt := openChanges(t)
	openTab(t, tt, "internal/bridge.go")
	settle(tt)
	h.ranged = map[string]proto.SessionDiff{string(proto.DiffRangeUncommitted): {Session: "s1", Repos: []proto.RepoDiff{
		{Path: ".", State: proto.RepoOK, SHA: "u1", Files: []proto.FileDiff{{Path: "notes.md", Status: proto.FileStatusAdded, Additions: 2}}},
	}}}
	if err := tt.Click("范围：会话累计"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	for _, want := range []string{"建会话以来", "对比 HEAD", "分支改动", "先选一个仓库"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in the menu: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("未提交的改动"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if got := h.diffAsked[len(h.diffAsked)-1]; got.Range != proto.DiffRangeUncommitted {
		t.Fatalf("asked %+v", got)
	}
	if !tt.HasText("范围：未提交的改动") || !tt.HasText("· 1 个文件") {
		t.Errorf("the tab did not move: %q", tt.Texts())
	}
	if d := a.s.diffs["s1"].diff; d == nil || len(d.Repos[0].Files) != 2 {
		t.Errorf("another range overwrote the conversation's changes: %+v", d)
	}
}

func TestScopeMenuCommits(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.diffs = map[proto.SessionID]proto.SessionDiff{"s1": oneRepo()}
	h.commits = proto.SessionCommits{Repos: []proto.RepoCommits{{Path: ".", Base: "origin/main", Commits: []proto.Commit{
		{SHA: "1111111aaaa", Subject: "Add heartbeat"},
		{SHA: "2222222bbbb", Subject: "Tune heartbeat"},
	}, Truncated: true}}}
	h.ranged = map[string]proto.SessionDiff{"commit:2222222bbbb": {Session: "s1", Repos: []proto.RepoDiff{
		{Path: ".", State: proto.RepoOK, SHA: "c2", Files: []proto.FileDiff{{Path: "sync.go", Status: proto.FileStatusModified, Additions: 4}}},
	}}}
	tt.SetSize(1700, 900)
	a.Select("s1")
	settle(tt)
	a.ShowChanges()
	settle(tt)
	openTab(t, tt, "internal/bridge.go")
	settle(tt)
	if err := tt.Click("范围：会话累计"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if !tt.HasText("对比 main") || !tt.HasText("2+") || tt.HasText("先选一个仓库") {
		t.Fatalf("menu %q", tt.Texts())
	}
	if len(h.commitsFor) == 0 || h.commitsFor[0].Repo != "." {
		t.Fatalf("commits asked %+v", h.commitsFor)
	}
	if err := tt.Click("提交"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	tt.Type("tune")
	settle(tt)
	if tt.HasText("Add heartbeat") || !tt.HasText("Tune heartbeat") {
		t.Fatalf("search %q", tt.Texts())
	}
	if err := tt.Click("Tune heartbeat"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	got := h.diffAsked[len(h.diffAsked)-1]
	if got.Range != proto.DiffRangeCommit || got.Commit != "2222222bbbb" || got.Repo != "." {
		t.Fatalf("asked %+v", got)
	}
	if !tt.HasText("范围：提交 2222222") || !tt.HasText("sync.go") {
		t.Errorf("the tab did not move: %q", tt.Texts())
	}
}

func TestRepoFilter(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.diffs = map[proto.SessionID]proto.SessionDiff{"s1": twoRepos()}
	tt.SetSize(1700, 900)
	a.Select("s1")
	settle(tt)
	a.ShowChanges()
	settle(tt)
	if err := tt.Click("web"); err != nil { // the heading, before the tab names it too
		t.Fatal(err)
	}
	openTab(t, tt, "web/app.ts")
	settle(tt)
	ch := &a.panelFor("s1").ch
	if ch.repo != "web" || !tt.HasText("显示全部仓库") || !tt.HasText("· 1 个文件") {
		t.Fatalf("repo %q in %q", ch.repo, tt.Texts())
	}
	if err := tt.Click("显示全部仓库"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if ch.repo != "" || tt.HasText("显示全部仓库") || !tt.HasText("· 2 个文件") {
		t.Fatalf("repo %q in %q", ch.repo, tt.Texts())
	}
}

func TestDoubleClickShowsOneFile(t *testing.T) {
	a, _, tt := openChanges(t)
	openTab(t, tt, "internal/bridge.go")
	settle(tt)
	for range 2 {
		if err := tt.Click("internal/bridge.go"); err != nil {
			t.Fatal(err)
		}
	}
	settle(tt)
	ch := &a.panelFor("s1").ch
	if ch.only != "internal/bridge.go" || !tt.HasText("显示全部文件") || !tt.HasText("· 1 个文件") {
		t.Fatalf("only %q in %q", ch.only, tt.Texts())
	}
	if err := tt.Click("显示全部文件"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if ch.only != "" || !tt.HasText("· 2 个文件") {
		t.Fatalf("only %q in %q", ch.only, tt.Texts())
	}
}

func TestChangesFlat(t *testing.T) {
	a, _, tt := openChanges(t)
	if err := tt.Click("平铺显示"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if !a.panelFor("s1").ch.flat || !tt.HasText("按目录显示") {
		t.Fatalf("not flat: %q", tt.Texts())
	}
	rows := changeList(a.panelFor("s1").ch.diff, "/w/hoplet", nil, true)
	var dirs []string
	for _, r := range rows {
		if r.kind == changeDir {
			t.Errorf("a directory row %q when flat", r.label)
		}
		if r.kind == changeFile {
			dirs = append(dirs, r.label+"@"+r.dir)
		}
	}
	if !slices.Equal(dirs, []string{"bridge.go@internal", "notes.md@"}) {
		t.Errorf("flat rows %q", dirs)
	}
}

// numbered is n lines, "line 1" to "line n".
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestFoldBarShowsWholeFile(t *testing.T) {
	a, h, tt := openChanges(t)
	text := numbered(60)
	h.contents = map[string]proto.FileContent{
		"internal/bridge.go":      {Path: "internal/bridge.go", Text: text},
		"base:internal/bridge.go": {Path: "internal/bridge.go", Text: strings.Replace(text, "line 13\n", "old 13\n", 1)},
	}
	openTab(t, tt, "internal/bridge.go")
	settle(tt)
	if err := tt.Click("展开 9 行未改动"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	fc := a.panelFor("s1").ch.files["internal/bridge.go"]
	if fc == nil || !fc.whole {
		t.Fatalf("the file was not read whole: %+v", fc)
	}
	if !fc.opened[0] || tt.HasText("9 行未改动") || !tt.HasText("45 行未改动") {
		t.Errorf("the gap clicked did not open, or another did: %v, %q", fc.opened, tt.Texts())
	}
	var base bool
	for _, p := range h.read {
		base = base || p.Baseline && p.Path == "internal/bridge.go"
	}
	if !base {
		t.Errorf("the old side was not read: %+v", h.read)
	}
}

func TestContextPills(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.diffs = map[proto.SessionID]proto.SessionDiff{"s1": changedDiff("a1")}
	tt.SetSize(1700, 900)
	a.Select("s1")
	settle(tt)
	if err := tt.Click("在 Finder 中显示"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(h.revealed, []string{"/w/hoplet"}) {
		t.Errorf("revealed %q", h.revealed)
	}
	if err := tt.Click("变更"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	settle(tt)
	if !a.panelOpen || a.panelFor("s1").column != columnChanges {
		t.Errorf("the branch pill did not open the changes")
	}
}
