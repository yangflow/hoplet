package shell

import (
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
	ui.Column(c).Width(theme.SidebarWidth).Background(k.Side).Children(func() {
		ui.Row(c).Height(topHeight(bar)).Padding(0, 12, 0, bar.Left+8).Gap(2).AlignItems(ui.Center).DragWindow().Children(func() {
			a.windowButtons(c, k)
			ui.Spacer(c)
			a.modeSwitch(c, k)
		})
		a.entries(c, k)
		ui.Row(c).Height(theme.RowHeight).Padding(0, 8, 0, 16).AlignItems(ui.Center).Children(func() {
			label := "会话"
			switch {
			case a.inbox:
				label = "收件箱"
			case a.mode == proto.ModePersonal:
				label = "伙伴"
			case a.archived:
				label = "已归档"
			}
			theme.Label.On(ui.Text(c, label)).TextColor(k.Muted).Grow(1)
			if a.mode == proto.ModeCode && !a.archived && !a.inbox {
				iconButton(c, k, a.icons.Arrange, "排列", false).Menu(a.arrangeMenu)
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

// entries are the rows under the top row: new session, search and the
// inbox with the count of sessions waiting on me. New session comes with
// the session view and is disabled until then.
func (a *App) entries(c *ui.Context, k theme.Colors) {
	ui.Column(c).Padding(4, 8).Children(func() {
		entry(c, k, a.icons.New, "新建", "新建会话 ⌘N", false).Disabled(true)
		if a.search {
			a.searchField(c, k)
		} else if entry(c, k, a.icons.Search, "搜索", "搜索 ⌘K", false).Clicked() {
			a.Search()
		}
		inbox := entry(c, k, a.icons.Inbox, "收件箱", "收件箱 ⇧⌘I", a.inbox)
		if n := a.cat.Counts().NeedsYou; n > 0 {
			inbox.Children(func() {
				ui.Box(c).Padding(0, 6).Radius(theme.RadiusPill).Background(k.Attn).Children(func() {
					theme.Caption.On(ui.Textf(c, "%d", n)).TextColor(k.OnAttn).FontWeight(600).FontFeatures("tnum")
				})
			})
		}
		if inbox.Clicked() {
			a.ToggleInbox()
		}
	})
}

// entry is a row of the entries: icon and label; tip names its shortcut.
// Callers may add children after the label.
func entry(c *ui.Context, k theme.Colors, icon *ui.SVG, label, tip string, on bool) ui.Element {
	r := ui.Row(c.Key(label)).Height(theme.RowHeight).Padding(0, 8).Gap(8).AlignItems(ui.Center).
		Radius(theme.RadiusRow).Label(label).Tooltip(tip)
	switch {
	case on:
		r.Background(k.SelRow)
	case r.Hovered():
		r.Background(k.Fill)
	}
	return r.Children(func() {
		ui.Icon(c, icon).FontSize(theme.IconSize).TextColor(k.Fg3)
		theme.UI.On(ui.Text(c, label)).TextColor(k.Fg).Grow(1)
	})
}

// searchField filters the list by title and preview; Escape closes it.
func (a *App) searchField(c *ui.Context, k theme.Colors) {
	ui.Row(c).Height(theme.RowHeight).Padding(0, 8).Gap(8).AlignItems(ui.Center).Radius(theme.RadiusRow).Background(k.Fill).Children(func() {
		ui.Icon(c, a.icons.Search).FontSize(theme.IconSize).TextColor(k.Fg3)
		in := ui.TextInput(c, &a.query).AutoFocus().Placeholder("搜索会话").Label("搜索会话").Grow(1)
		if in.Shortcut(0, ui.KeyEscape) {
			a.closeSearch()
		}
	})
}

// modeSwitch is the Personal | Code segmented switch, icons only.
func (a *App) modeSwitch(c *ui.Context, k theme.Colors) {
	ui.Row(c).Padding(2).Gap(2).Radius(theme.RadiusControl).Background(k.Fill2).Children(func() {
		for _, m := range []struct {
			mode proto.Mode
			icon *ui.SVG
			tip  string
		}{
			{proto.ModePersonal, a.icons.Personal, "Personal ⌘1"},
			{proto.ModeCode, a.icons.Code, "Code ⌘2"},
		} {
			b := iconButton(c.Key(m.mode), k, m.icon, m.tip, a.mode == m.mode).Size(30, 24)
			if a.mode == m.mode {
				b.Background(k.Raised)
			}
			if b.Clicked() {
				a.SetMode(m.mode)
			}
		}
	})
}

// arrangeMenu sorts and filters the Code list.
func (a *App) arrangeMenu(m *ui.Menu) {
	for _, s := range []struct {
		by    catalog.Sort
		label string
	}{{catalog.ByUpdated, "最近更新"}, {catalog.ByCreated, "创建时间"}, {catalog.ByName, "名称"}} {
		if m.Item(s.label).Radio(a.sort == s.by).Chosen() {
			a.sort = s.by
		}
	}
	m.Separator()
	for _, s := range []struct {
		show  catalog.Show
		label string
	}{{catalog.ShowAll, "全部"}, {catalog.ShowNeedsYou, "需要我处理的"}, {catalog.ShowRunning, "运行中"}} {
		if m.Item(s.label).Radio(a.show == s.show).Chosen() {
			a.show = s.show
		}
	}
}

// list draws the sessions of the current view.
func (a *App) list(c *ui.Context, k theme.Colors) {
	if a.inbox {
		waiting := all(a.cat.List(catalog.View{Show: catalog.ShowNeedsYou, Query: a.query}))
		if len(waiting) == 0 {
			theme.UI.On(ui.Text(c, "没有需要你的会话")).TextColor(k.Muted).Padding(8, 16)
			return
		}
		ui.Column(c).Padding(0, 8).Children(func() {
			for _, s := range waiting {
				a.row(c, k, s)
			}
		})
		return
	}
	l := a.cat.List(catalog.View{Mode: a.mode, Archived: a.archived, Sort: a.sort, Show: a.show, Query: a.query})
	if len(l.Pinned) == 0 && len(l.Groups) == 0 {
		empty := "没有会话"
		switch {
		case a.query != "":
			empty = "没有匹配的会话"
		case a.archived:
			empty = "没有归档的会话"
		case a.mode == proto.ModePersonal:
			empty = "没有伙伴"
		}
		theme.UI.On(ui.Text(c, empty)).TextColor(k.Muted).Padding(8, 16)
		return
	}
	ui.Column(c).Padding(0, 8).Children(func() {
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

// groupLabel is a small title over a group of rows.
func groupLabel(c *ui.Context, k theme.Colors, label string) {
	ui.Row(c).Height(theme.RowHeight).Padding(0, 8).AlignItems(ui.Center).Children(func() {
		theme.Label.On(ui.Text(c, label)).TextColor(k.Muted)
	})
}

// groupHeader is a working directory's row: folder icon and name; a click
// folds the group.
func (a *App) groupHeader(c *ui.Context, k theme.Colors, g catalog.Group) {
	h := ui.Row(c).Height(theme.RowHeight).Padding(0, 8).Gap(6).AlignItems(ui.Center).Tooltip(g.Dir)
	if h.Clicked() {
		a.closed[g.Dir] = !a.closed[g.Dir]
	}
	h.Children(func() {
		ui.Icon(c, a.icons.Folder).FontSize(theme.IconSizeInRow).TextColor(k.Fg3)
		theme.UI.On(ui.Text(c, g.Name)).TextColor(k.Fg3).SingleLine()
	})
}

// row draws one session: a line for Code, two for a Personal companion.
// The selected row has a neutral background; the accent never marks a
// selection.
func (a *App) row(c *ui.Context, k theme.Colors, s proto.Session) {
	c = c.Key(s.ID)
	personal := s.Mode == proto.ModePersonal
	h := float32(theme.RowHeight)
	if personal {
		h = 44
	}
	r := ui.Row(c).Height(h).Padding(0, 8).Gap(8).AlignItems(ui.Center).Radius(theme.RadiusRow).Label(name(s))
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
			a.harnessIcon(c, k, s.Harness.Kind, theme.IconSizeInRow)
		}
		ui.Column(c).Grow(1).Shrink(1).AlignItems(ui.Stretch).Children(func() {
			if a.renaming == s.ID {
				a.renameInput(c)
				return
			}
			title := theme.UI
			if s.Unread || personal {
				title = theme.UIStrong
			}
			title.On(ui.Text(c, name(s))).TextColor(k.Fg).SingleLine()
			if personal && s.Preview != "" {
				theme.Secondary.On(ui.Text(c, s.Preview)).TextColor(k.Muted).SingleLine()
			}
		})
		a.trailing(c, k, s, hovered)
	})
}

// trailing is the one thing at a row's end: a state mark, the age of an
// idle session, or, on hover, pin and archive.
func (a *App) trailing(c *ui.Context, k theme.Colors, s proto.Session, hovered bool) {
	switch {
	case hovered && a.renaming != s.ID:
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
	ui.Box(c).Size(size, size).Radius(theme.RadiusXS).Background(k.Fill2).Center().Label(kind).Children(func() {
		ui.Text(c, initial(kind)).FontSize(size * 0.7).FontWeight(600).TextColor(k.Fg3)
	})
}

// avatar is a companion's picture: its initial on neutral gray, with its
// harness in the corner.
func (a *App) avatar(c *ui.Context, k theme.Colors, s proto.Session) {
	ui.Box(c).Size(28, 28).Radius(14).Background(k.Fill2).Center().Children(func() {
		theme.UIStrong.On(ui.Text(c, initial(name(s)))).TextColor(k.Fg2)
		ui.Box(c).Absolute().Right(-3).Bottom(-3).Padding(1).Radius(theme.RadiusXS).Background(k.Raised).Children(func() {
			a.harnessIcon(c, k, s.Harness.Kind, 10)
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
	act := func(label string, mods ui.Modifiers, key ui.Key, ok bool, do func()) {
		it := m.Item(label).Disabled(!ok)
		if key != 0 {
			it.Shortcut(mods, key)
		}
		if it.Chosen() {
			a.Select(s.ID)
			do()
		}
	}
	live := s.ArchivedAt == nil
	act(pin, ui.Shift|ui.Cmd, ui.KeyP, live, a.TogglePin)
	act("标为未读", ui.Shift|ui.Cmd, ui.KeyU, !s.Unread, a.MarkUnread)
	act("重命名", 0, 0, true, a.Rename)
	m.Separator()
	act("在 Finder 中显示", 0, 0, s.Directory != "", a.Reveal)
	act("停止", 0, 0, s.Status == proto.StatusRunning, a.Stop)
	act("归档", ui.Cmd, ui.KeyBackspace, live, a.Archive)
}

// footer shows this machine and the connection, and switches to the
// archived sessions.
func (a *App) footer(c *ui.Context, k theme.Colors) {
	ui.Row(c).Height(theme.TitleBar).Padding(0, 8, 0, 16).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, a.icons.Machine).FontSize(theme.IconSizeInRow).TextColor(k.Fg3)
		machine := a.machine
		if machine == "" {
			machine = "本机"
		}
		theme.UI.On(ui.Text(c, machine)).TextColor(k.Fg3).SingleLine().Shrink(1)
		if a.connected {
			ui.Icon(c, a.icons.Connected).FontSize(theme.IconSizeSmall).TextColor(k.Muted).Label("已连接").Tooltip("已连接守护进程")
		} else {
			ui.Icon(c, a.icons.Offline).FontSize(theme.IconSizeSmall).TextColor(k.Attn).Label("断线").Tooltip("连不上守护进程：" + a.offline)
		}
		ui.Spacer(c)
		if a.mode == proto.ModeCode && iconButton(c, k, a.icons.Archive, "已归档", a.archived).Clicked() {
			a.archived, a.inbox, a.selected = !a.archived, false, ""
		}
		iconButton(c, k, a.icons.Settings, "设置 ⌘,", false).Disabled(true)
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
