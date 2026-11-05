package shell

import (
	"path/filepath"
	"slices"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/catalog"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// newPage is the new-session page: a question over the same composer as
// a session's. Code has the context row above it, where the working
// directory is chosen; Personal has the companion's name in the box. The
// bottom row has the model and effort, where the harness is chosen with
// its model, and the permission mode. Sending creates the session and
// sends the message.
//
// Code has three states: no directory yet, where the directory pill
// stands out and the recent directories wait below the composer; a
// directory chosen; and offline, where the machine pill says so and the
// box is dashed until the connection is back, the choices kept.
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
	width := float32(theme.CodeColumn)
	if personal {
		width = theme.PersonalColumn
	}
	ui.Column(c).Grow(1).Children(func() {
		// The title in the middle of the room above; the composer at the
		// bottom, where a session has it.
		ui.Box(c).Grow(1).Center().Padding(theme.Space8).Children(func() {
			theme.Display.On(ui.Text(c, title)).TextColor(k.Fg).TextAlign(ui.Center)
		})
		ui.Row(c).FillWidth().Justify(ui.Center).Padding(0, 0, theme.Space4, 0).Children(func() {
			ui.Column(c).Grow(1).Shrink(1).MaxWidth(width-2*theme.Space8).Margin(0, theme.Space4).Gap(theme.Space2).Children(func() {
				if !personal {
					// The same context row as a session's, where the
					// directory is chosen; the branch comes once it exists.
					ui.Row(c).Gap(theme.Space1Half).AlignItems(ui.Center).Children(func() {
						a.machinePill(c, k, true)
						if a.connected { // the choice waits, hidden, for the connection
							a.dirChip(c, k, pick)
						}
					})
				}
				box := ui.Column(c).FillWidth().Padding(theme.Space3, theme.Space3, theme.Space2).Gap(theme.Space2).Radius(theme.RadiusComposer).
					Background(k.Surface).Border(theme.LineWidth, k.Line2)
				k.ShadowComposer.On(box)
				if !a.connected {
					// As the composer's offline state: I can write, but which
					// harnesses and models there are is known only once
					// connected.
					box.Border(theme.LineWidth, k.Line2).BorderStyle(ui.BorderDashed)
				}
				box.Children(func() {
					if personal {
						ui.TextInput(c, &pick.name).Placeholder("名字").Label("伙伴的名字").Width(theme.FieldShort).Height(theme.RowGroup).FontSize(theme.Secondary.Size()).
							Padding(0, theme.Space2).Radius(theme.RadiusControl).Border(0, ui.Transparent).Background(k.Fill)
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
					in := ui.TextArea(c, &draft).Lines(2, 8).MinHeight(0).Placeholder(hint).Label("消息").AutoFocus().
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
					ui.Row(c).Gap(theme.SpaceHalf).AlignItems(ui.Center).Children(func() {
						set := a.newSink()
						a.runButton(c, k, pick.run, a.s.harnesses, set)
						if !personal {
							a.modeControl(c, k, pick.run, h.Capabilities, set)
						}
						ui.Spacer(c)
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
	})
}

// recentLine offers the last three working directories below the
// composer, while none is chosen; one click chooses one.
func (a *App) recentLine(c *ui.Context, k theme.Colors, pick *newChoice) {
	dirs := a.recentDirs()
	if len(dirs) == 0 {
		return
	}
	ui.Row(c).Padding(0, theme.Space3).Gap(theme.Space1Half).Wrap().AlignItems(ui.Center).Children(func() {
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
		perm:      func(m proto.PermissionMode) { r.perm = m },
		plan:      func(on bool) { r.plan = on },
		effort:    func(e proto.Effort) { r.effort = e },
		fast:      func(on bool) { r.fast = on },
		ultracode: func(on bool) { r.ultracode = on },
		model: func(kind string, m proto.ModelInfo) {
			if kind != r.harness {
				r.effort, r.fast, r.ultracode = "", false, false
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

// dirChip is the context row's directory pill on the new-session page: the
// chosen working directory, or 选择工作目录 in the text color before one
// is chosen; it opens the directories of existing sessions and the folder
// chooser.
func (a *App) dirChip(c *ui.Context, k theme.Colors, pick *newChoice) {
	b := contextPill(ui.Button(c, "").Label("工作目录").Tooltip(pick.dir), k)
	label, color := tilde(pick.dir), k.Fg2
	if pick.dir == "" {
		label, color = "选择工作目录", k.Fg
	}
	if b.Clicked() {
		a.s.dirOpen = !a.s.dirOpen
	}
	b.Children(func() {
		ui.Image(c, a.icons.FolderType()).Size(theme.IconRow, theme.IconRow)
		text := ui.Text(c, label).FontSize(theme.Secondary.Size()).TextColor(color)
		if pick.dir != "" {
			theme.CodeCaption.On(text)
		}
		ui.Icon(c, a.icons.Menu).FontSize(theme.Caption.Size()).TextColor(k.Faint)
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
				ui.Icon(c, a.icons.Folder).FontSize(theme.IconRow).TextColor(k.Fg3)
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
			ui.Icon(c, a.icons.New).FontSize(theme.IconRow).TextColor(k.Fg3)
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
		Effort: r.effort, Fast: r.fast, Ultracode: r.ultracode, Model: r.model, ModelProvider: r.provider}
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
