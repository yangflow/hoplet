package shell

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
)

const (
	hintFor  = 4 * time.Second        // how long a hint stays
	fadeTime = 300 * time.Millisecond // how long it takes to go
)

// toast is the one note shown above the composer: a failure, which stays
// until closed, or a hint, which goes after a while. A new one replaces
// the old.
type toast struct {
	text   string
	failed bool
	retry  func()    // asks again; nil when it cannot
	until  time.Time // when a hint goes; zero until it is first drawn
}

// hint shows a passing note.
func (a *App) hint(text string) { a.toast = toast{text: text} }

// drawToast draws the note above composer, or near the bottom of the
// window when composer is nil, as when no composer is drawn.
func (a *App) drawToast(c *ui.Context, k theme.Colors, composer *ui.Element) {
	t := &a.toast
	if t.text == "" {
		return
	}
	now := c.Now()
	if !t.failed && t.until.IsZero() {
		t.until = now.Add(hintFor)
	}
	ui.Overlay(c, func() {
		e := ui.Row(c).Height(36).MaxWidth(420).Padding(0, 6, 0, 12).Gap(8).AlignItems(ui.Center).
			Radius(10).Background(k.Popover).Border(1, k.Line2).Shadow(0, 10, 30, 0, k.Shadow).Tooltip(t.text)
		if composer != nil {
			a.s.toastShown = true
			e.AttachTo(*composer, ui.AnchorTop, ui.AnchorBottom).Margin(0, 0, 12, 0)
		} else {
			e.Absolute().Bottom(24).Left(0).Right(0).Margin(0, ui.Auto)
		}
		if !t.failed {
			e.Padding(0, 12)
			switch left := t.until.Sub(now); {
			case e.Hovered():
				t.until = now.Add(hintFor) // stays while I read it
				c.After(hintFor)
			case left <= 0:
				*t = toast{}
				return
			case left < fadeTime:
				e.Opacity(float32(left) / float32(fadeTime))
				c.After(16 * time.Millisecond)
			default:
				c.After(left - fadeTime)
			}
		}
		e.Children(func() {
			if t.failed {
				ui.Icon(c, a.icons.Failed).FontSize(theme.IconSize).TextColor(k.Fg)
			}
			theme.UI.On(ui.Text(c, t.text)).TextColor(k.Fg).SingleLine().Shrink(1)
			if t.retry != nil {
				b := ui.Button(c, "重试").Height(24).Padding(0, 8).Radius(6).Border(0, ui.Transparent).Background(k.Fill)
				if b.Clicked() {
					retry := t.retry
					*t = toast{}
					retry()
				}
			}
			if t.failed && iconButton(c, k, a.icons.Close, "关闭", false).Size(24, 24).Clicked() {
				*t = toast{}
			}
		})
	})
}
