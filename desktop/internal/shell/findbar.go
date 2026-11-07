package shell

import (
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/yangflow/hoplet/desktop/internal/theme"
)

// Find bar field widths: as wide as findField, giving way first to a narrow
// column, down to findFieldMin.
const (
	findField    = 200
	findFieldMin = 96
	findCount    = 48 // the count of matches, as wide as "12/34"
)

// findBar builds the find bar of a read-only code view over its top right
// corner: the query, matching case, whole words and regular expressions,
// the count of matches, the buttons through them and closing. The bar is
// at most the code's width less a margin each side; the field gives way
// first.
func (a *App) findBar(k theme.Colors) func(c *ui.Context, bar ui.Element, f *editor.FindBar) {
	return func(c *ui.Context, bar ui.Element, f *editor.FindBar) {
		// Placed from the right, the bar may take what the left margin
		// leaves: twice the margin keeps one each side.
		bar.Row().Top(theme.Space2).Right(theme.Space2).Margin(0, 0, 0, 2*theme.Space2).
			Gap(theme.SpaceHalf).Padding(theme.Space1).AlignItems(ui.Center).
			Radius(theme.RadiusCard).Background(k.SurfaceFloat).Border(theme.LineWidth, k.Line2)
		k.ShadowPopover.On(bar)
		count, current := f.Matches()
		bar.Children(func() {
			ui.Row(c).Basis(findField).MinWidth(findFieldMin).Shrink(1).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space1Half).
				AlignItems(ui.Center).Radius(theme.RadiusControl).Background(k.Fill).Children(func() {
				ui.Icon(c, a.icons.Search).FontSize(theme.IconRow).TextColor(k.Muted)
				f.QueryInput(ui.TextInputBase(c, f.Query()).Placeholder("查找").Label("查找").Grow(1).Shrink(1).MinWidth(0).
					FontSize(theme.Secondary.Size()).TextColor(k.Fg))
			})
			a.findToggle(c, k, a.icons.MatchCase, "区分大小写", f.CaseSensitive())
			a.findToggle(c, k, a.icons.WholeWord, "全字匹配", f.WholeWord())
			a.findToggle(c, k, a.icons.Regexp, "正则表达式", f.Regexp())
			text, color := "", k.Muted
			switch {
			case *f.Query() == "":
			case count == 0:
				text, color = "无结果", k.Del
			default:
				text = strconv.Itoa(current+1) + "/" + strconv.Itoa(count)
			}
			theme.Caption.On(ui.Text(c, text)).TextColor(color).FontFeatures("tnum").TextAlign(ui.Center).
				Width(findCount).Padding(0, theme.Space1).Shrink(0).Role(ui.RoleStatus)
			if a.findButton(c, k, a.icons.FindUp, "上一个 ⇧↩", count == 0).Clicked() {
				f.Previous()
			}
			if a.findButton(c, k, a.icons.FindDown, "下一个 ↩", count == 0).Clicked() {
				f.Next()
			}
			if a.findButton(c, k, a.icons.Close, "关闭 esc", false).Clicked() {
				f.Close()
			}
		})
	}
}

// findToggle is one of the find bar's switches, pressed while on.
func (a *App) findToggle(c *ui.Context, k theme.Colors, icon *ui.SVG, tip string, on *bool) {
	b := ui.ToggleBase(c.Key(tip), on).Label(tip).Tooltip(tip).Size(theme.RowDense, theme.RowDense).Shrink(0).
		Center().Radius(theme.RadiusControl).KeepFocus().Transition(hoverMotion)
	switch {
	case *on:
		b.Background(k.Hover)
	case b.Hovered():
		b.Background(k.Fill)
	}
	b.Children(func() { ui.Icon(c, icon).FontSize(theme.Icon).TextColor(k.Fg3) })
}

// findButton is one of the find bar's buttons, faded while it has nothing
// to do.
func (a *App) findButton(c *ui.Context, k theme.Colors, icon *ui.SVG, tip string, disabled bool) ui.Element {
	b := ui.ButtonBase(c.Key(tip)).Label(tip).Tooltip(tip).Size(theme.RowDense, theme.RowDense).Shrink(0).
		Center().Radius(theme.RadiusControl).KeepFocus().Disabled(disabled).Transition(hoverMotion)
	switch {
	case disabled:
		b.Opacity(theme.OpacityDisabled)
	case b.Hovered():
		b.Background(k.Fill)
	}
	return b.Children(func() { ui.Icon(c, icon).FontSize(theme.Icon).TextColor(k.Fg3) })
}
