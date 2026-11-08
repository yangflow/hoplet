package shell

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/proto"
)

// art is an artifact at a version, published minutes after a fixed time.
func art(id, session, name string, version, minutes int) proto.Artifact {
	return proto.Artifact{ID: proto.ArtifactID(id), Session: proto.SessionID(session), Name: name, Kind: proto.ArtifactMarkdown, Version: version, Entry: "a.md",
		UpdatedAt: time.Date(2026, 11, 8, 10, minutes, 0, 0, time.Local)}
}

func names(items []proto.Artifact) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

func published() proto.SessionItem {
	return proto.SessionItem{Events: []proto.Event{{Type: proto.TypeArtifactPublished}}}
}

func TestPublishedRefetchesTheList(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.artifacts = map[proto.SessionID][]proto.Artifact{"s1": {art("b", "s1", "b", 1, 2), art("c", "s1", "c", 1, 1)}}
	a.Select("s1")
	a.ShowPanel(columnArtifacts)
	settle(tt)
	ps := a.panelFor("s1")
	if got := names(ps.arts.items); !slices.Equal(got, []string{"b", "c"}) {
		t.Fatalf("list %q", got)
	}
	h.artifacts["s1"] = []proto.Artifact{art("c", "s1", "c", 2, 9), art("b", "s1", "b", 1, 2)}
	a.ApplySession("s1", published())
	if got := names(ps.arts.items); !slices.Equal(got, []string{"c", "b"}) {
		t.Errorf("after publishing %q", got)
	}
}

func TestCompanionListsItsChildren(t *testing.T) {
	a, h, tt, _ := newApp(t)
	a.Apply(proto.CatalogItem{Version: 6, Sessions: []proto.Session{{ID: "k1", Parent: "p1", Mode: proto.ModePersonal, Status: proto.StatusRunning}}})
	h.artifacts = map[proto.SessionID][]proto.Artifact{"p1": {art("own", "p1", "own", 1, 1)}, "k1": {art("kid", "k1", "kid", 1, 5)}}
	a.Select("p1")
	a.TogglePanel()
	settle(tt)
	ps := a.panelFor("p1")
	if got := names(ps.arts.items); !slices.Equal(got, []string{"kid", "own"}) {
		t.Fatalf("list %q", got)
	}
	// The child publishes outside the followed session: its turn ending
	// is what refetches it.
	h.artifacts["k1"] = []proto.Artifact{art("kid2", "k1", "kid2", 1, 7), art("kid", "k1", "kid", 1, 5)}
	a.Apply(proto.CatalogItem{Version: 7, Sessions: []proto.Session{{ID: "k1", Parent: "p1", Mode: proto.ModePersonal, Status: proto.StatusIdle}}})
	settle(tt)
	if got := names(ps.arts.items); !slices.Equal(got, []string{"kid2", "kid", "own"}) {
		t.Errorf("after the child's turn %q", got)
	}
}

func TestTabFollowsLatestOnlyWhenOnLatest(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.artifacts = map[proto.SessionID][]proto.Artifact{"s1": {art("x", "s1", "x", 2, 2)}}
	h.artVersions = map[proto.ArtifactID][]proto.Artifact{"x": {art("x", "s1", "x", 2, 2), art("x", "s1", "x", 1, 1)}}
	tt.SetSize(1700, 900)
	h.artFiles = map[string][]byte{"x/v1": []byte("one"), "x/v2": []byte("two"), "x/v3": []byte("three")}
	a.Select("s1")
	a.ShowPanel(columnArtifacts)
	settle(tt)
	ps := a.panelFor("s1")
	a.openArtifact(ps, ps.arts.items[0])
	settle(tt)
	v := ps.arts.views["x"]
	if v.text != "two" {
		t.Fatalf("content %q", v.text)
	}

	h.artifacts["s1"] = []proto.Artifact{art("x", "s1", "x", 3, 3)}
	a.ApplySession("s1", published())
	settle(tt)
	if v.showing() != 3 || v.text != "three" {
		t.Errorf("on the latest: v%d %q", v.showing(), v.text)
	}

	v.choose(1)
	settle(tt)
	h.artifacts["s1"] = []proto.Artifact{art("x", "s1", "x", 4, 4)}
	a.ApplySession("s1", published())
	settle(tt)
	if v.showing() != 1 || v.text != "one" || v.latest.Version != 4 {
		t.Errorf("on an old version: v%d %q latest v%d", v.showing(), v.text, v.latest.Version)
	}
}

func TestRailSwitchesToArtifacts(t *testing.T) {
	a, h, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	h.artifacts = map[proto.SessionID][]proto.Artifact{"s1": {art("x", "s1", "报告", 3, 2)}}
	a.Select("s1")
	a.ShowTree()
	settle(tt)
	if err := tt.Click("产物"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	for _, want := range []string{"报告", "v3"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if !tt.HasText("Fix sync") {
		t.Error("switching columns moved the content area")
	}
	if a.panelFor("s1").column != columnArtifacts {
		t.Error("the column did not change")
	}
}

func TestPersonalRailHasOnlyArtifacts(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	a.Select("p1")
	a.TogglePanel()
	settle(tt)
	if _, ok := tt.Find("文件树 ⌥⌘E"); ok {
		t.Error("a Personal session offers the file tree")
	}
	for _, want := range []string{"产物", "还没有产物"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
}

func TestRowOpensItsTabOnce(t *testing.T) {
	a, h, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	h.artifacts = map[proto.SessionID][]proto.Artifact{"s1": {art("x", "s1", "报告", 1, 2)}}
	a.Select("s1")
	a.ShowPanel(columnArtifacts)
	settle(tt)
	ps := a.panelFor("s1")
	for range 2 {
		if err := tt.Click("报告"); err != nil {
			t.Fatal(err)
		}
		settle(tt)
	}
	if len(ps.tabs) != 1 || ps.tabs[0].artifact != "x" || ps.active != 0 {
		t.Errorf("tabs %+v, active %d", ps.tabs, ps.active)
	}
}

// openedArtifact opens a tab on artifact x, of a kind, whose two versions
// are listed.
func openedArtifact(t *testing.T, kind proto.ArtifactKind, h func(*host)) (*App, *host, *ui.Tester, *artifactView) {
	t.Helper()
	a, fake, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	latest, old := art("x", "s1", "报告", 2, 2), art("x", "s1", "报告", 1, 1)
	latest.Kind, old.Kind = kind, kind
	fake.artifacts = map[proto.SessionID][]proto.Artifact{"s1": {latest}}
	fake.artVersions = map[proto.ArtifactID][]proto.Artifact{"x": {latest, old}}
	h(fake)
	a.Select("s1")
	a.ShowPanel(columnArtifacts)
	settle(tt)
	ps := a.panelFor("s1")
	a.openArtifact(ps, latest)
	settle(tt)
	return a, fake, tt, ps.arts.views["x"]
}

func TestVersionMenuAndBackToLatest(t *testing.T) {
	_, _, tt, v := openedArtifact(t, proto.ArtifactMarkdown, func(h *host) {
		h.artFiles = map[string][]byte{"x/v1": []byte("第一版"), "x/v2": []byte("第二版")}
	})
	if !tt.HasText("· 最新") || !tt.HasText("第二版") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.Click("版本 v2"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	texts := tt.Texts()
	if a, b := slices.Index(texts, "v2"), slices.Index(texts, "v1"); a < 0 || b < 0 || a > b {
		t.Errorf("menu order in %q", texts)
	}
	if err := tt.Click("v1"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if v.menu || !tt.HasText("这是旧版本，最新是 v2") || !tt.HasText("第一版") {
		t.Fatalf("on v1: menu %v, texts %q", v.menu, tt.Texts())
	}
	if err := tt.Click("回到最新"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if tt.HasText("这是旧版本，最新是 v2") || !tt.HasText("第二版") {
		t.Errorf("back on the latest: %q", tt.Texts())
	}
}

func TestFailedReadRetries(t *testing.T) {
	var fake *host
	_, _, tt, v := openedArtifact(t, proto.ArtifactMarkdown, func(h *host) {
		fake = h
		h.artErr = errors.New("file not found")
		h.artFiles = map[string][]byte{"x/v2": []byte("好了")}
	})
	if !tt.HasText("打不开这个版本") || !tt.HasText("读不了 a.md：file not found") {
		t.Fatalf("texts %q", tt.Texts())
	}
	asked := len(fake.readArts)
	fake.artErr = nil
	if err := tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if len(fake.readArts) != asked+1 || v.err != "" || !tt.HasText("好了") {
		t.Errorf("reads %d after %d, err %q, texts %q", len(fake.readArts), asked, v.err, tt.Texts())
	}
}

func TestCSVShowsAsATable(t *testing.T) {
	_, _, tt, v := openedArtifact(t, proto.ArtifactCSV, func(h *host) {
		h.artFiles = map[string][]byte{"x/v2": []byte("名字,数量\n苹果,3\n")}
	})
	if len(v.rows) != 2 {
		t.Fatalf("rows %q", v.rows)
	}
	for _, want := range []string{"名字", "数量", "苹果", "3"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
}

func TestHTMLUsesTheHostPageAndCloses(t *testing.T) {
	a, fake, tt, _ := openedArtifact(t, proto.ArtifactHTML, func(*host) {})
	if !slices.Equal(fake.pages, []string{"x/v2"}) || len(fake.readArts) != 0 {
		t.Errorf("pages %q, reads %v", fake.pages, fake.readArts)
	}
	ps := a.panelFor("s1")
	a.closeTab(ps, 0)
	settle(tt)
	if !slices.Equal(fake.closedArts, []proto.ArtifactID{"x"}) {
		t.Errorf("closed %v", fake.closedArts)
	}
}
