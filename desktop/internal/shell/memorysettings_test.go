package shell

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/proto"
)

func memSettingsData() Memory {
	return Memory{
		Entries:  []proto.ProfileEntry{memEntry("g1", "先给结论", 1, scopeGlobal)},
		Settings: proto.ProfileSettings{UsePersonal: true, UseCode: true, Extraction: true, AutoSave: true},
		Extraction: proto.ProfileExtractionStatus{Harnesses: []proto.ProfileExtractionHarness{
			{Kind: "codex", Unsupported: "no structured output"},
			{Kind: "claude"},
		}},
	}
}

// clickSwitch clicks a setting's switch, at the end of its row, under the
// end of the row choosing the harness.
func clickSwitch(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	row, ok := tt.Find(name)
	pick, ok2 := tt.Find("提炼使用：跟随会话")
	if !ok || !ok2 {
		t.Fatalf("no %q in %q", name, tt.Texts())
	}
	tt.ClickAt(pick.X+pick.W-8, row.Y+row.H/2)
	tt.Frame()
}

func TestMemorySettingsToggle(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  func(proto.ProfileSettings) bool
		want bool
	}{
		{"Personal 会话", func(s proto.ProfileSettings) bool { return s.UsePersonal }, false},
		{"Code 会话", func(s proto.ProfileSettings) bool { return s.UseCode }, false},
		{"后台提炼", func(s proto.ProfileSettings) bool { return s.Extraction }, false},
		{"包含 Code 会话", func(s proto.ProfileSettings) bool { return s.IncludeCode }, true},
		{"自动保存", func(s proto.ProfileSettings) bool { return s.AutoSave }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, h, tt := openMemoryOn(t, memSettingsData())
			clickAll(t, tt, "记忆设置")
			clickSwitch(t, tt, tc.name)
			if len(h.mem.writes) != 1 {
				t.Fatalf("writes %+v", h.mem.writes)
			}
			if got := tc.got(h.mem.writes[0].(proto.ProfileSettings)); got != tc.want {
				t.Fatalf("wrote %v, want %v", got, tc.want)
			}
			if got := tc.got(a.m.data.Settings); got != tc.want {
				t.Fatalf("shows %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMemorySettingsRevert(t *testing.T) {
	a, h, tt := openMemoryOn(t, memSettingsData())
	clickAll(t, tt, "记忆设置")
	h.mem.writeErr = errors.New("offline")
	clickSwitch(t, tt, "自动保存")
	if !a.m.data.Settings.AutoSave || !tt.HasText("设置没有保存") {
		t.Fatalf("settings %+v, texts %q", a.m.data.Settings, tt.Texts())
	}
}

func TestMemorySettingsHarness(t *testing.T) {
	a, h, tt := openMemoryOn(t, memSettingsData())
	clickAll(t, tt, "记忆设置", "提炼使用：跟随会话")
	for _, want := range []string{"跟随会话", "Claude Code", "Codex", "不支持", "指定后，所有会话的材料都发给它"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	clickAll(t, tt, "Codex")
	if len(h.mem.writes) != 0 {
		t.Fatalf("an unsupported harness was chosen: %+v", h.mem.writes)
	}
	clickAll(t, tt, "Claude Code")
	if len(h.mem.writes) != 1 || h.mem.writes[0].(proto.ProfileSettings).ExtractionHarness != "claude" {
		t.Fatalf("writes %+v", h.mem.writes)
	}
	if a.m.settings.pick || !a.m.settings.open {
		t.Fatalf("view %+v", a.m.settings)
	}
}

func TestMemoryExtractionFailed(t *testing.T) {
	data := memSettingsData()
	_, _, tt := openMemoryOn(t, data)
	if _, ok := tt.Find("出错"); ok {
		t.Fatal("an error mark without failures")
	}
	data.Extraction.Failed = 2
	_, _, tt = openMemoryOn(t, data)
	if _, ok := tt.Find("出错"); !ok {
		t.Fatalf("no error mark in %q", tt.Texts())
	}
	clickAll(t, tt, "记忆设置")
	if !tt.HasText("2 次失败") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestMemorySettingsNeedRead(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.mem.err = errors.New("boom")
	a.OpenMemory("")
	tt.Frame()
	if _, ok := tt.Find("记忆设置"); ok {
		t.Fatal("settings offered before they were read")
	}
}
