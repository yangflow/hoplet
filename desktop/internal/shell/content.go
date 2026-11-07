package shell

import (
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/convo"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// content draws the content area: the selected session's title bar. With
// the sidebar hidden, the traffic lights and the window buttons move into
// it, and it grows to the sidebar's top row so they line up. The
// conversation comes with the session view.
func (a *App) content(c *ui.Context, k theme.Colors) {
	bar := c.TitleBar()
	ui.Column(c).Grow(1).Background(k.Pane).Children(func() {
		left, h := float32(theme.Space4), a.titleHeight(bar)
		if a.hideSide {
			left = bar.Left + theme.Space4
		}
		head := ui.Row(c).Height(h).Padding(0, theme.Space2, 0, left).Gap(theme.Space1Half).AlignItems(ui.Center).DragWindow()
		head.Children(func() {
			if a.hideSide {
				ui.Row(c).Gap(theme.SpaceHalf).Children(func() { a.windowButtons(c, k) })
			}
			if a.inbox {
				a.inboxTitle(c, k)
				return
			}
			if a.s.newPage {
				title := "新会话"
				if a.mode == proto.ModePersonal {
					title = "新伙伴"
				}
				theme.UIStrong.On(ui.Text(c, title)).TextColor(k.Fg)
				return
			}
			s, ok := a.current()
			if !ok {
				return
			}
			if s.Mode == proto.ModeCode {
				a.harnessIcon(c, k, s.Harness.Kind, theme.Icon)
			} else {
				a.avatarSized(c, k, s, theme.AvatarSmall)
			}
			title := theme.UIStrong.On(ui.Text(c, name(s))).TextColor(k.Fg).SingleLine().Shrink(1)
			if s.Directory != "" {
				title.Tooltip(s.Directory)
			}
			if title.DoubleClicked() {
				a.Rename()
			}
			iconButton(c, k, a.icons.Menu, "会话菜单", false).Size(theme.ControlXS, theme.ControlXS).Margin(0, 0, 0, -theme.Space1).Menu(func(m *ui.Menu) { a.sessionMenu(m, s) })
			ui.Spacer(c)
			tip := "会话详情 ⌘I"
			if s.Mode == proto.ModePersonal {
				tip = "伙伴详情 ⌘I"
			}
			b := iconButton(c, k, a.icons.Details, tip, a.s.detailOpen && a.detailsDocked() || a.s.detailFloat)
			if b.Clicked() || c.Shortcut(ui.Cmd, ui.KeyI) {
				a.ToggleDetails()
			}
			a.detailsPopover(c, k, head, b, s)
			if _, ok := a.panelSession(); ok && a.fit.panel == 0 && iconButton(c, k, a.icons.Panel, "显示右栏 ⌥⌘I", false).Clicked() {
				a.TogglePanel()
			}
		})
		s, ok := a.current()
		switch {
		case a.inbox:
			a.inboxPage(c, k)
		case a.s.newPage:
			a.newPage(c, k)
		case ok:
			// The details sit beside the conversation, not over it.
			ui.Row(c).Grow(1).Shrink(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				// No transition of its own: it follows the details as they open,
				// frame by frame, rather than chase them a beat behind.
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Children(func() { a.conversation(c, k, s) })
				if a.s.detailOpen && a.detailsDocked() {
					ui.Scroll(c.Key("details")).Width(theme.DetailsWidth).ClipX().Transition(openMotion).Children(func() {
						ui.Column(c).Padding(theme.Space3, theme.Space3, theme.Space3, 0).Gap(theme.Space3).Children(func() {
							for _, g := range a.detailGroups(c, k, s) {
								a.detailBlock(c, k, g, false)
							}
						})
					})
				}
			})
		default:
			ui.Box(c).Grow(1).Center().Children(func() {
				ui.Icon(c, a.icons.Mark).FontSize(theme.MarkEmpty).TextColor(k.Faint)
			})
		}
	})
}

// details is a Code session's details in blocks: where it runs (machine,
// directory, branch), how (harness and version, model and effort,
// permission) and the session (state and when it was made, its ID and
// the harness's own ID to copy). A row without a value is left out, a block without rows too.
func (a *App) details(c *ui.Context, k theme.Colors, s proto.Session) []detailGroup {
	h, _ := a.harness(s.Harness.Kind)
	model, _ := currentModel(runSettings{model: s.Model}, h)
	var branch string
	if d := a.diff(s); d != nil {
		for _, r := range d.Repos {
			if r.Path == "." {
				branch = r.Branch
			}
		}
	}
	place := detailGroup{title: "", rows: []detail{
		{icon: a.icons.Machine, value: a.machine},
		{image: a.icons.FolderType(), value: tilde(s.Directory), tip: s.Directory, mono: true, end: func() {
			if iconButton(c, k, a.icons.Folder, "在 Finder 中显示", false).Size(theme.RowDense, theme.RowDense).Clicked() {
				a.host.Reveal(s.Directory)
			}
		}},
		{icon: a.icons.Task, value: branch, mono: true},
	}}
	perm := modeName(s.Permission)
	if s.Plan {
		perm += " · Plan"
	}
	run := []detail{{harness: s.Harness.Kind, value: harnessName(s.Harness.Kind), version: s.Harness.Version}}
	if s.Harness.Kind == "" {
		run = nil
	}
	run = append(run, detail{icon: a.icons.Thinking, value: model + " · 推理强度 " + effortName(s.Effort)}, detail{icon: a.modeIcon(s.Permission), value: perm})
	gs := []detailGroup{place, {title: "运行", rows: run}, {title: "会话", rows: []detail{
		{icon: a.icons.Made, value: statusName(s.Status) + " · 建于 " + s.CreatedAt.Local().Format("1 月 2 日")},
		{icon: a.icons.ID, value: string(s.ID), mono: true, end: func() { a.copyButton(c, k, "id:"+string(s.ID), string(s.ID), "复制会话 ID") }},
		// The harness's own ID, once it started one: what resumes the
		// session in the harness itself.
		{harness: s.Harness.Kind, value: s.Harness.NativeID, tip: harnessName(s.Harness.Kind) + " 会话 ID：" + s.Harness.NativeID, mono: true, end: func() {
			a.copyButton(c, k, "native:"+string(s.ID), s.Harness.NativeID, "复制 "+harnessName(s.Harness.Kind)+" 会话 ID")
		}},
	}}}
	if agents := a.conv(s.ID).Agents(); len(agents) > 0 {
		gs = append(gs, detailGroup{title: "子代理", body: func() { a.agentRows(c, k, s, agents) }})
	}
	return gs
}

// agentRows are the details' subagents: those running first, each with a
// spinner, its title and time; the finished ones fold into 已结束 N 个,
// folded at first, and open to their state, title and time or 已停止.
// A row opens the subagent's record in the viewer.
func (a *App) agentRows(c *ui.Context, k theme.Colors, s proto.Session, agents []convo.Subagent) {
	var ended []convo.Subagent
	row := func(g convo.Subagent) {
		r := ui.Row(c.Key(g.ID)).Height(theme.RowStep).Padding(0, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).Label(g.Title)
		if r.Hovered() {
			r.Background(k.Fill)
		}
		if r.Clicked() {
			a.openAgent(s, g.ID)
		}
		r.Children(func() {
			color := k.Fg
			if g.Status == proto.SubagentRunning {
				ui.Spinner(c).Size(theme.IconRow, theme.IconRow)
			} else {
				ui.Icon(c, a.agentIcon(g.Status)).FontSize(theme.IconRow).TextColor(k.Fg3)
				color = k.Fg2
			}
			ui.Text(c, g.Title).FontSize(theme.UI.Size()).TextColor(color).SingleLine().Grow(1).Shrink(1).Tooltip(g.Title)
			theme.Caption.On(ui.Text(c, agentTime(c, g))).TextColor(k.Faint).SingleLine().Shrink(0)
		})
	}
	for _, g := range agents {
		if g.Status == proto.SubagentRunning {
			row(g)
		} else {
			ended = append(ended, g)
		}
	}
	if len(ended) == 0 {
		return
	}
	key := "agents:" + string(s.ID)
	open := a.s.open[key]
	h := ui.Row(c).Height(theme.RowStep).Padding(0, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).Label(fmt.Sprintf("已结束 %d 个", len(ended)))
	if h.Clicked() {
		a.s.open[key] = !open
	}
	h.Children(func() {
		icon := a.icons.Expand
		if open {
			icon = a.icons.Menu
		}
		ui.Icon(c, icon).FontSize(theme.IconRow).TextColor(k.Fg3)
		theme.UI.On(ui.Textf(c, "已结束 %d 个", len(ended))).TextColor(k.Fg2)
	})
	if open {
		ui.Column(c).Transition(openMotion).Children(func() {
			for _, g := range ended {
				row(g)
			}
		})
	}
}

// detail is a row of the details column: an icon, or a harness's mark with
// its version, a value and maybe a button at its end.
type detail struct {
	icon    *ui.SVG
	image   *ui.SVG // an icon in its own colors, as a folder's
	harness string
	version string
	value   string
	tip     string
	mono    bool
	end     func()
}

// detailGroup is a block of the details: its title, "" for none, and
// its rows.
type detailGroup struct {
	title string
	rows  []detail
	body  func() // rows of its own in place of rows
}

// detailGroups is a session's details in blocks, rows without a value and
// blocks without rows left out.
func (a *App) detailGroups(c *ui.Context, k theme.Colors, s proto.Session) []detailGroup {
	gs := a.companionDetails(s)
	if s.Mode != proto.ModePersonal {
		gs = a.details(c, k, s)
	}
	out := gs[:0]
	for _, g := range gs {
		g.rows = slices.DeleteFunc(g.rows, func(d detail) bool { return d.value == "" })
		if len(g.rows) > 0 || g.body != nil {
			out = append(out, g)
		}
	}
	return out
}

// detailsDocked reports whether the content area, as last laid out, has
// room for the details beside the conversation.
func (a *App) detailsDocked() bool {
	return a.width-a.fit.side-a.fit.panel >= theme.DetailsDock
}

// ToggleDetails opens or closes the details. Docked, they open and close
// beside the conversation; without room they float under the title bar,
// only when I ask, and dock once the content area widens.
func (a *App) ToggleDetails() {
	switch {
	case a.detailsDocked():
		a.s.detailOpen, a.s.detailFloat = !a.s.detailOpen, false
	case a.s.detailFloat:
		a.s.detailOpen, a.s.detailFloat = false, false
	default:
		a.s.detailOpen, a.s.detailFloat = true, true
	}
}

// detailsPopover floats the details under the title bar, at its end, while
// the content area is too narrow for them to dock: flat blocks between
// hairlines on the popover's card. A click outside or ⎋ puts it away.
func (a *App) detailsPopover(c *ui.Context, k theme.Colors, head, button ui.Element, s proto.Session) {
	if a.detailsDocked() {
		a.s.detailFloat = false
		return
	}
	ui.PopoverBase(c, button, &a.s.detailFloat, func(panel ui.Element) {
		panel.AttachTo(head, ui.AnchorBottomEnd, ui.AnchorTopEnd).Margin(0, theme.Space2, 0, 0).Width(theme.DetailsWidth).Gap(theme.Space2).
			Transition(popMotion).Label("会话详情").Children(func() {
			for _, g := range a.detailGroups(c, k, s) {
				a.detailBlock(c, k, g, true)
			}
		})
	})
}

// detailBlock draws a block of the details as a card with a hairline:
// docked, on the surface beside the conversation; floating, with the
// popover's opaque surface, edge and shadow, so the conversation never
// reads through. Its title goes above the rows unless empty.
func (a *App) detailBlock(c *ui.Context, k theme.Colors, g detailGroup, float bool) {
	title, rows := g.title, g.rows
	block := ui.Column(c.Key("block:"+title)).Padding(theme.Space1, 0).Radius(theme.RadiusCard)
	if float {
		k.ShadowPopover.On(block.Background(k.SurfaceFloat).Border(theme.LineWidth, k.Line2))
	} else {
		block.Background(k.Surface).Border(theme.LineWidth, k.Line)
	}
	block.Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(theme.Caption.Size()).FontWeight(theme.UIStrong.Weight()).TextColor(k.Muted).Padding(theme.Space2, theme.Space3, theme.SpaceHalf, theme.Space3)
		}
		if g.body != nil {
			g.body()
			return
		}
		for i, d := range rows {
			ui.Row(c.Key(i)).Height(theme.RowStep).Padding(0, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
				switch {
				case d.harness != "":
					a.harnessIcon(c, k, d.harness, theme.IconRow)
				case d.image != nil:
					ui.Image(c, d.image).Size(theme.IconRow, theme.IconRow)
				default:
					ui.Icon(c, d.icon).FontSize(theme.IconRow).TextColor(k.Fg3)
				}
				tip := d.tip
				if tip == "" {
					tip = d.value
				}
				v := ui.Text(c, d.value).FontSize(theme.UI.Size()).TextColor(k.Fg).SingleLine().Grow(1).Shrink(1).Selectable().Tooltip(tip)
				if d.mono {
					v.Font(theme.Mono).FontSize(theme.Secondary.Size())
				}
				if d.version != "" {
					theme.Caption.On(ui.Text(c, d.version)).Font(theme.Mono).TextColor(k.Muted)
				}
				if d.end != nil {
					d.end()
				}
			})
		}
	})
}

// statusName names a session's state.
func statusName(st proto.SessionStatus) string {
	switch st {
	case proto.StatusRunning:
		return "运行中"
	case proto.StatusWaiting:
		return "需要你"
	case proto.StatusErrored:
		return "出错了"
	default:
		return "空闲"
	}
}

// companionDetails is a companion's details in blocks: its machine, then
// its harness, model and effort; its name is the title bar's. Its persona
// and what it remembers about me come with companions (M4).
func (a *App) companionDetails(s proto.Session) []detailGroup {
	h, _ := a.harness(s.Harness.Kind)
	model, _ := currentModel(runSettings{model: s.Model}, h)
	machine := a.machine
	if a.connected {
		machine += " · 在线"
	}
	return []detailGroup{{title: "", rows: []detail{{icon: a.icons.Machine, value: machine}}}, {title: "运行", rows: []detail{
		{harness: s.Harness.Kind, value: harnessName(s.Harness.Kind), version: s.Harness.Version},
		{icon: a.icons.Thinking, value: model + " · 推理强度 " + effortName(s.Effort)},
	}}}
}

// titleHeight is the height of the content area's title bar and of the
// right panel's top rows beside it: the sidebar's top row's, so that every
// button along the top sits on the traffic lights' center line.
func (a *App) titleHeight(bar ui.TitleBar) float32 { return topHeight(bar) }
