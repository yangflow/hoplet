package shell

import (
	"bytes"
	"cmp"
	"encoding/csv"
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/yangflow/hoplet/proto"
)

// artifactSource is what a panel knows of one session's artifacts.
type artifactSource struct {
	items    []proto.Artifact
	asked    bool // asked since the panel was made
	answered bool
	busy     bool
	again    bool // something was published while the question was out
	status   proto.SessionStatus
}

// artifactList is a panel's artifacts: its session's and, for a companion,
// those of the sessions it delegated to.
type artifactList struct {
	sources map[proto.SessionID]*artifactSource
	items   []proto.Artifact // newest first
	views   map[proto.ArtifactID]*artifactView
}

// answered reports whether any source has answered, so that an empty list
// can say so.
func (l *artifactList) answered() bool {
	for _, src := range l.sources {
		if src.answered {
			return true
		}
	}
	return false
}

// artifactView is an open artifact tab: which version it shows and that
// version's content.
type artifactView struct {
	latest   proto.Artifact
	versions []proto.Artifact // newest first, once listed
	listed   bool
	version  int // the version chosen; 0 follows the latest

	loaded  int // the version the content below is, or is being read, for
	loading int // tells this read's answer from an older one's
	waiting bool
	err     string

	page  ui.NativeWebView
	ed    editor.State
	md    markdown.Options
	text  string
	img   ui.ImageSource
	rows  [][]string
	table ui.ListState
	menu  bool // the version menu shows
}

// showing is the version number the tab shows.
func (v *artifactView) showing() int {
	if v.version != 0 {
		return v.version
	}
	return v.latest.Version
}

// record returns the description of a version, the latest's when it is
// not listed.
func (v *artifactView) record(n int) proto.Artifact {
	if i := slices.IndexFunc(v.versions, func(a proto.Artifact) bool { return a.Version == n }); i >= 0 {
		return v.versions[i]
	}
	return v.latest
}

// choose shows a version; the latest one is followed from then on.
func (v *artifactView) choose(n int) {
	if n == v.latest.Version {
		n = 0
	}
	v.version = n
}

// clear forgets the content.
func (v *artifactView) clear() {
	v.page, v.text, v.img, v.rows, v.err = nil, "", nil, nil, ""
	v.ed = editor.State{}
}

// artifactSources are the sessions whose artifacts a panel lists.
func (a *App) artifactSources(s proto.Session) []proto.Session {
	out := []proto.Session{s}
	if s.Mode == proto.ModePersonal {
		out = append(out, a.cat.Children(s.ID)...)
	}
	return out
}

// sourceFor returns a list's record of a session, made on first use.
func (l *artifactList) sourceFor(id proto.SessionID) *artifactSource {
	if l.sources == nil {
		l.sources = map[proto.SessionID]*artifactSource{}
	}
	src, ok := l.sources[id]
	if !ok {
		src = &artifactSource{}
		l.sources[id] = src
	}
	return src
}

// syncArtifacts asks for what an open panel shows and has not got: the
// lists, again once a child's turn ended, and the open tabs' versions and
// content. Children publish outside the followed session's events.
func (a *App) syncArtifacts(s proto.Session, ps *panelState) {
	if !a.connected {
		return
	}
	shown := s.Mode == proto.ModePersonal || ps.column == columnArtifacts || slices.ContainsFunc(ps.tabs, func(t panelTab) bool { return t.artifact != "" })
	if !shown {
		return
	}
	l := &ps.arts
	for _, from := range a.artifactSources(s) {
		src := l.sourceFor(from.ID)
		ended := from.ID != s.ID && src.status == proto.StatusRunning && from.Status != proto.StatusRunning
		src.status = from.Status
		if !src.asked || ended {
			a.fetchArtifacts(ps, from.ID)
		}
	}
	for i, t := range ps.tabs {
		v := l.views[t.artifact]
		if t.artifact == "" || v == nil {
			continue
		}
		a.listVersions(t.artifact, v)
		if i == ps.active {
			a.loadArtifact(t.artifact, v)
		}
	}
}

// fetchArtifacts asks for one session's artifacts; asked while a question
// is out, it asks again after the answer.
func (a *App) fetchArtifacts(ps *panelState, id proto.SessionID) {
	src := ps.arts.sourceFor(id)
	if src.busy {
		src.again = true
		return
	}
	src.asked, src.busy = true, true
	a.host.Artifacts(id, func(items []proto.Artifact, err error) {
		src.busy = false
		switch {
		case err != nil:
			a.Fail(err, func() { a.fetchArtifacts(ps, id) })
		default:
			src.items, src.answered = items, true
			a.mergeArtifacts(ps)
		}
		if src.again {
			src.again = false
			a.fetchArtifacts(ps, id)
		}
	})
}

// mergeArtifacts rebuilds a panel's list from its sources, newest first,
// and brings the open tabs up to it.
func (a *App) mergeArtifacts(ps *panelState) {
	l := &ps.arts
	l.items = l.items[:0]
	for _, src := range l.sources {
		l.items = append(l.items, src.items...)
	}
	slices.SortFunc(l.items, func(x, y proto.Artifact) int {
		return cmp.Or(y.UpdatedAt.Compare(x.UpdatedAt), cmp.Compare(x.ID, y.ID))
	})
	for _, art := range l.items {
		v := l.views[art.ID]
		if v == nil {
			continue
		}
		if art.Version != v.latest.Version {
			v.listed, v.versions = false, nil
		}
		v.latest = art // a tab following the latest loads it when next drawn
	}
}

// artifactEvents refetches a followed session's list when it publishes. A
// snapshot may have skipped publications, so it refetches every source.
func (a *App) artifactEvents(id proto.SessionID, it proto.SessionItem) {
	ps, ok := a.s.panels[id]
	if !ok {
		return
	}
	if it.Snapshot != nil {
		for sid, src := range ps.arts.sources {
			if src.asked {
				a.fetchArtifacts(ps, sid)
			}
		}
		return
	}
	src := ps.arts.sources[id]
	if src != nil && src.asked && slices.ContainsFunc(it.Events, func(e proto.Event) bool { return e.Type == proto.TypeArtifactPublished }) {
		a.fetchArtifacts(ps, id)
	}
}

// openArtifact opens an artifact's tab, or shows it when it is open.
func (a *App) openArtifact(ps *panelState, art proto.Artifact) {
	if ps.arts.views == nil {
		ps.arts.views = map[proto.ArtifactID]*artifactView{}
	}
	if ps.arts.views[art.ID] == nil {
		ps.arts.views[art.ID] = &artifactView{latest: art}
	}
	a.openTab(ps, panelTab{artifact: art.ID})
}

// listVersions asks once per latest version for the versions of a tab.
func (a *App) listVersions(id proto.ArtifactID, v *artifactView) {
	if v.listed {
		return
	}
	v.listed = true
	a.host.ArtifactVersions(id, func(vs []proto.Artifact, err error) {
		if err != nil {
			a.Fail(err, nil)
			return
		}
		v.versions = vs
	})
}

// loadArtifact brings a tab's content to the version it shows.
func (a *App) loadArtifact(id proto.ArtifactID, v *artifactView) {
	n := v.showing()
	if n == 0 || v.loaded == n {
		return
	}
	v.clear()
	v.loaded, v.waiting = n, true
	v.loading++
	at := v.loading
	art := v.record(n)
	if art.Kind == proto.ArtifactHTML {
		page, err := a.host.ArtifactPage(id, n, func(err error) {
			if v.loaded == n {
				v.err = fmt.Sprintf("读不了 %s：%s", art.Entry, err)
			}
		})
		v.waiting = false
		if err != nil {
			v.err = err.Error()
			return
		}
		v.page = page
		return
	}
	a.host.ReadArtifact(proto.ArtifactsReadParams{Artifact: id, Version: n, Path: art.Entry}, func(data []byte, err error) {
		if at != v.loading {
			return // another version was asked for since
		}
		v.waiting = false
		if err == nil {
			err = v.setContent(art, data)
		}
		if err != nil {
			v.err = fmt.Sprintf("读不了 %s：%s", art.Entry, err)
		}
	})
}

// reloadArtifact reads what a tab shows again: retrying, or at the top
// row's button.
func (a *App) reloadArtifact(id proto.ArtifactID, v *artifactView) {
	if v.page != nil {
		v.err = ""
		a.host.ReloadArtifactPage(id, v.showing())
		return
	}
	v.loaded = 0
	a.loadArtifact(id, v)
}

// setContent prepares a read file for showing by its artifact's kind.
func (v *artifactView) setContent(art proto.Artifact, data []byte) error {
	switch art.Kind {
	case proto.ArtifactImage:
		img, err := ui.DecodeImage(data)
		if err != nil {
			return fmt.Errorf("shell: decode image: %w", err)
		}
		v.img = img
	case proto.ArtifactCSV:
		r := csv.NewReader(bytes.NewReader(data))
		r.FieldsPerRecord, r.LazyQuotes = -1, true
		rows, err := r.ReadAll()
		if err != nil {
			return fmt.Errorf("shell: parse csv: %w", err)
		}
		v.rows = rows
	case proto.ArtifactMarkdown:
		v.text = string(data)
	default:
		v.text = string(data)
		buf := editor.NewBuffer(v.text)
		if l := editor.LanguageFor(art.Entry); l != nil {
			buf.SetLanguage(l)
		}
		v.ed.Buffer = buf
	}
	return nil
}
