package shell

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/proto"
)

const bridgePatch = `diff --git a/internal/bridge.go b/internal/bridge.go
--- a/internal/bridge.go
+++ b/internal/bridge.go
@@ -10,4 +10,5 @@ func (b *bridge) pump() {
 	for {
-		if err := b.ping(); err != nil {
+		if b.silent() {
+			return errSilent
 		}
 	}
@@ -40,3 +41,3 @@ func (b *bridge) silent() bool {
 	now := time.Now()
-	return now.Sub(b.last) > b.every
+	return now.Sub(b.last) > 3*b.every
 }
diff --git a/notes.md b/notes.md
new file mode 100644
--- /dev/null
+++ b/notes.md
@@ -0,0 +1,2 @@
+# Notes
+Heartbeat.
`

// changedDiff is s1's changes: two files of one repository with their
// patch, and a clean second repository.
func changedDiff(sha string) proto.SessionDiff {
	return proto.SessionDiff{Session: "s1", Repos: []proto.RepoDiff{
		{Path: ".", Branch: "feat/heartbeat", State: proto.RepoOK, SHA: sha, Patch: bridgePatch, Files: []proto.FileDiff{
			{Path: "internal/bridge.go", Status: proto.FileStatusModified, Additions: 3, Deletions: 2},
			{Path: "notes.md", Status: proto.FileStatusAdded, Additions: 2},
		}},
		{Path: "site", Branch: "main", State: proto.RepoOK, SHA: "site1"},
	}}
}

// openChanges opens s1's panel on the changes column.
func openChanges(t *testing.T) (*App, *host, *ui.Tester) {
	t.Helper()
	a, h, tt, _ := newApp(t)
	h.diffs = map[proto.SessionID]proto.SessionDiff{"s1": changedDiff("a1")}
	tt.SetSize(1700, 900)
	a.Select("s1")
	settle(tt)
	a.ShowChanges()
	settle(tt)
	return a, h, tt
}

func TestChangesColumnLists(t *testing.T) {
	_, h, tt := openChanges(t)
	for _, want := range []string{"2 个文件 ·", "+5", "−2", "hoplet", "feat/heartbeat", "internal", "bridge.go", "notes.md", "site", "无改动"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if len(h.patched) != 1 {
		t.Errorf("patches asked %d times, want once", len(h.patched))
	}
}

func TestChangesColumnNotes(t *testing.T) {
	for _, tc := range []struct {
		diff proto.SessionDiff
		want string
	}{
		{proto.SessionDiff{Repos: []proto.RepoDiff{{Path: ".", State: proto.RepoOK}}}, "没有改动"},
		{proto.SessionDiff{Repos: []proto.RepoDiff{}}, "这个目录不是 git 仓库，看不到改动"},
		{proto.SessionDiff{BaselineError: "快照超时"}, "没有基线：快照超时"},
	} {
		a, h, tt, _ := newApp(t)
		h.diffs = map[proto.SessionID]proto.SessionDiff{"s1": tc.diff}
		tt.SetSize(1700, 900)
		a.Select("s1")
		settle(tt)
		a.ShowChanges()
		settle(tt)
		if !tt.HasText(tc.want) {
			t.Errorf("no %q in %q", tc.want, tt.Texts())
		}
	}
}

// TestChangesFileOpensTab clicks a file in the column: the changes tab
// opens with every file's lines, and the file chosen comes to the top.
func TestChangesFileOpensTab(t *testing.T) {
	a, _, tt := openChanges(t)
	tt.SetSize(1700, 360) // too low for both files: the tab scrolls
	settle(tt)
	if err := tt.Click("notes.md"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	ps := a.panelFor("s1")
	if ps.active < 0 || !ps.tabs[ps.active].changes {
		t.Fatalf("tabs %+v, active %d", ps.tabs, ps.active)
	}
	for _, want := range []string{"变更 · 累计 · 2 个文件", "internal", "9 行未改动", "26 行未改动", "func (b *bridge) silent() bool"} {
		if !strings.Contains(strings.Join(tt.Texts(), "\n"), want) {
			t.Errorf("no %q in the tab: %q", want, tt.Texts())
		}
	}
	if ps.ch.target != "" {
		t.Errorf("the tab did not scroll to %q", ps.ch.target)
	}
	if ps.ch.scroll.Y <= 0 {
		t.Errorf("the tab stays at its top, %v, rather than at notes.md", ps.ch.scroll.Y)
	}
}

// TestChangesKeepUnchangedRepos reads the changes again: a repository
// whose SHA stayed keeps its files' views, one whose SHA moved remakes
// them.
func TestChangesKeepUnchangedRepos(t *testing.T) {
	a, h, tt := openChanges(t)
	ch := &a.panelFor("s1").ch
	before := ch.files["internal/bridge.go"]
	if before == nil || before.diff == nil {
		t.Fatalf("no lines made for bridge.go: %+v", ch.files)
	}
	ch.take(changedDiff("a1"))
	if ch.files["internal/bridge.go"] != before {
		t.Error("an unchanged repository's files were made again")
	}
	h.diffs["s1"] = changedDiff("a2")
	ch.take(changedDiff("a2"))
	if ch.files["internal/bridge.go"] == before {
		t.Error("a changed repository kept its old files")
	}
	tt.Frame()
}

// TestChangesAskAgainAfterChange reads the changes again once the session
// changes a file, not before.
func TestChangesAskAgainAfterChange(t *testing.T) {
	a, h, tt := openChanges(t)
	tt.Frame()
	if len(h.patched) != 1 {
		t.Fatalf("asked %d times before any change", len(h.patched))
	}
	payload, err := json.Marshal(proto.FileChange{Path: "/w/hoplet/notes.md", Op: proto.FileModified})
	if err != nil {
		t.Fatal(err)
	}
	a.ApplySession("s1", proto.SessionItem{Events: []proto.Event{{Session: "s1", Seq: 9, Type: proto.TypeFileChange, V: 1, Payload: payload}}})
	tt.Frame()
	if len(h.patched) != 2 {
		t.Errorf("asked %d times after a file changed, want twice", len(h.patched))
	}
}

// TestChangeCardOpensChanges clicks a file of a turn's change card: the
// panel opens on the changes tab, aimed at that file.
func TestChangeCardOpensChanges(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.diffs = map[proto.SessionID]proto.SessionDiff{"s1": changedDiff("a1")}
	tt.SetSize(1700, 900)
	a.Select("s1")
	settle(tt)
	a.showChange(proto.Session{ID: "s1"}, "internal/bridge.go")
	if !a.panelOpen {
		t.Fatal("the panel stays closed")
	}
	ps := a.panelFor("s1")
	if ps.active < 0 || !ps.tabs[ps.active].changes || ps.ch.chosen != "internal/bridge.go" {
		t.Errorf("tabs %+v, active %d, chosen %q", ps.tabs, ps.active, ps.ch.chosen)
	}
}
