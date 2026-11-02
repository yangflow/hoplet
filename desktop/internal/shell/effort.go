package shell

import (
	"math"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// The effort slider's motion: the knob eases to a level fast, then slow;
// the label's letters rise in one after another; 极限 bursts as it comes
// and fades as it goes.
var (
	knobMove  = ui.Animation{Duration: 500 * time.Millisecond, Ease: ui.CubicBezier(0.19, 1, 0.22, 1)}
	labelIn   = ui.Animation{Duration: 320 * time.Millisecond, Ease: ui.CubicBezier(0.25, 0.1, 0.25, 1)}
	labelOut  = ui.Animation{Duration: 355 * time.Millisecond, Ease: ui.CubicBezier(0.25, 0.1, 0.25, 1)}
	ultraFade = ui.Animation{Duration: 300 * time.Millisecond}
)

const (
	knobInset     = 16                     // the dots' inset from the track's ends: half the knob
	letterStagger = 15 * time.Millisecond  // between the letters of a label
	burstTime     = 650 * time.Millisecond // the ring of pixels as 极限 comes
	glintTime     = 550 * time.Millisecond // the flash as 极限 comes
	ultraPixels   = 28                     // twinkling in the 极限 track
)

// effortLabel is the effort popover's big label: the level's name, 默认
// before one is chosen; in 极限 the accent's. A new label rises in letter
// by letter while the old one goes up and fades.
func (a *App) effortLabel(c *ui.Context, k theme.Colors, e proto.Effort) {
	name := effortName(e)
	color := k.Fg
	if e == proto.EffortUltra {
		color = k.AccentFg
	}
	ui.Row(c.Key("effort:" + name)).Height(22).AlignItems(ui.Center).
		Transition(ui.ElementTransition{Animation: &labelOut, Exit: &ui.Motion{Y: -3.4}}).Children(func() {
		for i, r := range []rune(name) {
			in := labelIn
			in.Delay = letterStagger * time.Duration(i)
			theme.Reading.On(ui.Text(c.Key(i), string(r))).FontWeight(600).TextColor(color).
				Transition(ui.ElementTransition{Animation: &in, Enter: &ui.Motion{Y: 3}})
		}
	})
}

// effortSlider draws the levels as a pill track filled up to the knob.
// Before a level is chosen it shows only the dots. The knob eases to a
// level, and follows the pointer while dragged; 极限 turns the track into
// a deep gradient with pixels twinkling, brighter near the knob.
func (a *App) effortSlider(c *ui.Context, k theme.Colors, levels []proto.Effort, cur proto.Effort, set func(proto.Effort)) {
	const pad = knobInset
	n := len(levels)
	chosen := slices.Index(levels, cur)
	v := float64(n / 2) // where the keys start from before a level is chosen
	if chosen >= 0 {
		v = float64(chosen)
	}
	s := ui.SliderBase(c, &v, 0, float64(n-1)).Step(1).Label("推理强度").FillWidth().Height(28).PaddingX(pad)
	if s.Changed() {
		e := levels[int(v)]
		if e == proto.EffortUltra && cur != e {
			a.s.ultraAt = c.Now()
		}
		set(e)
	}
	var knob float32
	if _, _, held := s.Dragged(); held && chosen >= 0 {
		x, _, _ := s.PointerPosition()
		w := s.Bounds().W - 2*pad
		knob = s.Jump("knob", min(max((x-pad)/max(w, 1)*float32(n-1), 0), float32(n-1)))
	} else if chosen >= 0 {
		knob = s.AnimateTo("knob", float32(chosen), knobMove)
	}
	ultra := float32(0)
	if cur == proto.EffortUltra {
		ultra = 1
	}
	g := s.AnimateTo("ultra", ultra, ultraFade)
	still := c.Preferences().ReduceMotion
	since := c.Now().Sub(a.s.ultraAt)
	focus := s.FocusVisible()
	s.FocusRing(false).Draw(func(p *ui.Painter, r ui.Rect) {
		round := r.H / 2
		x := func(at float32) float32 {
			if c.Direction() == ui.RightToLeft {
				at = 1 - at
			}
			return r.X + pad + (r.W-2*pad)*at
		}
		p.Fill(r, k.Sel, round)
		kx := x(knob / float32(n-1))
		if chosen >= 0 {
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: max(kx-r.X, r.H), H: r.H}, k.Accent, round)
		}
		moving := false
		if g > 0 {
			p.FillGradient(r, ui.LinearGradient{From: k.UltraFrom.Alpha(g), To: k.UltraTo.Alpha(g), Angle: 90, Oklab: true}, round)
			t := p.Now().Sub(a.s.ultraAt).Seconds()
			for i := range ultraPixels {
				px, py := r.X+8+(r.W-16)*hash(i, 1), r.Y+5+(r.H-10)*hash(i, 2)
				b := 0.3 + 0.7*(px-r.X)/r.W // brighter toward the knob
				if !still {
					b *= 0.35 + 0.65*twinkle(i, t)
					moving = true
				}
				size := float32(1.5) + hash(i, 3)
				p.Fill(ui.Rect{X: px, Y: py, W: size, H: size}, ui.RGBA(255, 255, 255, b*g), 0.5)
			}
		}
		cy := r.Y + r.H/2
		for i := range n {
			dot := k.Faint
			if chosen >= 0 && float32(i) <= knob {
				dot = k.OnAccent.Alpha(0.55)
			}
			p.Fill(ui.Rect{X: x(float32(i)/float32(n-1)) - 2.5, Y: cy - 2.5, W: 5, H: 5}, dot.Alpha(1-g), 2.5)
		}
		if chosen >= 0 {
			kr := ui.Rect{X: kx - 16, Y: cy - 16, W: 32, H: 32} // past the track by 2 above and below
			if g > 0 {
				p.Shadow(kr, 16, 0, 0, 12, 2, k.Accent.Alpha(0.45*g))
			}
			if cur == proto.EffortUltra && !still && since < burstTime {
				burst(p, kx, cy, float32(since)/float32(burstTime))
				if f := float32(since) / float32(glintTime); f < 1 {
					p.Shadow(kr, 16, 0, 0, 24, 6, k.Accent.Alpha(0.6*float32(math.Pow(float64(1-f), 1.6))))
				}
				moving = true
			}
			p.Shadow(kr, 16, 0, 1, 3, 0, k.Shadow)
			p.Fill(kr, k.Popover, 16) // raised is see-through: the track would show
			p.Fill(kr, k.Raised, 16)
		}
		if focus {
			p.FocusRing(r, [4]float32{round, round, round, round})
		}
		if moving {
			p.AnimationFrame()
		}
	})
}

// burst draws the ring of pixels flying out of the knob centered on (cx,
// cy), f of the way through.
func burst(p *ui.Painter, cx, cy, f float32) {
	const n = 12
	d := 16 + 26*(1-(1-f)*(1-f))
	for i := range n {
		angle := 2 * math.Pi * (float64(i) + float64(hash(i, 4))*0.5) / n
		x, y := cx+d*float32(math.Cos(angle)), cy+d*float32(math.Sin(angle))
		p.Fill(ui.Rect{X: x - 1, Y: y - 1, W: 2, H: 2}, ui.RGBA(255, 255, 255, 1-f), 0.5)
	}
}

// hash is a number from 0 to 1 that depends only on its arguments.
func hash(a, b int) float32 {
	v := math.Sin(float64(a)*12.9898+float64(b)*78.233) * 43758.5453
	return float32(v - math.Floor(v))
}

// twinkle is how lit pixel i is t seconds in, from 0 to 1, at a pace of
// its own.
func twinkle(i int, t float64) float32 {
	speed := 1.2 + 1.6*float64(hash(i, 5))
	return float32(0.5 + 0.5*math.Sin(t*speed*2*math.Pi/1.6+float64(hash(i, 6))*2*math.Pi))
}

// modelPage is the effort popover turned to the models: ‹ back, the
// harnesses as icon tabs on the new-session page (one harness in a
// session, which keeps it), a search, and the models of the harness;
// choosing one applies it and closes the popover.
func (a *App) modelPage(c *ui.Context, k theme.Colors, st runSettings, cur *proto.ModelInfo, hs []proto.HarnessInfo, set settingsSink) {
	var tabs []proto.HarnessInfo
	for _, h := range hs {
		if len(h.Models) > 0 && (len(hs) > 1 || h.Capabilities.SetModel) {
			tabs = append(tabs, h)
		}
	}
	if len(tabs) == 0 {
		a.s.modelOpen = false
		return
	}
	tab := slices.IndexFunc(tabs, func(h proto.HarnessInfo) bool { return h.Kind == a.s.modelTab })
	if tab < 0 {
		tab = max(slices.IndexFunc(tabs, func(h proto.HarnessInfo) bool { return h.Kind == st.harness }), 0)
	}
	ui.Row(c).Height(32).Padding(2, 2, 0, 2).Gap(6).AlignItems(ui.Center).Children(func() {
		if iconButton(c, k, a.icons.Back, "返回 ⎋", false).Clicked() {
			a.s.modelOpen = false
		}
		ui.Spacer(c)
		if len(tabs) > 1 {
			was := tab
			seg := ui.SegmentedBase(c, &tab, len(tabs))
			seg.Track.Label("harness").Padding(2).Gap(2).Radius(8).Background(k.Fill).Children(func() {
				for i, h := range tabs {
					s := seg.Segment(i).Size(32, 24).Radius(6).AlignItems(ui.Center).Justify(ui.Center).
						Label(harnessName(h.Kind)).Tooltip(harnessName(h.Kind))
					if i == tab {
						s.Background(k.Raised).Shadow(0, 0.5, 1.5, 0, k.Shadow)
					}
					s.Children(func() { a.harnessIcon(c, k, h.Kind, 15) })
				}
			})
			if tab != was {
				a.s.modelPick = -1
			}
		} else {
			theme.UIStrong.On(ui.Text(c, harnessName(tabs[tab].Kind))).TextColor(k.Fg)
		}
		ui.Spacer(c)
		ui.Box(c).Size(28, 28)
	})
	a.s.modelTab = tabs[tab].Kind
	h := tabs[tab]
	q := strings.ToLower(strings.TrimSpace(a.s.modelQuery))
	var models []proto.ModelInfo
	for _, m := range h.Models {
		if q == "" || strings.Contains(strings.ToLower(m.Name), q) {
			models = append(models, m)
		}
	}
	current := func(m proto.ModelInfo) bool {
		return h.Kind == st.harness && cur != nil && cur.ID == m.ID && cur.Provider == m.Provider
	}
	pick := a.s.modelPick
	if pick < 0 || pick >= len(models) {
		pick = max(slices.IndexFunc(models, current), 0)
	}
	choose := func(m proto.ModelInfo) {
		set.model(h.Kind, m)
		a.s.modelOpen, a.s.effortOpen = false, false
	}
	ui.Row(c).Height(28).Margin(0, 2).Padding(0, 8).Gap(6).AlignItems(ui.Center).Radius(theme.RadiusControl).Background(k.Fill).Children(func() {
		ui.Icon(c, a.icons.Search).FontSize(14).TextColor(k.Muted)
		in := ui.TextInput(c, &a.s.modelQuery).Placeholder("搜索模型").Label("搜索模型").AutoFocus().Grow(1).
			FontSize(12.5).Background(ui.Transparent).Border(0, ui.Transparent).Padding(0)
		in.OnTextKey(func(key ui.TextKey) bool {
			switch {
			case key.Mods != 0:
				return false
			case key.Key == ui.KeyUp:
				a.s.modelPick = max(pick-1, 0)
			case key.Key == ui.KeyDown:
				a.s.modelPick = min(pick+1, len(models)-1)
			case key.Key == ui.KeyEnter:
				if pick < len(models) {
					choose(models[pick])
				}
			case key.Key == ui.KeyEscape:
				a.s.modelOpen = false
			default:
				return false
			}
			return true
		})
	})
	if len(models) == 0 {
		ui.Text(c, "没有匹配“"+a.s.modelQuery+"”的模型").FontSize(12.5).TextColor(k.Muted).Padding(6, 8, 8, 8)
		return
	}
	ui.Scroll(c.Key(h.Kind)).MaxHeight(8 * theme.RowHeight).Children(func() {
		for i, m := range models {
			r := menuItem(c.Key(i), k, i == pick).Height(theme.RowHeight).Padding(0, 8).Label(m.Name).Tooltip(m.Description)
			if r.Clicked() {
				choose(m)
			}
			r.Children(func() {
				a.harnessIcon(c, k, h.Kind, theme.IconSizeInRow)
				theme.UI.On(ui.Text(c, m.Name)).TextColor(k.Fg).SingleLine().Grow(1).Shrink(1)
				if m.Default && m.ID != "default" { // Claude names its default entry itself
					theme.Caption.On(ui.Text(c, "默认")).TextColor(k.Muted)
				}
				check(c, k, a, current(m))
			})
		}
	})
}
