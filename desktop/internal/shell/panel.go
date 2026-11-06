package shell

import (
	"math"
	"path"
	"strconv"
	"time"

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
	ch     changesTab
	tree   fileTree
	views  map[string]*fileView // open files, by path relative to the working directory
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

// TogglePanel opens or closes the right panel. Opening it hides the
// sidebar when only that makes room; closing it gives that sidebar back.
func (a *App) TogglePanel() {
	s, ok := a.panelSession()
	if !ok {
		return
	}
	a.panelOpen = !a.panelOpen
	if a.panelOpen {
		a.makeRoom(a.panelFor(s.ID))
	} else {
		a.giveBack(nil)
	}
}

// panelWidth is how wide the right panel is beside a content area of the
// given room, and whether it fits there at all: the column alone with no
// tab open, the viewer and the column with one, narrowed to leave the
// conversation content-min but not below panel-min.
func (a *App) panelWidth(ps *panelState, room float32) (float32, bool) {
	avail := room - theme.ContentMin
	if len(ps.tabs) == 0 {
		w := railWidth(ps)
		return w, w <= avail
	}
	w := min(a.panelW, avail)
	return w, w >= theme.PanelMin
}

// panelMotion is how the panel goes from one width to another: along
// motion-base, a wide move taking longer, up to a third more, so that it
// travels at about the sidebar's pace rather than twice as fast.
func panelMotion(from, to float32) ui.Animation {
	scale := math.Sqrt(math.Abs(float64(to-from)) / theme.SidebarWidth)
	scale = min(max(scale, 1), 4.0/3)
	m := theme.MotionBase
	m.Duration = time.Duration(float64(m.Duration) * scale)
	return m
}

// railWidth is the column's width, the same for the file tree and the
// changes: switching them moves nothing else.
func railWidth(*panelState) float32 { return theme.PanelRail }

// makeRoom hides the sidebar when the panel, as it grows, fits only
// without it.
func (a *App) makeRoom(ps *panelState) {
	if a.hideSide || !a.panelOpen {
		return
	}
	if _, ok := a.panelWidth(ps, a.width-a.side); ok {
		return
	}
	if _, ok := a.panelWidth(ps, a.width); ok {
		a.hideSide, a.sideMoves, a.sideAuto = true, true, true
	}
}

// giveBack shows the sidebar the panel hid, once the panel, closed or
// shrunk, leaves room for it. With ps nil the panel is closed.
func (a *App) giveBack(ps *panelState) {
	if !a.sideAuto {
		return
	}
	if ps != nil {
		if _, ok := a.panelWidth(ps, a.width-a.side); !ok {
			return
		}
	}
	a.hideSide, a.sideMoves, a.sideAuto = false, true, false
}

// sideShown is the sidebar, shown by me: the panel that no longer fits
// beside it closes, rather than take the sidebar back.
func (a *App) sideShown() {
	s, ok := a.panelSession()
	if !ok || !a.panelOpen {
		return
	}
	if _, ok := a.panelWidth(a.panelFor(s.ID), a.width-a.side); !ok {
		a.panelOpen = false
	}
}

// ShowPanel opens the right panel on a column.
func (a *App) ShowPanel(col panelColumn) {
	s, ok := a.panelSession()
	if !ok {
		return
	}
	ps := a.panelFor(s.ID)
	ps.column = col
	if !a.panelOpen {
		a.TogglePanel()
		return
	}
	a.makeRoom(ps)
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
// changes width (opening, closing, the viewer coming and going) along
// motion-smooth; at rest with the viewer open it sits in a split its edge
// drags. A panel that does not fit beside content-min of conversation
// waits, closed, until the window has room.
func (a *App) main(c *ui.Context, k theme.Colors, room float32) {
	s, ok := a.panelSession()
	var ps *panelState
	target, fits := float32(0), false
	if ok {
		ps = a.panelFor(s.ID)
		target, fits = a.panelWidth(ps, room)
	}
	want := ok && a.panelOpen && fits
	if !want {
		target = 0
	}
	if target != a.panelTarget {
		a.panelFrom, a.panelTarget, a.panelMoves = a.panelTarget, target, true
	}
	zoom := want && ps.zoom && len(ps.tabs) > 0
	row := ui.Row(c).Grow(1).Shrink(1).MinWidth(0).AlignItems(ui.Stretch)
	// Whole DIPs, as the sidebar's.
	if a.panelMoves && a.panelFrom != a.panelTarget {
		a.panelMotion = panelMotion(a.panelFrom, target)
	}
	shown := float32(math.Round(float64(row.AnimateTo("panel", target, a.panelMotion))))
	if a.panelMoves && (row.Arrived("panel") || math.Abs(float64(shown-target)) < 0.5) {
		a.panelMoves = false
		c.AnimationFrame()
	}
	// The viewer shows with a tab open, and while it folds after the last.
	viewer := ps != nil && (len(ps.tabs) > 0 || a.panelMoves && a.viewerWas)
	if !a.panelMoves {
		a.viewerWas = ps != nil && len(ps.tabs) > 0
	}
	row.Children(func() {
		switch {
		case zoom:
			// The spread viewer takes the focus, so that ⎋ restores it
			// rather than first leaving the button that spread it.
			col := ui.Column(c.Key("zoomed")).Grow(1).AlignItems(ui.Stretch).Focusable().FocusRing(false)
			if ps.focus {
				col.Focus()
				ps.focus = false
			}
			if col.Shortcut(0, ui.KeyEscape) || c.Shortcut(0, ui.KeyEscape) {
				ps.zoom = false
			}
			col.Children(func() {
				ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
					a.viewer(c, k, s, ps, 0)
					a.rail(c, k, s, ps, railWidth(ps))
				})
			})
		case want && !a.panelMoves && len(ps.tabs) > 0:
			first := room - target
			sp := ui.Split(c, &first, func() { a.content(c, k) }, func() {
				ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
					a.viewer(c, k, s, ps, 0)
					a.rail(c, k, s, ps, railWidth(ps))
				})
			})
			if sp.Grow(1).Changed() {
				a.panelW = min(max(room-first, theme.PanelMin), theme.PanelMax, room-theme.ContentMin)
				a.panelTarget, _ = a.panelWidth(ps, room) // dragged there: nothing to animate
			}
		default:
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).AlignItems(ui.Stretch).Children(func() { a.content(c, k) })
			if shown <= 0.5 || ps == nil {
				return
			}
			// With the viewer, the column keeps its width and the viewer,
			// drawn at its own, is uncovered from the left as the panel
			// widens. Alone, the column goes from one width to the other,
			// and opens and closes clipped.
			rail := railWidth(ps)
			ui.Row(c).Width(shown).ClipX().Shrink(0).AlignItems(ui.Stretch).BorderWidth(0, 0, 0, theme.LineWidth).BorderColor(k.Line).Children(func() {
				if viewer {
					a.viewer(c, k, s, ps, max(a.panelW, shown)-rail)
					a.rail(c, k, s, ps, rail)
					return
				}
				a.rail(c, k, s, ps, max(shown, rail))
			})
		}
	})
}

// viewer draws the panel's viewer: its tabs over the current tab. With
// width 0 it fills what the column leaves; otherwise it keeps that width,
// clipped from the left, while the panel opens or closes.
func (a *App) viewer(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState, width float32) {
	outer := ui.Row(c).Grow(1).Shrink(1).MinWidth(0).AlignItems(ui.Stretch).Justify(ui.End).ClipX()
	outer.Children(func() {
		// The pane lies under each part rather than the whole: the
		// changes' editors lay their own.
		col := ui.Column(c).AlignItems(ui.Stretch)
		if width > 0 {
			col.Width(width).Shrink(0)
		} else {
			col.Grow(1).Shrink(1).MinWidth(0)
		}
		col.Children(func() {
			a.panelTabs(c, k, ps)
			if ps.active >= 0 && ps.active < len(ps.tabs) && ps.tabs[ps.active].changes {
				ui.Column(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() { a.changesView(c, k, s, ps) })
				return
			}
			if ps.active >= 0 && ps.active < len(ps.tabs) {
				p := ps.tabs[ps.active].path
				ui.Column(c.Key(p)).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() { a.fileTab(c, k, s, ps, p) })
				return
			}
			ui.Box(c).Grow(1).Background(k.Pane)
		})
	})
}

// rail draws the panel's column, width wide, with its own top bar; its
// edge is a hairline.
func (a *App) rail(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState, width float32) {
	ui.Column(c).Width(width).Shrink(0).Background(k.Side).BorderWidth(0, 0, 0, theme.LineWidth).BorderColor(k.Line).Children(func() {
		a.railHead(c, k, s, ps)
		if ps.column == columnChanges {
			a.changesColumn(c, k, s, ps)
			return
		}
		a.fileColumn(c, k, s, ps)
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
	bar := c.TitleBar()
	left, h := float32(theme.Space2), a.titleHeight(bar)
	if lights {
		left = bar.Left + theme.Space4
	}
	ui.Row(c).Height(h).Padding(0, theme.Space2, 0, left).Gap(theme.SpaceHalf).AlignItems(ui.Center).Background(k.Pane).DragWindow().Children(func() {
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
		a.giveBack(ps)
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
	a.makeRoom(ps)
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
	ui.Row(c).Height(a.titleHeight(c.TitleBar())).Padding(0, theme.Space2).Gap(theme.Space1Half).AlignItems(ui.Center).DragWindow().Children(func() {
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
				// As the sidebar's mode switch: a segment a dense row square,
				// wider by the count it carries.
				b := ui.Button(c.Key(opt.tip), "").Label(opt.tip).Tooltip(opt.tip).Height(theme.RowDense).MinWidth(theme.RowDense).Padding(0).Gap(theme.Space1).
					Radius(theme.RadiusControl).Border(0, ui.Transparent).Background(ui.Transparent)
				color := k.Muted
				if on {
					color = k.Fg
				}
				face := func() {
					ui.Icon(c, opt.icon).FontSize(theme.Icon).TextColor(color)
					if opt.n > 0 {
						theme.CodeCaption.On(ui.Text(c, strconv.Itoa(opt.n))).TextColor(color)
					}
				}
				if opt.n > 0 {
					b.Padding(0, theme.Space2)
				}
				b.Children(func() {
					if !on {
						face()
						return
					}
					// The raised block, its face on it, slides from the old
					// column to the new.
					pill := ui.Row(c.Key("pill")).Absolute().Left(0).Top(0).Fill().Gap(theme.Space1).Center().Radius(theme.RadiusControl).Background(k.Raised).
						Transition(ui.ElementTransition{Match: railPill{}, Animation: &theme.MotionBase})
					k.ShadowRaised.On(pill).Children(face)
					// Under the block, the face keeps the button its width.
					ui.Row(c).Gap(theme.Space1).Opacity(0).Children(face)
				})
				if b.Clicked() {
					ps.column = opt.col
					a.makeRoom(ps)
				}
			}
		})
		ui.Spacer(c)
		if iconButton(c, k, a.icons.Panel, "收起右栏 ⌥⌘I", false).Clicked() {
			a.TogglePanel()
		}
	})
}
