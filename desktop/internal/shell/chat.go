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
//
// Bubbles of one speaker stand 2 apart, a change of speaker 12, anything
// else 6; a day line has 12 around it.
func (a *App) chat(c *ui.Context, k theme.Colors, s proto.Session, items []convo.Item, running bool) {
	const dayKind = convo.Kind(-2)
	prev := convo.Kind(-1)
	space := func(key string, next convo.Kind) {
		bubble := func(k convo.Kind) bool { return k == convo.KindMine || k == convo.KindText }
		g := float32(theme.Space1Half)
		switch {
		case prev == -1:
			g = 0
		case prev == next && bubble(next):
			g = theme.SpaceHalf
		case bubble(prev) && bubble(next), prev == dayKind, next == dayKind:
			g = theme.Space3
		}
		if g > 0 {
			ui.Box(c.Key("gap:" + key)).Height(g).Shrink(0)
		}
		prev = next
	}
	var day string
	who := a.mainSession(s) // a child session speaks as its companion
	for i, it := range items {
		if d := dayLabel(c.Now(), it.Time); d != day && dated(it.Kind) {
			day = d
			space("day:"+it.Key, dayKind)
			dayLine(c.Key("day:"+it.Key), k, d)
		}
		if it.Kind == convo.KindTurn || it.Kind == convo.KindApproval || it.Kind == convo.KindChanges {
			continue // not drawn in a chat
		}
		space(it.Key, it.Kind)
		last := i == len(items)-1 || items[i+1].Kind != it.Kind
		c := c.Key(it.Key)
		switch it.Kind {
		case convo.KindMine:
			a.myBubble(c, k, it, last)
		case convo.KindText:
			ui.Row(c).AlignSelf(ui.Start).MaxWidth(0.78 * theme.PersonalColumn).Gap(theme.Space2).AlignItems(ui.End).Children(func() {
				a.companionAvatar(c, k, who, last)
				ui.Box(c).Padding(theme.Space2, theme.Space3).Radius(theme.RadiusComposer, theme.RadiusComposer, theme.RadiusComposer, theme.RadiusRow).Background(k.Surface).Border(theme.LineWidth, k.Line).Children(func() {
					a.s.md.Theme = mdTheme(c, k)
					a.s.md.CodeBlock = a.codeBlock(k, it.Key)
					renderMarkdown(c, it, &a.s.md)
				})
			})
		case convo.KindWork:
			// The work trace lines up with the bubbles, past the avatar.
			ui.Row(c).Padding(0, 0, 0, theme.Row+theme.Space2).Height(theme.ControlXS).Gap(theme.Space3).AlignItems(ui.Center).Children(func() {
				w := it.Work
				line := summary(w)
				if w.Running {
					line += " · 运行中"
				}
				theme.UI.On(ui.Text(c, line)).TextColor(k.Muted).SingleLine().Shrink(1)
				if w.Failed > 0 {
					theme.Caption.On(ui.Text(c, "✕")).TextColor(k.Fg).FontWeight(theme.UIStrong.Weight())
				}
				if !w.Running {
					ui.Text(c, duration(w.End.Sub(w.Start))).Font(theme.Mono).FontSize(theme.Caption.Size()).TextColor(k.Faint)
				}
			})
		case convo.KindError:
			a.item(c, k, s, it)
		case convo.KindStopped:
			separator(c, k, "已停止 · "+clock(it.Time))
		case convo.KindEvent:
			a.eventRow(c, k, s, it)
		case convo.KindReport:
			a.reportCard(c, k, it, items)
		}
	}
	if running && (len(items) == 0 || items[len(items)-1].Kind != convo.KindText) {
		// It is thinking or working: three dots in a small bubble.
		space("typing", convo.KindText)
		ui.Row(c.Key("typing")).AlignSelf(ui.Start).Gap(theme.Space2).AlignItems(ui.End).Children(func() {
			a.companionAvatar(c, k, who, true)
			ui.Box(c).Padding(theme.Space2, theme.Space3).Radius(theme.RadiusComposer, theme.RadiusComposer, theme.RadiusComposer, theme.RadiusRow).Background(k.Surface).Border(theme.LineWidth, k.Line).Label("正在输入").Children(func() {
				theme.Reading.On(ui.Text(c, "···")).TextColor(k.Muted)
			})
		})
	}
}

// dated reports whether an item of this kind can start a new day.
func dated(k convo.Kind) bool {
	return k == convo.KindMine || k == convo.KindText || k == convo.KindEvent || k == convo.KindReport
}

// dayLine separates days: a thin line with the day in its middle.
func dayLine(c *ui.Context, k theme.Colors, label string) {
	ui.Row(c).FillWidth().Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Height(theme.LineWidth).Grow(1).Background(k.Line)
		theme.Caption.On(ui.Text(c, label)).TextColor(k.Faint)
		ui.Box(c).Height(theme.LineWidth).Grow(1).Background(k.Line)
	})
}

// myBubble is a message of mine in a chat: a gray bubble on the right with
// my avatar beside the last of a run, its time and copy on hover left of
// it.
func (a *App) myBubble(c *ui.Context, k theme.Colors, it convo.Item, last bool) {
	r := ui.Row(c).AlignSelf(ui.End).MaxWidth(0.75 * theme.PersonalColumn).Gap(theme.Space2).AlignItems(ui.End)
	hovered := r.Hovered()
	r.Children(func() {
		ui.Box(c).Shrink(1).Children(func() {
			ui.Box(c).Padding(theme.Space2, theme.Space3).Radius(theme.RadiusComposer).Background(k.Bubble).Children(func() {
				theme.Reading.On(ui.Text(c, it.Text)).TextColor(k.Fg).Selectable()
			})
			if hovered {
				a.mineActions(c, k, it)
			}
		})
		a.meAvatar(c, k, last)
	})
}

// companionAvatar is the companion's 28-point avatar beside the last
// bubble of a run; an empty space of its size otherwise.
func (a *App) companionAvatar(c *ui.Context, k theme.Colors, s proto.Session, show bool) {
	b := ui.Box(c).Size(theme.Row, theme.Row)
	if !show {
		return
	}
	b.Radius(theme.RadiusPill).Background(k.Line2).Center().Children(func() {
		theme.UIStrong.On(ui.Text(c, initial(name(s)))).TextColor(k.Fg2)
	})
}

// meAvatar is my default avatar: a person on fg2 (dark gray in the light
// theme, light gray in the dark one); an empty space of its size when show
// is false.
func (a *App) meAvatar(c *ui.Context, k theme.Colors, show bool) {
	b := ui.Box(c).Size(theme.Row, theme.Row)
	if !show {
		return
	}
	b.Radius(theme.RadiusPill).Background(k.Fg2).Center().Label("我").Children(func() {
		ui.Icon(c, a.icons.Me).FontSize(theme.IconRow).TextColor(c.Theme().InverseText)
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
