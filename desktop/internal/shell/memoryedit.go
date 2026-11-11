package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// memoryEditorHeight is the height of the edit dialog's text.
const memoryEditorHeight = 120

// memoryEdit is the edit dialog.
type memoryEdit struct {
	open   bool
	entry  proto.ProfileEntry // as the dialog found it
	text   string
	scope  proto.ProfileScope
	scopes []proto.ProfileScope
	menu   bool // its scope menu shows
	saving bool
}

// openMemoryEdit opens the edit dialog on e. Until the entry's details have
// been read its scope can only stay or go global.
func (a *App) openMemoryEdit(e proto.ProfileEntry) {
	scopes := a.m.detail.Scopes
	if len(scopes) == 0 {
		scopes = []proto.ProfileScope{{Kind: "global"}}
		if e.Scope.Kind != "global" {
			scopes = append(scopes, e.Scope)
		}
	}
	a.m.edit = memoryEdit{open: true, entry: e, text: e.Body, scope: e.Scope, scopes: scopes}
}

// saveMemoryEdit saves the dialog's text and scope; nothing changed just
// closes it.
func (a *App) saveMemoryEdit() {
	d := &a.m.edit
	if d.saving {
		return
	}
	var scope *proto.ProfileScope
	key := "update\x00" + d.entry.ID + "\x00" + d.text
	if d.scope != d.entry.Scope {
		s := d.scope
		scope = &s
		key += "\x00" + s.Kind + "\x00" + s.Value
	}
	if d.text == d.entry.Body && scope == nil {
		d.open = false
		return
	}
	d.saving = true
	p := proto.ProfileUpdateParams{ID: d.entry.ID, ExpectedVersion: d.entry.Version, Body: d.text, Scope: scope, OpID: a.memoryOp(key)}
	a.host.UpdateMemory(p, func(r proto.ProfileWriteResult, err error) {
		d.saving = false
		if !a.memoryWritten(key, r, err, a.saveMemoryEdit) {
			return
		}
		d.open = false
		a.reloadMemory()
	})
}

// memoryEditDialog is the edit dialog in the middle of the window.
func (a *App) memoryEditDialog(c *ui.Context, k theme.Colors) {
	d := &a.m.edit
	if !d.open {
		return
	}
	ui.DialogBase(c, &d.open, func(_, panel ui.Element) {
		panel.Width(theme.DialogWidth).Padding(theme.Space4).Gap(theme.Space3).Radius(theme.RadiusPanel).Background(k.SurfaceFloat).Border(theme.LineWidth, k.Line2).Label("编辑记忆").
			Transition(ui.ElementTransition{Animation: &theme.MotionBase, Enter: &ui.Motion{Scale: theme.MotionScaleDialog}, Exit: &ui.Motion{}})
		k.ShadowPopover.On(panel)
		panel.Children(func() {
			ui.Column(c).Gap(theme.SpaceHalf).Children(func() {
				theme.UIStrong.On(ui.Text(c, "编辑记忆")).TextColor(k.Fg)
				theme.Secondary.On(ui.Text(c, "保存后，新启动的会话才会用上")).TextColor(k.Muted)
			})
			theme.Reading.On(ui.TextArea(c, &d.text).Label("正文").AutoFocus().FillWidth().Height(memoryEditorHeight))
			ui.Row(c).FillWidth().Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
				a.scopeControl(c, k, d.scope, d.scopes, &d.menu, func(s proto.ProfileScope) { d.scope = s })
				ui.Box(c).Grow(1)
				if secondaryButton(c, k, "取消").Clicked() {
					d.open = false
				}
				if primaryButton(c, k, "保存").Clicked() {
					a.saveMemoryEdit()
				}
			})
		})
	})
}
