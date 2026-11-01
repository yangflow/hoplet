package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// content draws the content area: the selected session's title bar, as
// tall as the sidebar's top row so both line up with the traffic lights.
// The conversation comes with the session view.
func (a *App) content(c *ui.Context, k theme.Colors) {
	bar := c.TitleBar()
	ui.Column(c).Grow(1).Background(k.Pane).Children(func() {
		left := float32(16)
		if a.hideSide {
			left = bar.Left + 8
		}
		ui.Row(c).Height(topHeight(bar)).Padding(0, 12, 0, left).Gap(8).AlignItems(ui.Center).DragWindow().Children(func() {
			if a.hideSide {
				ui.Row(c).Gap(2).Children(func() { a.windowButtons(c, k) })
			}
			s, ok := a.current()
			if !ok {
				return
			}
			if s.Mode == proto.ModeCode {
				a.harnessIcon(c, k, s.Harness.Kind, theme.IconSize)
			}
			title := theme.UIStrong.On(ui.Text(c, name(s))).TextColor(k.Fg).SingleLine().Shrink(1)
			if s.Directory != "" {
				title.Tooltip(s.Directory)
			}
			if title.DoubleClicked() {
				a.Rename()
			}
			iconButton(c, k, a.icons.Menu, "会话菜单", false).Size(24, 24).Menu(func(m *ui.Menu) { a.sessionMenu(m, s) })
		})
		ui.Box(c).Grow(1).Center().Children(func() {
			if _, ok := a.current(); !ok {
				ui.Icon(c, a.icons.Mark).FontSize(48).TextColor(k.Faint)
			}
		})
	})
}
