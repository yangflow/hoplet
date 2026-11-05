package shell

import (
	"fmt"
	"strconv"
	"time"
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
	ui.Column(c).Fill().Background(k.Side).Children(func() {
		ui.Row(c).Height(topHeight(bar)).Padding(0, theme.Space2, 0, bar.Left+theme.Space4).Gap(theme.SpaceHalf).AlignItems(ui.Center).DragWindow().Children(func() {
			a.windowButtons(c, k)
			ui.Spacer(c)
			a.modeSwitch(c, k)
		})
		// Search and the inbox join the new-session entry with the
		// command palette and the inbox page.
		// Rows stand 2 apart; the entries sit 4 under the top row and 16
		// over the list's header, a section of their own.
		ui.Column(c).Padding(0, theme.Space2).Margin(theme.Space1, 0, 0, 0).Gap(theme.SpaceHalf).Children(func() {
			e := ui.Row(c).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusRow).
				Label("新建").Tooltip("新建会话 ⌘N")
			if e.Hovered() {
				e.Background(k.Fill)
			}
			if e.Clicked() {
				a.NewSession()
			}
			e.Children(func() {
				ui.Icon(c, a.icons.New).FontSize(theme.Icon).TextColor(k.Fg3)
				theme.UI.On(ui.Text(c, "新建")).TextColor(k.Fg)
			})
			a.searchEntry(c, k)
			a.inboxEntry(c, k)
		})
		// The machine scope ("全部机器 ▾") comes with several machines.
		ui.Row(c).Height(theme.Row).Margin(theme.Space4, 0, theme.Space1, 0).Padding(0, theme.Space2, 0, theme.Space4).AlignItems(ui.Center).Children(func() {
			label := "全部机器"
			if a.archived {
				label = "已归档"
			}
			theme.Label.On(ui.Text(c, label)).TextColor(k.Muted).Grow(1)
			switch {
			case a.mode == proto.ModeCode && !a.archived:
				iconButton(c, k, a.icons.Arrange, "排列", false).Size(theme.RowGroup, theme.RowGroup).Menu(a.arrangeMenu)
			case a.mode == proto.ModePersonal:
				if iconButton(c, k, a.icons.New, "新建伙伴 ⇧⌘N", false).Size(theme.RowGroup, theme.RowGroup).Clicked() {
					a.NewSession()
				}
			}
		})
		ui.Scroll(c).Grow(1).Children(func() { a.list(c, k) })
		a.footer(c, k)
	})
}

// modeSwitchWidth is the Personal | Code switch's width: two buttons a
// row wide, half a space apart and inside.
const modeSwitchWidth = 2*theme.Row + 3*theme.SpaceHalf

// sideMin is the least the sidebar narrows to: SidebarMin, or what its top
// row needs beside the traffic lights, whichever is wider, so its buttons
// always fit.
func sideMin(bar ui.TitleBar) float32 {
	row := bar.Left + theme.Space4 + 3*theme.Row + 2*theme.SpaceHalf + theme.Space3 + modeSwitchWidth + theme.Space2
	return max(theme.SidebarMin, row)
}

// searchEntry opens the command palette.
func (a *App) searchEntry(c *ui.Context, k theme.Colors) {
	e := ui.Row(c).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusRow).
		Label("搜索").Tooltip("搜索 ⌘K")
	if a.s.palOpen || e.Hovered() {
		e.Background(k.Fill)
	}
	if e.Clicked() {
		a.OpenPalette()
	}
	e.Children(func() {
		ui.Icon(c, a.icons.Search).FontSize(theme.Icon).TextColor(k.Fg3)
		theme.UI.On(ui.Text(c, "搜索")).TextColor(k.Fg)
	})
}

// inboxEntry is the sidebar's way into the inbox: its icon, its name and
// how many sessions need me, on every machine; the row is selected while
// the inbox shows.
func (a *App) inboxEntry(c *ui.Context, k theme.Colors) {
	n := a.cat.Counts().NeedsYou
	e := ui.Row(c).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusRow).
		Label("收件箱").Tooltip("收件箱 ⇧⌘I")
	switch {
	case a.inbox:
		e.Background(k.SelRow)
	case e.Hovered():
		e.Background(k.Fill)
	}
	if e.Clicked() {
		a.OpenInbox()
	}
	e.Children(func() {
		ui.Icon(c, a.icons.Inbox).FontSize(theme.Icon).TextColor(k.Fg3)
		name := theme.UI.On(ui.Text(c, "收件箱")).TextColor(k.Fg).Grow(1)
		if a.inbox {
			name.FontWeight(theme.UIStrong.Weight())
		}
		if n > 0 {
			ui.Badge(c, strconv.Itoa(n)).Background(k.Attn).TextColor(k.OnAttn)
		}
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
	ui.Row(c).Padding(theme.SpaceHalf).Gap(theme.SpaceHalf).Radius(theme.RadiusControl).Background(k.Line).Children(func() {
		for _, m := range []struct {
			mode proto.Mode
			icon *ui.SVG
			tip  string
		}{
			{proto.ModePersonal, a.icons.Personal, "Personal ⌘1"},
			{proto.ModeCode, a.icons.Code, "Code ⌘2"},
		} {
			on := a.mode == m.mode
			b := ui.Button(c.Key(m.mode), "").Label(m.tip).Tooltip(m.tip).Size(theme.Row, theme.RowGroup).Padding(0).
				Radius(theme.RadiusControl).Border(0, ui.Transparent).Background(ui.Transparent)
			color := k.Muted
			if on {
				k.ShadowRaised.On(b.Background(k.Raised))
				color = k.Fg
			}
			b.Children(func() { ui.Icon(c, m.icon).FontSize(theme.Icon).TextColor(color) })
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
		a.emptyList(c, k)
		return
	}
	// ⌘⌥1–9 opens a pinned session; ↑↓ moves among the rows shown while
	// the list has the focus. The list is a widget of its own rather than
	// a ui.List, whose chosen row takes the accent color: the selected row
	// is neutral, and it shows where the focus is, so the list draws no
	// ring.
	for i, s := range l.Pinned[:min(len(l.Pinned), 9)] {
		if c.Shortcut(ui.Cmd|ui.Alt, ui.Key1+ui.Key(i)) {
			a.Select(s.ID)
		}
	}
	col := ui.Column(c).Padding(0, theme.Space2).Gap(theme.SpaceHalf).Focusable().FocusRing(false).Role(ui.RoleList).Label("会话")
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
		for i, g := range l.Groups {
			if g.Dir != "" {
				a.groupHeader(c.Key("dir:"+g.Dir), k, g, i == 0 && len(l.Pinned) == 0)
			}
			if !a.closed[g.Dir] {
				for _, s := range g.Sessions {
					a.row(c, k, s)
				}
			}
		}
	})
}

// emptyList says why the list is empty, with no button: new sessions start
// from the sidebar's 新建, the list header's ＋ or the keys. An empty
// filter links back to all.
func (a *App) emptyList(c *ui.Context, k theme.Colors) {
	text, tip := "还没有会话", ""
	switch {
	case a.archived:
		text = "没有归档的会话"
	case !a.connected && a.cat.Version() == 0:
		text = "连接后显示会话" // reconnecting is on the machine row
	case a.mode == proto.ModePersonal:
		text = "还没有伙伴"
	case a.show == catalog.ShowNeedsYou:
		text, tip = "没有需要你处理的会话", "显示全部"
	case a.show == catalog.ShowRunning:
		text, tip = "没有运行中的会话", "显示全部"
	}
	ui.Column(c).Padding(theme.Space4, theme.Space4, 0).Gap(theme.Space2).AlignItems(ui.Start).Children(func() {
		theme.SecondaryBlock.On(ui.Text(c, text)).TextColor(k.Muted)
		if tip != "" && theme.SecondaryBlock.On(ui.Button(c, tip)).Height(theme.SecondaryBlock.Line()).Padding(0).Background(ui.Transparent).Border(0, ui.Transparent).
			TextColor(k.Fg3).Underline().Clicked() {
			a.show = catalog.ShowAll
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
	// 8 above and below, the list's gap of 2 included.
	ui.Row(c).Height(theme.RowDense).Margin(theme.Space2, 0, theme.Space1Half, 0).Padding(0, theme.Space2).AlignItems(ui.Center).Children(func() {
		theme.Label.On(ui.Text(c, label)).TextColor(k.Muted)
	})
}

// groupHeader is a working directory's title: folder icon and name; a
// click folds the group, hovering shows the full path. The ＋ for a new
// session in the directory comes with the new-session page. Groups stand
// 12 apart, the first 4 under the list's header.
func (a *App) groupHeader(c *ui.Context, k theme.Colors, g catalog.Group, first bool) {
	top := float32(theme.Space3 - theme.SpaceHalf) // with the list's gap
	if first {
		top = theme.Space1
	}
	h := ui.Row(c).Height(theme.Row).Margin(top, 0, 0, 0).Padding(0, theme.Space1Half, 0, theme.Space2).Gap(theme.Space1Half).AlignItems(ui.Center).Tooltip(g.Dir)
	hovered := h.Hovered()
	if h.Clicked() {
		a.closed[g.Dir] = !a.closed[g.Dir]
	}
	h.Children(func() {
		ui.Icon(c, a.icons.Folder).FontSize(theme.Icon).TextColor(k.Muted)
		theme.UI.On(ui.Text(c, g.Name)).FontWeight(theme.UIMedium.Weight()).TextColor(k.Fg2).SingleLine().Grow(1)
		// ＋ starts a session in this directory: faint, solid while the
		// title is hovered, with a background only under the pointer.
		plus := iconButton(c, k, a.icons.New, "在此目录新建会话", false).Size(theme.ControlXS, theme.ControlXS)
		switch {
		case plus.Hovered():
			plus.Background(k.Fill2)
		case !hovered:
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
	r := ui.Row(c).AlignItems(ui.Center).Label(name(s)).Transition(hoverMotion)
	if personal {
		// A companion: two lines beside a 32-point avatar.
		r.Padding(theme.Space1Half, theme.Space2).Gap(theme.Space2).Radius(theme.RadiusControl)
	} else {
		r.Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space2).Radius(theme.RadiusRow)
	}
	hovered := r.Hovered()
	switch {
	case a.selected == string(s.ID):
		r.Background(k.SelRow) // the selection already marks a session made here
		delete(a.s.flash, s.ID)
	case hovered:
		r.Background(k.Fill)
	default:
		a.flash(c, k, r, s.ID)
	}
	if r.Clicked() {
		a.Select(s.ID)
	}
	r.ContextMenu(func(m *ui.Menu) { a.sessionMenu(m, s) }).Children(func() {
		if personal {
			a.avatar(c, k, s)
		} else {
			a.harnessIcon(c, k, s.Harness.Kind, theme.Icon)
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

// flashTime is how long the row of a session made elsewhere takes to go
// out.
var flashTime = theme.MotionFlash.Duration

// flash lights the row of a session made elsewhere with fill2 as it first
// shows and fades it out; when the desktop asks for less motion, the
// light stays for flashTime and goes at once.
func (a *App) flash(c *ui.Context, k theme.Colors, r ui.Element, id proto.SessionID) {
	shown, ok := a.s.flash[id]
	if !ok {
		return
	}
	target := k.Fill2.Alpha(0)
	if !shown {
		target = k.Fill2
		a.s.flash[id] = true
		c.AnimationFrame() // the next frame starts the fade
	}
	fade := ui.Animation{Duration: flashTime}
	if c.Preferences().ReduceMotion {
		fade = ui.Animation{Delay: flashTime, Duration: time.Millisecond}
	}
	r.Background(r.AnimateColor("flash", target, fade))
	if r.Arrived("flash") {
		delete(a.s.flash, id)
	}
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
		if iconButton(c, k, pin, tip, false).Size(theme.RowDense, theme.RowDense).Clicked() {
			a.Select(s.ID)
			a.TogglePin()
		}
		if s.ArchivedAt == nil && iconButton(c, k, a.icons.Archive, "归档 ⌘⌫", false).Size(theme.RowDense, theme.RowDense).Clicked() {
			a.Select(s.ID)
			a.Archive()
		}
	case s.Status == proto.StatusRunning:
		ui.Spinner(c).Size(theme.IconSmall, theme.IconSmall).Label("运行中")
	case s.Status == proto.StatusWaiting:
		ui.Box(c).Size(theme.Dot, theme.Dot).Radius(theme.RadiusPill).Background(k.Attn).Label("需要你")
	case s.Status == proto.StatusErrored:
		ui.Box(c).Size(theme.Dot, theme.Dot).Radius(theme.RadiusMark).Background(k.Fg).Label("出错")
	case s.Unread:
		ui.Box(c).Size(theme.DotSmall, theme.DotSmall).Radius(theme.RadiusPill).Background(k.AccentFg).Label("未读")
	default:
		theme.Caption.On(ui.Text(c, ago(c.Now(), s.UpdatedAt))).TextColor(k.Muted).FontFeatures("tnum")
	}
}

// renameInput edits a session's title in its row: Enter or a press
// outside it keeps it, Escape drops it.
func (a *App) renameInput(c *ui.Context) {
	in := ui.TextInput(c, &a.draft).AutoFocus().Label("标题")
	switch {
	case in.Submitted(), in.PressedOutside():
		a.commitRename()
	case in.Shortcut(0, ui.KeyEscape):
		a.renaming = ""
	}
}

// harnessIcon is the harness's mark at size, in the text color; a harness
// without a mark shows its initial.
func (a *App) harnessIcon(c *ui.Context, k theme.Colors, kind string, size float32) {
	if mark := a.icons.Harness(kind); mark != nil {
		ui.Icon(c, mark).FontSize(size).TextColor(k.Fg3).Label(harnessName(kind))
		return
	}
	ui.Box(c).Size(size, size).Radius(theme.RadiusXS).Background(k.Fill2).Center().Label(harnessName(kind)).Children(func() {
		ui.Text(c, initial(kind)).FontSize(0.7 * size).FontWeight(theme.UIStrong.Weight()).TextColor(k.Fg3)
	})
}

// avatar is a companion's picture: its initial on neutral gray, with its
// harness in a round badge at the corner.
func (a *App) avatar(c *ui.Context, k theme.Colors, s proto.Session) {
	a.avatarSized(c, k, s, theme.RowStep)
}

// avatarSized is avatar at size points: 32 in the sidebar, 22 in the
// title bar.
func (a *App) avatarSized(c *ui.Context, k theme.Colors, s proto.Session, size float32) {
	badge := float32(theme.AvatarBadge)
	if size < theme.RowStep {
		badge = theme.AvatarBadgeSmall
	}
	ui.Box(c).Size(size, size).Radius(size / 2).Background(k.Fill2).Center().Children(func() {
		style := theme.UIStrong
		if size < theme.RowStep {
			style = theme.Label
		}
		style.On(ui.Text(c, initial(name(s)))).TextColor(k.Fg2)
		ui.Box(c).Absolute().Right(-2).Bottom(-2).Size(badge, badge).Radius(badge/2).Background(k.Side).Border(theme.LineWidth, k.Line).Center().Children(func() {
			a.harnessIcon(c, k, s.Harness.Kind, badge-theme.Space1)
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
	theme.Bar.P(0, theme.Space2, 0, theme.Space3).On(ui.Row(c)).Children(func() {
		ui.Row(c).Height(theme.Row).Padding(0, theme.Space1Half).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusControl).Grow(1).Shrink(1).Children(func() {
			ui.Icon(c, a.icons.Machine).FontSize(theme.Icon).TextColor(k.Fg3)
			machine := a.machine
			if machine == "" {
				machine = "本机"
			}
			theme.UI.On(ui.Text(c, machine)).FontWeight(theme.UIMedium.Weight()).TextColor(k.Fg).SingleLine().Shrink(1)
			if a.connected {
				ui.Icon(c, a.icons.Connected).FontSize(theme.IconSmall).TextColor(k.Muted).Label("已连接").Tooltip("已连接本机守护进程")
			} else {
				ui.Icon(c, a.icons.Offline).FontSize(theme.IconSmall).TextColor(k.Attn).Label("断线").Tooltip("连不上守护进程：" + a.offline)
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
	b := ui.Button(c, "").Label(tip).Tooltip(tip).Size(theme.Row, theme.Row).Padding(0).
		Radius(theme.RadiusControl).Background(ui.Transparent).Border(0, ui.Transparent).Transition(hoverMotion)
	if on || b.Hovered() {
		b.Background(k.Fill2)
	}
	return b.Children(func() {
		ui.Icon(c, icon).FontSize(theme.Icon).TextColor(k.Fg3)
	})
}
