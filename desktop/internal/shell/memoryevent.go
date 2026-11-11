package shell

import (
	"strconv"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/convo"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// savedEntry is the entry a chat's line names, read the first time it is
// asked for. The log keeps only its ID, so one that cannot be read shows
// nothing.
func (a *App) savedEntry(id string) (proto.ProfileEntry, bool) {
	if !a.m.savedAsked[id] {
		if a.m.savedAsked == nil {
			a.m.savedAsked = map[string]bool{}
		}
		a.m.savedAsked[id] = true
		a.readSaved(id)
	}
	e, ok := a.m.saved[id]
	return e, ok
}

func (a *App) readSaved(id string) {
	a.host.MemoryEntry(id, func(d proto.ProfileDetail, err error) {
		if err != nil {
			return
		}
		if a.m.saved == nil {
			a.m.saved = map[string]proto.ProfileEntry{}
		}
		a.m.saved[id] = d.Entry
	})
}

// refreshSaved reads again the entries chats show, after a write here.
func (a *App) refreshSaved() {
	for id := range a.m.saved {
		a.readSaved(id)
	}
}

// memoryEventRow is a memory saved, in a chat: a line that opens the entry
// in the settings, and once withdrawn only says so.
func (a *App) memoryEventRow(c *ui.Context, k theme.Colors, s proto.Session, it convo.Item) {
	id := it.Event.Memory
	e, ok := a.savedEntry(id)
	if !ok {
		return
	}
	gone := e.Status == memoryWithdrawn
	title, ink := e.Title, k.Fg
	if gone {
		title, ink = "已撤回", k.Muted
	}
	r := ui.Row(c).AlignSelf(ui.Start).MaxWidth(chatCardMax).Height(theme.RowStep).Padding(0, theme.Space3).
		Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusCard).Border(theme.LineWidth, k.Line).Label("记住了 " + title)
	if s.Mode == proto.ModePersonal {
		r.Margin(0, 0, 0, chatAvatarRoom)
	}
	if !gone {
		if r.Hovered() {
			r.Background(k.Fill)
		}
		if r.Clicked() {
			a.OpenMemory(id)
		}
	}
	r.Children(func() {
		ui.Icon(c, a.icons.Memory).FontSize(theme.IconRow).TextColor(k.Fg3)
		theme.UI.On(ui.Text(c, "记住了")).TextColor(k.Muted).SingleLine().Shrink(0)
		theme.UIMedium.On(ui.Text(c, title)).TextColor(ink).SingleLine().Shrink(1)
		if !gone {
			ui.Icon(c, a.icons.Expand).FontSize(theme.IconRow).TextColor(k.Fg3)
		}
	})
}

// sessionUsage is what a session used, read once each time its details
// open; the last answer stays until the next one comes.
func (a *App) sessionUsage(id proto.SessionID) (proto.ProfileUsage, bool) {
	if !a.m.usageAsked[id] {
		if a.m.usageAsked == nil {
			a.m.usageAsked = map[proto.SessionID]bool{}
		}
		a.m.usageAsked[id] = true
		a.host.MemoryUsage(id, func(u proto.ProfileUsage, err error) {
			if a.m.usage == nil {
				a.m.usage = map[proto.SessionID]proto.ProfileUsage{}
			}
			if err != nil {
				delete(a.m.usage, id)
				return
			}
			a.m.usage[id] = u
		})
	}
	u, ok := a.m.usage[id]
	return u, ok
}

// memoryBlock is the details' block on the memories a session used: a few
// of them and how many, or that it could not use any. It is left out while
// unknown, when the mode does not use memory, and when none were used.
func (a *App) memoryBlock(c *ui.Context, k theme.Colors, s proto.Session) detailGroup {
	u, ok := a.sessionUsage(s.ID)
	switch {
	case !ok || u.Hidden:
		return detailGroup{title: "记忆"}
	case u.Failed:
		return detailGroup{title: "记忆", end: func() { memoryErrorDot(c, k) }, body: func() {
			ui.Row(c).Height(theme.RowStep).Padding(0, theme.Space3).AlignItems(ui.Center).Children(func() {
				theme.UI.On(ui.Text(c, "本次没有用上记忆")).TextColor(k.Fg3).SingleLine()
			})
		}}
	case u.Count == 0:
		return detailGroup{title: "记忆"}
	}
	return detailGroup{title: "记忆", body: func() {
		for _, e := range u.Entries {
			r := memoryBlockRow(c.Key(e.ID), k, e.Title)
			if r.Clicked() {
				a.OpenMemory(e.ID)
			}
			r.Children(func() { theme.UI.On(ui.Text(c, e.Title)).TextColor(k.Fg).SingleLine().Grow(1).Shrink(1) })
		}
		line := "本次用了 " + strconv.Itoa(u.Count) + " 条"
		r := memoryBlockRow(c.Key("all"), k, line)
		if r.Clicked() {
			a.OpenMemory("")
		}
		r.Children(func() {
			theme.UI.On(ui.Text(c, line)).TextColor(k.Fg3).SingleLine().Grow(1).Shrink(1)
			ui.Icon(c, a.icons.Expand).FontSize(theme.IconSmall).TextColor(k.Faint)
		})
	}}
}

// memoryBlockRow is a link row of the block.
func memoryBlockRow(c *ui.Context, k theme.Colors, label string) ui.Element {
	r := ui.Row(c).Height(theme.RowStep).Padding(0, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).Label(label)
	if r.Hovered() {
		r.Background(k.Fill)
	}
	return r
}
