package shell

import (
	"math"
	"path"
	"strconv"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// panelColumn is what the right panel's column shows.
type panelColumn int

// Columns.
const (
	columnTree panelColumn = iota
	columnChanges
)

// panelTab is a tab of the right panel's viewer: a file, or the one
// changes tab.
type panelTab struct {
	changes bool
	path    string // the file, relative to the working directory
}

// panelState is a session's right panel: its tabs, the current one, the
// column and whether the viewer covers the content area. Each session keeps
// its own.
type panelState struct {
	tabs   []panelTab
	active int // the current tab, -1 with none open
	column panelColumn
	zoom   bool
	focus  bool // the spread viewer takes the focus in its next frame
}

// railPill is the identity of the column switch's raised block.
type railPill struct{}

// panelFor returns a session's panel, made empty on first use.
func (a *App) panelFor(id proto.SessionID) *panelState {
	if a.s.panels == nil {
		a.s.panels = map[proto.SessionID]*panelState{}
	}
	ps, ok := a.s.panels[id]
	if !ok {
		ps = &panelState{active: -1}
		a.s.panels[id] = ps
	}
	return ps
}

// panelSession is the session the right panel belongs to: a Code session
// shown in the content area. Personal sessions, the inbox and the new
// page have none.
func (a *App) panelSession() (proto.Session, bool) {
	s, ok := a.current()
	if !ok || a.inbox || a.s.newPage || s.Mode != proto.ModeCode {
		return proto.Session{}, false
	}
	return s, true
}

// TogglePanel opens or closes the right panel. Closing it gives back the
// sidebar it hid to make room.
func (a *App) TogglePanel() {
	if _, ok := a.panelSession(); !ok {
		return
	}
	a.panelOpen, a.panelMoves = !a.panelOpen, true
	if !a.panelOpen && a.sideAuto {
		a.hideSide, a.sideMoves, a.sideAuto = false, true, false
	}
}

// ShowPanel opens the right panel on a column.
func (a *App) ShowPanel(col panelColumn) {
	s, ok := a.panelSession()
	if !ok {
		return
	}
	a.panelFor(s.ID).column = col
	if !a.panelOpen {
		a.TogglePanel()
	}
}

// ShowTree opens the right panel on the file tree. ⌥⌘I, ⌥⌘E and ⌥⌘D
// are the menu bar's, as ⌃⌘S is: a key the view also took would act twice.
func (a *App) ShowTree() { a.ShowPanel(columnTree) }

// ShowChanges opens the right panel on the changes.
func (a *App) ShowChanges() { a.ShowPanel(columnChanges) }

// OpenFilePicker finds a file to open in the right panel. Until the
// palette searches files, it opens the palette.
func (a *App) OpenFilePicker() { a.OpenPalette() }

// main draws the content area and, beside it, the right panel. The panel
// opens and closes by its width, along motion-smooth; at rest and open it
// sits in a split its edge drags. Where the window leaves the conversation
// less than content-min, the sidebar makes way first, then the panel.
func (a *App) main(c *ui.Context, k theme.Colors, room, full float32) {
	s, ok := a.panelSession()
	// Without the sidebar the conversation still needs content-min beside
	// the panel; the panel waits until the window has it.
	want := ok && a.panelOpen && full-a.panelW >= theme.ContentMin
	if want && !a.hideSide && room-a.panelW < theme.ContentMin {
		a.hideSide, a.sideMoves, a.sideAuto = true, true, true
	}
	zoom := want && a.panelFor(s.ID).zoom
	row := ui.Row(c).Grow(1).Shrink(1).MinWidth(0).AlignItems(ui.Stretch)
	target := float32(0)
	if want {
		target = a.panelW
	}
	shown := row.AnimateTo("panel", target, theme.MotionSmooth)
	if a.panelMoves && (row.Arrived("panel") || math.Abs(float64(shown-target)) < 0.5) {
		a.panelMoves = false
		c.AnimationFrame()
	}
	row.Children(func() {
		switch {
		case zoom:
			// The spread viewer takes the focus, so that ⎋ restores it
			// rather than first leaving the button that spread it.
			ps := a.panelFor(s.ID)
			col := ui.Column(c.Key("zoomed")).Grow(1).AlignItems(ui.Stretch).Focusable().FocusRing(false)
			if ps.focus {
				col.Focus()
				ps.focus = false
			}
			if col.Shortcut(0, ui.KeyEscape) || c.Shortcut(0, ui.KeyEscape) {
				ps.zoom = false
			}
			col.Children(func() { a.panel(c, k, s) })
		case want && !a.panelMoves:
			first := room - a.panelW
			ui.Split(c, &first, func() { a.content(c, k) }, func() { a.panel(c, k, s) }).Grow(1)
			a.panelW = min(max(room-first, theme.PanelMin), theme.PanelMax)
		default:
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).AlignItems(ui.Stretch).Children(func() { a.content(c, k) })
			if shown > 0.5 && ok {
				ui.Column(c).Width(shown).ClipX().Shrink(0).BorderWidth(0, 0, 0, theme.LineWidth).BorderColor(k.Line).Children(func() {
					ui.Row(c).Width(a.panelW).Grow(1).AlignItems(ui.Stretch).Children(func() { a.panel(c, k, s) })
				})
			}
		}
	})
}

// panel draws the right panel: the viewer with its tabs, and the column
// with its own top bar. The column's edge is a hairline.
func (a *App) panel(c *ui.Context, k theme.Colors, s proto.Session) {
	ps := a.panelFor(s.ID)
	ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Background(k.Pane).Children(func() {
			a.panelTabs(c, k, ps)
			if ps.active < 0 || ps.active >= len(ps.tabs) {
				ui.Box(c).Grow(1).Center().Children(func() {
					theme.Secondary.On(ui.Text(c, "从右侧打开文件或变更")).TextColor(k.Muted)
				})
				return
			}
			ui.Box(c).Grow(1) // the tab's content comes with the changes and the file view
		})
		width := float32(theme.PanelTree)
		if ps.column == columnChanges {
			width = theme.PanelChanges
		}
		ui.Column(c).Width(width).Shrink(0).Background(k.Side).BorderWidth(0, 0, 0, theme.LineWidth).BorderColor(k.Line).Children(func() {
			a.railHead(c, k, s, ps)
			ui.Box(c).Grow(1) // the file tree or the changes
		})
	})
}

// panelTabs is the viewer's tab bar: a tab per open file and the changes,
// the current one raised with its ✕; a menu to open more, and at the end
// the button that spreads the viewer over the content area.
func (a *App) panelTabs(c *ui.Context, k theme.Colors, ps *panelState) {
	// Spread over the content area with the sidebar hidden, the viewer's
	// top row takes the traffic lights and the window buttons, as the
	// content area's title bar does.
	lights := ps.zoom && a.hideSide
	left, h := float32(theme.Space2), float32(theme.TitleBar)
	if lights {
		bar := c.TitleBar()
		left, h = bar.Left+theme.Space4, topHeight(bar)
	}
	ui.Row(c).Height(h).Padding(0, theme.Space2, 0, left).Gap(theme.SpaceHalf).AlignItems(ui.Center).DragWindow().Children(func() {
		if lights {
			ui.Row(c).Gap(theme.SpaceHalf).Children(func() { a.windowButtons(c, k) })
		}
		for i, t := range ps.tabs {
			on := i == ps.active
			name := "变更"
			if !t.changes {
				name = path.Base(t.path)
			}
			b := ui.Button(c.Key(strconv.Itoa(i)+name), "").Label(name).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space1Half).
				Radius(theme.RadiusControl).Border(0, ui.Transparent).Background(ui.Transparent).Transition(hoverMotion)
			switch {
			case on:
				k.ShadowRaised.On(b.Background(k.Raised))
			case b.Hovered():
				b.Background(k.Hover)
			}
			b.Children(func() {
				if t.changes {
					ui.Icon(c, a.icons.Changes).FontSize(theme.IconRow).TextColor(k.Fg3)
				} else if img := a.icons.FileType(t.path); img != nil {
					ui.Image(c, img).Size(theme.IconRow, theme.IconRow)
				}
				style, color := theme.UI, k.Fg3
				if on {
					style, color = theme.UIMedium, k.Fg
				}
				style.On(ui.Text(c, name)).TextColor(color).SingleLine()
				if on {
					x := ui.Button(c.Key("close"), "").Label("关闭 "+name).Tooltip("关闭").Size(theme.ControlXS, theme.ControlXS).Padding(0).
						Radius(theme.RadiusRow).Border(0, ui.Transparent).Background(ui.Transparent)
					if x.Hovered() {
						x.Background(k.Hover)
					}
					x.Children(func() { ui.Icon(c, a.icons.Close).FontSize(theme.IconSmall).TextColor(k.Faint) })
					if x.Clicked() {
						a.closeTab(ps, i)
					}
				}
			})
			if b.Clicked() {
				ps.active = i
			}
		}
		iconButton(c, k, a.icons.New, "打开文件 ⌘P", false).Menu(func(m *ui.Menu) {
			if m.Item("文件").Shortcut(ui.Cmd, ui.KeyP).Chosen() {
				a.OpenFilePicker()
			}
		})
		ui.Spacer(c)
		if iconButton(c, k, a.icons.Maximize, "放大到占满正文区", ps.zoom).Clicked() {
			ps.zoom = !ps.zoom
			ps.focus = ps.zoom
		}
	})
}

// closeTab closes tab i; the current tab moves to its neighbor.
func (a *App) closeTab(ps *panelState, i int) {
	ps.tabs = append(ps.tabs[:i], ps.tabs[i+1:]...)
	if ps.active >= len(ps.tabs) {
		ps.active = len(ps.tabs) - 1
	}
	if len(ps.tabs) == 0 {
		ps.zoom = false
	}
}

// openTab opens a tab, or shows it when it is open already.
func (a *App) openTab(ps *panelState, t panelTab) {
	for i, o := range ps.tabs {
		if o == t {
			ps.active = i
			return
		}
	}
	ps.tabs = append(ps.tabs, t)
	ps.active = len(ps.tabs) - 1
}

// railHead is the column's top bar, as tall as the tabs: the file tree or
// the changes, with how many files changed, and the panel's own close.
func (a *App) railHead(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState) {
	changed := 0
	if d := a.diff(s); d != nil {
		for _, r := range d.Repos {
			changed += len(r.Files)
		}
	}
	ui.Row(c).Height(theme.TitleBar).Padding(0, theme.Space2).Gap(theme.Space1Half).AlignItems(ui.Center).DragWindow().Children(func() {
		ui.Row(c).Padding(theme.SpaceHalf).Gap(theme.SpaceHalf).Radius(theme.RadiusControl).Background(k.Line).Children(func() {
			for _, opt := range []struct {
				col  panelColumn
				icon *ui.SVG
				tip  string
				n    int
			}{
				{columnTree, a.icons.Tree, "文件树 ⌥⌘E", -1},
				{columnChanges, a.icons.Changes, "变更 ⌥⌘D", changed},
			} {
				on := ps.column == opt.col
				b := ui.Button(c.Key(opt.tip), "").Label(opt.tip).Tooltip(opt.tip).Height(theme.RowDense).Padding(0, theme.Space2).Gap(theme.Space1).
					Radius(theme.RadiusRow).Border(0, ui.Transparent).Background(ui.Transparent)
				b.Children(func() {
					if on {
						pill := ui.Box(c.Key("pill")).Absolute().Left(0).Top(0).Fill().Radius(theme.RadiusRow).Background(k.Raised).
							Transition(ui.ElementTransition{Match: railPill{}, Animation: &theme.MotionSnappy})
						k.ShadowRaised.On(pill)
					}
					ui.Icon(c, opt.icon).FontSize(theme.IconRow).TextColor(k.Fg3)
					if opt.n > 0 {
						theme.CodeCaption.On(ui.Text(c, strconv.Itoa(opt.n))).TextColor(k.Muted)
					}
				})
				if b.Clicked() {
					ps.column = opt.col
				}
			}
		})
		ui.Spacer(c)
		if iconButton(c, k, a.icons.Panel, "收起右栏 ⌥⌘I", true).Clicked() {
			a.TogglePanel()
		}
	})
}
