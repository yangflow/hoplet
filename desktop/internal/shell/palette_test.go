package shell

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/proto"
)

func TestMatchScore(t *testing.T) {
	for _, tc := range []struct {
		q, s      string
		score, at int
	}{
		{"fix", "Fix sync", 3, 0},
		{"sync", "Fix sync", 2, 4},
		{"ix", "Fix sync", 1, 1},
		{"hop", "~/w/hoplet", 2, 4},
		{"tray", "Fix sync", 0, -1},
		{"", "Fix sync", 0, -1},
		{"收件", "收件箱", 3, 0},
	} {
		if score, at := matchScore(tc.q, tc.s); score != tc.score || at != tc.at {
			t.Errorf("matchScore(%q, %q) = %d, %d; want %d, %d", tc.q, tc.s, score, at, tc.score, tc.at)
		}
	}
}

func TestPaletteResults(t *testing.T) {
	sess := func(title, where string) paletteEntry {
		s := proto.Session{Title: title}
		return paletteEntry{session: &s, title: title, where: where}
	}
	sessions := []paletteEntry{sess("Add tray", "studio · ~/w/hoplet"), sess("Fix sync", "studio · ~/w/hoplet"), sess("Sync docs", "vps · /srv")}
	for i := range 4 {
		sessions = append(sessions, sess("sync "+string(rune('a'+i)), "vps · /srv"))
	}
	commands := []paletteEntry{{title: "新建会话"}, {title: "收件箱"}, {title: "归档"}}
	titles := func(gs []paletteGroup) []string {
		var out []string
		for _, g := range gs {
			out = append(out, g.label+":")
			for _, h := range g.rows {
				out = append(out, h.title)
			}
		}
		return out
	}
	for name, tc := range map[string]struct {
		q     string
		scope paletteScope
		want  []string
	}{
		"empty":         {"", scopeAll, []string{"最近会话:", "Add tray", "Fix sync", "Sync docs", "sync a", "sync b", "快捷操作:", "新建会话", "收件箱"}},
		"empty, cmds":   {"", scopeCommands, []string{"快捷操作:", "新建会话", "收件箱"}},
		"best first":    {"sync", scopeAll, []string{"会话:", "Sync docs", "sync a", "sync b", "sync c", "sync d"}},
		"all sessions":  {"sync", scopeSessions, []string{"会话:", "Sync docs", "sync a", "sync b", "sync c", "sync d", "Fix sync"}},
		"where matches": {"srv", scopeSessions, []string{"会话:", "Sync docs", "sync a", "sync b", "sync c", "sync d"}},
		"commands":      {"收件", scopeAll, []string{"命令:", "收件箱"}},
		"nothing":       {"gemini", scopeAll, nil},
	} {
		t.Run(name, func(t *testing.T) {
			if got := titles(paletteResults(sessions, commands, []string{"新建会话", "收件箱"}, tc.q, tc.scope)); !slices.Equal(got, tc.want) {
				t.Fatalf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// ⌘K finds a session by part of its title and opens it; a command runs
// from the commands scope; esc closes.
func TestPalette(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if !a.s.palOpen || !tt.HasText("最近会话") {
		t.Fatalf("⌘K: open %v in %q", a.s.palOpen, tt.Texts())
	}
	tt.Type("tray")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.s.palOpen || a.selected != "s2" {
		t.Fatalf("↩ opened %q (palette %v)", a.selected, a.s.palOpen)
	}

	a.OpenPalette()
	tt.Frame()
	tt.Key(ui.Cmd, ui.Key3)
	tt.Type("新建会话")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !a.s.newPage || a.s.palOpen {
		t.Fatalf("新建会话: new page %v, palette %v", a.s.newPage, a.s.palOpen)
	}

	a.OpenPalette()
	a.ModeKey(proto.ModeCode) // ⌘2 picks the sessions scope while it shows
	if a.s.palScope != scopeSessions || a.mode != proto.ModeCode {
		t.Fatalf("⌘2 in the palette: scope %v, mode %v", a.s.palScope, a.mode)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.s.palOpen {
		t.Fatal("esc leaves the palette open")
	}
}

// The new-session page keeps its composer at the bottom, as a session
// does, with the title in the middle of the room above; the palette is in
// the middle of the window.
func TestCentered(t *testing.T) {
	a, _, tt, _ := newApp(t)
	tt.SetSize(1200, 900)
	tt.Frame()
	box, ok := tt.Find("消息")
	title, ok2 := tt.Find("在 Hoplet 上做什么？")
	if !ok || !ok2 {
		t.Fatalf("new page %q", tt.Texts())
	}
	content := a.side + (float32(1200)-a.side)/2
	for name, r := range map[string]ui.Rect{"box": box, "title": title} {
		if mid := r.X + r.W/2; mid < content-40 || mid > content+40 {
			t.Errorf("the %s is centered at %v, the content at %v", name, mid, content)
		}
	}
	if box.Y < 700 || title.Y+title.H > box.Y {
		t.Errorf("box at %v, title at %v, of 900", box.Y, title.Y)
	}
	a.OpenPalette()
	tt.Frame()
	r, ok := tt.Find("命令面板")
	if !ok {
		t.Fatal("no palette")
	}
	if mid := r.X + r.W/2; mid < 580 || mid > 620 {
		t.Errorf("the palette is centered at %v of 1200", mid)
	}
	if mid := r.Y + r.H/2; mid < 410 || mid > 490 {
		t.Errorf("the palette's middle is at %v of 900", mid)
	}
}
