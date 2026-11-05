// Package theme holds the desktop design tokens and applies them to MyGO's
// theme. The values are generated from the design system's tokens.json
// and layout.json (go generate; see gen), the same files the canvas's
// tokens.css and layout.css come from: change them there, copy them here,
// regenerate.
package theme

//go:generate go run ./gen

import "github.com/egoist/mygo/ui"

// Apply makes the frame use the tokens of the system's appearance and
// returns its colors. Call it at the top of every frame: it starts from
// MyGO's theme for the current appearance, so it follows the system.
func Apply(c *ui.Context) Colors {
	t := *c.Theme()
	k := For(t.Dark)
	t.Background = ui.Transparent
	t.Surface, t.SurfaceHover, t.SurfacePressed = k.Fill, k.Fill2, k.Sel
	t.Border = k.Line2
	t.Text, t.TextMuted = k.Fg, k.Muted
	t.Accent, t.AccentText = k.Accent, k.OnAccent
	t.Warning, t.Success, t.Danger = k.Attn, k.Add, k.Del
	t.Selection, t.Focus = k.SelRow, k.AccentFg
	// Tooltips and toasts: the theme turned over, opaque. Left unset,
	// MyGO would color their text with the transparent Background.
	t.Inverse, t.InverseText = k.Fg, k.OnInverse
	t.Radius, t.Spacing, t.FontSize = RadiusControl, Space1, UI.Size()
	c.SetTheme(&t)
	return k
}

// Shadow is one of the design system's shadows.
type Shadow struct {
	X, Y, Blur, Spread float32
	Color              ui.Color
}

// On gives e the shadow.
func (s Shadow) On(e ui.Element) ui.Element {
	return e.Shadow(s.X, s.Y, s.Blur, s.Spread, s.Color)
}

// Style is one of the design system's text styles.
type Style int

type styleSpec struct {
	size, line, spacing float32
	weight              int
	mono                bool
}

// Size is the style's font size in points.
func (s Style) Size() float32 { return styles[s].size }

// On sets the style on e: size, line height, weight, letter spacing, and
// for the code styles the bundled monospace with ligatures off.
func (s Style) On(e ui.Element) ui.Element {
	st := styles[s]
	e = e.FontSize(st.size).LineHeight(st.line / st.size).FontWeight(st.weight)
	if st.spacing != 0 {
		e = e.LetterSpacing(st.spacing)
	}
	if st.mono {
		e = e.Font(Mono).FontFeatures("liga=0")
	}
	return e
}

// Layout is one of the design system's layouts: how an element lays out
// its children. Every assembly of smaller pieces into a larger one uses
// one, so spacing and alignment come from the same place everywhere.
type Layout struct {
	Column     bool      // children top to bottom; otherwise side by side
	Gap        float32   // between children
	Padding    []float32 // as Element.Padding takes it
	Align      ui.Align  // on the cross axis, when AlignSet
	AlignSet   bool
	Justify    ui.Align // on the main axis, when JustifySet
	JustifySet bool
	Fill       bool    // as wide as the parent; otherwise as wide as its content
	Wrap       bool    // children wrap onto new lines
	Height     float32 // a fixed height, when not zero
	MaxWidth   float32 // at most this wide, when not zero
	CenterSelf bool    // centered in its parent
	Grow       float32 // takes the room left, when not zero
}

// On lays e out as l says.
func (l Layout) On(e ui.Element) ui.Element {
	if l.Column {
		e = e.Column()
	} else if l.Gap != 0 || l.AlignSet || l.JustifySet || l.Wrap {
		e = e.Row()
	}
	if l.Gap != 0 {
		e = e.Gap(l.Gap)
	}
	if len(l.Padding) > 0 {
		e = e.Padding(l.Padding...)
	}
	if l.AlignSet {
		e = e.AlignItems(l.Align)
	}
	if l.JustifySet {
		e = e.Justify(l.Justify)
	}
	if l.Fill {
		e = e.FillWidth()
	}
	if l.Wrap {
		e = e.Wrap()
	}
	if l.Height != 0 {
		e = e.Height(l.Height)
	}
	if l.MaxWidth != 0 {
		e = e.MaxWidth(l.MaxWidth)
	}
	if l.CenterSelf {
		e = e.Margin(0, ui.Auto)
	}
	if l.Grow != 0 {
		e = e.Grow(l.Grow).Shrink(1)
	}
	return e
}

// H returns l at another of the design system's heights.
func (l Layout) H(h float32) Layout { l.Height = h; return l }

// P returns l with other padding, taken from the spacing tokens.
func (l Layout) P(v ...float32) Layout { l.Padding = v; return l }

// G returns l with another gap, taken from the spacing tokens.
func (l Layout) G(v float32) Layout { l.Gap = v; return l }

// Weight is the style's font weight.
func (s Style) Weight() int { return styles[s].weight }

// Line is the style's line height in points.
func (s Style) Line() float32 { return styles[s].line }
