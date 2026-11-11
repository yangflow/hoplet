package shell

import (
	"cmp"
	"crypto/rand"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// openEntry opens id in place, or closes the one open when "".
func (a *App) openEntry(id string) {
	a.m.entry, a.m.versions, a.m.confirm, a.m.scopeOpen = id, false, false, false
	a.m.detail = proto.ProfileDetail{}
	a.loadEntry()
}

// loadEntry reads the open entry and its history; a candidate has what it
// shows already.
func (a *App) loadEntry() {
	a.m.detailLoad++
	n, id := a.m.detailLoad, a.m.entry
	if id == "" || a.isCandidate(id) {
		return
	}
	a.host.MemoryEntry(id, func(d proto.ProfileDetail, err error) {
		if n != a.m.detailLoad {
			return
		}
		if err != nil {
			a.Fail(err, a.loadEntry)
			return
		}
		a.m.detail = d
	})
}

func (a *App) isCandidate(id string) bool {
	return slices.ContainsFunc(a.m.data.Candidates, func(c proto.ProfileCandidate) bool { return c.Entry.ID == id })
}

// reloadMemory reads the list and the open entry again.
func (a *App) reloadMemory() {
	a.loadMemory()
	a.loadEntry()
}

// memoryOp is the operation ID of the write key names: made once, and kept
// until the write has an answer, so that a retry cannot apply it twice.
func (a *App) memoryOp(key string) string {
	if a.m.ops == nil {
		a.m.ops = map[string]string{}
	}
	if _, ok := a.m.ops[key]; !ok {
		a.m.ops[key] = "desktop-" + rand.Text()
	}
	return a.m.ops[key]
}

// memoryAnswered takes a write's answer and reports whether it went
// through. An entry changed meanwhile says so and reads it again; another
// failure can be retried under the same operation ID.
func (a *App) memoryAnswered(key string, err error, retry func()) bool {
	var api *proto.APIError
	switch {
	case err == nil:
		delete(a.m.ops, key)
		return true
	case errors.As(err, &api) && api.Code == proto.CodeConflict:
		delete(a.m.ops, key)
		a.m.edit.open = false
		a.hint("这条记忆刚被改过，已重新读取")
		a.reloadMemory()
		return false
	}
	a.Fail(err, retry)
	return false
}

// memoryWritten is memoryAnswered for a write the daemon can refuse.
func (a *App) memoryWritten(key string, r proto.ProfileWriteResult, err error, retry func()) bool {
	if !a.memoryAnswered(key, err, retry) {
		return false
	}
	if r.Outcome == "rejected" {
		a.hint("没有保存：" + r.Reason)
		return false
	}
	return true
}

// withdrawEntry withdraws e after the confirmation.
func (a *App) withdrawEntry(e proto.ProfileEntry) {
	key := fmt.Sprintf("withdraw\x00%s\x00%d", e.ID, e.Version)
	p := proto.ProfileWithdrawParams{ID: e.ID, ExpectedVersion: e.Version, Reason: "withdrawn by the user", OpID: a.memoryOp(key)}
	a.host.WithdrawMemory(p, func(r proto.ProfileWriteResult, err error) {
		if !a.memoryWritten(key, r, err, func() { a.withdrawEntry(e) }) {
			return
		}
		a.openEntry("")
		a.loadMemory()
	})
}

// unlinkEntry removes the link between id and other.
func (a *App) unlinkEntry(id, other string) {
	key := "unlink\x00" + id + "\x00" + other
	a.host.UnlinkMemory(proto.ProfileUnlinkParams{ID: id, Other: other, OpID: a.memoryOp(key)}, func(err error) {
		if a.memoryAnswered(key, err, func() { a.unlinkEntry(id, other) }) {
			a.reloadMemory()
		}
	})
}

// acceptCandidate carries out a candidate, in the scope chosen if one was.
func (a *App) acceptCandidate(cand proto.ProfileCandidate) {
	id := cand.Entry.ID
	var scope *proto.ProfileScope
	key := "accept\x00" + id
	if s, ok := a.m.picked[id]; ok && s != cand.Entry.Scope {
		scope = &s
		key += "\x00" + s.Kind + "\x00" + s.Value
	}
	a.host.AcceptMemory(proto.ProfileAcceptParams{ID: id, Scope: scope, OpID: a.memoryOp(key)}, func(_ proto.ProfileEntry, err error) {
		if a.memoryAnswered(key, err, func() { a.acceptCandidate(cand) }) {
			a.candidateDecided(id)
		}
	})
}

// rejectCandidate turns a candidate down.
func (a *App) rejectCandidate(cand proto.ProfileCandidate) {
	id := cand.Entry.ID
	key := "reject\x00" + id
	a.host.RejectMemory(proto.ProfileRejectParams{ID: id, OpID: a.memoryOp(key)}, func(_ proto.ProfileEntry, err error) {
		if a.memoryAnswered(key, err, func() { a.rejectCandidate(cand) }) {
			a.candidateDecided(id)
		}
	})
}

func (a *App) candidateDecided(id string) {
	delete(a.m.picked, id)
	a.openEntry("")
	a.loadMemory()
}

// entryDetail is an open entry: its title and actions, what it says, what
// it relates to and replaced, how often it was used and its versions.
func (a *App) entryDetail(c *ui.Context, k theme.Colors, e proto.ProfileEntry) {
	ui.Row(c).FillWidth().Height(theme.Row).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		head := ui.Row(c.Key("title")).Grow(1).Shrink(1).Height(theme.Row).AlignItems(ui.Center).Label("收起")
		if head.Clicked() {
			a.openEntry("")
		}
		head.Children(func() { theme.UIMedium.On(ui.Text(c, e.Title)).TextColor(k.Fg).SingleLine() })
		if iconButton(c, k, a.icons.Edit, "编辑", false).Clicked() {
			a.openMemoryEdit(e)
		}
		withdraw := iconButton(c, k, a.icons.Withdraw, "撤回", a.m.confirm)
		if withdraw.Clicked() {
			a.m.confirm = !a.m.confirm
		}
		popoverBelow(c, k, withdraw, &a.m.confirm, true, theme.MenuWidth, theme.Space3, func() {
			ui.Column(c).FillWidth().Gap(theme.Space2).Children(func() {
				theme.UI.On(ui.Text(c, "新启动的会话不再用这条记忆")).TextColor(k.Fg)
				ui.Row(c).Gap(theme.Space2).Justify(ui.End).Children(func() {
					if secondaryButton(c, k, "取消").Clicked() {
						a.m.confirm = false
					}
					if primaryButton(c.Key("confirm"), k, "撤回").Label("确认撤回").Clicked() {
						a.m.confirm = false
						a.withdrawEntry(e)
					}
				})
			})
		})
	})
	theme.Reading.On(ui.Text(c, e.Body)).TextColor(k.Fg)
	a.entryLinks(c, k, e)
	a.entryReplaced(c, k, e)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		theme.Caption.On(ui.Text(c, "用过 "+strconv.Itoa(e.Uses)+" 次")).TextColor(k.Muted).Grow(1)
		v := ui.Row(c.Key("versions")).Gap(theme.Space1).AlignItems(ui.Center).Label("版本")
		if v.Clicked() {
			a.m.versions = !a.m.versions
		}
		v.Children(func() {
			ui.Textf(c, "v%d", e.Version).Font(theme.Mono).FontSize(theme.Caption.Size()).TextColor(k.Muted)
			icon := a.icons.Expand
			if a.m.versions {
				icon = a.icons.Menu
			}
			ui.Icon(c, icon).FontSize(theme.IconSmall).TextColor(k.Faint)
		})
	})
	if a.m.versions {
		ui.Column(c.Key("history")).FillWidth().Gap(theme.Space2).Children(func() {
			for _, v := range a.m.detail.History {
				a.entryVersion(c.Key(v.Version), k, v)
			}
		})
	}
}

// entryLinks lists the entries related to e; each leads there, and the
// pointer brings out a button removing the link.
func (a *App) entryLinks(c *ui.Context, k theme.Colors, e proto.ProfileEntry) {
	if len(e.Links) == 0 {
		return
	}
	ui.Column(c.Key("links")).FillWidth().Gap(theme.SpaceHalf).Children(func() {
		theme.Label.On(ui.Text(c, "相关")).TextColor(k.Muted)
		for _, id := range e.Links {
			other, ok := a.memoryEntry(id)
			if !ok {
				continue
			}
			row := ui.Row(c.Key(id)).FillWidth().Height(theme.Row).AlignItems(ui.Center)
			row.Children(func() {
				link := ui.Row(c.Key("go")).Grow(1).Shrink(1).Height(theme.Row).AlignItems(ui.Center).Label(other.Title)
				if link.Clicked() {
					a.openEntry(id)
				}
				link.Children(func() { theme.UI.On(ui.Text(c, other.Title)).TextColor(k.AccentFg).SingleLine() })
				x := iconButton(c, k, a.icons.Close, "移除关联", false)
				if !row.Hovered() && !x.Focused() {
					x.Opacity(0)
				}
				if x.Clicked() {
					a.unlinkEntry(e.ID, id)
				}
			})
		}
	})
}

// entryReplaced lists the old entries e took the place of.
func (a *App) entryReplaced(c *ui.Context, k theme.Colors, e proto.ProfileEntry) {
	var old []proto.ProfileEntry
	for _, x := range a.m.data.Entries {
		if x.ReplacedBy == e.ID {
			old = append(old, x)
		}
	}
	if len(old) == 0 {
		return
	}
	ui.Column(c.Key("replaced")).FillWidth().Gap(theme.SpaceHalf).Children(func() {
		theme.Label.On(ui.Text(c, "替代了")).TextColor(k.Muted)
		for _, x := range old {
			ui.Row(c.Key(x.ID)).FillWidth().Height(theme.Row).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
				theme.UI.On(ui.Text(c, x.Title)).TextColor(k.Muted).Strikethrough().SingleLine().Grow(1).Shrink(1)
				theme.Caption.On(ui.Text(c, memoryWhen(c.Now(), x.UpdatedAt))).TextColor(k.Muted).Shrink(0)
			})
		}
	})
}

// entryVersion is one version: its number, its source, when, and what it
// said.
func (a *App) entryVersion(c *ui.Context, k theme.Colors, v proto.ProfileVersion) {
	ui.Column(c).FillWidth().Gap(theme.SpaceHalf).Children(func() {
		ui.Row(c).FillWidth().Gap(theme.Space1Half).AlignItems(ui.Center).Children(func() {
			ui.Textf(c, "v%d", v.Version).Font(theme.Mono).FontSize(theme.Caption.Size()).TextColor(k.Muted).Shrink(0)
			src, linked := evidenceSession(v.Evidence)
			who := versionSource(v)
			s, known := a.cat.Get(src)
			if who != "" {
				if linked && known && v.Actor != "user" {
					who += " ·"
				}
				theme.Secondary.On(ui.Text(c, who)).TextColor(k.Muted).Shrink(0)
			}
			if linked && known && v.Actor != "user" {
				a.sourceLink(c, k, s)
			}
			ui.Box(c).Grow(1)
			theme.Caption.On(ui.Text(c, memoryWhen(c.Now(), v.At))).TextColor(k.Muted).Shrink(0)
		})
		theme.Secondary.On(ui.Text(c, v.Body)).TextColor(k.Fg2)
	})
}

// versionSource says who a version came from, when it was me.
func versionSource(v proto.ProfileVersion) string {
	if v.Actor == "user" {
		return "你编辑"
	}
	if slices.ContainsFunc(v.Evidence, func(e proto.ProfileEvidence) bool { return e.Kind == "user_stated" }) {
		return "你说的"
	}
	return ""
}

// evidenceSession finds the session behind a piece of evidence.
func evidenceSession(evidence []proto.ProfileEvidence) (proto.SessionID, bool) {
	for _, e := range evidence {
		if rest, ok := strings.CutPrefix(e.Ref, "event:"); ok {
			if i := strings.LastIndex(rest, "/"); i > 0 {
				return proto.SessionID(rest[:i]), true
			}
		}
	}
	return "", false
}

// sourceLink is a link to the session a memory came from.
func (a *App) sourceLink(c *ui.Context, k theme.Colors, s proto.Session) {
	l := ui.Row(c.Key("source")).Shrink(1).AlignItems(ui.Center).Label(name(s))
	if l.Clicked() {
		a.Select(s.ID)
	}
	l.Children(func() { theme.Secondary.On(ui.Text(c, name(s))).TextColor(k.AccentFg).SingleLine() })
}

// candidateTitle is what a candidate is called: a proposal to withdraw
// names the entry it would withdraw.
func (a *App) candidateTitle(cand proto.ProfileCandidate) string {
	if t, ok := a.memoryEntry(cand.Target); ok && cand.Action == actionWithdraw {
		return t.Title
	}
	return cand.Entry.Title
}

// candidateDetail is an open candidate: what it proposes, where it came
// from, its scope and the decision.
func (a *App) candidateDetail(c *ui.Context, k theme.Colors, cand proto.ProfileCandidate) {
	head := ui.Row(c.Key("title")).FillWidth().AlignItems(ui.Center).Label("收起")
	if head.Clicked() {
		a.openEntry("")
	}
	head.Children(func() { theme.UIMedium.On(ui.Text(c, a.candidateTitle(cand))).TextColor(k.Fg).SingleLine() })
	retire := cand.Action == actionWithdraw
	if t, ok := a.memoryEntry(cand.Target); ok && cand.Action == actionUpdate {
		ui.Column(c.Key("now")).FillWidth().Gap(theme.SpaceHalf).Children(func() {
			theme.Label.On(ui.Text(c, "现在")).TextColor(k.Muted)
			theme.Reading.On(ui.Text(c, t.Body)).TextColor(k.Muted)
		})
	}
	if !retire {
		theme.Reading.On(ui.Text(c, cand.Entry.Body)).TextColor(k.Fg)
	}
	ui.Row(c.Key("note")).FillWidth().Gap(theme.Space1Half).AlignItems(ui.Center).Children(func() {
		theme.Secondary.On(ui.Text(c, proposalNote(cand.Action)+" ·")).TextColor(k.Muted).Shrink(0)
		if id, ok := evidenceSession(cand.Entry.Evidence); ok {
			if s, known := a.cat.Get(id); known {
				a.sourceLink(c, k, s)
				theme.Secondary.On(ui.Text(c, "·")).TextColor(k.Muted).Shrink(0)
			}
		}
		theme.Secondary.On(ui.Text(c, memoryWhen(c.Now(), cand.CreatedAt))).TextColor(k.Muted).Shrink(0)
	})
	ui.Row(c.Key("decide")).FillWidth().Height(theme.Row).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		if !retire {
			cur := cand.Entry.Scope
			if s, ok := a.m.picked[cand.Entry.ID]; ok {
				cur = s
			}
			a.scopeControl(c, k, cur, cand.Scopes, &a.m.scopeOpen, func(s proto.ProfileScope) {
				if a.m.picked == nil {
					a.m.picked = map[string]proto.ProfileScope{}
				}
				a.m.picked[cand.Entry.ID] = s
			})
		}
		ui.Box(c).Grow(1)
		if secondaryButton(c, k, "拒绝").Clicked() {
			a.rejectCandidate(cand)
		}
		ok := "确认"
		if retire {
			ok = "撤回"
		}
		if primaryButton(c, k, ok).Clicked() {
			a.acceptCandidate(cand)
		}
	})
}

// proposalNote says what a candidate proposes.
func proposalNote(action string) string {
	switch action {
	case actionUpdate:
		return "提议修改"
	case actionWithdraw:
		return "提议撤回"
	}
	return "提议新增"
}

// memoryWhen is a date in a memory's details: today with the time, 昨天,
// else the date.
func memoryWhen(now, t time.Time) string {
	now, t = now.Local(), t.Local()
	switch dayLabel(now, t) {
	case "今天":
		return t.Format("今天 15:04")
	case "昨天":
		return "昨天"
	}
	return t.Format("1 月 2 日")
}

// scopeOrder is the order scopes are listed in.
func scopeOrder(kind string) int {
	switch kind {
	case "global":
		return 0
	case "companion":
		return 1
	case "repo":
		return 2
	}
	return 3
}

// scopeName is how a scope is written: a companion by its name, a
// repository by its last path element, a host as it is.
func (a *App) scopeName(s proto.ProfileScope) string {
	switch s.Kind {
	case "global":
		return "全局"
	case "companion":
		for _, co := range a.s.companions {
			if string(co.ID) != s.Value {
				continue
			}
			if co.Name != "" {
				return co.Name
			}
			if m, ok := a.cat.Get(co.Main); ok {
				return name(m)
			}
		}
		return "伙伴"
	case "repo":
		return path.Base(s.Value)
	}
	return s.Value
}

// scopeKindName is the word at a scope menu row's end.
func scopeKindName(kind string) string {
	switch kind {
	case "companion":
		return "伙伴"
	case "repo":
		return "仓库"
	case "host":
		return "主机"
	}
	return ""
}

func (a *App) scopeIcon(kind string) *ui.SVG {
	switch kind {
	case "companion":
		return a.icons.Me
	case "repo":
		return a.icons.Repos
	case "host":
		return a.icons.Machine
	}
	return a.icons.Web
}

// scopeControl is a scope's dropdown, listing the scopes this memory can
// have.
func (a *App) scopeControl(c *ui.Context, k theme.Colors, cur proto.ProfileScope, scopes []proto.ProfileScope, open *bool, pick func(proto.ProfileScope)) {
	label := a.scopeName(cur)
	b := ui.Button(c.Key("scope"), "").Label("范围："+label).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space1Half).
		Radius(theme.RadiusControl).Background(ui.Transparent).Border(theme.LineWidth, k.Line).Transition(hoverMotion)
	if *open || b.Hovered() {
		b.Background(k.Hover)
	}
	if b.Clicked() {
		*open = !*open
	}
	b.Children(func() {
		ui.Icon(c, a.scopeIcon(cur.Kind)).FontSize(theme.IconRow).TextColor(k.Fg3)
		theme.UI.On(ui.Text(c, label)).TextColor(k.Fg).SingleLine()
		ui.Icon(c, a.icons.Menu).FontSize(theme.IconSmall).TextColor(k.Faint)
	})
	sorted := slices.Clone(scopes)
	slices.SortStableFunc(sorted, func(x, y proto.ProfileScope) int {
		return cmp.Or(cmp.Compare(scopeOrder(x.Kind), scopeOrder(y.Kind)), cmp.Compare(x.Value, y.Value))
	})
	popoverBelow(c, k, b, open, false, theme.MenuWidth, theme.Space1, func() {
		for _, s := range sorted {
			on := s == cur
			name := a.scopeName(s)
			r := menuItem(c.Key(s), k, on).Label(name)
			if r.Clicked() {
				pick(s)
				*open = false
			}
			r.Children(func() {
				ui.Icon(c, a.scopeIcon(s.Kind)).FontSize(theme.Icon).TextColor(k.Fg3)
				theme.UI.On(ui.Text(c, name)).TextColor(k.Fg).SingleLine().Grow(1).Shrink(1)
				if note := scopeKindName(s.Kind); note != "" {
					theme.Caption.On(ui.Text(c, note)).TextColor(k.Muted).Shrink(0)
				}
				check(c, k, a, on)
			})
		}
	})
}

// popoverBelow shows a panel under anchor while *open, in the look of the
// design's small floating cards; end aligns it with the anchor's end.
func popoverBelow(c *ui.Context, k theme.Colors, anchor ui.Element, open *bool, end bool, width, padding float32, fn func()) {
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		at, self := ui.AnchorBottomStart, ui.AnchorTopStart
		if end {
			at, self = ui.AnchorBottomEnd, ui.AnchorTopEnd
		}
		panel.AttachTo(anchor, at, self).Margin(theme.Space1, 0, 0, 0).Width(width).Padding(padding).Gap(theme.SpaceHalf).
			Radius(theme.RadiusCard).Background(k.SurfaceFloat).Border(theme.LineWidth, k.Line2).Transition(popMotion)
		k.ShadowPopover.On(panel).Children(fn)
	})
}
