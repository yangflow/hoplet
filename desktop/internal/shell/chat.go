package shell

import (
	"time"

	"github.com/egoist/mygo/ui"

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
			dayLine(c.Key("day:"+it.Key), k, d)
		}
		last := i == len(items)-1 || items[i+1].Kind != it.Kind
		c := c.Key(it.Key)
		switch it.Kind {
		case convo.KindMine:
			a.myBubble(c, k, it, last)
		case convo.KindText:
			ui.Row(c).AlignSelf(ui.Start).MaxWidth(0.78 * personalColumn).Gap(10).AlignItems(ui.End).Children(func() {
				a.companionAvatar(c, k, s, last)
				ui.Box(c).Padding(9, 14).Radius(18, 18, 18, 6).Background(k.Bg).Border(1, k.Line).Children(func() {
					a.s.md.Theme = mdTheme(c, k)
					a.s.md.CodeBlock = a.codeBlock(k)
					renderMarkdown(c, it, &a.s.md)
				})
			})
		case convo.KindWork:
			// The work trace lines up with the bubbles, past the avatar.
			ui.Row(c).Padding(0, 0, 0, 38).Height(22).Gap(12).AlignItems(ui.Center).Children(func() {
				w := it.Work
				theme.UI.On(ui.Text(c, summary(w))).TextColor(k.Muted).SingleLine().Shrink(1)
				if w.Failed > 0 {
					theme.Caption.On(ui.Text(c, "✕")).TextColor(k.Fg).FontWeight(600)
				}
				if !w.Running {
					ui.Text(c, duration(w.End.Sub(w.Start))).Font(theme.Mono).FontSize(11).TextColor(k.Faint)
				}
			})
		case convo.KindError:
			a.item(c, k, s, it)
		case convo.KindStopped:
			separator(c, k, "已停止 · "+clock(it.Time))
		}
	}
	if running && (len(items) == 0 || items[len(items)-1].Kind != convo.KindText) {
		// It is thinking or working: three dots in a small bubble.
		ui.Row(c.Key("typing")).AlignSelf(ui.Start).Gap(10).AlignItems(ui.End).Children(func() {
			a.companionAvatar(c, k, s, true)
			ui.Box(c).Padding(9, 14).Radius(18, 18, 18, 6).Background(k.Bg).Border(1, k.Line).Label("正在输入").Children(func() {
				theme.Reading.On(ui.Text(c, "···")).TextColor(k.Muted)
			})
		})
	}
}

// dayLine separates days: a thin line with the day in its middle.
func dayLine(c *ui.Context, k theme.Colors, label string) {
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Height(1).Grow(1).Background(k.Line)
		theme.Caption.On(ui.Text(c, label)).TextColor(k.Faint)
		ui.Box(c).Height(1).Grow(1).Background(k.Line)
	})
}

// myBubble is a message of mine in a chat: a gray bubble on the right with
// my avatar beside the last of a run, its time and copy on hover.
func (a *App) myBubble(c *ui.Context, k theme.Colors, it convo.Item, last bool) {
	r := ui.Row(c).AlignSelf(ui.End).MaxWidth(0.75 * personalColumn).Gap(10).AlignItems(ui.End)
	hovered := r.Hovered()
	r.Children(func() {
		ui.Column(c).AlignItems(ui.End).Gap(4).Shrink(1).Children(func() {
			ui.Box(c).Padding(9, 14).Radius(18, 18, 6, 18).Background(k.Bubble).Children(func() {
				theme.Reading.On(ui.Text(c, it.Text)).TextColor(k.Fg).Selectable()
			})
			row := ui.Row(c).Height(20).Gap(2).AlignItems(ui.Center)
			if !hovered {
				row.Opacity(0)
			}
			row.Children(func() {
				theme.Caption.On(ui.Text(c, clock(it.Time))).TextColor(k.Muted)
				a.copyButton(c, k, it.Key, it.Text, "复制")
			})
		})
		a.meAvatar(c, k, last)
	})
}

// companionAvatar is the companion's 28-point avatar beside the last
// bubble of a run; an empty space of its size otherwise.
func (a *App) companionAvatar(c *ui.Context, k theme.Colors, s proto.Session, show bool) {
	b := ui.Box(c).Size(28, 28)
	if !show {
		return
	}
	b.Radius(14).Background(k.Line2).Center().Children(func() {
		theme.UIStrong.On(ui.Text(c, initial(name(s)))).TextColor(k.Fg2)
	})
}

// meAvatar is my default avatar: a person on fg2 (dark gray in the light
// theme, light gray in the dark one); an empty space of its size when show
// is false.
func (a *App) meAvatar(c *ui.Context, k theme.Colors, show bool) {
	b := ui.Box(c).Size(28, 28)
	if !show {
		return
	}
	b.Radius(14).Background(k.Fg2).Center().Label("我").Children(func() {
		ui.Icon(c, a.icons.Me).FontSize(theme.IconSizeInRow).TextColor(c.Theme().InverseText)
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
