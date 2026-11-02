// Package theme holds the desktop design tokens (docs/design/0014) and
// applies them to MyGO's theme.
package theme

import "github.com/egoist/mygo/ui"

// Colors are the semantic colors of one appearance. Surfaces are
// translucent: the window's material shows through them.
type Colors struct {
	Pane      ui.Color // content area
	Side      ui.Color // sidebar and the right panel's column
	SideLine  ui.Color // the sidebar's edge
	Glass     ui.Color // composer and controls floating over content
	GlassLine ui.Color // the composer's hairline
	Raised    ui.Color // selected segment, tab or mode
	Fill      ui.Color // control fill
	Fill2     ui.Color // control fill on hover; segmented track
	Sel       ui.Color // current menu item or option
	SelRow    ui.Color // selected session row
	Line      ui.Color // separators, card edges
	Line2     ui.Color // control edges

	Fg    ui.Color // body text
	Fg2   ui.Color // secondary body text
	Fg3   ui.Color // icons, control labels, tool rows
	Muted ui.Color // paths, times, counts
	Faint ui.Color // placeholders, disabled; never needed information

	Accent   ui.Color // fill of buttons, switches, send
	OnAccent ui.Color // text on Accent
	AccentFg ui.Color // links, focus ring, running, unread

	Attn     ui.Color // needs you: dot, badge, approval outline
	OnAttn   ui.Color // text on Attn
	AttnFg   ui.Color // needs-you text
	AttnLine ui.Color // the composer's outline during an approval

	Add   ui.Color // diff additions
	Del   ui.Color // diff deletions
	AddBg ui.Color
	DelBg ui.Color

	Bubble ui.Color // my message bubble
	Shadow ui.Color // popover shadow
}

// For returns the colors of the dark or the light appearance.
func For(dark bool) Colors {
	if dark {
		return Colors{
			Pane: ui.RGBA(14, 16, 20, .42), Side: ui.RGBA(255, 255, 255, .05), SideLine: ui.RGBA(255, 255, 255, .07),
			Glass: ui.RGBA(255, 255, 255, .05), GlassLine: ui.RGBA(255, 255, 255, .12),
			Raised: ui.RGBA(255, 255, 255, .16), Fill: ui.RGBA(255, 255, 255, .06), Fill2: ui.RGBA(255, 255, 255, .10),
			Sel: ui.RGBA(255, 255, 255, .10), SelRow: ui.RGBA(255, 255, 255, .09),
			Line: ui.RGBA(255, 255, 255, .08), Line2: ui.RGBA(255, 255, 255, .14),
			Fg: ui.Hex("#F4F4F5"), Fg2: ui.Hex("#D3D6DB"), Fg3: ui.Hex("#B0B4BB"), Muted: ui.Hex("#8E939B"), Faint: ui.Hex("#666B73"),
			Accent: ui.Hex("#3CC4C9"), OnAccent: ui.Hex("#0C1A1C"), AccentFg: ui.Hex("#3CC4C9"),
			Attn: ui.Hex("#F0A35B"), OnAttn: ui.Hex("#1E1408"), AttnFg: ui.Hex("#F5BB82"), AttnLine: ui.RGBA(240, 163, 91, .40),
			Add: ui.Hex("#7BC86C"), Del: ui.Hex("#F0758A"), AddBg: ui.RGBA(123, 200, 108, .12), DelBg: ui.RGBA(240, 117, 138, .11),
			Bubble: ui.RGBA(255, 255, 255, .10), Shadow: ui.RGBA(0, 0, 0, .45),
		}
	}
	return Colors{
		Pane: ui.RGBA(255, 255, 255, .10), Side: ui.RGBA(236, 238, 241, .45), SideLine: ui.RGBA(0, 0, 0, .06),
		Glass: ui.RGBA(120, 128, 140, .10), GlassLine: ui.Transparent,
		Raised: ui.RGBA(255, 255, 255, .88), Fill: ui.RGBA(0, 0, 0, .04), Fill2: ui.RGBA(0, 0, 0, .06),
		Sel: ui.RGBA(0, 0, 0, .055), SelRow: ui.RGBA(0, 0, 0, .06),
		Line: ui.RGBA(0, 0, 0, .06), Line2: ui.RGBA(0, 0, 0, .10),
		Fg: ui.Hex("#23272D"), Fg2: ui.Hex("#383B40"), Fg3: ui.Hex("#53575D"), Muted: ui.Hex("#5E6268"), Faint: ui.Hex("#A0A4AA"),
		Accent: ui.Hex("#066A70"), OnAccent: ui.Hex("#FFFFFF"), AccentFg: ui.Hex("#066A70"),
		Attn: ui.Hex("#A85C14"), OnAttn: ui.Hex("#FFFFFF"), AttnFg: ui.Hex("#8A4B0F"), AttnLine: ui.RGBA(217, 130, 43, .40),
		Add: ui.Hex("#2E7031"), Del: ui.Hex("#B02A55"), AddBg: ui.RGBA(46, 112, 49, .10), DelBg: ui.RGBA(176, 42, 85, .08),
		Bubble: ui.RGBA(120, 128, 140, .14), Shadow: ui.RGBA(0, 0, 0, .12),
	}
}

// inverseText is the text on tooltips and toasts, whose fill is Fg.
func inverseText(dark bool) ui.Color {
	if dark {
		return ui.Hex("#15181C")
	}
	return ui.Hex("#FFFFFF")
}

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
	t.Inverse, t.InverseText = k.Fg, inverseText(t.Dark)
	t.Radius, t.Spacing, t.FontSize = RadiusControl, 4, UI.Size()
	c.SetTheme(&t)
	return k
}

// Radii, in points.
const (
	RadiusXS       = 4   // inline code, key caps
	RadiusRow      = 6   // sidebar rows, menu items
	RadiusControl  = 7   // buttons, tabs, icon buttons
	RadiusCard     = 10  // code blocks, popovers, change cards
	RadiusPanel    = 14  // inbox cards, command palette
	RadiusComposer = 18  // composer, bubbles
	RadiusPill     = 999 // effort pill, send button
)

// Fixed sizes, in points.
const (
	RowHeight     = 28  // session rows, buttons, icon buttons
	RowDense      = 24  // tool rows, file tree rows
	TitleBar      = 44  // title bars
	SideTop       = 52  // the sidebar's first row, beside the traffic lights
	SidebarWidth  = 260 // default; draggable 220–360
	IconSize      = 16  // icons in buttons and title bars
	IconSizeInRow = 14
	IconSizeSmall = 12
)

// Style is one step of the type scale. Only these sizes and the weights
// 400, 500 and 600 are used.
type Style int

// The type scale.
const (
	Display   Style = iota // 22/28 600: new-session page, empty states
	Title                  // 15/20 600: section titles, card numbers
	Reading                // 13.5/22: messages, composer
	UI                     // 13/18: sidebar, menus, buttons, tool rows
	UIStrong               // 13/18 600: title bar title, unread rows, companion names
	Secondary              // 12/16: paths, previews
	Caption                // 11/14: times, counts, shortcuts
	Label                  // 11/14 600: group titles
	Code                   // 12/19 monospace
)

// metrics returns the style's size and line height in points and its
// weight.
func (s Style) metrics() (size, line float32, weight int) {
	switch s {
	case Display:
		return 22, 28, 600
	case Title:
		return 15, 20, 600
	case Reading:
		return 13.5, 22, 400
	case UIStrong:
		return 13, 18, 600
	case Secondary:
		return 12, 16, 400
	case Caption:
		return 11, 14, 400
	case Label:
		return 11, 14, 600
	case Code:
		return 12, 19, 400
	}
	return 13, 18, 400
}

// Size is the style's font size in points.
func (s Style) Size() float32 {
	size, _, _ := s.metrics()
	return size
}

// On sets the style on e.
func (s Style) On(e ui.Element) ui.Element {
	size, line, weight := s.metrics()
	return e.FontSize(size).LineHeight(line / size).FontWeight(weight)
}
