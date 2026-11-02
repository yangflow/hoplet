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
					Radius(theme.RadiusCard).Background(k.Popover).Border(1, k.Line2).Shadow(0, 10, 30, 0, k.Shadow).Children(func() {
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

// details is a Code session's details: its harness, name, state and when
// it was made; then machine, directory, branch, harness and version, model
// and effort, and permission; under a line, its ID to copy. A row without
// a value is left out.
func (a *App) details(c *ui.Context, k theme.Colors, s proto.Session) {
	h, _ := a.harness(s.Harness.Kind)
	model, _ := currentModel(runSettings{model: s.Model}, h)
	ui.Row(c).Padding(4, 6, 8, 6).Gap(10).AlignItems(ui.Center).Children(func() {
		a.harnessIcon(c, k, s.Harness.Kind, 18)
		ui.Column(c).Grow(1).Shrink(1).Children(func() {
			theme.Reading.On(ui.Text(c, name(s))).FontWeight(600).TextColor(k.Fg).SingleLine()
			theme.Caption.On(ui.Text(c, statusName(s.Status)+" · 建于 "+s.CreatedAt.Local().Format("1 月 2 日"))).TextColor(k.Muted)
		})
	})
	detailRow(c, k, "机器", a.machine, false, nil)
	detailRow(c, k, "目录", tilde(s.Directory), true, func() {
		if iconButton(c, k, a.icons.Folder, "在 Finder 中显示", false).Size(24, 24).Clicked() {
			a.host.Reveal(s.Directory)
		}
	})
	if d := a.diff(s); d != nil {
		for _, r := range d.Repos {
			if r.Path == "." {
				detailRow(c, k, "分支", r.Branch, true, nil)
			}
		}
	}
	if s.Harness.Kind != "" {
		ui.Row(c.Key("harness")).Padding(3, 6).Gap(10).AlignItems(ui.Center).Children(func() {
			theme.Caption.On(ui.Text(c, "harness")).TextColor(k.Muted).Width(56)
			ui.Row(c).Grow(1).Gap(6).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, harnessName(s.Harness.Kind)).FontSize(12.5).TextColor(k.Fg)
				theme.Caption.On(ui.Text(c, s.Harness.Version)).Font(theme.Mono).TextColor(k.Muted)
			})
		})
	}
	detailRow(c, k, "模型", model+" · 推理强度 "+effortName(s.Effort), false, nil)
	perm := modeName(s.Permission)
	if s.Plan {
		perm += " · Plan"
	}
	detailRow(c, k, "审批", perm, false, nil)
	ui.Box(c).Height(1).Margin(6, 6).Background(k.Line)
	detailRow(c, k, "会话 ID", string(s.ID), true, func() { a.copyButton(c, k, "id:"+string(s.ID), string(s.ID), "复制") })
}

// detailRow is a label and value row of a details popover, with an
// optional button at its end; it is left out without a value.
func detailRow(c *ui.Context, k theme.Colors, label, value string, mono bool, end func()) {
	if value == "" {
		return
	}
	ui.Row(c.Key(label)).Height(24).Padding(0, 6).Gap(10).AlignItems(ui.Center).Children(func() {
		theme.Caption.On(ui.Text(c, label)).TextColor(k.Muted).Width(56)
		v := ui.Text(c, value).FontSize(12.5).TextColor(k.Fg).SingleLine().Grow(1).Shrink(1).Selectable().Tooltip(value)
		if mono {
			v.Font(theme.Mono).FontSize(12)
		}
		if end != nil {
			end()
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
	detailRow(c, k, "harness", harnessName(s.Harness.Kind), false, nil)
	detailRow(c, k, "模型", model, false, nil)
	detailRow(c, k, "强度", effortName(s.Effort), false, nil)
	detailRow(c, k, "机器", machine, false, nil)
}
