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
	ui.Column(c).Grow(1).Background(k.Pane).Children(func() {
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
			d := iconButton(c, k, a.icons.Info, "会话详情 ⌘I", a.s.detailOpen)
			if d.Clicked() || c.Shortcut(ui.Cmd, ui.KeyI) {
				a.s.detailOpen = !a.s.detailOpen
			}
			ui.Popover(c, d, &a.s.detailOpen, func() { a.details(c, k, s) })
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
// version, model, effort, plan; and copying the session ID.
func (a *App) details(c *ui.Context, k theme.Colors, s proto.Session) {
	h, _ := a.harness(s.Harness.Kind)
	model, _ := currentModel(s, h)
	rows := [][2]string{
		{"机器", a.machine},
		{"目录", s.Directory},
		{"harness", s.Harness.Kind + " " + s.Harness.Version},
		{"模型", model},
		{"强度", effortName(s.Effort)},
		{"审批", modeName(s.Permission)},
	}
	if s.Plan {
		rows = append(rows, [2]string{"Plan", "开"})
	}
	ui.Column(c).Width(320).Padding(10, 12).Gap(6).Children(func() {
		for _, r := range rows {
			if r[1] == "" {
				continue
			}
			ui.Row(c.Key(r[0])).Gap(10).Children(func() {
				theme.Caption.On(ui.Text(c, r[0])).TextColor(k.Muted).Width(56)
				theme.Secondary.On(ui.Text(c, r[1])).TextColor(k.Fg).Grow(1).Selectable()
			})
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			theme.Caption.On(ui.Text(c, "会话 ID")).TextColor(k.Muted).Width(56)
			ui.Text(c, string(s.ID)).Font(theme.Mono).FontSize(12).TextColor(k.Fg3).Grow(1)
			a.copyButton(c, k, "id:"+string(s.ID), string(s.ID))
		})
	})
}
