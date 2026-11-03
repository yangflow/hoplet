package shell

import (
	"fmt"
	"math"
	"strconv"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// contextRing shows how full the context window is: a 16-point ring in a
// 28-point spot of the composer's controls, its arc from twelve o'clock
// clockwise to the part used; fg3 below 75%, attn from 75%, del from 90%.
// The pointer on it says the numbers. It shows once the harness has said
// both the tokens used and the window's size.
func (a *App) contextRing(c *ui.Context, k theme.Colors, u proto.ContextUsage) {
	if u.Used <= 0 || u.Window <= 0 {
		return
	}
	f := min(float32(u.Used)/float32(u.Window), 1)
	color := k.Fg3
	switch {
	case f >= 0.9:
		color = k.Del
	case f >= 0.75:
		color = k.Attn
	}
	tip := contextTip(u)
	b := ui.Box(c).Size(theme.RowHeight, theme.RowHeight).Radius(theme.RadiusControl).Label(tip).Tooltip(tip)
	if b.Hovered() {
		b.Background(k.Fill2)
	}
	b.Draw(func(p *ui.Painter, r ui.Rect) {
		const radius, width = 6, 2
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		p.StrokePath(ringPath(cx, cy, radius), width, k.Line2)
		if f > 0 {
			p.StrokePath(ringPath(cx, cy, radius).Trim(0, f), width, color)
		}
	})
}

// ringPath is a circle starting at twelve o'clock, going clockwise.
func ringPath(cx, cy, r float32) *ui.Path {
	const k = 0.5522847498 // a quarter circle's control point distance
	var p ui.Path
	p.MoveTo(cx, cy-r)
	p.CubeTo(cx+k*r, cy-r, cx+r, cy-k*r, cx+r, cy)
	p.CubeTo(cx+r, cy+k*r, cx+k*r, cy+r, cx, cy+r)
	p.CubeTo(cx-k*r, cy+r, cx-r, cy+k*r, cx-r, cy)
	p.CubeTo(cx-r, cy-k*r, cx-k*r, cy-r, cx, cy-r)
	return &p
}

// contextTip is the ring's tip: 上下文已用 N% · used / window tokens.
func contextTip(u proto.ContextUsage) string {
	pct := int(math.Round(float64(u.Used) * 100 / float64(u.Window)))
	return fmt.Sprintf("上下文已用 %d%% · %s / %s tokens", min(pct, 100), tokens(u.Used), tokens(u.Window))
}

// tokens writes a token count short: 84K, 1.2M; one decimal at most, none
// when it is whole.
func tokens(n int64) string {
	short := func(v float64, unit string) string {
		return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) + unit
	}
	switch {
	case n >= 1_000_000:
		return short(float64(n)/1_000_000, "M")
	case n >= 1_000:
		return short(float64(n)/1_000, "K")
	}
	return strconv.FormatInt(n, 10)
}
