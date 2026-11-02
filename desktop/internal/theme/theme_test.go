package theme

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// wall approximates the desktop behind the glass in each appearance.
func wall(dark bool) ui.Color {
	if dark {
		return ui.Hex("#15181C")
	}
	return ui.Hex("#DCDFE3")
}

// over composites c over an opaque bg.
func over(c, bg ui.Color) ui.Color {
	a := float64(c.A) / 255
	mix := func(x, y uint8) uint8 { return uint8(math.Round(float64(x)*a + float64(y)*(1-a))) }
	return ui.RGB(mix(c.R, bg.R), mix(c.G, bg.G), mix(c.B, bg.B))
}

// contrast is the WCAG contrast ratio of two opaque colors.
func contrast(a, b ui.Color) float64 {
	lum := func(c ui.Color) float64 {
		ch := func(v uint8) float64 {
			x := float64(v) / 255
			if x <= 0.03928 {
				return x / 12.92
			}
			return math.Pow((x+0.055)/1.055, 2.4)
		}
		return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
	}
	la, lb := lum(a), lum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestTextContrast(t *testing.T) {
	for _, dark := range []bool{true, false} {
		k := For(dark)
		pane, side := over(k.Pane, wall(dark)), over(k.Side, wall(dark))
		for name, pair := range map[string][2]ui.Color{
			"fg on pane":         {k.Fg, pane},
			"fg2 on pane":        {k.Fg2, pane},
			"fg3 on side":        {k.Fg3, side},
			"muted on pane":      {k.Muted, pane},
			"muted on side":      {k.Muted, side},
			"accent-fg on side":  {k.AccentFg, side},
			"attn-fg on pane":    {k.AttnFg, pane},
			"on-accent":          {k.OnAccent, k.Accent},
			"on-attn":            {k.OnAttn, k.Attn},
			"fg on selected row": {k.Fg, over(k.SelRow, side)},
			"tooltip and toast":  {inverseText(dark), k.Fg},
		} {
			if r := contrast(pair[0], pair[1]); r < 4.5 {
				t.Errorf("dark=%v %s: %.2f, want ≥ 4.5", dark, name, r)
			}
		}
	}
}

func TestApply(t *testing.T) {
	var got ui.Theme
	var colors Colors
	tt := ui.NewTester(func(c *ui.Context) {
		colors = Apply(c)
		got = *c.Theme()
		UIStrong.On(ui.Text(c, "title"))
	}, 200, 100)
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		want := For(dark)
		if colors != want || got.Dark != dark || got.Accent != want.Accent || got.Text != want.Fg ||
			got.Background != ui.Transparent || got.Radius != RadiusControl || got.FontSize != 13 ||
			got.Inverse != want.Fg || got.InverseText.A != 255 {
			t.Errorf("dark=%v: theme %+v", dark, got)
		}
	}
	if !tt.HasText("title") {
		t.Fatalf("texts %q", tt.Texts())
	}
}

func TestRegisterFonts(t *testing.T) {
	if err := RegisterFonts(); err != nil {
		t.Fatal(err)
	}
}
