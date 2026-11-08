package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// personaEditorHeight is the persona editor's fixed height; longer text
// scrolls inside it.
const personaEditorHeight = 240

// personaEdit is the edit-persona dialog.
type personaEdit struct {
	open   bool
	who    proto.Companion
	text   string
	saving bool
}

// loadCompanions lists the companions.
func (a *App) loadCompanions() {
	a.host.Companions(func(cs []proto.Companion, err error) {
		if err != nil {
			a.Fail(err, a.loadCompanions)
			return
		}
		for _, co := range cs {
			a.s.companions[co.Main] = co
		}
	})
}

// findCompanions lists the companions again when a catalog update brings a
// main session not known yet.
func (a *App) findCompanions(ss []proto.Session) {
	found := false
	for _, s := range ss {
		if _, ok := a.s.companions[s.ID]; ok || s.Mode != proto.ModePersonal || s.Parent != "" || a.s.companionsIn[s.ID] {
			continue
		}
		a.s.companionsIn[s.ID], found = true, true
	}
	if found && a.connected {
		a.loadCompanions()
	}
}

// persona returns a companion's persona as last read, reading it on first
// use.
func (a *App) persona(id proto.CompanionID) string {
	if !a.s.personaAsked[id] && a.connected {
		a.s.personaAsked[id] = true
		a.host.Persona(id, func(text string, err error) {
			if err != nil {
				a.Fail(err, nil)
				return
			}
			a.s.personas[id] = text
		})
	}
	return a.s.personas[id]
}

// editPersona reads the whole persona afresh and opens it for editing.
func (a *App) editPersona(co proto.Companion) {
	a.host.Persona(co.ID, func(text string, err error) {
		if err != nil {
			a.Fail(err, nil)
			return
		}
		a.s.personas[co.ID] = text
		a.s.personaEdit = personaEdit{open: true, who: co, text: text}
	})
}

// personaApplies is when a changed persona should take effect: a main
// session that has not run yet reads it when it first starts.
func (a *App) personaApplies(co proto.Companion) proto.PersonaApplies {
	if s, ok := a.cat.Get(co.Main); ok && s.Harness.NativeID != "" {
		return proto.AppliesNewMainSession
	}
	return proto.AppliesNextLaunch
}

// personaNote says when a changed persona takes effect.
func personaNote(applies proto.PersonaApplies) string {
	if applies == proto.AppliesNewMainSession {
		return "保存后，新的主会话才会用上"
	}
	return "保存后，下次启动时用上"
}

// savePersona saves the dialog's text once. It closes when saved and stays
// when not; the daemon's answer on when it applies has the last word.
func (a *App) savePersona() {
	d := &a.s.personaEdit
	if d.saving {
		return
	}
	d.saving = true
	id, text, shown := d.who.ID, d.text, a.personaApplies(d.who)
	a.host.SetPersona(id, text, func(r proto.SetPersonaResult, err error) {
		d.saving = false
		if err != nil {
			a.Fail(err, nil)
			return
		}
		a.s.personas[id] = text
		d.open = false
		if r.Applies != shown {
			a.hint(personaNote(r.Applies))
		}
	})
}

// personaDialog is the edit-persona dialog in the middle of the window.
func (a *App) personaDialog(c *ui.Context, k theme.Colors) {
	d := &a.s.personaEdit
	ui.DialogBase(c, &d.open, func(_, panel ui.Element) {
		panel.Width(theme.DialogWidth).Padding(theme.Space4).Gap(theme.Space3).Radius(theme.RadiusPanel).Background(k.SurfaceFloat).Border(theme.LineWidth, k.Line2).Label("编辑人格").
			Transition(ui.ElementTransition{Animation: &theme.MotionBase, Enter: &ui.Motion{Scale: theme.MotionScaleDialog}, Exit: &ui.Motion{}})
		k.ShadowPopover.On(panel)
		panel.Children(func() {
			ui.Column(c).Gap(theme.SpaceHalf).Children(func() {
				theme.Title.On(ui.Text(c, "编辑人格")).TextColor(k.Fg)
				who := "伙伴"
				if s, ok := a.cat.Get(d.who.Main); ok {
					who = name(s)
				}
				theme.Secondary.On(ui.Text(c, who+" · 它每次启动都会先读这段话")).TextColor(k.Muted)
			})
			ui.TextArea(c, &d.text).Label("人格").AutoFocus().FillWidth().Height(personaEditorHeight)
			ui.Row(c).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, a.icons.Details).FontSize(theme.IconRow).TextColor(k.Fg3)
				theme.Secondary.On(ui.Text(c, personaNote(a.personaApplies(d.who)))).TextColor(k.Muted).Grow(1).Shrink(1)
				cancel := theme.UI.On(ui.Button(c, "取消")).Height(theme.Row).Padding(0, theme.Space3).Radius(theme.RadiusControl).Background(k.Fill).Border(0, ui.Transparent).TextColor(k.Fg)
				if cancel.Clicked() {
					d.open = false
				}
				save := theme.UI.On(ui.Button(c, "保存")).Height(theme.Row).Padding(0, theme.Space3).Radius(theme.RadiusControl).Background(k.Fg).Border(0, ui.Transparent).TextColor(c.Theme().InverseText)
				if save.Clicked() {
					a.savePersona()
				}
			})
		})
	})
}
