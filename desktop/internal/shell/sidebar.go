package shell

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/catalog"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// sidebar draws the sidebar: the top row beside the traffic lights, the
// list header, the sessions and the footer.
func (a *App) sidebar(c *ui.Context, k theme.Colors) {
	bar := c.TitleBar()
	ui.Column(c).Width(theme.SidebarWidth).Background(k.Side).BorderWidth(0, 1, 0, 0).BorderColor(k.SideLine).Children(func() {
		ui.Row(c).Height(topHeight(bar)).Padding(0, 8, 0, bar.Left+8).Gap(2).AlignItems(ui.Center).DragWindow().Children(func() {
			a.windowButtons(c, k)
			ui.Spacer(c)
			a.modeSwitch(c, k)
		})
		// Search and the inbox join the new-session entry with the
		// command palette and the inbox page.
		ui.Column(c).Padding(0, 8).Children(func() {
			e := ui.Row(c).Height(theme.RowHeight).Padding(0, 8).Gap(8).AlignItems(ui.Center).Radius(theme.RadiusRow).
				Label("新建").Tooltip("新建会话 ⌘N")
			switch {
			case a.s.newPage:
				e.Background(k.SelRow)
			case e.Hovered():
				e.Background(k.Fill)
			}
			if e.Clicked() {
				a.NewSession()
			}
			e.Children(func() {
				ui.Icon(c, a.icons.New).FontSize(theme.IconSize).TextColor(k.Fg3)
				theme.UI.On(ui.Text(c, "新建")).TextColor(k.Fg)
			})
		})
		// The machine scope ("全部机器 ▾") comes with several machines.
		ui.Row(c).Height(theme.RowHeight).Margin(8, 0, 0, 0).Padding(0, 8, 0, 16).AlignItems(ui.Center).Children(func() {
			label := "全部机器"
			if a.archived {
				label = "已归档"
			}
			theme.UIStrong.On(ui.Text(c, label)).TextColor(k.Fg).Grow(1)
			switch {
			case a.mode == proto.ModeCode && !a.archived:
				iconButton(c, k, a.icons.Arrange, "排列", false).Size(26, 26).Menu(a.arrangeMenu)
			case a.mode == proto.ModePersonal:
				if iconButton(c, k, a.icons.New, "新建伙伴 ⇧⌘N", false).Size(26, 26).Clicked() {
					a.NewSession()
				}
			}
		})
		ui.Scroll(c).Grow(1).Children(func() { a.list(c, k) })
		a.footer(c, k)
	})
}

// topHeight is the height of the window's top row: the title bar's, which
// leaves as much room above the traffic lights as below, so the row's
// center lines up with them; SideTop in full screen, where there is none.
func topHeight(bar ui.TitleBar) float32 {
	if bar.Height > 0 {
		return bar.Height
	}
	return theme.SideTop
}

// windowButtons are the buttons beside the traffic lights: hide or show
// the sidebar, back and forward.
func (a *App) windowButtons(c *ui.Context, k theme.Colors) {
	tip := "隐藏侧栏 ⌃⌘S"
	if a.hideSide {
		tip = "显示侧栏 ⌃⌘S"
	}
	if iconButton(c, k, a.icons.Sidebar, tip, false).Clicked() {
		a.ToggleSidebar()
	}
	if b := iconButton(c, k, a.icons.Back, "后退 ⌘[", false).Disabled(a.step(-1) < 0); b.Clicked() {
		a.Back()
	}
	if b := iconButton(c, k, a.icons.Forward, "前进 ⌘]", false).Disabled(a.step(1) < 0); b.Clicked() {
		a.Forward()
	}
}

// modeSwitch is the Personal | Code segmented switch, icons only; the
// chosen side is raised.
func (a *App) modeSwitch(c *ui.Context, k theme.Colors) {
	ui.Row(c).Padding(2).Gap(2).Radius(8).Background(k.Line).Children(func() {
		for _, m := range []struct {
			mode proto.Mode
			icon *ui.SVG
			tip  string
		}{
			{proto.ModePersonal, a.icons.Personal, "Personal ⌘1"},
			{proto.ModeCode, a.icons.Code, "Code ⌘2"},
		} {
			on := a.mode == m.mode
			b := ui.Button(c.Key(m.mode), "").Label(m.tip).Tooltip(m.tip).Size(30, 26).Padding(0).
				Radius(theme.RadiusControl).Border(0, ui.Transparent).Background(ui.Transparent)
			color := k.Muted
			if on {
				b.Background(k.Raised).Shadow(0, 0.5, 1.5, 0, ui.RGBA(0, 0, 0, .18))
				color = k.Fg
			}
			b.Children(func() { ui.Icon(c, m.icon).FontSize(theme.IconSize).TextColor(color) })
			if b.Clicked() {
				a.SetMode(m.mode)
			}
		}
	})
}

// arrangeMenu has two submenus: how the Code list is sorted, and which
// sessions it shows, with their counts.
func (a *App) arrangeMenu(m *ui.Menu) {
	m.Submenu("排序", func(m *ui.Menu) {
		for _, s := range []struct {
			by    catalog.Sort
			label string
		}{{catalog.ByUpdated, "最近更新"}, {catalog.ByCreated, "创建时间"}, {catalog.ByName, "名称"}} {
			if m.Item(s.label).Radio(a.sort == s.by).Chosen() {
				a.sort = s.by
			}
		}
	})
	m.Submenu("显示", func(m *ui.Menu) {
		for _, s := range []struct {
			show  catalog.Show
			label string
		}{{catalog.ShowAll, "全部"}, {catalog.ShowNeedsYou, "需要我处理的"}, {catalog.ShowRunning, "运行中"}} {
			label := s.label
			if s.show != catalog.ShowAll {
				label = fmt.Sprintf("%s  %d", s.label, len(all(a.cat.List(catalog.View{Mode: proto.ModeCode, Show: s.show}))))
			}
			if m.Item(label).Radio(a.show == s.show).Chosen() {
				a.show = s.show
			}
		}
	})
}

// list draws the sessions of the current view.
func (a *App) list(c *ui.Context, k theme.Colors) {
	l := a.cat.List(catalog.View{Mode: a.mode, Archived: a.archived, Sort: a.sort, Show: a.show})
	if len(l.Pinned) == 0 && len(l.Groups) == 0 {
		empty := "没有会话"
		switch {
		case a.archived:
			empty = "没有归档的会话"
		case a.mode == proto.ModePersonal:
			empty = "没有伙伴"
		}
		theme.UI.On(ui.Text(c, empty)).TextColor(k.Muted).Padding(8, 16)
		return
	}
	// ⌘⌥1–9 opens a pinned session; ↑↓ moves among the rows shown while
	// the list has the focus.
	for i, s := range l.Pinned[:min(len(l.Pinned), 9)] {
		if c.Shortcut(ui.Cmd|ui.Alt, ui.Key1+ui.Key(i)) {
			a.Select(s.ID)
		}
	}
	col := ui.Column(c).Padding(0, 8).Focusable().FocusRing(false)
	if col.Shortcut(0, ui.KeyUp) {
		a.move(a.shown(l), -1)
	}
	if col.Shortcut(0, ui.KeyDown) {
		a.move(a.shown(l), 1)
	}
	col.Children(func() {
		if len(l.Pinned) > 0 {
			groupLabel(c, k, "置顶")
			for _, s := range l.Pinned {
				a.row(c, k, s)
			}
		}
		for _, g := range l.Groups {
			if g.Dir != "" {
				a.groupHeader(c.Key("dir:"+g.Dir), k, g)
			}
			if !a.closed[g.Dir] {
				for _, s := range g.Sessions {
					a.row(c, k, s)
				}
			}
		}
	})
}

// shown returns the sessions of a list in the order the rows are drawn,
// leaving out those of folded groups.
func (a *App) shown(l catalog.List) []proto.Session {
	out := append([]proto.Session(nil), l.Pinned...)
	for _, g := range l.Groups {
		if !a.closed[g.Dir] {
			out = append(out, g.Sessions...)
		}
	}
	return out
}

// move selects the session by rows from the selected one, or the first
// or last row when none of them is selected.
func (a *App) move(rows []proto.Session, by int) {
	if len(rows) == 0 {
		return
	}
	at := -1
	for i, s := range rows {
		if string(s.ID) == a.selected {
			at = i
		}
	}
	switch {
	case at < 0 && by > 0:
		at = 0
	case at < 0:
		at = len(rows) - 1
	default:
		at = max(0, min(len(rows)-1, at+by))
	}
	a.Select(rows[at].ID)
}

// groupLabel is the small title over the pinned sessions.
func groupLabel(c *ui.Context, k theme.Colors, label string) {
	ui.Row(c).Height(24).Margin(8, 0, 0, 0).Padding(0, 8).AlignItems(ui.Center).Children(func() {
		theme.Label.On(ui.Text(c, label)).TextColor(k.Muted)
	})
}

// groupHeader is a working directory's title: folder icon and name; a
// click folds the group, hovering shows the full path. The ＋ for a new
// session in the directory comes with the new-session page.
func (a *App) groupHeader(c *ui.Context, k theme.Colors, g catalog.Group) {
	h := ui.Row(c).Height(26).Margin(10, 0, 0, 0).Padding(0, 6, 0, 8).Gap(6).AlignItems(ui.Center).Tooltip(g.Dir)
	hovered := h.Hovered()
	if h.Clicked() {
		a.closed[g.Dir] = !a.closed[g.Dir]
	}
	h.Children(func() {
		ui.Icon(c, a.icons.Folder).FontSize(theme.IconSize).TextColor(k.Muted)
		theme.UIStrong.On(ui.Text(c, g.Name)).TextColor(k.Fg3).SingleLine().Grow(1)
		// ＋ starts a session in this directory: faint, darker with a
		// background while the title is hovered.
		plus := iconButton(c, k, a.icons.New, "在此目录新建会话", hovered).Size(22, 22)
		if !hovered {
			plus.Opacity(0.5)
		}
		if plus.Clicked() {
			a.newSessionIn(g.Dir)
		}
	})
}

// row draws one session: a line for Code, two for a Personal companion.
// The selected row has a neutral background; the accent never marks a
// selection.
func (a *App) row(c *ui.Context, k theme.Colors, s proto.Session) {
	c = c.Key(s.ID)
	personal := s.Mode == proto.ModePersonal
	r := ui.Row(c).AlignItems(ui.Center).Label(name(s))
	if personal {
		// A companion: two lines beside a 32-point avatar.
		r.Padding(6, 8).Gap(10).Radius(8)
	} else {
		r.Height(theme.RowHeight).Padding(0, 8).Gap(8).Radius(theme.RadiusRow)
	}
	hovered := r.Hovered()
	switch {
	case a.selected == string(s.ID):
		r.Background(k.SelRow)
	case hovered:
		r.Background(k.Fill)
	}
	if r.Clicked() {
		a.Select(s.ID)
	}
	r.ContextMenu(func(m *ui.Menu) { a.sessionMenu(m, s) }).Children(func() {
		if personal {
			a.avatar(c, k, s)
		} else {
			a.harnessIcon(c, k, s.Harness.Kind, theme.IconSize)
		}
		ui.Column(c).Grow(1).Shrink(1).AlignItems(ui.Stretch).Children(func() {
			if a.renaming == s.ID {
				a.renameInput(c)
				return
			}
			title := theme.UI
			if s.Unread {
				title = theme.UIStrong
			}
			title.On(ui.Text(c, name(s))).TextColor(k.Fg).SingleLine()
			if personal && s.Preview != "" {
				theme.Caption.On(ui.Text(c, s.Preview)).TextColor(k.Muted).SingleLine()
			}
		})
		a.trailing(c, k, s, hovered)
	})
}

// trailing is the one thing at a row's end: a state mark, the age of an
// idle session, or, on hover, pin and archive.
func (a *App) trailing(c *ui.Context, k theme.Colors, s proto.Session, hovered bool) {
	switch {
	case hovered && a.renaming != s.ID && s.Mode == proto.ModeCode:
		pin, tip := a.icons.Pin, "置顶 ⇧⌘P"
		if s.Pinned {
			pin, tip = a.icons.Unpin, "取消置顶 ⇧⌘P"
		}
		if iconButton(c, k, pin, tip, false).Size(24, 24).Clicked() {
			a.Select(s.ID)
			a.TogglePin()
		}
		if s.ArchivedAt == nil && iconButton(c, k, a.icons.Archive, "归档 ⌘⌫", false).Size(24, 24).Clicked() {
			a.Select(s.ID)
			a.Archive()
		}
	case s.Status == proto.StatusRunning:
		ui.Spinner(c).Size(12, 12).Label("运行中")
	case s.Status == proto.StatusWaiting:
		ui.Box(c).Size(8, 8).Radius(4).Background(k.Attn).Label("需要你")
	case s.Status == proto.StatusErrored:
		ui.Box(c).Size(8, 8).Radius(2).Background(k.Del).Label("出错")
	case s.Unread:
		ui.Box(c).Size(6, 6).Radius(3).Background(k.AccentFg).Label("未读")
	default:
		theme.Caption.On(ui.Text(c, ago(c.Now(), s.UpdatedAt))).TextColor(k.Muted).FontFeatures("tnum")
	}
}

// renameInput edits a session's title in its row: Enter keeps it, Escape
// drops it.
func (a *App) renameInput(c *ui.Context) {
	in := ui.TextInput(c, &a.draft).AutoFocus().Label("标题")
	switch {
	case in.Submitted():
		a.commitRename()
	case in.Shortcut(0, ui.KeyEscape):
		a.renaming = ""
	}
}

// harnessIcon is the harness's mark at size: Claude's own, others their
// initial.
func (a *App) harnessIcon(c *ui.Context, k theme.Colors, kind string, size float32) {
	if kind == "claude" {
		ui.Icon(c, a.icons.Claude).FontSize(size).TextColor(k.Fg3).Label("Claude Code")
		return
	}
	ui.Box(c).Size(size, size).Radius(theme.RadiusXS).Background(k.Fill2).Center().Label(harnessName(kind)).Children(func() {
		ui.Text(c, initial(kind)).FontSize(size * 0.7).FontWeight(600).TextColor(k.Fg3)
	})
}

// avatar is a companion's picture: its initial on neutral gray, with its
// harness in a round badge at the corner.
func (a *App) avatar(c *ui.Context, k theme.Colors, s proto.Session) { a.avatarSized(c, k, s, 32) }

// avatarSized is avatar at size points: 32 in the sidebar, 22 in the
// title bar.
func (a *App) avatarSized(c *ui.Context, k theme.Colors, s proto.Session, size float32) {
	badge := float32(15)
	if size < 32 {
		badge = 13
	}
	ui.Box(c).Size(size, size).Radius(size / 2).Background(k.Fill2).Center().Children(func() {
		style := theme.UIStrong
		if size < 32 {
			style = theme.Label
		}
		style.On(ui.Text(c, initial(name(s)))).TextColor(k.Fg2)
		ui.Box(c).Absolute().Right(-2).Bottom(-2).Size(badge, badge).Radius(badge/2).Background(k.Side).Border(1, k.Line).Center().Children(func() {
			a.harnessIcon(c, k, s.Harness.Kind, badge-5)
		})
	})
}

// initial is the upper-cased first letter of s, or ? for an empty s.
func initial(s string) string {
	r, _ := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return "?"
	}
	return string(unicode.ToUpper(r))
}

// sessionMenu is a session's context menu; the title bar's menu is the
// same.
func (a *App) sessionMenu(m *ui.Menu, s proto.Session) {
	pin := "置顶"
	if s.Pinned {
		pin = "取消置顶"
	}
	act := func(label string, key ui.Key, ok bool, do func()) {
		if m.Item(label).Disabled(!ok).Shortcut(0, key).Chosen() {
			a.Select(s.ID)
			do()
		}
	}
	live := s.ArchivedAt == nil
	if s.Mode == proto.ModePersonal {
		// A companion's menu; editing its persona, clearing its memory and
		// deleting it come with companions (M4). Companions are not pinned
		// or archived.
		act("重命名", ui.KeyR, true, a.Rename)
		return
	}
	// While the menu is open, the letter beside an item chooses it.
	act(pin, ui.KeyP, live, a.TogglePin)
	act("标为未读", ui.KeyU, !s.Unread, a.MarkUnread)
	act("重命名", ui.KeyR, true, a.Rename)
	act("在 Finder 中显示目录", ui.KeyO, s.Directory != "", a.Reveal)
	act("停止", ui.KeyS, s.Status == proto.StatusRunning, a.Stop)
	act("归档", ui.KeyA, live, a.Archive)
}

// footer shows this machine and whether the window reaches its daemon,
// and switches to the archived sessions. Other machines and paired
// devices come with several machines; settings come with settings.
func (a *App) footer(c *ui.Context, k theme.Colors) {
	ui.Row(c).Height(theme.TitleBar).Padding(0, 8, 0, 12).Gap(2).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(k.Line).Children(func() {
		ui.Row(c).Height(30).Padding(0, 6).Gap(8).AlignItems(ui.Center).Radius(theme.RadiusControl).Grow(1).Shrink(1).Children(func() {
			ui.Icon(c, a.icons.Machine).FontSize(theme.IconSize).TextColor(k.Fg3)
			machine := a.machine
			if machine == "" {
				machine = "本机"
			}
			theme.UI.On(ui.Text(c, machine)).FontWeight(500).TextColor(k.Fg).SingleLine().Shrink(1)
			if a.connected {
				ui.Icon(c, a.icons.Connected).FontSize(theme.IconSizeSmall).TextColor(k.Muted).Label("已连接").Tooltip("已连接本机守护进程")
			} else {
				ui.Icon(c, a.icons.Offline).FontSize(theme.IconSizeSmall).TextColor(k.Attn).Label("断线").Tooltip("连不上守护进程：" + a.offline)
			}
		})
		if a.mode == proto.ModeCode && iconButton(c, k, a.icons.Archive, "已归档的会话", a.archived).Clicked() {
			a.archived, a.selected = !a.archived, ""
		}
	})
}

// iconButton is a 28-point button showing only an icon; tip names it for
// the tooltip and assistive technology.
func iconButton(c *ui.Context, k theme.Colors, icon *ui.SVG, tip string, on bool) ui.Element {
	b := ui.Button(c, "").Label(tip).Tooltip(tip).Size(theme.RowHeight, theme.RowHeight).Padding(0).
		Radius(theme.RadiusControl).Background(ui.Transparent).Border(0, ui.Transparent)
	if on {
		b.Background(k.Fill2)
	}
	return b.Children(func() {
		ui.Icon(c, icon).FontSize(theme.IconSize).TextColor(k.Fg3)
	})
}
