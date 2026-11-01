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
		})
		ui.Box(c).Grow(1).Center().Children(func() {
			if _, ok := a.current(); !ok {
				ui.Icon(c, a.icons.Mark).FontSize(48).TextColor(k.Faint)
			}
		})
	})
}
