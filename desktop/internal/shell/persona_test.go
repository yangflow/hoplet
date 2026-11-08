package shell

import (
	"errors"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/proto"
)

// companionApp is an app whose p1 is a known companion with a persona and
// a child, its details open.
func companionApp(t *testing.T, native string) (*App, *host, *ui.Tester) {
	t.Helper()
	a, h, tt, _ := newApp(t)
	tt.SetSize(1700, 900)
	h.personas = map[proto.CompanionID]string{"co1": "说话简短"}
	h.applies = proto.AppliesNextLaunch
	if native != "" {
		h.applies = proto.AppliesNewMainSession
	}
	h.companions = []proto.Companion{{ID: "co1", Main: "p1", Dir: "/h/companions/co1"}}
	a.loadCompanions()
	a.Apply(proto.CatalogItem{Version: 6, Sessions: []proto.Session{
		{ID: "p1", Mode: proto.ModePersonal, Harness: proto.HarnessRef{Kind: "hermes", NativeID: native}, Title: "小助手", UpdatedAt: tt.Now()},
		{ID: "c1", Mode: proto.ModePersonal, Parent: "p1", Harness: proto.HarnessRef{Kind: "hermes"}, Title: "查酒店", UpdatedAt: tt.Now()},
	}})
	a.Select("p1")
	a.s.detailOpen = true
	settle(tt)
	return a, h, tt
}

func TestPersonaBlockOnMainSessionOnly(t *testing.T) {
	a, _, tt := companionApp(t, "")
	if !tt.HasText("人格") || !tt.HasText("说话简短") || !tt.HasText("/h/companions/co1") {
		t.Errorf("details = %q", tt.Texts())
	}
	if tt.HasText("分支") {
		t.Errorf("a companion has a branch row: %q", tt.Texts())
	}
	a.Select("c1")
	settle(tt)
	if tt.HasText("人格") {
		t.Errorf("a child session has a persona block: %q", tt.Texts())
	}
}

func TestPersonaNote(t *testing.T) {
	for native, want := range map[string]string{"": "保存后，下次启动时用上", "n1": "保存后，新的主会话才会用上"} {
		_, _, tt := companionApp(t, native)
		if err := tt.Click("编辑人格"); err != nil {
			t.Fatal(err)
		}
		settle(tt)
		if !tt.HasText(want) {
			t.Errorf("native %q: %q", native, tt.Texts())
		}
	}
}

func TestPersonaCancelDoesNotSave(t *testing.T) {
	a, h, tt := companionApp(t, "")
	if err := tt.Click("编辑人格"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.s.personaEdit.open || h.savedPerson != nil {
		t.Errorf("open %v, saved %q", a.s.personaEdit.open, h.savedPerson)
	}
}

func TestPersonaSaves(t *testing.T) {
	a, h, tt := companionApp(t, "")
	if err := tt.Click("编辑人格"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	a.s.personaEdit.text = "说话再短些"
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if want := []string{"说话再短些"}; !reflect.DeepEqual(h.savedPerson, want) {
		t.Errorf("saved %q", h.savedPerson)
	}
	if a.s.personaEdit.open || a.s.personas["co1"] != "说话再短些" {
		t.Errorf("open %v, persona %q", a.s.personaEdit.open, a.s.personas["co1"])
	}
}

func TestPersonaSaveFailureStays(t *testing.T) {
	a, h, tt := companionApp(t, "")
	h.personaErr = errors.New("磁盘满了")
	if err := tt.Click("编辑人格"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if !a.s.personaEdit.open {
		t.Error("the dialog closed on a failed save")
	}
	if !tt.HasText("没做成：磁盘满了") {
		t.Errorf("no error shown: %q", tt.Texts())
	}
}

func TestPersonaAppliesFromTheDaemon(t *testing.T) {
	_, h, tt := companionApp(t, "")
	h.applies = proto.AppliesNewMainSession
	if err := tt.Click("编辑人格"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if !tt.HasText("保存后，新的主会话才会用上") {
		t.Errorf("the daemon's answer is not shown: %q", tt.Texts())
	}
}

func TestUnknownMainSessionListsCompanionsAgain(t *testing.T) {
	a, h, tt := companionApp(t, "")
	h.companions = append(h.companions, proto.Companion{ID: "co2", Main: "p2", Dir: "/h/companions/co2"})
	a.Apply(proto.CatalogItem{Version: 7, Sessions: []proto.Session{{ID: "p2", Mode: proto.ModePersonal, Title: "新伙伴", UpdatedAt: tt.Now()}}})
	if a.s.companions["p2"].ID != "co2" {
		t.Errorf("companions = %+v", a.s.companions)
	}
}
