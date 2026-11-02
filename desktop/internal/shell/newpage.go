package shell

import (
	"path/filepath"
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/catalog"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// newPage is the new-session page: a question and a composer whose first
// line chooses the harness and, for Code, the working directory; for
// Personal, the companion's name. Sending creates the session and sends
// the message.
func (a *App) newPage(c *ui.Context, k theme.Colors) {
	pick := &a.s.newPick
	if pick.harness == "" {
		pick.harness = a.defaultHarness()
	}
	personal := a.mode == proto.ModePersonal
	title := "在 hoplet 上做什么？"
	if personal {
		title = "新伙伴"
	}
	ui.Column(c).Grow(1).AlignItems(ui.Center).Padding(160, 32, 0, 32).Children(func() {
		ui.Column(c).Width(680).Gap(14).Children(func() {
			theme.Display.On(ui.Text(c, title)).TextColor(k.Fg)
			ui.Column(c).FillWidth().Padding(12, 14, 10, 14).Gap(10).Radius(theme.RadiusComposer).
				Background(k.Glass).Border(1, k.GlassLine).Shadow(0, 2, 10, 0, k.Shadow).Children(func() {
				ui.Row(c).Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
					a.harnessChip(c, k, pick)
					if personal {
						ui.TextInput(c, &pick.name).Placeholder("名字").Label("伙伴的名字").Width(160).Height(24).FontSize(12)
					} else {
						a.dirChip(c, k, pick)
					}
				})
				draft := a.s.drafts[""]
				hint := "要做什么？"
				if personal {
					hint = "和它说第一句话…"
				}
				in := ui.TextArea(c, &draft).Lines(1, 8).Placeholder(hint).Label("消息").AutoFocus().
					FontSize(theme.Reading.Size()).Background(ui.Transparent).Border(0, ui.Transparent).Padding(0)
				ready := draft != "" && pick.harness != "" && (personal || pick.dir != "")
				in.OnTextKey(func(key ui.TextKey) bool {
					if key.Key != ui.KeyEnter || key.Mods != 0 {
						return false
					}
					if ready {
						a.create(key.Text())
						key.SetText("")
					}
					return true
				})
				a.s.drafts[""] = draft
				ui.Row(c).Children(func() {
					ui.Spacer(c)
					if round(c, k, a.icons.Send, "发送 ↩", ready).Clicked() && ready {
						a.create(draft)
					}
				})
			})
		})
	})
}

// defaultHarness is the harness a new session starts with: Claude Code
// for Code, Hermes for Personal, else the first installed.
func (a *App) defaultHarness() string {
	want := "claude"
	if a.mode == proto.ModePersonal {
		want = "hermes"
	}
	if _, ok := a.harness(want); ok {
		return want
	}
	if len(a.s.harnesses) > 0 {
		return a.s.harnesses[0].Kind
	}
	return ""
}

// harnessChip shows the chosen harness and lists the installed ones.
func (a *App) harnessChip(c *ui.Context, k theme.Colors, pick *newChoice) {
	label := pick.harness
	if label == "" {
		label = "没有可用的 harness"
	}
	b := ui.Button(c, "").Label("harness").Height(24).Padding(0, 8).Gap(5).Radius(theme.RadiusControl).Border(1, k.Line).Background(k.Fill)
	b.Children(func() {
		a.harnessIcon(c, k, pick.harness, theme.IconSizeSmall)
		ui.Text(c, label).FontSize(12).TextColor(k.Fg3)
		ui.Icon(c, a.icons.Menu).FontSize(10).TextColor(k.Faint)
	})
	b.Menu(func(m *ui.Menu) {
		for _, h := range a.s.harnesses {
			if m.Item(h.Kind).Radio(h.Kind == pick.harness).Disabled(!h.Supported).Chosen() {
				pick.harness = h.Kind
			}
		}
	})
}

// dirChip shows the chosen working directory and lists the directories of
// existing sessions, then the folder chooser.
func (a *App) dirChip(c *ui.Context, k theme.Colors, pick *newChoice) {
	label := "选择工作目录"
	if pick.dir != "" {
		label = filepath.Base(pick.dir)
	}
	b := ui.Button(c, "").Label("工作目录").Tooltip(pick.dir).Height(24).Padding(0, 8).Gap(5).Radius(theme.RadiusControl).Border(1, k.Line).Background(k.Fill)
	b.Children(func() {
		ui.Icon(c, a.icons.Folder).FontSize(theme.IconSizeSmall).TextColor(k.Fg3)
		ui.Text(c, label).Font(theme.Mono).FontSize(12).TextColor(k.Fg3)
		ui.Icon(c, a.icons.Menu).FontSize(10).TextColor(k.Faint)
	})
	b.Menu(func(m *ui.Menu) {
		for _, dir := range a.recentDirs() {
			if m.Item(dir).Radio(dir == pick.dir).Chosen() {
				pick.dir = dir
			}
		}
		m.Separator()
		if m.Item("选择文件夹…").Chosen() {
			a.host.ChooseFolder(func(dir string) { pick.dir = dir })
		}
	})
}

// recentDirs are the working directories of Code sessions, most recently
// used first.
func (a *App) recentDirs() []string {
	var dirs []string
	for _, s := range all(a.cat.List(catalog.View{Mode: proto.ModeCode})) {
		if s.Directory != "" && !slices.Contains(dirs, s.Directory) {
			dirs = append(dirs, s.Directory)
		}
	}
	return dirs
}

// create creates the session the page describes and sends it the first
// message; the new session opens.
func (a *App) create(text string) {
	pick := a.s.newPick
	p := proto.CreateParams{Mode: a.mode, Harness: pick.harness}
	if a.mode == proto.ModeCode {
		p.Directory = pick.dir
	} else {
		p.Title = pick.name
	}
	a.s.drafts[""] = ""
	a.host.Create(p, func(s proto.Session) {
		a.cat.Apply(proto.CatalogItem{Version: a.cat.Version(), Sessions: []proto.Session{s}})
		a.Select(s.ID)
		a.send(s, text)
	})
}
