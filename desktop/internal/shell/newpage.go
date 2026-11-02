package shell

import (
	"path/filepath"
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/catalog"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// newPage is the new-session page: a question over a composer whose first
// line holds the working directory (Code) or the companion's name
// (Personal); the bottom row has the permission mode and the effort and
// model pill, where the harness is chosen with its model. Sending creates
// the session and sends the message.
func (a *App) newPage(c *ui.Context, k theme.Colors) {
	pick := &a.s.newPick
	if pick.run.harness == "" {
		pick.run.harness = a.defaultHarness()
	}
	personal := a.mode == proto.ModePersonal
	title := "在 Hoplet 上做什么？"
	if personal {
		title = "新伙伴"
	}
	h, _ := a.harness(pick.run.harness)
	ui.Column(c).Grow(1).AlignItems(ui.Center).Padding(160, 32, 0, 32).Children(func() {
		ui.Column(c).Width(680).Gap(14).Children(func() {
			theme.Display.On(ui.Text(c, title)).TextColor(k.Fg)
			ui.Column(c).FillWidth().Padding(12, 14, 10, 14).Gap(10).Radius(theme.RadiusComposer).
				Background(k.Glass).Border(1, k.GlassLine).Shadow(0, 2, 10, 0, k.Shadow).Children(func() {
				ui.Row(c).Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
					if personal {
						ui.TextInput(c, &pick.name).Placeholder("名字").Label("伙伴的名字").Width(160).Height(24).FontSize(12).
							Padding(0, 8).Radius(theme.RadiusControl).Border(1, k.Line).Background(k.Fill)
					} else {
						a.dirChip(c, k, pick)
					}
				})
				draft := a.s.drafts[""]
				hint := "要做什么？"
				if personal {
					hint = "和它说第一句话…"
				}
				in := ui.TextArea(c, &draft).Lines(1, 8).MinHeight(0).Placeholder(hint).Label("消息").AutoFocus().
					FontSize(theme.Reading.Size()).Background(ui.Transparent).Border(0, ui.Transparent).Padding(0)
				ready := draft != "" && pick.run.harness != "" && (personal || pick.dir != "")
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
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					set := a.newSink()
					if !personal {
						a.modeControl(c, k, pick.run, h.Capabilities, set)
					}
					ui.Spacer(c)
					if !a.connected {
						theme.Secondary.On(ui.Text(c, "未连接")).TextColor(k.Muted)
					} else {
						a.effortPill(c, k, pick.run, a.s.harnesses, set)
					}
					if round(c, k, a.icons.Send, "发送 ↩", ready).Clicked() && ready {
						a.create(draft)
					}
				})
			})
		})
	})
}

// newSink records the new-session page's choices; choosing a model of
// another harness switches to that harness.
func (a *App) newSink() settingsSink {
	r := &a.s.newPick.run
	return settingsSink{
		perm:   func(m proto.PermissionMode) { r.perm = m },
		plan:   func(on bool) { r.plan = on },
		effort: func(e proto.Effort) { r.effort = e },
		fast:   func(on bool) { r.fast = on },
		model: func(kind string, m proto.ModelInfo) {
			if kind != r.harness {
				r.effort, r.fast = "", false
			}
			r.harness, r.model, r.provider = kind, m.ID, m.Provider
		},
	}
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

// dirChip shows the chosen working directory; it opens the directories of
// existing sessions and the folder chooser.
func (a *App) dirChip(c *ui.Context, k theme.Colors, pick *newChoice) {
	label := "选择工作目录"
	if pick.dir != "" {
		label = filepath.Base(pick.dir)
	}
	b := ui.Button(c, "").Label("工作目录").Tooltip(pick.dir).Height(24).Padding(0, 8).Gap(5).
		Radius(theme.RadiusControl).Border(1, k.Line).Background(k.Fill)
	if b.Clicked() {
		a.s.dirOpen = !a.s.dirOpen
	}
	b.Children(func() {
		ui.Icon(c, a.icons.Folder).FontSize(theme.IconSizeSmall).TextColor(k.Fg3)
		ui.Text(c, label).Font(theme.Mono).FontSize(12).TextColor(k.Fg3)
		ui.Icon(c, a.icons.Menu).FontSize(10).TextColor(k.Faint)
	})
	popover(c, k, b, &a.s.dirOpen, false, 320, func() {
		dirs := a.recentDirs()
		if len(dirs) > 0 {
			menuHeader(c, k, "最近用过")
		}
		for _, dir := range dirs {
			r := menuItem(c.Key(dir), k, dir == pick.dir).Label(dir).Tooltip(dir)
			if r.Clicked() {
				pick.dir, a.s.dirOpen = dir, false
			}
			r.Children(func() {
				ui.Icon(c, a.icons.Folder).FontSize(theme.IconSizeInRow).TextColor(k.Fg3)
				theme.UI.On(ui.Text(c, filepath.Base(dir))).TextColor(k.Fg)
				theme.Caption.On(ui.Text(c, filepath.Dir(dir))).TextColor(k.Faint).SingleLine().Shrink(1)
			})
		}
		if len(dirs) > 0 {
			menuSeparator(c, k)
		}
		r := menuItem(c, k, false).Label("选择文件夹…")
		if r.Clicked() {
			a.s.dirOpen = false
			a.host.ChooseFolder(func(dir string) { pick.dir = dir })
		}
		r.Children(func() {
			ui.Icon(c, a.icons.New).FontSize(theme.IconSizeInRow).TextColor(k.Fg3)
			theme.UI.On(ui.Text(c, "选择文件夹…")).TextColor(k.Fg)
		})
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

// create creates the session the page describes, with its settings, and
// sends it the first message; the new session opens.
func (a *App) create(text string) {
	pick := a.s.newPick
	r := pick.run
	p := proto.CreateParams{Mode: a.mode, Harness: pick.run.harness, Permission: r.perm, Plan: r.plan,
		Effort: r.effort, Fast: r.fast, Model: r.model, ModelProvider: r.provider}
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
