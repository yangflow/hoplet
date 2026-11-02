package shell

import (
	"slices"

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
			if iconButton(c, k, a.icons.Details, tip, a.s.detailOpen).Clicked() || c.Shortcut(ui.Cmd, ui.KeyI) {
				a.s.detailOpen = !a.s.detailOpen
			}
		})
		s, ok := a.current()
		switch {
		case a.s.newPage:
			a.newPage(c, k)
		case ok:
			// The details sit beside the conversation, not over it.
			ui.Row(c).Grow(1).Shrink(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Grow(1).Shrink(1).Children(func() { a.conversation(c, k, s) })
				if a.s.detailOpen {
					ui.Scroll(c).Width(detailsWidth).Children(func() {
						ui.Column(c).Padding(12, 12, 12, 0).Gap(10).Children(func() {
							if s.Mode == proto.ModePersonal {
								a.companionDetails(c, k, s)
							} else {
								a.details(c, k, s)
							}
						})
					})
				}
			})
		default:
			ui.Box(c).Grow(1).Center().Children(func() {
				ui.Icon(c, a.icons.Mark).FontSize(48).TextColor(k.Faint)
			})
		}
	})
}

// detailsWidth is the width of the details column.
const detailsWidth = 280

// details is a Code session's details in blocks: where it runs (machine,
// directory, branch), how (harness and version, model and effort,
// permission) and the session (state and when it was made, its ID to
// copy). A row without a value is left out, a block without rows too.
func (a *App) details(c *ui.Context, k theme.Colors, s proto.Session) {
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
	a.detailBlock(c, k, "", []detail{
		{icon: a.icons.Machine, value: a.machine},
		{icon: a.icons.Folder, value: tilde(s.Directory), tip: s.Directory, mono: true, end: func() {
			if iconButton(c, k, a.icons.Folder, "在 Finder 中显示", false).Size(24, 24).Clicked() {
				a.host.Reveal(s.Directory)
			}
		}},
		{icon: a.icons.Task, value: branch, mono: true},
	})
	perm := modeName(s.Permission)
	if s.Plan {
		perm += " · Plan"
	}
	run := []detail{{harness: s.Harness.Kind, value: harnessName(s.Harness.Kind), version: s.Harness.Version}}
	if s.Harness.Kind == "" {
		run = nil
	}
	run = append(run, detail{icon: a.icons.Thinking, value: model + " · 推理强度 " + effortName(s.Effort)}, detail{icon: a.modeIcon(s.Permission), value: perm})
	a.detailBlock(c, k, "运行", run)
	a.detailBlock(c, k, "会话", []detail{
		{icon: a.icons.Made, value: statusName(s.Status) + " · 建于 " + s.CreatedAt.Local().Format("1 月 2 日")},
		{icon: a.icons.ID, value: string(s.ID), mono: true, end: func() { a.copyButton(c, k, "id:"+string(s.ID), string(s.ID), "复制会话 ID") }},
	})
}

// detail is a row of the details column: an icon, or a harness's mark with
// its version, a value and maybe a button at its end.
type detail struct {
	icon    *ui.SVG
	harness string
	version string
	value   string
	tip     string
	mono    bool
	end     func()
}

// detailBlock draws a block of the details column: a card with a hairline,
// its title above the rows unless empty. Rows without a value are left
// out, and the block without any.
func (a *App) detailBlock(c *ui.Context, k theme.Colors, title string, rows []detail) {
	rows = slices.DeleteFunc(rows, func(d detail) bool { return d.value == "" })
	if len(rows) == 0 {
		return
	}
	ui.Column(c.Key("block:"+title)).Padding(4, 0).Radius(12).Border(1, k.Line2).Background(k.Bg2).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(11).FontWeight(600).TextColor(k.Muted).Padding(8, 12, 2, 12)
		}
		for i, d := range rows {
			ui.Row(c.Key(i)).Height(32).Padding(0, 12).Gap(10).AlignItems(ui.Center).Children(func() {
				if d.harness != "" {
					a.harnessIcon(c, k, d.harness, theme.IconSizeInRow)
				} else {
					ui.Icon(c, d.icon).FontSize(theme.IconSizeInRow).TextColor(k.Fg3)
				}
				tip := d.tip
				if tip == "" {
					tip = d.value
				}
				v := ui.Text(c, d.value).FontSize(13).TextColor(k.Fg).SingleLine().Grow(1).Shrink(1).Selectable().Tooltip(tip)
				if d.mono {
					v.Font(theme.Mono).FontSize(12)
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
func (a *App) companionDetails(c *ui.Context, k theme.Colors, s proto.Session) {
	h, _ := a.harness(s.Harness.Kind)
	model, _ := currentModel(runSettings{model: s.Model}, h)
	machine := a.machine
	if a.connected {
		machine += " · 在线"
	}
	a.detailBlock(c, k, "", []detail{{icon: a.icons.Machine, value: machine}})
	a.detailBlock(c, k, "运行", []detail{
		{harness: s.Harness.Kind, value: harnessName(s.Harness.Kind), version: s.Harness.Version},
		{icon: a.icons.Thinking, value: model + " · 推理强度 " + effortName(s.Effort)},
	})
}
