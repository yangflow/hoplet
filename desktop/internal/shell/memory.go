package shell

import (
	"cmp"
	"slices"
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

	detail     proto.ProfileDetail // the open entry and its history, once read
	detailLoad int                 // counts the detail reads, as load does
	versions   bool                // the open entry's versions show
	confirm    bool                // the open entry asks before withdrawing
	scopeOpen  bool                // the open candidate's scope menu shows

	picked map[string]proto.ProfileScope // scopes chosen for candidates, by ID
	ops    map[string]string             // operation IDs of unfinished writes
	edit   memoryEdit

	settings memorySettingsView

	saved      map[string]proto.ProfileEntry          // entries chats' lines name, by ID
	savedAsked map[string]bool                        // those read or being read
	usage      map[proto.SessionID]proto.ProfileUsage // what sessions used
	usageAsked map[proto.SessionID]bool               // read since their details opened
}

// Entry statuses and candidate actions, as the daemon writes them.
const (
	memoryActive    = "active"
	memoryStale     = "stale"
	memoryWithdrawn = "withdrawn"

	actionUpdate   = "update"
	actionWithdraw = "withdraw"
)

// memoryFew is how many entries the list shows in one column; more take
// two.
const memoryFew = 5

// Sizes of a row's leading dot, by version, and of a group's ring.
const (
	memoryDotColumn    = 12
	memoryDotCandidate = 7
	memoryRing         = 9
	memoryRingLine     = 1.5
)

// OpenMemory shows growth memory in the settings, with entry, when not
// "", opened in place, and records it for Back and Forward.
func (a *App) OpenMemory(entry string) {
	a.showMemory(entry)
	a.visit(page{kind: pageSettings, entry: entry})
}

// showMemory shows the settings page and reads growth memory afresh: the
// page follows no feed, so each visit reads it once.
func (a *App) showMemory(entry string) {
	a.m.open, a.m.known = true, false
	a.inbox, a.selected, a.s.newPage = false, "", false
	a.loadMemory()
	a.openEntry(entry)
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
	a.memoryBarEnd(c, k)
}

// memoryPage is the settings page's content: the entries, or one line
// while they are read, when they could not be, or when there are none.
func (a *App) memoryPage(c *ui.Context, k theme.Colors) {
	ui.Scroll(c.Key("memory")).Grow(1).Children(func() {
		switch line := a.memoryLine(); {
		case a.openWithdrawn():
			memoryFrame(c, true, func() {
				memoryCard(c.Key("withdrawn"), k).Padding(theme.Space3).Gap(theme.Space3).Children(func() {
					theme.UIMedium.On(ui.Text(c, "这条记忆已撤回")).TextColor(k.Fg)
					theme.SecondaryBlock.On(ui.Text(c, "新启动的会话不再用它。")).TextColor(k.Muted)
				})
			})
		case line != "":
			memoryFrame(c, true, func() {
				ui.Column(c).Padding(theme.Space4, theme.Space4, 0).AlignItems(ui.Start).Children(func() {
					theme.SecondaryBlock.On(ui.Text(c, line)).TextColor(k.Muted)
				})
			})
		default:
			a.memoryList(c, k)
		}
	})
	a.memoryEditDialog(c, k)
}

// memoryLine is the one line the page shows in place of entries, or "".
func (a *App) memoryLine() string {
	switch {
	case !a.m.known:
		return "正在读取…"
	case a.m.err != nil:
		return "读取失败"
	case len(a.memoryShown()) == 0 && len(a.m.data.Candidates) == 0:
		return "还没有记忆"
	}
	return ""
}

// openWithdrawn reports whether the entry opened is one already withdrawn,
// reached from a chat's line, which the page shows alone.
func (a *App) openWithdrawn() bool {
	e, ok := a.memoryEntry(a.m.entry)
	return a.m.known && a.m.err == nil && ok && e.Status == memoryWithdrawn
}

// memoryEntry finds an entry by ID.
func (a *App) memoryEntry(id string) (proto.ProfileEntry, bool) {
	i := slices.IndexFunc(a.m.data.Entries, func(e proto.ProfileEntry) bool { return e.ID == id })
	if i < 0 {
		return proto.ProfileEntry{}, false
	}
	return a.m.data.Entries[i], true
}

// memoryShown are the entries the list shows: those in effect and those to
// check.
func (a *App) memoryShown() []proto.ProfileEntry {
	var out []proto.ProfileEntry
	for _, e := range a.m.data.Entries {
		if e.Status == memoryActive || e.Status == memoryStale {
			out = append(out, e)
		}
	}
	return out
}

// memoryGroup is the entries of one scope.
type memoryGroup struct {
	scope   proto.ProfileScope
	name    string
	entries []proto.ProfileEntry
}

// memoryGroups puts the shown entries in their scopes, in the menu's order.
func (a *App) memoryGroups() []memoryGroup {
	var out []memoryGroup
	for _, e := range a.memoryShown() {
		i := slices.IndexFunc(out, func(g memoryGroup) bool { return g.scope == e.Scope })
		if i < 0 {
			out = append(out, memoryGroup{scope: e.Scope, name: a.scopeName(e.Scope)})
			i = len(out) - 1
		}
		out[i].entries = append(out[i].entries, e)
	}
	slices.SortStableFunc(out, func(x, y memoryGroup) int {
		return cmp.Or(cmp.Compare(scopeOrder(x.scope.Kind), scopeOrder(y.scope.Kind)), cmp.Compare(x.name, y.name))
	})
	return out
}

// splitGroups is where two columns divide the groups, in order, so that
// their heights are as even as they can be.
func splitGroups(groups []memoryGroup) int {
	total := 0
	for _, g := range groups {
		total += len(g.entries) + 1
	}
	at, best, left := 1, total, 0
	for i, g := range groups {
		left += len(g.entries) + 1
		if d := max(left, total-left) - min(left, total-left); d < best {
			at, best = i+1, d
		}
	}
	return at
}

// memoryList is the changes waiting for me, then the entries by scope: one
// column for a few or for one scope, two otherwise.
func (a *App) memoryList(c *ui.Context, k theme.Colors) {
	groups := a.memoryGroups()
	few := len(a.memoryShown()) <= memoryFew || len(groups) < 2
	memoryFrame(c, few, func() {
		if cs := a.m.data.Candidates; len(cs) > 0 {
			memoryCard(c.Key("candidates"), k).Children(func() {
				memoryGroupHead(c, k, "待确认", len(cs))
				for _, cand := range cs {
					a.candidateRow(c.Key(cand.Entry.ID), k, cand)
				}
			})
		}
		if few {
			a.memoryGroupCards(c, k, groups)
			return
		}
		at := splitGroups(groups)
		ui.Row(c).FillWidth().Gap(theme.Space3).AlignItems(ui.Start).Children(func() {
			for i, part := range [][]memoryGroup{groups[:at], groups[at:]} {
				ui.Column(c.Key(i)).Grow(1).Shrink(1).Basis(0).MinWidth(0).Gap(theme.Space3).Children(func() {
					a.memoryGroupCards(c, k, part)
				})
			}
		})
	})
}

// memoryGroupCards draws each group as a card.
func (a *App) memoryGroupCards(c *ui.Context, k theme.Colors, groups []memoryGroup) {
	for _, g := range groups {
		memoryCard(c.Key(g.scope), k).Children(func() {
			memoryGroupHead(c, k, g.name, len(g.entries))
			for _, e := range g.entries {
				a.entryRow(c.Key(e.ID), k, e)
			}
		})
	}
}

// memoryCard is the card around a group.
func memoryCard(c *ui.Context, k theme.Colors) ui.Element {
	return ui.Column(c).FillWidth().Padding(theme.Space1).Gap(theme.SpaceHalf).Radius(theme.RadiusCard).Background(k.Surface).Border(theme.LineWidth, k.Line)
}

// memoryGroupHead names a group and counts it.
func memoryGroupHead(c *ui.Context, k theme.Colors, name string, count int) {
	ui.Row(c).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(memoryRing, memoryRing).Radius(theme.RadiusPill).Border(memoryRingLine, k.Fg3)
		theme.Label.On(ui.Text(c, name)).TextColor(k.Fg2).Grow(1).SingleLine()
		theme.Caption.On(ui.Text(c, strconv.Itoa(count))).TextColor(k.Muted)
	})
}

// memoryRowLook is what a row shows.
type memoryRowLook struct {
	title     string
	version   int
	candidate bool
	stale     bool
	note      string
}

// memoryRow is a row of the list, a button that opens the entry in place.
func memoryRow(c *ui.Context, k theme.Colors, a *App, l memoryRowLook) ui.Element {
	r := ui.Row(c).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusRow).Label(l.title).Transition(hoverMotion)
	if r.Hovered() {
		r.Background(k.Hover)
	}
	return r.Children(func() {
		ui.Row(c).Width(memoryDotColumn).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			size := float32(memoryDotCandidate)
			tone := k.Attn
			if !l.candidate {
				size, tone = memoryDot(l.version), k.Fg2
			}
			dot := ui.Box(c).Size(size, size).Radius(theme.RadiusPill).Background(tone)
			if l.stale {
				dot.Opacity(.4)
			}
		})
		ink := k.Fg
		if l.stale {
			ink = k.Muted
		}
		theme.UI.On(ui.Text(c, l.title)).TextColor(ink).SingleLine().Grow(1).Shrink(1)
		if l.version > 1 {
			ui.Textf(c, "v%d", l.version).Font(theme.Mono).FontSize(theme.Caption.Size()).TextColor(k.Muted).Shrink(0)
		}
		if l.note != "" {
			theme.Caption.On(ui.Text(c, l.note)).TextColor(k.Muted).Shrink(0)
		}
		if l.stale {
			ui.Icon(c, a.icons.Offline).FontSize(theme.IconSmall).TextColor(k.Muted).Label("待核对")
		}
	})
}

// memoryDot is a row's dot for a version: larger as it has grown.
func memoryDot(version int) float32 {
	switch {
	case version >= 3:
		return 9
	case version == 2:
		return 7
	}
	return 5
}

// entryRow is an entry's row, or its details while it is open.
func (a *App) entryRow(c *ui.Context, k theme.Colors, e proto.ProfileEntry) {
	if a.m.entry == e.ID {
		memoryOpenBlock(c, k).Children(func() { a.entryDetail(c, k, e) })
		return
	}
	if memoryRow(c, k, a, memoryRowLook{title: e.Title, version: e.Version, stale: e.Status == memoryStale}).Clicked() {
		a.openEntry(e.ID)
	}
}

// candidateRow is a candidate's row, or its details while it is open.
func (a *App) candidateRow(c *ui.Context, k theme.Colors, cand proto.ProfileCandidate) {
	if a.m.entry == cand.Entry.ID {
		memoryOpenBlock(c, k).Children(func() { a.candidateDetail(c, k, cand) })
		return
	}
	look := memoryRowLook{title: a.candidateTitle(cand), candidate: true, note: a.scopeName(cand.Entry.Scope)}
	if memoryRow(c, k, a, look).Clicked() {
		a.openEntry(cand.Entry.ID)
	}
}

// memoryOpenBlock holds an open entry in the list.
func memoryOpenBlock(c *ui.Context, k theme.Colors) ui.Element {
	return ui.Column(c).FillWidth().Padding(theme.Space3).Gap(theme.Space3).Background(k.Selected).Radius(theme.RadiusRow).Transition(openMotion)
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
