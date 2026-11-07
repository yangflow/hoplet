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
	top, left := a.titleHeight(c.TitleBar()), a.fit.side
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(top+theme.Space3, 0, 0, left).Justify(ui.Start).AlignItems(ui.Center)
		for _, t := range toasts {
			toast := ui.ToastBase(c, t)
			toast.Root.Row().MinHeight(theme.RowStep).MaxWidth(theme.ToastMax).Padding(0, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).
				Radius(theme.RadiusCard).Background(k.SurfaceFloat).Border(theme.LineWidth, k.Line2).
				Tooltip(t.Title).Transition(ui.ElementTransition{Animation: &theme.MotionBase, Enter: &ui.Motion{Y: -theme.MotionDrop}, Exit: &ui.Motion{Y: -theme.MotionLift}})
			k.ShadowPopover.On(toast.Root)
			failed := t.Type == "error"
			if failed {
				toast.Root.Padding(0, theme.Space1Half, 0, theme.Space3)
			}
			toast.Root.Children(func() {
				if failed {
					ui.Icon(c, a.icons.Failed).FontSize(theme.Icon).TextColor(k.Fg)
				}
				theme.UI.On(ui.Text(c, t.Title)).TextColor(k.Fg).SingleLine().Shrink(1)
				if t.Action != "" {
					toast.ActionButton().Height(theme.RowDense).Padding(0, theme.Space2).Radius(theme.RadiusRow).Background(k.Fill).AlignItems(ui.Center).Children(func() {
						theme.Secondary.On(ui.Text(c, t.Action)).TextColor(k.Fg)
					})
				}
				if failed {
					close := toast.CloseButton().Label("关闭").Size(theme.RowDense, theme.RowDense).Radius(theme.RadiusRow).AlignItems(ui.Center).Justify(ui.Center)
					if close.Hovered() {
						close.Background(k.Hover)
					}
					close.Children(func() {
						ui.Icon(c, a.icons.Close).FontSize(theme.IconRow).TextColor(k.Fg3)
					})
				}
			})
		}
	})
}
