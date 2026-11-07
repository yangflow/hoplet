package shell

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// contextPill is a small raised pill of the context row above the
// composer: 24 high, rounded 6, on the raised fill with a light shadow.
func contextPill(e ui.Element, k theme.Colors) ui.Element {
	return k.ShadowRaised.On(e).Height(theme.RowDense).Padding(0, theme.Space2).Gap(theme.Space1Half).AlignItems(ui.Center).Radius(theme.RadiusRow).
		Background(k.Raised).Border(0, ui.Transparent)
}

// hoverPill gives a context pill that does something the hover tint over
// its raised fill under the pointer.
func hoverPill(e ui.Element, k theme.Colors) ui.Element {
	if e.Hovered() {
		e.Background(k.Hover.Over(k.Raised))
	}
	return e
}

// contextRow is a Code session's row above its composer: the machine,
// the working directory and the branch, as pills. When the row is short
// of room the machine name and the directory are cut short; the 本机
// mark and the branch stay whole. The directory shows in Finder; the
// branch opens the changes.
func (a *App) contextRow(c *ui.Context, k theme.Colors, s proto.Session) {
	ui.Row(c).Gap(theme.Space1Half).Margin(0, 0, theme.Space2, 0).AlignItems(ui.Center).Children(func() {
		a.machinePill(c, k, false)
		dim := k.Fg2
		if !a.connected {
			dim = k.Faint
		}
		dir := hoverPill(contextPill(ui.Button(c, "").Label("在 Finder 中显示").Tooltip(s.Directory), k), k).Shrink(1)
		if dir.Clicked() {
			a.host.Reveal(s.Directory)
		}
		dir.Children(func() {
			ui.Image(c, a.icons.FolderType()).Size(theme.IconRow, theme.IconRow)
			theme.CodeCaption.On(ui.Text(c, tilde(s.Directory))).TextColor(dim).SingleLine().Shrink(1)
		})
		branch, repos := a.branch(s)
		if branch == "" {
			return
		}
		pill := hoverPill(contextPill(ui.Button(c, "").Label("变更"), k), k).Shrink(0)
		if repos > 1 {
			pill.Tooltip(fmt.Sprintf("%d 个仓库", repos))
		}
		if pill.Clicked() {
			a.ShowChanges()
		}
		pill.Children(func() {
			ui.Icon(c, a.icons.Task).FontSize(theme.IconRow).TextColor(dim)
			theme.CodeCaption.On(ui.Text(c, branch)).TextColor(dim).SingleLine()
		})
	})
}

// branch is the session's branch, the top repository's if it has one,
// and how many repositories its directory holds, from its diff.
func (a *App) branch(s proto.Session) (string, int) {
	d := a.diff(s)
	if d == nil {
		return "", 0
	}
	var branch string
	var repos int
	for _, r := range d.Repos {
		if r.State == proto.RepoMissing {
			continue
		}
		repos++
		if branch == "" || r.Path == "." {
			branch = r.Branch
		}
	}
	return branch, repos
}

// machinePill is the machine the daemon runs on, marked 本机. Out of
// reach, it says how long it has been gone (or, on the new-session page,
// that it is not connected), and a click reconnects.
func (a *App) machinePill(c *ui.Context, k theme.Colors, newPage bool) {
	machine := a.machine
	if machine == "" {
		machine = "本机"
	}
	if a.connected {
		contextPill(ui.Row(c), k).Shrink(1).Children(func() {
			ui.Icon(c, a.icons.Machine).FontSize(theme.IconRow).TextColor(k.Fg2)
			ui.Text(c, machine).FontSize(theme.Secondary.Size()).TextColor(k.Fg2).SingleLine().Shrink(1)
			theme.Caption.On(ui.Text(c, "本机")).TextColor(k.Muted).Padding(0, theme.Space1).Radius(theme.RadiusXS).Background(k.Fill).SingleLine().Shrink(0)
		})
		return
	}
	status := "未连接"
	if !newPage {
		status = "断开 " + duration(c.Now().Sub(a.s.offlineSince))
		c.After(time.Second)
	}
	b := contextPill(ui.Button(c, "").Label("立即重连").Tooltip("立即重连"), k).Shrink(0)
	if b.Clicked() {
		a.host.Reconnect()
	}
	b.Children(func() {
		ui.Icon(c, a.icons.Offline).FontSize(theme.UI.Size()).TextColor(k.Fg2)
		ui.Text(c, machine).FontSize(theme.Secondary.Size()).TextColor(k.Fg2).SingleLine()
		ui.Text(c, "· "+status).FontSize(theme.Secondary.Size()).TextColor(k.Faint).SingleLine()
		ui.Icon(c, a.icons.Reconnect).FontSize(theme.Secondary.Size()).TextColor(k.Fg3)
	})
}
