package shell

import (
	"strconv"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// Memory is growth memory as the settings page shows it: every entry,
// withdrawn ones too, the changes waiting for me, how background
// extraction stands and the settings.
type Memory struct {
	Entries    []proto.ProfileEntry
	Candidates []proto.ProfileCandidate
	Extraction proto.ProfileExtractionStatus
	Settings   proto.ProfileSettings
}

// memoryView is the settings page's growth memory.
type memoryView struct {
	open  bool   // the settings page shows
	entry string // the entry opened in place, "" for none

	load    int  // counts the reads; an answer to an older one is dropped
	loading bool // a read is under way
	known   bool // a read has answered since the page opened
	err     error
	data    Memory
}

// OpenMemory shows growth memory in the settings, with entry, when not
// "", opened in place, and records it for Back and Forward.
func (a *App) OpenMemory(entry string) {
	a.showMemory(entry)
	a.visit(page{kind: pageSettings, entry: entry})
}

// showMemory shows the settings page and reads growth memory afresh: the
// page follows no feed, so each visit reads it once.
func (a *App) showMemory(entry string) {
	a.m.open, a.m.entry, a.m.known = true, entry, false
	a.inbox, a.selected, a.s.newPage = false, "", false
	a.loadMemory()
}

// loadMemory reads growth memory; an answer to an older read is dropped.
func (a *App) loadMemory() {
	a.m.load++
	n := a.m.load
	a.m.loading = true
	a.host.Memory(func(m Memory, err error) {
		if n != a.m.load {
			return
		}
		a.m.loading, a.m.known, a.m.err = false, true, err
		if err == nil {
			a.m.data = m
		}
	})
}

// LeaveSettings goes back to the page I was on before the settings: the
// nearest one before them in the history that still exists, or the
// new-session page.
func (a *App) LeaveSettings() {
	for i := a.at - 1; i >= 0; i-- {
		p := a.history[i]
		if p.kind == pageSettings {
			continue
		}
		if _, ok := a.cat.Get(p.id); p.kind == pageSession && !ok {
			continue
		}
		a.at = i
		a.showPage(p)
		return
	}
	a.newSessionIn("")
}

// settingsNav is the sidebar on the settings page: the way back, and the
// settings, of which growth memory is the only one, with how many changes
// wait for me.
func (a *App) settingsNav(c *ui.Context, k theme.Colors) {
	ui.Column(c).Grow(1).Padding(0, theme.Space2).Margin(theme.Space1, 0, 0, 0).Gap(theme.SpaceHalf).Children(func() {
		if sideEntry(c, k, a.icons.Back, "返回", false, 0).Clicked() {
			a.LeaveSettings()
		}
		ui.Row(c).Height(theme.RowDense).Margin(theme.Space3, 0, 0, 0).Padding(0, theme.Space2).AlignItems(ui.Center).Children(func() {
			theme.Label.On(ui.Text(c, "设置")).TextColor(k.Muted)
		})
		if sideEntry(c, k, a.icons.Memory, "成长记忆", true, len(a.m.data.Candidates)).Clicked() {
			a.OpenMemory("")
		}
	})
}

// sideEntry is a row of the sidebar's entries: an icon, a name, and a
// count for a badge when above 0; a selected row has the selected fill
// and its name strong.
func sideEntry(c *ui.Context, k theme.Colors, icon *ui.SVG, name string, selected bool, count int) ui.Element {
	e := ui.Row(c.Key(name)).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusRow).Label(name)
	switch {
	case selected:
		e.Background(k.Selected)
	case e.Hovered():
		e.Background(k.Fill)
	}
	return e.Children(func() {
		ui.Icon(c, icon).FontSize(theme.Icon).TextColor(k.Fg3)
		t := theme.UI.On(ui.Text(c, name)).TextColor(k.Fg).Grow(1)
		if selected {
			t.FontWeight(theme.UIStrong.Weight())
		}
		if count > 0 {
			ui.Badge(c, strconv.Itoa(count)).Background(k.Attn).TextColor(k.OnAttn)
		}
	})
}

// memoryTitle is the settings page's title bar: growth memory's icon and
// name.
func (a *App) memoryTitle(c *ui.Context, k theme.Colors) {
	ui.Icon(c, a.icons.Memory).FontSize(theme.Icon).TextColor(k.Fg3)
	theme.UIStrong.On(ui.Text(c, "成长记忆")).TextColor(k.Fg)
}

// memoryPage is the settings page's content: the entries, or one line
// while they are read, when they could not be, or when there are none.
func (a *App) memoryPage(c *ui.Context, k theme.Colors) {
	ui.Scroll(c.Key("memory")).Grow(1).Children(func() {
		if line := a.memoryLine(); line != "" {
			memoryFrame(c, true, func() {
				ui.Column(c).Padding(theme.Space4, theme.Space4, 0).AlignItems(ui.Start).Children(func() {
					theme.SecondaryBlock.On(ui.Text(c, line)).TextColor(k.Muted)
				})
			})
			return
		}
	})
}

// memoryLine is the one line the page shows in place of entries, or "".
func (a *App) memoryLine() string {
	switch {
	case !a.m.known:
		return "正在读取…"
	case a.m.err != nil:
		return "读取失败"
	case len(a.m.data.Entries) == 0 && len(a.m.data.Candidates) == 0:
		return "还没有记忆"
	}
	return ""
}

// memoryFrame is the list's frame under the title bar: 12 above, 24 at
// the sides and 32 below; one column stays within the code column's
// width, centered.
func memoryFrame(c *ui.Context, narrow bool, body func()) {
	f := ui.Column(c).WidthPercent(100).Padding(theme.Space3, theme.Space6, theme.Space8).Gap(theme.Space3)
	if narrow {
		f.MaxWidth(theme.CodeColumn).Margin(0, ui.Auto)
	}
	f.Children(body)
}
