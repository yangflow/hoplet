package shell

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// The range menu's width and the most its commits take before they
// scroll; the side of a filter chip's ✕.
const (
	scopeMenuWidth   = 236
	commitListHeight = theme.Row * 10
	chipClose        = 20
)

// diffScope is what the changes tab compares: the session's baseline,
// HEAD or the default branch with now, or one commit of one repository
// with its parent.
type diffScope struct {
	rng    proto.DiffRange // empty is the session's
	commit proto.Commit
	repo   string // the commit's repository
}

// params asks for the scope's changes of a session.
func (sc diffScope) params(id proto.SessionID) proto.DiffParams {
	p := proto.DiffParams{Session: id, Range: sc.rng}
	if sc.rng == proto.DiffRangeCommit {
		p.Commit, p.Repo = sc.commit.SHA, sc.repo
	}
	return p
}

// session reports a scope of the session's baseline, whose file list is
// the conversation's too.
func (sc diffScope) session() bool { return sc.rng == "" || sc.rng == proto.DiffRangeSession }

// label is the scope as its chip says it.
func (sc diffScope) label() string {
	switch sc.rng {
	case proto.DiffRangeUncommitted:
		return "未提交的改动"
	case proto.DiffRangeBranch:
		return "分支改动"
	case proto.DiffRangeCommit:
		return "提交 " + shortSHA(sc.commit.SHA)
	}
	return "会话累计"
}

// shortSHA is a commit's id cut to seven characters.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// setScope makes the changes tab compare sc, asking for its changes
// again; an answer for the scope before is dropped.
func (ch *changesTab) setScope(sc diffScope) {
	ch.menu, ch.inCommits = false, false
	if sc == ch.scope {
		return
	}
	ch.scope = sc
	ch.gen++
	ch.sent, ch.waiting = false, false
	ch.diff, ch.built, ch.files = nil, nil, nil
	ch.only = ""
}

// setRepo makes the changes tab look at one repository, or with "" at
// all; leaving a commit's repository leaves the commit.
func (ch *changesTab) setRepo(repo string) {
	ch.repo = repo
	ch.only = ""
	if ch.scope.rng == proto.DiffRangeCommit && repo != "" && repo != ch.scope.repo {
		ch.setScope(diffScope{})
	}
}

// commitRepo is the repository whose commits the range menu lists: the
// one looked at, or the only one; "" when there are several and none is
// picked.
func (ch *changesTab) commitRepo() (string, bool) {
	if ch.repo != "" {
		return ch.repo, true
	}
	if ch.scope.rng == proto.DiffRangeCommit {
		return ch.scope.repo, true
	}
	if ch.diff != nil && len(ch.diff.Repos) == 1 {
		return ch.diff.Repos[0].Path, true
	}
	return "", false
}

// listCommits asks for the commits the range menu lists, and the
// default branch the branch range compares with.
func (a *App) listCommits(s proto.Session, ch *changesTab) {
	repo, _ := ch.commitRepo()
	gen := ch.gen
	ch.listing = true
	a.host.Commits(proto.CommitsParams{Session: s.ID, Repo: repo}, func(cs proto.SessionCommits, err error) {
		ch.listing = false
		if err != nil || gen != ch.gen {
			return
		}
		ch.commits = &cs
	})
}

// branchBase names what the branch range compares with: the default
// branch the repositories share, as "对比 main".
func (ch *changesTab) branchBase() string {
	base := ""
	if ch.commits != nil {
		for _, r := range ch.commits.Repos {
			switch {
			case r.Base == "":
			case base == "":
				base = r.Base
			case base != r.Base:
				return "对比默认分支"
			}
		}
	}
	if base == "" {
		return "对比默认分支"
	}
	return "对比 " + strings.TrimPrefix(base, "origin/")
}

// repoCommits are the commits listed for the repository the menu lists.
func (ch *changesTab) repoCommits() *proto.RepoCommits {
	repo, ok := ch.commitRepo()
	if !ok || ch.commits == nil {
		return nil
	}
	for i := range ch.commits.Repos {
		if r := &ch.commits.Repos[i]; r.Path == repo {
			return r
		}
	}
	return nil
}

// scopeChip is the changes tab's range: its name and ⌄, opening the
// range menu under it.
func (a *App) scopeChip(c *ui.Context, k theme.Colors, s proto.Session, ch *changesTab) {
	label := ch.scope.label()
	b := ui.Button(c.Key("scope"), "").Label("范围："+label).Height(theme.Row).Padding(0, theme.Space2).Gap(theme.Space1Half).
		Radius(theme.RadiusControl).Border(0, ui.Transparent).Background(ui.Transparent).Shrink(0).Transition(hoverMotion)
	fg := k.Fg2
	switch {
	case ch.menu:
		b.Background(k.Selected)
		fg = k.Fg
	case b.Hovered():
		b.Background(k.Hover)
	}
	if b.Clicked() {
		ch.menu, ch.inCommits, ch.query = !ch.menu, false, ""
		if ch.menu {
			a.listCommits(s, ch)
		}
	}
	b.Children(func() {
		theme.Secondary.On(ui.Text(c, label)).TextColor(fg).SingleLine()
		ui.Icon(c, a.icons.Menu).FontSize(theme.IconSmall).TextColor(k.Faint)
	})
	dropdown(c, k, b, &ch.menu, scopeMenuWidth, func() {
		if ch.inCommits {
			a.commitMenu(c, k, ch)
			return
		}
		a.scopeMenu(c, k, ch)
	})
}

// scopeMenu lists the ranges: the session's, HEAD's, the branch's, then
// the commits of the repository looked at.
func (a *App) scopeMenu(c *ui.Context, k theme.Colors, ch *changesTab) {
	for _, it := range []struct {
		rng         proto.DiffRange
		label, note string
	}{
		{proto.DiffRangeSession, "会话累计", "建会话以来"},
		{proto.DiffRangeUncommitted, "未提交的改动", "对比 HEAD"},
		{proto.DiffRangeBranch, "分支改动", ch.branchBase()},
	} {
		on := ch.scope.rng == it.rng || it.rng == proto.DiffRangeSession && ch.scope.session()
		r := menuItem(c.Key(it.rng), k, false).Label(it.label)
		if r.Clicked() {
			ch.setScope(diffScope{rng: it.rng})
		}
		r.Children(func() {
			theme.UI.On(ui.Text(c, it.label)).TextColor(k.Fg).SingleLine().Grow(1)
			theme.Caption.On(ui.Text(c, it.note)).TextColor(k.Muted).SingleLine()
			check(c, k, a, on)
		})
	}
	menuSeparator(c, k)
	if _, ok := ch.commitRepo(); !ok {
		ui.Row(c).MinHeight(theme.Row).Padding(theme.Space1, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Label("提交：先选一个仓库").Children(func() {
			theme.UI.On(ui.Text(c, "提交")).TextColor(k.Faint).Grow(1)
			theme.Caption.On(ui.Text(c, "先选一个仓库")).TextColor(k.Faint)
		})
		return
	}
	r := menuItem(c.Key("commits"), k, ch.scope.rng == proto.DiffRangeCommit).Label("提交")
	if r.Clicked() {
		ch.inCommits, ch.query = true, ""
	}
	r.Children(func() {
		theme.UI.On(ui.Text(c, "提交")).TextColor(k.Fg).Grow(1)
		if rc := ch.repoCommits(); rc != nil {
			n := strconv.Itoa(len(rc.Commits))
			if rc.Truncated {
				n += "+"
			}
			theme.Caption.On(ui.Text(c, n)).TextColor(k.Muted)
		}
		ui.Icon(c, a.icons.Expand).FontSize(theme.IconSmall).TextColor(k.Faint)
	})
}

// commitMenu is the range menu on the commits: a row back, a search and
// the commits, newest first; picking one shows its changes.
func (a *App) commitMenu(c *ui.Context, k theme.Colors, ch *changesTab) {
	back := menuItem(c.Key("back"), k, false).Label("返回范围")
	if back.Clicked() {
		ch.inCommits = false
	}
	back.Children(func() {
		ui.Icon(c, a.icons.Back).FontSize(theme.IconRow).TextColor(k.Fg3)
		theme.UI.On(ui.Text(c, "提交")).TextColor(k.Fg).Grow(1)
	})
	menuSeparator(c, k)
	ui.Box(c).Padding(0, theme.SpaceHalf, theme.SpaceHalf, theme.SpaceHalf).Children(func() {
		ui.SearchField(c, &ch.query).Placeholder("搜索提交").Label("搜索提交").AutoFocus().FillWidth().Height(theme.Row).
			FontSize(theme.Secondary.Size()).Radius(theme.RadiusControl).Background(k.Fill).Border(0, ui.Transparent)
	})
	repo, _ := ch.commitRepo()
	rc := ch.repoCommits()
	if rc == nil {
		return
	}
	ui.Scroll(c.Key("commits")).MaxHeight(commitListHeight).Scrollbars(false).Children(func() {
		ui.Column(c).Gap(theme.SpaceHalf).Children(func() {
			q := strings.ToLower(strings.TrimSpace(ch.query))
			for _, cm := range rc.Commits {
				if q != "" && !strings.Contains(strings.ToLower(cm.Subject), q) && !strings.HasPrefix(cm.SHA, q) {
					continue
				}
				on := ch.scope.rng == proto.DiffRangeCommit && ch.scope.commit.SHA == cm.SHA
				r := menuItem(c.Key(cm.SHA), k, on).Gap(theme.Space3).Label(cm.Subject)
				if r.Clicked() {
					ch.setScope(diffScope{rng: proto.DiffRangeCommit, commit: cm, repo: repo})
				}
				r.Children(func() {
					theme.UI.On(ui.Text(c, cm.Subject)).TextColor(k.Fg).SingleLine().Grow(1).Shrink(1)
					theme.CodeCaption.On(ui.Text(c, shortSHA(cm.SHA))).TextColor(k.Muted).Shrink(0)
				})
			}
		})
	})
}

// dropdown shows a panel under anchor while *open, in the popovers' look,
// its start on the anchor's.
func dropdown(c *ui.Context, k theme.Colors, anchor ui.Element, open *bool, width float32, fn func()) {
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		panel.AttachTo(anchor, ui.AnchorBottomStart, ui.AnchorTopStart).Margin(theme.SpaceHalf, 0, 0, 0).Width(width).Padding(theme.Space1).Gap(theme.SpaceHalf).
			Radius(theme.RadiusCard).Background(k.SurfaceFloat).Border(theme.LineWidth, k.Line2).Transition(popMotion)
		k.ShadowPopover.On(panel).Children(fn)
	})
}

// filterChip is a raised chip of the changes tab saying it looks at one
// repository or file; its ✕ looks at them all again.
func (a *App) filterChip(c *ui.Context, k theme.Colors, icon func(), label, clear string) bool {
	cleared := false
	chip := ui.Row(c).Height(theme.RowDense).Padding(0, theme.SpaceHalf, 0, theme.Space2).Gap(theme.Space1).AlignItems(ui.Center).
		Margin(0, 0, 0, theme.Space1).Radius(theme.RadiusRow).Background(k.Raised).Shrink(1)
	k.ShadowRaised.On(chip).Children(func() {
		icon()
		theme.Secondary.On(ui.Text(c, label)).TextColor(k.Fg).SingleLine().Shrink(1)
		x := ui.Button(c.Key(clear), "").Label(clear).Tooltip(clear).Size(chipClose, chipClose).Padding(0).
			Radius(theme.RadiusXS).Background(ui.Transparent).Border(0, ui.Transparent).Shrink(0).Transition(hoverMotion)
		if x.Hovered() {
			x.Background(k.Hover)
		}
		x.Children(func() { ui.Icon(c, a.icons.Close).FontSize(theme.IconSmall).TextColor(k.Muted) })
		cleared = x.Clicked()
	})
	return cleared
}
