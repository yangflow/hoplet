package shell

import (
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// artifactIcon is the icon of an artifact's kind.
func (a *App) artifactIcon(kind proto.ArtifactKind) *ui.SVG {
	switch kind {
	case proto.ArtifactHTML:
		return a.icons.Source
	case proto.ArtifactImage:
		return a.icons.Picture
	case proto.ArtifactCSV:
		return a.icons.Sheet
	}
	return a.icons.Read
}

// artifactTime is when a version was published: the time today, else the
// day.
func artifactTime(now, t time.Time) string {
	if d := dayLabel(now, t); d != "今天" {
		return d
	}
	return clock(t)
}

// artifactColumn is the right column's list of artifacts, newest first; a
// row with its tab open is selected. Nothing shows until the first answer.
func (a *App) artifactColumn(c *ui.Context, k theme.Colors, ps *panelState) {
	l := &ps.arts
	if len(l.items) == 0 {
		if l.answered() {
			ui.Column(c).Padding(theme.Space4, theme.Space4, 0).AlignItems(ui.Start).Children(func() {
				theme.SecondaryBlock.On(ui.Text(c, "还没有产物")).TextColor(k.Muted)
			})
		}
		return
	}
	ui.Scroll(c.Key("artifacts")).Grow(1).MinHeight(0).Scrollbars(false).Children(func() {
		ui.Column(c).FillWidth().Padding(theme.Space1, theme.Space2, 0).Gap(theme.SpaceHalf).Children(func() {
			for _, art := range l.items {
				a.artifactRow(c.Key(string(art.ID)), k, ps, art)
			}
		})
	})
}

// artifactRow is one row of the list: a click opens the artifact's tab or
// goes to it.
func (a *App) artifactRow(c *ui.Context, k theme.Colors, ps *panelState, art proto.Artifact) {
	row := theme.ItemRow.On(ui.Row(c)).Radius(theme.RadiusRow).Label(art.Name).Transition(hoverMotion)
	open := false
	for _, t := range ps.tabs {
		open = open || t.artifact == art.ID
	}
	switch {
	case open:
		row.Background(k.Selected)
	case row.Hovered():
		row.Background(k.Hover)
	}
	if row.Clicked() {
		a.openArtifact(ps, art)
	}
	row.Children(func() {
		ui.Icon(c, a.artifactIcon(art.Kind)).FontSize(theme.IconRow).TextColor(k.Fg3)
		theme.UI.On(ui.Text(c, art.Name)).TextColor(k.Fg).SingleLine().Grow(1).Shrink(1).MinWidth(0)
		theme.CodeCaption.On(ui.Text(c, "v"+strconv.Itoa(art.Version))).TextColor(k.Muted)
		theme.Caption.On(ui.Text(c, artifactTime(c.Now(), art.UpdatedAt))).TextColor(k.Muted)
	})
}

// artifactTab is an artifact's tab: its top row, the note on an old
// version, and the version's content.
func (a *App) artifactTab(c *ui.Context, k theme.Colors, ps *panelState, id proto.ArtifactID) {
	v := ps.arts.views[id]
	if v == nil {
		ui.Box(c).Grow(1).Background(k.Pane)
		return
	}
	a.artifactTop(c, k, id, v)
	if v.version != 0 {
		a.artifactOld(c, k, v)
	}
	switch {
	case v.err != "":
		a.artifactFailed(c, k, id, v)
	case v.page != nil:
		ui.WebView(c.Key("page"+strconv.Itoa(v.showing())), v.page).Grow(1)
	case v.img != nil:
		ui.Column(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Background(k.Pane).Children(func() {
			ui.Image(c, v.img).Fit(ui.Contain).Grow(1).MinHeight(0)
		})
	case len(v.rows) > 0:
		a.artifactTable(c, k, v)
	case v.ed.Buffer != nil:
		st := &v.ed
		st.ReadOnly, st.NoActiveLine, st.Label = true, true, v.latest.Name
		st.Font, st.LineHeight = codeFont(), theme.Code.Line()/theme.Code.Size()
		st.Theme, st.FitContent, st.SoftWrap, st.RenderFindBar = codeTheme(c, k), true, a.wrap, a.findBar(k)
		quietScroll(c, k, func() { editor.View(c, st).FillWidth() })
	case v.text != "":
		v.md.Theme = mdTheme(c, k)
		ui.Scroll(c.Key("preview")).Grow(1).MinHeight(0).Background(k.Pane).Children(func() {
			ui.Column(c).FillWidth().Padding(theme.Space4, theme.Space6).Children(func() {
				markdown.View(c, v.text, &v.md).FillWidth()
			})
		})
	default:
		ui.Box(c).Grow(1).Background(k.Pane) // being read
	}
}

// artifactTop is the row over an artifact: the version chip and its menu,
// when the version was published, and reloading.
func (a *App) artifactTop(c *ui.Context, k theme.Colors, id proto.ArtifactID, v *artifactView) {
	n := v.showing()
	ui.Row(c).Height(theme.RowStep).Padding(0, theme.Space1).Gap(theme.SpaceHalf).AlignItems(ui.Center).Background(k.Pane).Children(func() {
		name := "v" + strconv.Itoa(n)
		chip := ui.Button(c.Key("version"), "").Label("版本 "+name).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space1Half).
			Radius(theme.RadiusControl).Border(0, ui.Transparent).Background(ui.Transparent).Shrink(0).Transition(hoverMotion)
		color := k.Fg2
		switch {
		case v.menu:
			chip.Background(k.Selected)
			color = k.Fg
		case chip.Hovered():
			chip.Background(k.Hover)
		}
		if chip.Clicked() {
			v.menu = !v.menu
		}
		chip.Children(func() {
			theme.Secondary.On(ui.Text(c, name)).TextColor(color)
			if v.version == 0 {
				theme.Secondary.On(ui.Text(c, "· 最新")).TextColor(k.Fg3)
			}
			ui.Icon(c, a.icons.Menu).FontSize(theme.IconSmall).TextColor(k.Faint)
		})
		dropdown(c, k, chip, &v.menu, theme.MenuNarrow, func() { a.artifactMenu(c, k, v) })
		theme.Secondary.On(ui.Text(c, "· "+artifactTime(c.Now(), v.record(n).UpdatedAt))).TextColor(k.Muted)
		ui.Spacer(c)
		if denseButton(c, k, a.icons.Reconnect, "重新加载", false).Clicked() {
			a.reloadArtifact(id, v)
		}
	})
}

// artifactMenu lists a tab's versions, newest first, the shown one checked.
func (a *App) artifactMenu(c *ui.Context, k theme.Colors, v *artifactView) {
	versions := v.versions
	if len(versions) == 0 {
		versions = []proto.Artifact{v.latest}
	}
	for _, art := range versions {
		on := art.Version == v.showing()
		name := "v" + strconv.Itoa(art.Version)
		row := menuItem(c.Key(name), k, on).Label(name)
		if row.Clicked() {
			v.choose(art.Version)
			v.menu = false
		}
		row.Children(func() {
			theme.UI.On(ui.Text(c, name)).TextColor(k.Fg)
			theme.Caption.On(ui.Text(c, artifactTime(c.Now(), art.UpdatedAt))).TextColor(k.Muted).Grow(1)
			check(c, k, a, on)
		})
	}
}

// artifactOld is the strip saying the tab shows an old version.
func (a *App) artifactOld(c *ui.Context, k theme.Colors, v *artifactView) {
	ui.Row(c).Padding(theme.Space1, theme.Space1, theme.Space1, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).
		Background(k.Fill).BorderWidth(theme.LineWidth, 0, theme.LineWidth, 0).BorderColor(k.Line).Children(func() {
		ui.Icon(c, a.icons.Made).FontSize(theme.IconRow).TextColor(k.Fg3)
		theme.Secondary.On(ui.Text(c, "这是旧版本，最新是 v"+strconv.Itoa(v.latest.Version))).TextColor(k.Fg2).Grow(1).Shrink(1)
		b := theme.Secondary.On(ui.Button(c, "回到最新")).Height(theme.Row).Padding(0, theme.Space2).Radius(theme.RadiusControl).
			Border(0, ui.Transparent).Background(ui.Transparent).TextColor(k.Fg).Transition(hoverMotion)
		if b.Hovered() {
			b.Background(k.Hover)
		}
		if b.Clicked() {
			v.version = 0
		}
	})
}

// artifactFailed says a version would not open, why, and offers to retry.
func (a *App) artifactFailed(c *ui.Context, k theme.Colors, id proto.ArtifactID, v *artifactView) {
	ui.Column(c).Grow(1).MinHeight(0).Background(k.Pane).Padding(theme.Space8, theme.Space6).Gap(theme.Space3).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, a.icons.Failed).FontSize(theme.IconEmpty).TextColor(k.Muted)
		ui.Column(c).Gap(theme.Space1).AlignItems(ui.Center).Children(func() {
			theme.UIMedium.On(ui.Text(c, "打不开这个版本")).TextColor(k.Fg)
			theme.Secondary.On(ui.Text(c, v.err)).TextColor(k.Muted)
		})
		b := theme.UI.On(ui.Button(c, "重试")).Height(theme.Row).Padding(0, theme.Space3).Radius(theme.RadiusControl).
			Border(0, ui.Transparent).Background(k.Fill).TextColor(k.Fg)
		if b.Clicked() {
			a.reloadArtifact(id, v)
		}
	})
}

// artifactTable shows a CSV: its first row heads the columns.
func (a *App) artifactTable(c *ui.Context, k theme.Colors, v *artifactView) {
	head, rows := v.rows[0], v.rows[1:]
	columns := make([]ui.TableColumn, len(head))
	for i, title := range head {
		columns[i] = ui.TableColumn{Title: title, ID: strconv.Itoa(i)}
	}
	ui.Column(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Background(k.Pane).Children(func() {
		ui.Table(c, &v.table, columns, len(rows), func(row, col int) {
			if col < len(rows[row]) {
				theme.UI.On(ui.Text(c, rows[row][col])).TextColor(k.Fg).SingleLine()
			}
		}).Grow(1)
	})
}
