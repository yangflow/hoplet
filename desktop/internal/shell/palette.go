package shell

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/catalog"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// paletteScope is what the command palette searches.
type paletteScope int

// Palette scopes, ⌘1–3 in the palette.
const (
	scopeAll paletteScope = iota
	scopeSessions
	scopeCommands
)

// paletteEntry is a row of the command palette: a session to open or a
// command to run.
type paletteEntry struct {
	session *proto.Session // a session, else a command
	title   string
	where   string // a session's machine and place
	text    string // more of a session to match: its preview
	key     string // a command's shortcut
	icon    *ui.SVG
	run     func()
}

// paletteGroup is a titled group of rows.
type paletteGroup struct {
	label string
	rows  []paletteHit
}

// paletteHit is an entry that matched, and where in its title.
type paletteHit struct {
	paletteEntry
	at, n int // the match in the title, bytes; n 0 for none shown
}

// matchScore says how well q matches s, ignoring case: 3 at the start, 2
// at the start of a word, 1 anywhere, 0 not at all; and where.
func matchScore(q, s string) (score, at int) {
	lq, ls := strings.ToLower(q), strings.ToLower(s)
	at = strings.Index(ls, lq)
	switch {
	case lq == "" || at < 0:
		return 0, -1
	case at == 0:
		return 3, at
	case strings.ContainsRune(" /-_.·", rune(ls[at-1])):
		return 2, at
	}
	return 1, at
}

// paletteResults are the groups the palette shows for q in scope: with no
// q, the recent sessions and the quick commands; else the sessions and the
// commands that match, best first, five of each in scopeAll.
func paletteResults(sessions, commands []paletteEntry, quick []string, q string, scope paletteScope) []paletteGroup {
	q = strings.TrimSpace(q)
	var out []paletteGroup
	add := func(label string, rows []paletteHit) {
		if len(rows) > 0 {
			out = append(out, paletteGroup{label: label, rows: rows})
		}
	}
	if q == "" {
		var recent, cmds []paletteHit
		for _, e := range sessions[:min(len(sessions), 5)] {
			recent = append(recent, paletteHit{paletteEntry: e})
		}
		for _, e := range commands {
			if slices.Contains(quick, e.title) {
				cmds = append(cmds, paletteHit{paletteEntry: e})
			}
		}
		if scope != scopeCommands {
			add("最近会话", recent)
		}
		if scope != scopeSessions {
			add("快捷操作", cmds)
		}
		return out
	}
	rank := func(es []paletteEntry) []paletteHit {
		type scored struct {
			paletteHit
			score int
		}
		var hits []scored
		for _, e := range es {
			score, at := matchScore(q, e.title)
			n := len(q)
			if score == 0 {
				n = 0
				for _, s := range []string{e.where, e.text} {
					if sc, _ := matchScore(q, s); sc > 0 {
						score = 1
					}
				}
			}
			if score > 0 {
				hits = append(hits, scored{paletteHit{paletteEntry: e, at: at, n: n}, score})
			}
		}
		slices.SortStableFunc(hits, func(a, b scored) int { return cmp.Compare(b.score, a.score) })
		out := make([]paletteHit, 0, len(hits))
		for _, h := range hits {
			out = append(out, h.paletteHit)
		}
		if scope == scopeAll {
			out = out[:min(len(out), 5)]
		}
		return out
	}
	if scope != scopeCommands {
		add("会话", rank(sessions))
	}
	if scope != scopeSessions {
		add("命令", rank(commands))
	}
	return out
}

// ModeKey is ⌘1 (Personal) or ⌘2 (Code): while the palette shows, it
// picks the palette's scope instead, all or sessions.
func (a *App) ModeKey(m proto.Mode) {
	switch {
	case a.s.palOpen && m == proto.ModePersonal:
		a.s.palScope, a.s.palSel = scopeAll, 0
	case a.s.palOpen:
		a.s.palScope, a.s.palSel = scopeSessions, 0
	default:
		a.SetMode(m)
	}
}

// OpenPalette shows the command palette (⌘K).
func (a *App) OpenPalette() {
	a.s.palOpen, a.s.palQuery, a.s.palScope, a.s.palSel = true, "", scopeAll, 0
}

// paletteSessions are the sessions the palette offers, the ones I opened
// last first, then the rest by when they changed.
func (a *App) paletteSessions() []paletteEntry {
	var ss []proto.Session
	for _, m := range []proto.Mode{proto.ModeCode, proto.ModePersonal} {
		ss = append(ss, all(a.cat.List(catalog.View{Mode: m}))...)
	}
	opened := map[proto.SessionID]int{}
	for i, p := range a.history {
		if p.kind == pageSession {
			opened[p.id] = i + 1
		}
	}
	slices.SortStableFunc(ss, func(x, y proto.Session) int {
		if c := cmp.Compare(opened[y.ID], opened[x.ID]); c != 0 {
			return c
		}
		return cmp.Compare(y.UpdatedAt.UnixNano(), x.UpdatedAt.UnixNano())
	})
	machine := a.machine
	if machine == "" {
		machine = "本机"
	}
	out := make([]paletteEntry, 0, len(ss))
	for i := range ss {
		s := ss[i]
		out = append(out, paletteEntry{session: &s, title: name(s), where: machine + " · " + where(s), text: s.Preview,
			run: func() { a.Select(s.ID) }})
	}
	return out
}

// paletteCommands are the actions the palette runs: those of the window,
// and those of the selected session.
func (a *App) paletteCommands() []paletteEntry {
	cmds := []paletteEntry{
		{title: "新建会话", key: "⌘N", icon: a.icons.New, run: func() { a.SetMode(proto.ModeCode); a.NewSession() }},
		{title: "新建伙伴", key: "⇧⌘N", icon: a.icons.New, run: func() { a.SetMode(proto.ModePersonal); a.NewSession() }},
		{title: "收件箱", key: "⇧⌘I", icon: a.icons.Inbox, run: a.OpenInbox},
	}
	if a.mode == proto.ModeCode {
		cmds = append(cmds, paletteEntry{title: "切到 Personal", key: "⌘1", icon: a.icons.Personal, run: func() { a.SetMode(proto.ModePersonal) }})
	} else {
		cmds = append(cmds, paletteEntry{title: "切到 Code", key: "⌘2", icon: a.icons.Code, run: func() { a.SetMode(proto.ModeCode) }})
	}
	side := "隐藏侧栏"
	if a.hideSide {
		side = "显示侧栏"
	}
	cmds = append(cmds, paletteEntry{title: side, key: "⌃⌘S", icon: a.icons.Sidebar, run: a.ToggleSidebar})
	s, ok := a.current()
	if !ok {
		return cmds
	}
	pin := "置顶"
	if s.Pinned {
		pin = "取消置顶"
	}
	cmds = append(cmds,
		paletteEntry{title: "会话详情", key: "⌘I", icon: a.icons.Details, run: func() { a.s.detailOpen = !a.s.detailOpen }},
		paletteEntry{title: pin, key: "⇧⌘P", icon: a.icons.Pin, run: a.TogglePin},
		paletteEntry{title: "标为未读", key: "⇧⌘U", icon: a.icons.Menu, run: a.MarkUnread},
		paletteEntry{title: "重命名", icon: a.icons.Edit, run: a.Rename},
	)
	if s.Directory != "" {
		cmds = append(cmds, paletteEntry{title: "在 Finder 中显示", icon: a.icons.Folder, run: a.Reveal})
	}
	if s.Status == proto.StatusRunning {
		cmds = append(cmds, paletteEntry{title: "停止", icon: a.icons.Stop, run: a.Stop})
	}
	return append(cmds, paletteEntry{title: "归档", key: "⌘⌫", icon: a.icons.Archive, run: a.Archive})
}

// palette draws the command palette while it is open: a search over the
// sessions and the commands, ⌘1–3 for the scope, ↑↓ to choose, ↩ to open
// or run, esc to close.
func (a *App) palette(c *ui.Context, k theme.Colors) {
	if !a.s.palOpen {
		return
	}
	groups := paletteResults(a.paletteSessions(), a.paletteCommands(), []string{"新建会话", "收件箱", "切到 Personal", "切到 Code"}, a.s.palQuery, a.s.palScope)
	var flat []paletteHit
	for _, g := range groups {
		flat = append(flat, g.rows...)
	}
	a.s.palSel = max(min(a.s.palSel, len(flat)-1), 0)
	run := func(h paletteHit) {
		a.s.palOpen = false
		h.run()
	}
	ui.DialogBase(c, &a.s.palOpen, func(backdrop, panel ui.Element) {
		backdrop.Justify(ui.Start).AlignItems(ui.Center).Padding(96, 0, 0, 0)
		panel.Width(600).Radius(14).Clip().Background(k.Popover).Border(1, k.Line2).Shadow(0, 10, 30, 0, k.Shadow).Label("命令面板")
		ui.Row(c).Height(52).Padding(0, 16).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Children(func() {
			in := ui.SearchField(c, &a.s.palQuery).Placeholder("搜索会话和命令…").Label("搜索").AutoFocus().Grow(1).
				FontSize(15).Background(ui.Transparent).Border(0, ui.Transparent)
			if in.Changed() {
				a.s.palSel = 0
			}
			if in.Submitted() && len(flat) > 0 {
				run(flat[a.s.palSel])
			}
			in.OnTextKey(func(key ui.TextKey) bool {
				switch {
				case key.Mods == ui.Cmd && key.Key >= ui.Key1 && key.Key <= ui.Key3:
					a.s.palScope, a.s.palSel = paletteScope(key.Key-ui.Key1), 0
				case key.Mods != 0:
					return false
				case key.Key == ui.KeyUp:
					a.s.palSel = max(a.s.palSel-1, 0)
				case key.Key == ui.KeyDown:
					a.s.palSel = min(a.s.palSel+1, max(len(flat)-1, 0))
				default:
					return false
				}
				return true
			})
			theme.Caption.On(ui.Text(c, "esc")).TextColor(k.Muted)
		})
		ui.Row(c).Padding(8, 10).Gap(4).BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Children(func() {
			for i, label := range []string{"全部", "会话", "命令"} {
				on := a.s.palScope == paletteScope(i)
				t := ui.Row(c.Key(label)).Height(26).Padding(0, 10).Gap(6).AlignItems(ui.Center).Radius(theme.RadiusControl).Label(label)
				if on {
					t.Background(k.Sel)
				}
				if t.Clicked() {
					a.s.palScope, a.s.palSel = paletteScope(i), 0
				}
				t.Children(func() {
					w := theme.UI.On(ui.Text(c, label)).TextColor(k.Fg3)
					if on {
						w.TextColor(k.Fg).FontWeight(500)
					}
					theme.Caption.On(ui.Textf(c, "⌘%d", i+1)).TextColor(k.Muted)
				})
			}
		})
		ui.Scroll(c).MaxHeight(420).Children(func() {
			ui.Column(c).Padding(6).Children(func() {
				if len(flat) == 0 {
					theme.UI.On(ui.Text(c, "没有匹配“"+strings.TrimSpace(a.s.palQuery)+"”的会话或命令")).TextColor(k.Muted).Padding(12, 10)
					return
				}
				n := 0
				for _, g := range groups {
					theme.Label.On(ui.Text(c.Key("g:"+g.label), g.label)).TextColor(k.Muted).Padding(8, 10, 4, 10)
					for _, h := range g.rows {
						a.paletteRow(c, k, h, n, run)
						n++
					}
				}
			})
		})
		ui.Row(c).Padding(8, 14).Gap(8).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(k.Line).Children(func() {
			for _, kv := range [][2]string{{"↑↓", "选择"}, {"↩", "打开"}, {"esc", "关闭"}} {
				theme.Caption.On(ui.Text(c, kv[0])).TextColor(k.Muted).Padding(2, 5).Radius(5).Background(k.Fill)
				theme.Caption.On(ui.Text(c, kv[1])).TextColor(k.Muted)
			}
		})
	})
}

// paletteRow is one row: the icon or the harness's mark, the title with
// the match in the accent, where it is, and its shortcut.
func (a *App) paletteRow(c *ui.Context, k theme.Colors, h paletteHit, n int, run func(paletteHit)) {
	r := ui.Row(c.Key(n)).Height(34).Padding(0, 10).Gap(10).AlignItems(ui.Center).Radius(8).Label(h.title)
	if n == a.s.palSel {
		r.Background(k.Sel)
	}
	if r.Hovered() {
		a.s.palSel = n
	}
	if r.Clicked() {
		run(h)
	}
	r.Children(func() {
		if h.session != nil {
			a.harnessIcon(c, k, h.session.Harness.Kind, theme.IconSize)
		} else {
			ui.Icon(c, h.icon).FontSize(theme.IconSize).TextColor(k.Fg3)
		}
		ui.Row(c).Shrink(0).Children(func() {
			if h.n == 0 || h.at < 0 || h.at+h.n > len(h.title) {
				ui.Text(c, h.title).FontSize(13.5).TextColor(k.Fg).SingleLine()
				return
			}
			ui.Text(c, h.title[:h.at]).FontSize(13.5).TextColor(k.Fg)
			ui.Text(c, h.title[h.at:h.at+h.n]).FontSize(13.5).FontWeight(600).TextColor(k.AccentFg)
			ui.Text(c, h.title[h.at+h.n:]).FontSize(13.5).TextColor(k.Fg)
		})
		theme.Secondary.On(ui.Text(c, h.where)).TextColor(k.Muted).SingleLine().Grow(1).Shrink(1)
		if h.key != "" {
			theme.Caption.On(ui.Text(c, h.key)).TextColor(k.Muted).Padding(2, 5).Radius(5).Background(k.Fill)
		}
	})
}
