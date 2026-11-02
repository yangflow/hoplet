package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// content draws the content area: the selected session's title bar. With
// the sidebar hidden, the traffic lights and the window buttons move into
// it, and it grows to the sidebar's top row so they line up. The
// conversation comes with the session view.
func (a *App) content(c *ui.Context, k theme.Colors) {
	bar := c.TitleBar()
	bg := k.Pane
	if s, ok := a.current(); ok && !a.s.newPage && s.Mode == proto.ModePersonal {
		bg = ui.Transparent // a chat sits lighter than Code's pane
	}
	ui.Column(c).Grow(1).Background(bg).Children(func() {
		left, h := float32(20), float32(theme.TitleBar)
		if a.hideSide {
			left, h = bar.Left+8, topHeight(bar)
		}
		ui.Row(c).Height(h).Padding(0, 8, 0, left).Gap(8).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(k.Line).DragWindow().Children(func() {
			if a.hideSide {
				ui.Row(c).Gap(2).Children(func() { a.windowButtons(c, k) })
			}
			s, ok := a.current()
			if !ok {
				return
			}
			if s.Mode == proto.ModeCode {
				a.harnessIcon(c, k, s.Harness.Kind, theme.IconSize)
			} else {
				a.avatarSized(c, k, s, 22)
			}
			title := theme.UIStrong.On(ui.Text(c, name(s))).TextColor(k.Fg).SingleLine().Shrink(1)
			if s.Directory != "" {
				title.Tooltip(s.Directory)
			}
			if title.DoubleClicked() {
				a.Rename()
			}
			iconButton(c, k, a.icons.Menu, "会话菜单", false).Size(22, 22).Margin(0, 0, 0, -4).Menu(func(m *ui.Menu) { a.sessionMenu(m, s) })
			ui.Spacer(c)
			tip := "会话详情 ⌘I"
			if s.Mode == proto.ModePersonal {
				tip = "伙伴详情 ⌘I"
			}
			d := iconButton(c, k, a.icons.Info, tip, a.s.detailOpen)
			if d.Clicked() || c.Shortcut(ui.Cmd, ui.KeyI) {
				a.s.detailOpen = !a.s.detailOpen
			}
			ui.PopoverBase(c, d, &a.s.detailOpen, func(panel ui.Element) {
				panel.AttachTo(d, ui.AnchorBottomEnd, ui.AnchorTopEnd).Margin(6, 0, 0, 0).Width(320).Padding(10).Gap(4).
					Radius(12).Background(k.Popover).Border(1, k.Line2).Shadow(0, 10, 30, 0, k.Shadow).Children(func() {
					if s.Mode == proto.ModePersonal {
						a.companionDetails(c, k, s)
					} else {
						a.details(c, k, s)
					}
				})
			})
		})
		s, ok := a.current()
		switch {
		case a.s.newPage:
			a.newPage(c, k)
		case ok:
			a.conversation(c, k, s)
		default:
			ui.Box(c).Grow(1).Center().Children(func() {
				ui.Icon(c, a.icons.Mark).FontSize(48).TextColor(k.Faint)
			})
		}
	})
}

// details is the session details popover: machine, directory, harness and
// version, model, effort, permission and plan; and copying the session ID.
func (a *App) details(c *ui.Context, k theme.Colors, s proto.Session) {
	h, _ := a.harness(s.Harness.Kind)
	model, _ := currentModel(runSettings{model: s.Model}, h)
	rows := [][2]string{
		{"机器", a.machine},
		{"目录", tilde(s.Directory)},
		{"harness", harnessName(s.Harness.Kind) + " " + s.Harness.Version},
		{"模型", model},
		{"强度", effortName(s.Effort)},
		{"审批", modeName(s.Permission)},
	}
	if s.Plan {
		rows = append(rows, [2]string{"Plan", "开"})
	}
	detailRows(c, k, rows)
	ui.Row(c).Padding(0, 6).Gap(6).AlignItems(ui.Center).Children(func() {
		theme.Caption.On(ui.Text(c, "会话 ID")).TextColor(k.Muted).Width(56)
		ui.Text(c, string(s.ID)).Font(theme.Mono).FontSize(12).TextColor(k.Fg3).Grow(1)
		a.copyButton(c, k, "id:"+string(s.ID), string(s.ID))
	})
}

// companionDetails is a companion's details: its avatar, name and when it
// was made, then its harness, model and effort, and its machine. Its
// persona and what it remembers about me come with companions (M4).
func (a *App) companionDetails(c *ui.Context, k theme.Colors, s proto.Session) {
	h, _ := a.harness(s.Harness.Kind)
	model, _ := currentModel(runSettings{model: s.Model}, h)
	ui.Row(c).Padding(4, 6, 8, 6).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(40, 40).Radius(20).Background(k.Line2).Center().Children(func() {
			theme.Title.On(ui.Text(c, initial(name(s)))).TextColor(k.Fg2)
		})
		ui.Column(c).Grow(1).Children(func() {
			theme.Reading.On(ui.Text(c, name(s))).FontWeight(600).TextColor(k.Fg)
			theme.Caption.On(ui.Text(c, "建于 "+s.CreatedAt.Local().Format("1 月 2 日"))).TextColor(k.Muted)
		})
	})
	machine := a.machine
	if a.connected {
		machine += " · 在线"
	}
	detailRows(c, k, [][2]string{
		{"harness", harnessName(s.Harness.Kind)},
		{"模型", model},
		{"强度", effortName(s.Effort)},
		{"机器", machine},
	})
}

// detailRows are label and value rows of a details popover.
func detailRows(c *ui.Context, k theme.Colors, rows [][2]string) {
	for _, r := range rows {
		if r[1] == "" {
			continue
		}
		ui.Row(c.Key(r[0])).Padding(2, 6).Gap(10).Children(func() {
			theme.Caption.On(ui.Text(c, r[0])).TextColor(k.Muted).Width(56)
			theme.Secondary.On(ui.Text(c, r[1])).TextColor(k.Fg).Grow(1).Selectable()
		})
	}
}
