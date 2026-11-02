package shell

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/yangflow/hoplet/desktop/internal/convo"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// chat draws a Personal conversation as a chat: days apart, the
// companion's bubbles on the left and mine on the right, an avatar beside
// the last of a run, work folded to one line, and dots while it works.
func (a *App) chat(c *ui.Context, k theme.Colors, s proto.Session, items []convo.Item, running bool) {
	var day string
	for i, it := range items {
		if d := dayLabel(c.Now(), it.Time); d != day && (it.Kind == convo.KindMine || it.Kind == convo.KindText) {
			day = d
			ui.Row(c.Key("day:" + it.Key)).FillWidth().Justify(ui.Center).Children(func() {
				theme.Caption.On(ui.Text(c, d)).TextColor(k.Muted)
			})
		}
		last := i == len(items)-1 || items[i+1].Kind != it.Kind
		c := c.Key(it.Key)
		switch it.Kind {
		case convo.KindMine:
			ui.Row(c).AlignSelf(ui.End).Gap(8).AlignItems(ui.End).Children(func() {
				ui.Box(c).MaxWidth(0.70*personalColumn).Padding(9, 14).Radius(theme.RadiusComposer).Background(k.Bubble).Children(func() {
					theme.Reading.On(ui.Text(c, it.Text)).TextColor(k.Fg).Selectable()
				})
				a.meAvatar(c, k, last)
			})
		case convo.KindText:
			ui.Row(c).AlignSelf(ui.Start).Gap(8).AlignItems(ui.End).Children(func() {
				if last {
					a.avatarSized(c, k, s, 28)
				} else {
					ui.Box(c).Size(28, 28)
				}
				ui.Box(c).MaxWidth(0.78*personalColumn).Padding(9, 14).Radius(theme.RadiusComposer).Background(k.Raised).Border(1, k.Line).Children(func() {
					a.s.md.Theme = mdTheme(c, k)
					a.s.md.CodeBlock = a.codeBlock(k)
					if it.Streaming {
						markdown.Stream(c, it.Text, &a.s.md)
					} else {
						markdown.View(c, it.Text, &a.s.md)
					}
				})
			})
		case convo.KindWork:
			ui.Box(c).Padding(0, 0, 0, 36).Children(func() { a.work(c, k, s, it, true) })
		case convo.KindError:
			a.item(c, k, s, it)
		case convo.KindStopped:
			separator(c, k, "已停止 · "+clock(it.Time))
		}
	}
	if running && (len(items) == 0 || items[len(items)-1].Kind != convo.KindText) {
		// It is thinking or working: three dots in a small bubble.
		ui.Row(c.Key("typing")).AlignSelf(ui.Start).Gap(8).Children(func() {
			a.avatarSized(c, k, s, 28)
			ui.Box(c).Padding(9, 14).Radius(theme.RadiusComposer).Background(k.Raised).Border(1, k.Line).Label("正在输入").Children(func() {
				theme.Reading.On(ui.Text(c, "···")).TextColor(k.Muted)
			})
		})
	}
}

// meAvatar is my default avatar: a person on dark gray; an empty space of
// its size when show is false.
func (a *App) meAvatar(c *ui.Context, k theme.Colors, show bool) {
	b := ui.Box(c).Size(28, 28)
	if !show {
		return
	}
	b.Radius(14).Background(k.Fg3).Center().Children(func() {
		ui.Icon(c, a.icons.Me).FontSize(theme.IconSizeInRow).TextColor(k.OnAccent)
	})
}

// dayLabel names a day: 今天, 昨天, a weekday within the week, else the
// date.
func dayLabel(now, t time.Time) string {
	now, t = now.Local(), t.Local()
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	days := int(time.Date(y1, m1, d1, 0, 0, 0, 0, time.Local).Sub(time.Date(y2, m2, d2, 0, 0, 0, 0, time.Local)).Hours() / 24)
	switch {
	case days == 0:
		return "今天"
	case days == 1:
		return "昨天"
	case days < 7:
		return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[t.Weekday()]
	}
	return t.Format("1月2日")
}
