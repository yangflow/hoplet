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
	return e.Height(24).Padding(0, 8).Gap(6).AlignItems(ui.Center).Radius(6).
		Background(k.Raised).Border(0, ui.Transparent).Shadow(0, 0.5, 1.5, 0, k.Shadow)
}

// contextRow is a Code session's row above its composer: the machine,
// the working directory and the branch, as pills.
func (a *App) contextRow(c *ui.Context, k theme.Colors, s proto.Session) {
	ui.Row(c).Gap(6).Margin(0, 0, 8, 0).AlignItems(ui.Center).Children(func() {
		a.machinePill(c, k, false)
		dim := k.Fg2
		if !a.connected {
			dim = k.Faint
		}
		contextPill(ui.Row(c).Tooltip(s.Directory), k).Children(func() {
			ui.Image(c, a.icons.FolderType()).Size(theme.IconSizeInRow, theme.IconSizeInRow)
			ui.Text(c, tilde(s.Directory)).Font(theme.Mono).FontSize(11.5).TextColor(dim).SingleLine()
		})
		branch, repos := a.branch(s)
		if branch == "" {
			return
		}
		pill := contextPill(ui.Row(c), k)
		if repos > 1 {
			pill.Tooltip(fmt.Sprintf("%d 个仓库", repos))
		}
		pill.Children(func() {
			ui.Icon(c, a.icons.Task).FontSize(13).TextColor(dim)
			ui.Text(c, branch).Font(theme.Mono).FontSize(11.5).TextColor(dim).SingleLine()
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
		contextPill(ui.Row(c), k).Children(func() {
			ui.Icon(c, a.icons.Machine).FontSize(13).TextColor(k.Fg2)
			ui.Text(c, machine).FontSize(12).TextColor(k.Fg2).SingleLine()
			theme.Caption.On(ui.Text(c, "本机")).FontSize(10.5).TextColor(k.Muted).Padding(0, 4).Radius(4).Background(k.Fill)
		})
		return
	}
	status := "未连接"
	if !newPage {
		status = "断开 " + duration(c.Now().Sub(a.s.offlineSince))
		c.After(time.Second)
	}
	b := contextPill(ui.Button(c, "").Label("立即重连").Tooltip("立即重连"), k)
	if b.Clicked() {
		a.host.Reconnect()
	}
	b.Children(func() {
		ui.Icon(c, a.icons.Offline).FontSize(13).TextColor(k.Fg2)
		ui.Text(c, machine).FontSize(12).TextColor(k.Fg2).SingleLine()
		ui.Text(c, "· "+status).FontSize(12).TextColor(k.Faint).SingleLine()
		ui.Icon(c, a.icons.Reconnect).FontSize(12).TextColor(k.Fg3)
	})
}
