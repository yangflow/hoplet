package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
)

// inboxTitle is the inbox's title bar: its icon and name.
func (a *App) inboxTitle(c *ui.Context, k theme.Colors) {
	ui.Icon(c, a.icons.Inbox).FontSize(theme.IconSize).TextColor(k.Fg3)
	theme.UIStrong.On(ui.Text(c, "收件箱")).TextColor(k.Fg)
}

// inboxPage is the inbox: what waits on me, grouped.
func (a *App) inboxPage(c *ui.Context, k theme.Colors) {
	ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
		ui.Icon(c, a.icons.Inbox).FontSize(32).TextColor(k.Faint)
		theme.UI.On(ui.Text(c, "没有要你处理的事")).TextColor(k.Muted)
	})
}
