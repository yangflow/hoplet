package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
)

// TestFindBar opens the find bar over a file: Hoplet's own, counting the
// matches, and closing.
func TestFindBar(t *testing.T) {
	a, _, tt := openTree(t)
	ps := a.panelFor("s1")
	a.openFile(ps, "internal/bridge.go")
	settle(tt)
	v := ps.viewFor("internal/bridge.go")
	v.ed.Find("silent")
	settle(tt)
	for _, want := range []string{"查找", "区分大小写", "全字匹配", "正则表达式", "1/1", "上一个 ⇧↩", "下一个 ↩", "关闭 esc"} {
		if _, ok := tt.Find(want); !ok {
			t.Errorf("no %q on the find bar: %q", want, tt.Texts())
		}
	}
	if tt.HasText("Aa") {
		t.Error("the editor's own find bar shows")
	}
	// A narrow column: the field gives way, the buttons stay whole.
	wide, _ := tt.Find("查找")
	a.panelW = 360
	settle(tt)
	narrow, _ := tt.Find("查找")
	if narrow.W >= wide.W {
		t.Errorf("the field keeps %v wide in a narrow column", narrow.W)
	}
	if r, _ := tt.Find("关闭 esc"); r.W != theme.RowDense {
		t.Errorf("the close button is %v wide", r.W)
	}
	v.ed.Find("heartbeat")
	settle(tt)
	if !tt.HasText("无结果") {
		t.Errorf("no note of no match: %q", tt.Texts())
	}
	if err := tt.Click("关闭 esc"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if _, ok := tt.Find("区分大小写"); ok {
		t.Error("the find bar stays open")
	}
}

// TestFileTabWraps wraps every file tab's lines from one, and offers no
// wrapping to a Markdown preview.
func TestFileTabWraps(t *testing.T) {
	a, _, tt := openTree(t)
	ps := a.panelFor("s1")
	a.openFile(ps, "notes.md")
	settle(tt)
	if _, ok := tt.Find("自动换行"); ok {
		t.Error("a Markdown preview offers wrapping")
	}
	a.openFile(ps, "internal/bridge.go")
	settle(tt)
	if err := tt.Click("自动换行"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if v := ps.viewFor("internal/bridge.go"); !a.wrap || !v.ed.SoftWrap {
		t.Fatalf("wrap %v, soft wrap %v", a.wrap, v.ed.SoftWrap)
	}
	if err := tt.Click("显示更改"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if v := ps.viewFor("internal/bridge.go"); !v.diff.Wrap {
		t.Error("the changes do not wrap")
	}
	if err := tt.Click("显示源码"); err == nil {
		t.Error("a Go file offers its source")
	}
	a.openFile(ps, "notes.md")
	settle(tt)
	if err := tt.Click("显示源码"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if v := ps.viewFor("notes.md"); !v.ed.SoftWrap {
		t.Error("another file's source does not wrap")
	}
}

// TestSearchFieldClears clears the tree's filter with its button and with
// esc.
func TestSearchFieldClears(t *testing.T) {
	a, _, tt := openTree(t)
	tr := &a.panelFor("s1").tree
	if _, ok := tt.Find("清除"); ok {
		t.Error("an empty field offers clearing")
	}
	if err := tt.Click("筛选文件"); err != nil {
		t.Fatal(err)
	}
	tt.Type("bri")
	settle(tt)
	if tr.query != "bri" {
		t.Fatalf("query %q", tr.query)
	}
	if err := tt.Click("清除"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if tr.query != "" || !tt.Focused("筛选文件") {
		t.Fatalf("query %q, focused %v after clearing", tr.query, tt.Focused("筛选文件"))
	}
	tt.Type("no")
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if tr.query != "" {
		t.Errorf("esc left %q", tr.query)
	}
}
