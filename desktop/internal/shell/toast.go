package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
)

// noteID names the one toast: a new note replaces the one showing.
const noteID = "note"

// hint shows a passing note, which goes after the toasts' 4 seconds.
func (a *App) hint(text string) { a.pending = ui.Toast{ID: noteID, Title: text} }

// failure is the toast for something that did not happen: it stays until
// closed; retry, when not nil, asks again.
func failure(text string, retry func()) ui.Toast {
	t := ui.Toast{ID: noteID, Type: "error", Title: text, Timeout: -1}
	if retry != nil {
		t.Action, t.OnAction = "重试", retry
	}
	return t
}

// toasts adds the note Fail or hint left for the view and draws the toast at the
// top of the content area, below its title bar: the composer below
// changes height and would cover it.
func (a *App) toasts(c *ui.Context, k theme.Colors) {
	if a.pending.Title != "" {
		c.AddToast(a.pending)
		a.pending = ui.Toast{}
	}
	top, left := float32(theme.TitleBar), a.side
	if a.hideSide {
		top, left = topHeight(c.TitleBar()), 0
	}
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(top+12, 0, 0, left).Justify(ui.Start).AlignItems(ui.Center)
		for _, t := range toasts {
			toast := ui.ToastBase(c, t)
			toast.Root.Row().Height(36).MaxWidth(420).Padding(0, 12).Gap(8).AlignItems(ui.Center).
				Radius(theme.RadiusCard).Background(k.Popover).Border(1, k.Line2).Shadow(0, 10, 30, 0, k.Shadow).
				Tooltip(t.Title).Transition(ui.ElementTransition{Enter: &ui.Motion{}, Exit: &ui.Motion{}})
			failed := t.Type == "error"
			if failed {
				toast.Root.Padding(0, 6, 0, 12)
			}
			toast.Root.Children(func() {
				if failed {
					ui.Icon(c, a.icons.Failed).FontSize(theme.IconSize).TextColor(k.Fg)
				}
				theme.UI.On(ui.Text(c, t.Title)).TextColor(k.Fg).SingleLine().Shrink(1)
				if t.Action != "" {
					toast.ActionButton().Height(24).Padding(0, 8).Radius(6).Background(k.Fill).AlignItems(ui.Center).Children(func() {
						theme.Secondary.On(ui.Text(c, t.Action)).TextColor(k.Fg)
					})
				}
				if failed {
					close := toast.CloseButton().Label("关闭").Size(24, 24).Radius(6).AlignItems(ui.Center).Justify(ui.Center)
					if close.Hovered() {
						close.Background(k.Fill2)
					}
					close.Children(func() {
						ui.Icon(c, a.icons.Close).FontSize(14).TextColor(k.Fg3)
					})
				}
			})
		}
	})
}
