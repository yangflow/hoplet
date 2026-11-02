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
//
// Code has three states: no directory yet, where the directory chip
// stands out and the recent directories wait below the composer; a
// directory chosen; and offline, where the offline line replaces the
// first line until the connection is back, the choices kept.
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
	ui.Column(c).Grow(1).AlignItems(ui.Center).Padding(200, 32, 0, 32).Children(func() {
		ui.Column(c).Width(680).Gap(14).Children(func() {
			theme.Display.On(ui.Text(c, title)).TextColor(k.Fg)
			box := ui.Column(c).FillWidth().Padding(12, 14, 10, 14).Gap(10).Radius(theme.RadiusComposer).
				Background(k.Glass).Border(1, k.GlassLine).Shadow(0, 2, 10, 0, k.Shadow)
			if !a.connected {
				// As the composer's offline state: I can write, but which
				// harnesses and models there are is known only once
				// connected.
				box.Border(1, k.Line2).BorderStyle(ui.BorderDashed)
			}
			box.Children(func() {
				switch {
				case !a.connected:
					a.offlineLine(c, k, true)
				case personal:
					ui.TextInput(c, &pick.name).Placeholder("名字").Label("伙伴的名字").Width(160).Height(26).FontSize(12).
						Padding(0, 8).Radius(theme.RadiusControl).Border(1, k.Line).Background(k.Bg)
				default:
					ui.Row(c).Gap(6).Wrap().AlignItems(ui.Center).Children(func() { a.dirChip(c, k, pick) })
				}
				draft := a.s.drafts[""]
				hint := "要做什么？"
				switch {
				case !a.connected:
					hint = "连接后才能新建会话"
				case personal:
					hint = "和它说第一句话…"
				case pick.dir == "":
					hint = "先选工作目录"
				}
				in := ui.TextArea(c, &draft).Lines(1, 8).MinHeight(0).Placeholder(hint).Label("消息").AutoFocus().
					FontSize(theme.Reading.Size()).Background(ui.Transparent).Border(0, ui.Transparent).Padding(0)
				ready := a.connected && draft != "" && pick.run.harness != "" && (personal || pick.dir != "")
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
					a.effortPill(c, k, pick.run, a.s.harnesses, set)
					if round(c, k, a.icons.Send, "发送 ↩", ready).Clicked() && ready {
						a.create(draft)
					}
				})
			})
			if !personal && a.connected && pick.dir == "" {
				a.recentLine(c, k, pick)
			}
		})
	})
}

// recentLine offers the last three working directories below the
// composer, while none is chosen; one click chooses one.
func (a *App) recentLine(c *ui.Context, k theme.Colors, pick *newChoice) {
	dirs := a.recentDirs()
	if len(dirs) == 0 {
		return
	}
	ui.Row(c).Padding(0, 14).Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
		theme.Caption.On(ui.Text(c, "最近：")).TextColor(k.Muted)
		for i, dir := range dirs[:min(len(dirs), 3)] {
			if i > 0 {
				theme.Caption.On(ui.Text(c, "·")).TextColor(k.Faint)
			}
			r := ui.Row(c.Key(dir)).Label(dir).Tooltip(dir)
			color := k.Muted
			if r.Hovered() {
				color = k.Fg
			}
			if r.Clicked() {
				pick.dir = dir
			}
			r.Children(func() {
				ui.Text(c, tilde(dir)).Font(theme.Mono).FontSize(theme.Caption.Size()).TextColor(color)
			})
		}
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
// existing sessions and the folder chooser. Before one is chosen it stands
// out, as the first thing to do.
func (a *App) dirChip(c *ui.Context, k theme.Colors, pick *newChoice) {
	b := ui.Button(c, "").Label("工作目录").Tooltip(pick.dir).Height(26).Padding(0, 8).Gap(5).
		Radius(theme.RadiusControl).Border(1, k.Line).Background(k.Bg)
	label, color := tilde(pick.dir), k.Fg3
	if pick.dir == "" {
		label, color = "选择工作目录", k.Fg
		b.Border(1, k.Line2)
	}
	if b.Clicked() {
		a.s.dirOpen = !a.s.dirOpen
	}
	b.Children(func() {
		ui.Icon(c, a.icons.Folder).FontSize(13).TextColor(color)
		text := theme.Secondary.On(ui.Text(c, label)).TextColor(color)
		if pick.dir != "" {
			text.Font(theme.Mono)
		}
		ui.Icon(c, a.icons.Menu).FontSize(11).TextColor(k.Faint)
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
		r := menuItem(c, k, false).Label("选择其他目录…")
		if r.Clicked() {
			a.s.dirOpen = false
			a.host.ChooseFolder(func(dir string) { pick.dir = dir })
		}
		r.Children(func() {
			ui.Icon(c, a.icons.New).FontSize(theme.IconSizeInRow).TextColor(k.Fg3)
			theme.UI.On(ui.Text(c, "选择其他目录…")).TextColor(k.Fg)
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
