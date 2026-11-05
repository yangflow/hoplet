package theme

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

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

// TestContrastAudit checks every text color against every background
// that carries text, as the design system's audit section composes them:
// regions and surfaces are layered over the window material's reference
// color, so a color change that breaks a target fails here.
func TestContrastAudit(t *testing.T) {
	src, err := os.ReadFile("tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Color struct {
			Tokens []struct {
				Name  string          `json:"name"`
				Value json.RawMessage `json:"value"`
			} `json:"tokens"`
		} `json:"color"`
		Audit struct {
			Material    map[string]string `json:"material"`
			Backgrounds []struct {
				Name  string   `json:"name"`
				Stack []string `json:"stack"`
			} `json:"backgrounds"`
			Targets map[string]float64 `json:"targets"`
			Pairs   []struct {
				Fg, Bg string
				Target float64
			} `json:"pairs"`
		} `json:"audit"`
	}
	if err := json.Unmarshal(src, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Audit.Targets) == 0 || len(doc.Audit.Backgrounds) == 0 {
		t.Fatal("tokens.json has no audit section")
	}
	for _, theme := range []string{"dark", "light"} {
		colors := map[string]ui.Color{}
		for _, tk := range doc.Color.Tokens {
			var v string
			if json.Unmarshal(tk.Value, &v) != nil {
				var m map[string]string
				if json.Unmarshal(tk.Value, &m) != nil {
					t.Fatalf("%s: value %s", tk.Name, tk.Value)
				}
				v = m[theme]
			}
			if c, ok := parseColor(v); ok {
				colors[tk.Name] = c
			}
		}
		material, ok := parseColor(doc.Audit.Material[theme])
		if !ok {
			t.Fatalf("%s: no material reference", theme)
		}
		for role, target := range doc.Audit.Targets {
			fg, ok := colors[role]
			if !ok {
				t.Fatalf("%s: no color %q", theme, role)
			}
			for _, bg := range doc.Audit.Backgrounds {
				base := material
				for _, layer := range bg.Stack {
					c, ok := colors[layer]
					if !ok {
						t.Fatalf("%s: no color %q", theme, layer)
					}
					base = over(c, base)
				}
				if r := contrast(fg, base); r < target {
					t.Errorf("%s: %s on %s is %.2f, want ≥ %.1f", theme, role, bg.Name, r, target)
				}
			}
		}
		for _, p := range doc.Audit.Pairs {
			if r := contrast(colors[p.Fg], colors[p.Bg]); r < p.Target {
				t.Errorf("%s: %s on %s is %.2f, want ≥ %.1f", theme, p.Fg, p.Bg, r, p.Target)
			}
		}
	}
}

// parseColor reads a token's #rrggbb or rgba(r,g,b,a) value; gradients and
// other values report false.
func parseColor(v string) (ui.Color, bool) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "#") && len(v) == 7 {
		n, err := strconv.ParseUint(v[1:], 16, 32)
		if err != nil {
			return ui.Color{}, false
		}
		return ui.RGB(uint8(n>>16), uint8(n>>8), uint8(n)), true
	}
	if !strings.HasPrefix(v, "rgba(") || !strings.HasSuffix(v, ")") {
		return ui.Color{}, false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(v, "rgba("), ")"), ",")
	if len(parts) != 4 {
		return ui.Color{}, false
	}
	var f [4]float64
	for i, p := range parts {
		x, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return ui.Color{}, false
		}
		f[i] = x
	}
	return ui.RGBA(uint8(f[0]), uint8(f[1]), uint8(f[2]), float32(f[3])), true
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
