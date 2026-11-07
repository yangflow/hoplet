package shell

import (
	"cmp"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/catalog"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// paletteScope is what the command palette searches.
type paletteScope int

// Palette scopes, ⌘1–4 in the palette; files only while a Code session
// shows.
const (
	scopeAll paletteScope = iota
	scopeSessions
	scopeCommands
	scopeFiles
)

// paletteEntry is a row of the command palette: a session to open, a
// command to run or a file to open.
type paletteEntry struct {
	session *proto.Session // a session, else a command or a file
	file    string         // a file's path in the working directory
	title   string         // a file's name
	where   string         // a session's machine and place, a file's directory
	text    string         // more of a session to match: its preview
	key     string         // a command's shortcut
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
// q, the recent sessions and the quick commands; else the sessions, the
// commands and the files that match, best first, five of each in scopeAll.
// A file's name matching ranks above its path matching.
func paletteResults(sessions, commands, files []paletteEntry, quick []string, q string, scope paletteScope) []paletteGroup {
	q = strings.TrimSpace(q)
	var out []paletteGroup
	add := func(label string, rows []paletteHit) {
		if len(rows) > 0 {
			out = append(out, paletteGroup{label: label, rows: rows})
		}
	}
	in := func(s paletteScope) bool { return scope == scopeAll || scope == s }
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
		if in(scopeSessions) {
			add("最近会话", recent)
		}
		if in(scopeCommands) {
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
			switch {
			case score > 0 && e.file != "":
				score += 3 // above every match in the path
			case score > 0:
			case e.file != "":
				n = 0
				score, _ = matchScore(q, e.file)
			default:
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
	if in(scopeSessions) {
		add("会话", rank(sessions))
	}
	if in(scopeCommands) {
		add("命令", rank(commands))
	}
	if in(scopeFiles) {
		add("文件", rank(files))
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

// OpenFilePicker shows the command palette on its files (⌘P), or on all
// of it when no Code session shows.
func (a *App) OpenFilePicker() {
	a.OpenPalette()
	if _, ok := a.panelSession(); ok {
		a.s.palScope = scopeFiles
	}
}

// filePicker is every file of a session's working directory as the
// palette searches them, read once and again after the session changed
// its files.
type filePicker struct {
	list *proto.FileList
	at   uint64 // the conversation's last change when the list was read
	wait bool
}

// paletteFiles are the files the palette offers: those of the Code
// session that shows, the ones .gitignore leaves out left out. It reads
// them the first time and after the session changed its files; until the
// answer comes it offers what it read before.
func (a *App) paletteFiles(s proto.Session) (files []paletteEntry, truncated bool) {
	fp := &a.panelFor(s.ID).picker
	at := a.conv(s.ID).Changed()
	if a.connected && !fp.wait && (fp.list == nil || at > fp.at) {
		fp.wait, fp.at = true, at
		a.host.Files(proto.FilesListParams{Session: s.ID, All: true}, func(fl proto.FileList, err error) {
			fp.wait = false
			if err == nil {
				fp.list = &fl
			}
		})
	}
	if fp.list == nil {
		return nil, false
	}
	for _, e := range fp.list.Entries {
		if e.Kind == proto.FileKindDir {
			continue
		}
		dir := path.Dir(e.Path)
		if dir == "." {
			dir = ""
		}
		files = append(files, paletteEntry{file: e.Path, title: path.Base(e.Path), where: dir,
			run: func() { a.openFromPalette(s.ID, e.Path) }})
	}
	return files, fp.list.Truncated
}

// openFromPalette opens a file in a tab of the right panel, bringing the
// panel forward.
func (a *App) openFromPalette(id proto.SessionID, p string) {
	a.openFile(a.panelFor(id), p)
	if !a.panelOpen || a.fit.panel == 0 {
		a.bringPanel()
	}
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
		paletteEntry{title: "会话详情", key: "⌘I", icon: a.icons.Details, run: a.ToggleDetails},
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
// sessions, the commands and the Code session's files, ⌘1–4 for the
// scope, ↑↓ to choose, ↩ to open or run, esc to close.
func (a *App) palette(c *ui.Context, k theme.Colors) {
	if !a.s.palOpen {
		return
	}
	scopes := []string{"全部", "会话", "命令"}
	s, code := a.panelSession()
	if code {
		scopes = append(scopes, "文件")
	} else if a.s.palScope == scopeFiles {
		a.s.palScope = scopeAll
	}
	query := strings.TrimSpace(a.s.palQuery)
	var files []paletteEntry
	truncated := false
	if code && (a.s.palScope == scopeFiles || a.s.palScope == scopeAll && query != "") {
		files, truncated = a.paletteFiles(s)
	}
	groups := paletteResults(a.paletteSessions(), a.paletteCommands(), files, []string{"新建会话", "收件箱", "切到 Personal", "切到 Code"}, a.s.palQuery, a.s.palScope)
	var flat []paletteHit
	for _, g := range groups {
		flat = append(flat, g.rows...)
	}
	a.s.palSel = max(min(a.s.palSel, len(flat)-1), 0)
	run := func(h paletteHit) {
		a.s.palOpen = false
		h.run()
	}
	ui.DialogBase(c, &a.s.palOpen, func(_, panel ui.Element) {
		panel.Width(theme.DialogWidth).Radius(theme.RadiusPanel).Clip().Background(k.SurfaceFloat).Border(theme.LineWidth, k.Line2).Label("命令面板").
			Transition(ui.ElementTransition{Animation: &theme.MotionBase, Enter: &ui.Motion{Scale: theme.MotionScaleDialog}, Exit: &ui.Motion{}})
		k.ShadowPopover.On(panel)
		// The query, then its scope: all, sessions, commands or files (⌘1–4).
		theme.ItemRow.H(theme.BarCompact).P(0, theme.Space4).On(ui.Row(c)).BorderWidth(0, 0, theme.LineWidth, 0).BorderColor(k.Line).Children(func() {
			in := ui.SearchField(c, &a.s.palQuery).Placeholder("搜索会话和命令…").Label("搜索").AutoFocus().Grow(1).
				FontSize(theme.Title.Size()).Background(ui.Transparent).Border(0, ui.Transparent)
			if in.Changed() {
				a.s.palSel = 0
			}
			if in.Submitted() && len(flat) > 0 {
				run(flat[a.s.palSel])
			}
			in.OnTextKey(func(key ui.TextKey) bool {
				switch {
				case key.Mods == ui.Cmd && key.Key >= ui.Key1 && int(key.Key-ui.Key1) < len(scopes):
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
			for i, label := range scopes {
				on := a.s.palScope == paletteScope(i)
				t := theme.Control.P(0, theme.Space2).On(ui.Row(c.Key(label))).Radius(theme.RadiusControl).Label(label)
				if on {
					t.Background(k.Selected)
				} else if t.Hovered() {
					t.Background(k.Hover)
				}
				if t.Clicked() {
					a.s.palScope, a.s.palSel = paletteScope(i), 0
				}
				t.Children(func() {
					w := theme.Secondary.On(ui.Textf(c, "%s ⌘%d", label, i+1)).TextColor(k.Fg2)
					if on {
						w.TextColor(k.Fg)
					}
				})
			}
		})
		ui.Scroll(c).MaxHeight(paletteRows * theme.RowStep).Children(func() {
			theme.StackTight.P(theme.Space2).On(ui.Column(c)).Children(func() {
				switch {
				case len(flat) > 0:
				case query == "":
					return // the files scope before anything is typed
				case a.s.palScope == scopeFiles:
					theme.UI.On(ui.Text(c, "没有匹配的文件")).TextColor(k.Muted).Padding(theme.Space3, theme.Space2)
					return
				default:
					theme.UI.On(ui.Text(c, "没有匹配“"+query+"”的会话或命令")).TextColor(k.Muted).Padding(theme.Space3, theme.Space2)
					return
				}
				n := 0
				for i, g := range groups {
					top := float32(0)
					if i > 0 {
						top = theme.Space2
					}
					theme.ItemRow.H(theme.RowDense).On(ui.Row(c.Key("g:"+g.label))).Margin(top, 0, 0, 0).Children(func() {
						theme.Label.On(ui.Text(c, g.label)).TextColor(k.Muted)
					})
					for _, h := range g.rows {
						a.paletteRow(c, k, h, n, run)
						n++
					}
				}
				if truncated {
					theme.ItemRow.H(theme.RowDense).On(ui.Row(c.Key("truncated"))).Margin(theme.Space2, 0, 0, 0).Children(func() {
						theme.Secondary.On(ui.Text(c, "只搜索了前 "+strconv.Itoa(len(files))+" 个文件")).TextColor(k.Muted)
					})
				}
			})
		})
		theme.ItemRow.P(0, theme.Space4).G(theme.Space3).On(ui.Row(c)).BorderWidth(theme.LineWidth, 0, 0, 0).BorderColor(k.Line).Children(func() {
			for _, kv := range [][2]string{{"↑↓", "选择"}, {"↩", "打开"}, {"esc", "关闭"}} {
				theme.Inline.G(theme.Space1).On(ui.Row(c)).Children(func() {
					keyCap(c, k, kv[0])
					theme.Caption.On(ui.Text(c, kv[1])).TextColor(k.Muted)
				})
			}
		})
	})
}

// paletteRows is how many rows the palette shows before it scrolls.
const paletteRows = 12

// keyCap is a key on its cap: caption text on fill, rounded xs.
func keyCap(c *ui.Context, k theme.Colors, key string) ui.Element {
	return theme.Caption.On(ui.Text(c, key)).TextColor(k.Muted).Height(theme.Icon).Padding(0, theme.Space1).Radius(theme.RadiusXS).Background(k.Fill)
}

// paletteRow is one row: the icon, the harness's mark or the file's
// type, the title with the match in the accent, where it is, and its
// shortcut.
func (a *App) paletteRow(c *ui.Context, k theme.Colors, h paletteHit, n int, run func(paletteHit)) {
	r := theme.ItemRow.H(theme.RowStep).On(ui.Row(c.Key(n))).Radius(theme.RadiusRow).Label(h.title)
	if n == a.s.palSel {
		r.Background(k.Selected)
	}
	if r.Hovered() {
		a.s.palSel = n
	}
	if r.Clicked() {
		run(h)
	}
	r.Children(func() {
		switch {
		case h.session != nil:
			a.harnessIcon(c, k, h.session.Harness.Kind, theme.Icon)
		case h.file != "":
			ui.Row(c).Size(theme.Icon, theme.Icon).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
				if img := a.icons.FileType(h.title); img != nil {
					ui.Image(c, img).Size(theme.IconRow, theme.IconRow)
				}
			})
		default:
			ui.Icon(c, h.icon).FontSize(theme.Icon).TextColor(k.Fg3)
		}
		ui.Row(c).Shrink(0).Children(func() {
			if h.n == 0 || h.at < 0 || h.at+h.n > len(h.title) {
				ui.Text(c, h.title).FontSize(theme.Reading.Size()).TextColor(k.Fg).SingleLine()
				return
			}
			ui.Text(c, h.title[:h.at]).FontSize(theme.Reading.Size()).TextColor(k.Fg)
			ui.Text(c, h.title[h.at:h.at+h.n]).FontSize(theme.Reading.Size()).FontWeight(theme.UIStrong.Weight()).TextColor(k.AccentFg)
			ui.Text(c, h.title[h.at+h.n:]).FontSize(theme.Reading.Size()).TextColor(k.Fg)
		})
		theme.Secondary.On(ui.Text(c, h.where)).TextColor(k.Muted).SingleLine().Grow(1).Shrink(1)
		if h.key != "" {
			keyCap(c, k, h.key)
		}
	})
}
