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
	columnArtifacts
)

// panelTab is a tab of the right panel's viewer: a file, the one changes
// tab, a subagent's record or an artifact.
type panelTab struct {
	changes  bool
	path     string           // the file, relative to the working directory
	agent    string           // the subagent, by ID
	artifact proto.ArtifactID // the artifact; the version it shows is its view's
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
	picker filePicker
	views  map[string]*fileView // open files, by path relative to the working directory
	arts   artifactList
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

// panelSession is the session the right panel belongs to: the session
// shown in the content area. The inbox and the new page have none.
func (a *App) panelSession() (proto.Session, bool) {
	s, ok := a.current()
	if !ok || a.inbox || a.s.newPage {
		return proto.Session{}, false
	}
	return s, true
}

// codePanelSession is panelSession for what only a Code session's panel
// has: its files and changes.
func (a *App) codePanelSession() (proto.Session, bool) {
	s, ok := a.panelSession()
	if !ok || s.Mode != proto.ModeCode {
		return proto.Session{}, false
	}
	return s, true
}

// TogglePanel opens or closes the right panel. Open but yielding its
// place, it comes forward instead, as one I just opened.
func (a *App) TogglePanel() {
	if _, ok := a.panelSession(); !ok {
		return
	}
	if a.panelOpen && a.fit.panel > 0 {
		a.panelOpen = false
		return
	}
	a.bringPanel()
}

// bringPanel opens the right panel as the column I opened last: short of
// room, an earlier one yields to it.
func (a *App) bringPanel() {
	a.panelOpen = true
	a.clock++
	a.panelAt = a.clock
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

// fitColumns lays out the sidebar and the right panel in the window as
// it was last drawn.
func (a *App) fitColumns(sideMin float32) fitOut {
	in := fitIn{width: a.width, sideOpen: !a.sideClosed, side: a.side, sideMin: sideMin, sideAt: a.sideAt,
		rail: a.railW, panel: a.panelW, panelAt: a.panelAt}
	if s, ok := a.panelSession(); ok && a.panelOpen {
		in.panelOpen, in.viewer = true, len(a.panelFor(s.ID).tabs) > 0
	}
	return fit(in)
}

// ShowPanel opens the right panel on a column.
func (a *App) ShowPanel(col panelColumn) {
	s, ok := a.codePanelSession()
	if !ok {
		return
	}
	a.panelFor(s.ID).column = col
	if !a.panelOpen || a.fit.panel == 0 {
		a.bringPanel()
	}
}

// ShowTree opens the right panel on the file tree. ⌥⌘I, ⌥⌘E and ⌥⌘D
// are the menu bar's, as ⌃⌘S is: a key the view also took would act twice.
func (a *App) ShowTree() { a.ShowPanel(columnTree) }

// ShowChanges opens the right panel on the changes.
func (a *App) ShowChanges() { a.ShowPanel(columnChanges) }

// main draws the content area and, beside it, the right panel as fit
// leaves it. The panel changes width (opening, closing, the viewer coming
// and going) along motion-base; at rest it sits in a split its edge drags,
// the viewer and the column in another.
func (a *App) main(c *ui.Context, k theme.Colors, room float32) {
	s, ok := a.panelSession()
	var ps *panelState
	if ok {
		ps = a.panelFor(s.ID)
	}
	target := a.fit.panel
	want := ps != nil && target > 0
	if want {
		a.syncArtifacts(s, ps)
	}
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
					a.rail(c, k, s, ps, a.fit.rail)
				})
			})
		case want && !a.panelMoves:
			first := room - target
			sp := ui.Split(c, &first, func() { a.content(c, k) }, func() { a.panelBody(c, k, s, ps, target) })
			if sp.Grow(1).Changed() {
				w := room - first
				if len(ps.tabs) > 0 {
					w = min(max(w, theme.ViewerMin+a.fit.rail), room-theme.ContentMin)
					a.panelW = w
				} else {
					w = min(max(w, theme.PanelRailMin), theme.PanelRailMax, room-theme.ContentMin)
					a.railW = w
				}
				a.fit.panel, a.panelTarget = w, w // dragged there: nothing to animate
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
			rail := max(a.fit.rail, a.railShown)
			ui.Row(c).Width(shown).ClipX().Shrink(0).AlignItems(ui.Stretch).BorderWidth(0, 0, 0, theme.LineWidth).BorderColor(k.Line).Children(func() {
				if viewer {
					a.viewer(c, k, s, ps, max(shown, a.panelFrom, a.panelTarget)-rail)
					a.rail(c, k, s, ps, rail)
					return
				}
				a.rail(c, k, s, ps, max(shown, rail))
			})
		}
	})
	if a.fit.rail > 0 {
		a.railShown = a.fit.rail
	}
}

// panelBody is the panel at rest, width wide: the column alone, or the
// viewer and the column in a split whose edge sizes the column.
func (a *App) panelBody(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState, width float32) {
	if len(ps.tabs) == 0 {
		a.rail(c, k, s, ps, 0)
		return
	}
	first := width - a.fit.rail
	sp := ui.Split(c, &first, func() { a.viewer(c, k, s, ps, 0) }, func() { a.rail(c, k, s, ps, 0) })
	if sp.Fill().Changed() {
		r := min(max(width-first, theme.PanelRailMin), theme.PanelRailMax, width-theme.ViewerMin)
		a.railW, a.fit.rail = r, r
	}
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
			a.panelTabs(c, k, s, ps)
			if ps.active >= 0 && ps.active < len(ps.tabs) && ps.tabs[ps.active].agent != "" {
				id := ps.tabs[ps.active].agent
				ui.Column(c.Key("agent:" + id)).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Background(k.Pane).Children(func() { a.agentView(c, k, s, id) })
				return
			}
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
	col := ui.Column(c).Background(k.Side)
	if width > 0 {
		col.Width(width).Shrink(0).BorderWidth(0, 0, 0, theme.LineWidth).BorderColor(k.Line)
	} else {
		col.Grow(1).Shrink(1).MinWidth(0) // in a split, whose line is its edge
	}
	col.Children(func() {
		a.railHead(c, k, s, ps)
		if s.Mode == proto.ModePersonal || ps.column == columnArtifacts {
			a.artifactColumn(c, k, ps)
			return
		}
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
func (a *App) panelTabs(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState) {
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
			switch {
			case t.agent != "":
				name = "子代理"
				if g, ok := a.conv(s.ID).Agent(t.agent); ok && g.Title != "" {
					name = g.Title
				}
			case !t.changes:
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
				} else if t.agent != "" {
					ui.Icon(c, a.icons.Agent).FontSize(theme.IconRow).TextColor(k.Fg3)
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
	if id := ps.tabs[i].artifact; id != "" {
		delete(ps.arts.views, id)
		a.host.CloseArtifactPages(id)
	}
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
	// The viewer opening comes forward, as a column I opened.
	if len(ps.tabs) == 0 {
		a.clock++
		a.panelAt = a.clock
	}
	ps.tabs = append(ps.tabs, t)
	ps.active = len(ps.tabs) - 1
}

// railHead is the column's top bar, as tall as the tabs: the switch among
// the file tree, the changes with how many files changed and the artifacts,
// or a Personal session's one column named; and the panel's own close.
func (a *App) railHead(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState) {
	personal := s.Mode == proto.ModePersonal
	changed := 0
	if !personal {
		if d := a.diff(s); d != nil {
			for _, r := range d.Repos {
				changed += len(r.Files)
			}
		}
	}
	ui.Row(c).Height(a.titleHeight(c.TitleBar())).Padding(0, theme.Space2).Gap(theme.Space1Half).AlignItems(ui.Center).DragWindow().Children(func() {
		if personal {
			theme.UIMedium.On(ui.Text(c, "产物")).TextColor(k.Fg).Margin(0, 0, 0, theme.Space1)
		}
		a.railSwitch(c, k, ps, changed, personal)
		ui.Spacer(c)
		if iconButton(c, k, a.icons.Panel, "收起右栏 ⌥⌘I", false).Clicked() {
			a.TogglePanel()
		}
	})
}

// railSwitch is the segmented switch among a Code session's columns.
func (a *App) railSwitch(c *ui.Context, k theme.Colors, ps *panelState, changed int, personal bool) {
	if personal {
		return
	}
	ui.Row(c).Padding(theme.SpaceHalf).Gap(theme.SpaceHalf).Radius(theme.RadiusControl).Background(k.Line).Children(func() {
		for _, opt := range []struct {
			col  panelColumn
			icon *ui.SVG
			tip  string
			n    int
		}{
			{columnTree, a.icons.Tree, "文件树 ⌥⌘E", -1},
			{columnChanges, a.icons.Changes, "变更 ⌥⌘D", changed},
			{columnArtifacts, a.icons.Tool, "产物", -1},
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
			}
		}
	})
}
