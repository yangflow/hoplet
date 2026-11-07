package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
)

// searchField is a field that narrows a list: a search icon, the text and,
// once something is typed, a button that clears it. Out of a menu, esc
// clears the text too; in one (keys not nil), esc closes the menu as it
// always does. Every key the field does not take goes to keys, so a menu
// may move its selection; keys may be nil. The returned element is the
// text input, to focus.
func (a *App) searchField(c *ui.Context, k theme.Colors, query *string, label string, keys func(ui.TextKey) bool) ui.Element {
	var in ui.Element
	ui.Row(c).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space1Half).AlignItems(ui.Center).
		Radius(theme.RadiusControl).Background(k.Fill).Children(func() {
		ui.Icon(c, a.icons.Search).FontSize(theme.IconRow).TextColor(k.Muted)
		in = ui.TextInput(c.Key("input"), query).Placeholder(label).Label(label).Grow(1).
			FontSize(theme.Secondary.Size()).Background(ui.Transparent).Border(0, ui.Transparent).Padding(0)
		in.OnTextKey(func(key ui.TextKey) bool {
			if keys == nil && key.Mods == 0 && key.Key == ui.KeyEscape && *query != "" {
				*query = ""
				return true
			}
			return keys != nil && keys(key)
		})
		if *query == "" {
			return
		}
		tip := "清除 esc"
		if keys != nil {
			tip = "清除"
		}
		x := ui.Button(c.Key("clear"), "").Label("清除").Tooltip(tip).Size(theme.ControlXS, theme.ControlXS).Padding(0).
			Radius(theme.RadiusControl).Background(ui.Transparent).Border(0, ui.Transparent).
			KeepFocus().Transition(hoverMotion)
		if x.Hovered() {
			x.Background(k.Hover)
		}
		if x.Clicked() {
			*query = ""
		}
		x.Children(func() {
			ui.Icon(c, a.icons.Close).FontSize(theme.IconRow).TextColor(k.Fg3)
		})
	})
	return in
}
