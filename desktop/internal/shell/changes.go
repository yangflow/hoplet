package shell

import (
	"path"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// changesTab is a session's changes as the right panel shows them: the
// diff with its patches as last read, a view of each changed file, and
// what I folded, chose and asked to see.
type changesTab struct {
	diff    *proto.SessionDiff
	sent    bool   // a question went out
	waiting bool   // and has no answer yet
	asked   uint64 // the conversation's last change when it did
	gen     int    // counts the scopes asked for; an answer for one before is dropped

	scope     diffScope             // what the tab compares
	menu      bool                  // the range menu shows
	inCommits bool                  // on its commits
	query     string                // the commits' search
	commits   *proto.SessionCommits // as the menu last listed them
	listing   bool                  // a listing went out

	repo string // the one repository the tab looks at, "" for all
	only string // the one file it looks at, "" for all
	flat bool   // the column lists files without their directories

	built  map[string]string      // repository path → the SHA its files were made from
	files  map[string]*fileChange // by path relative to the working directory
	folded map[string]bool        // files whose lines are put away
	closed map[string]bool        // directories closed in the column
	split  bool                   // old and new side by side
	scroll ui.ScrollState         // the changes tab's place
	chosen string                 // the row the column's keys are on
	target string                 // the file to bring to the top once laid out
	heads  map[string]*ui.Handle  // each file's heading, to scroll to
}

// fileChange is one changed file's lines, made from its patch.
type fileChange struct {
	diff  *editor.DiffState // nil when no lines show
	hunks []editor.PatchHunk
	note  string // why no lines show

	whole   bool         // the diff is of the file's whole texts, not its patch
	loading bool         // they are being read
	reveal  int          // the old line whose unchanged lines show once whole, -1 for none
	opened  map[int]bool // the runs of unchanged lines shown, by first old line
}

// patches returns a session's changes with their patches, asking again
// once its files may have changed since the last ask; nil until the first
// answer.
func (a *App) patches(s proto.Session, ps *panelState) *proto.SessionDiff {
	ch := &ps.ch
	at := a.conv(s.ID).Changed()
	if a.connected && s.Directory != "" && !ch.waiting && (!ch.sent || at > ch.asked) {
		ch.sent, ch.waiting, ch.asked = true, true, at
		gen, sc := ch.gen, ch.scope
		a.host.DiffPatch(sc.params(s.ID), func(d proto.SessionDiff, err error) {
			if gen != ch.gen {
				return // for a scope since left
			}
			ch.waiting = false
			if err != nil {
				return // the next change asks again
			}
			ch.take(d)
			// The session's file list is the conversation's too.
			if st, ok := a.s.diffs[s.ID]; ok && sc.session() {
				st.diff = &d
			}
		})
	}
	return ch.diff
}

// take keeps a new read of the changes, making again only the files of
// repositories whose SHA moved.
func (ch *changesTab) take(d proto.SessionDiff) {
	if ch.files == nil {
		ch.built, ch.files = map[string]string{}, map[string]*fileChange{}
	}
	seen := map[string]bool{}
	for _, r := range d.Repos {
		seen[r.Path] = true
		if sha, ok := ch.built[r.Path]; ok && sha == r.SHA && r.SHA != "" {
			continue
		}
		ch.built[r.Path] = r.SHA
		ch.drop(r.Path)
		byName := map[string]editor.FilePatch{}
		if fps, err := editor.ParsePatch(r.Patch); err == nil {
			for _, fp := range fps {
				name := fp.NewName
				if name == "" {
					name = fp.OldName
				}
				byName[name] = fp
			}
		}
		for _, f := range r.Files {
			ch.files[inRepo(r.Path, f.Path)] = makeFileChange(f, byName, r.Truncated)
		}
	}
	for repo := range ch.built {
		if !seen[repo] {
			delete(ch.built, repo)
			ch.drop(repo)
		}
	}
	ch.diff = &d
}

// drop forgets the files made for a repository.
func (ch *changesTab) drop(repo string) {
	for key := range ch.files {
		if repo == "." || repo == "" || key == repo || len(key) > len(repo) && key[:len(repo)+1] == repo+"/" {
			delete(ch.files, key)
		}
	}
}

// makeFileChange makes a file's lines from its patch, or says why there
// are none: a binary file, one a filter stores, or a patch cut short.
func makeFileChange(f proto.FileDiff, byName map[string]editor.FilePatch, truncated bool) *fileChange {
	switch {
	case f.Binary:
		return &fileChange{note: "二进制文件，不显示改动"}
	case f.Filter != "":
		return &fileChange{note: "由 " + f.Filter + " 过滤器存放，不显示改动"}
	}
	fp, ok := byName[f.Path]
	if !ok || len(fp.Hunks) == 0 {
		if truncated {
			return &fileChange{note: "改动太多，这里没有列出"}
		}
		return &fileChange{note: "没有改动的行"}
	}
	st := &editor.DiffState{FitContent: true, Indicators: editor.DiffBars, Layout: editor.DiffUnified,
		Font: ui.Font{Family: theme.Mono, Size: theme.Code.Size(), Features: "liga=0"}, LineHeight: theme.Code.Line() / theme.Code.Size()}
	st.SetPatch(fp)
	return &fileChange{diff: st, hunks: fp.Hunks, reveal: -1}
}

// codeTheme colors code on the pane: its text, line numbers and the
// changes' bars and tints from the theme's tokens, the rest of the
// editor's own.
func codeTheme(c *ui.Context, k theme.Colors) *editor.Theme {
	ct := editor.DefaultTheme(c.Theme())
	ct.Background, ct.Text = k.Pane, k.Fg
	ct.LineNumber, ct.ActiveLineNumber = k.Faint, k.Muted
	ct.Added, ct.Deleted = k.Add, k.Del
	return ct
}

// showChange opens the changes tab on a file, from the column or a
// change card: the panel opens, the file unfolds and comes to the top.
func (a *App) showChange(s proto.Session, key string) {
	ps := a.panelFor(s.ID)
	a.openTab(ps, panelTab{changes: true})
	ps.ch.chosen, ps.ch.target = key, key
	// A file the filters leave out brings back all of them.
	if ps.ch.only != "" && ps.ch.only != key {
		ps.ch.only = ""
	}
	if r := ps.ch.repo; r != "" && r != "." && key != r && !strings.HasPrefix(key, r+"/") {
		ps.ch.setRepo("")
	}
	if ps.ch.folded != nil {
		delete(ps.ch.folded, key)
	}
	if !a.panelOpen || a.fit.panel == 0 {
		a.bringPanel()
	}
}

// changesColumn is the panel's column on the changes: how many files and
// lines, then each repository with its files by directory, or a line
// saying why there are none. ↑↓ move between rows, ← → close and open a
// directory, ↩ opens a file.
func (a *App) changesColumn(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState) {
	d := a.patches(s, ps)
	if d == nil {
		ui.Box(c).Grow(1)
		return
	}
	if note := changesNote(d); note != "" {
		ui.Column(c).Grow(1).Padding(theme.Space4, theme.Space3).Children(func() {
			theme.SecondaryBlock.On(ui.Text(c, note)).TextColor(k.Muted)
		})
		return
	}
	ch := &ps.ch
	rows := changeList(d, s.Directory, ch.closed, ch.flat)
	files, add, del := changeTotals(d)
	col := ui.Scroll(c.Key("changes")).Grow(1).Focusable().FocusRing(false).Label("变更")
	a.changeKeys(col, s, ps, rows)
	col.Children(func() {
		ui.Column(c).Padding(0, theme.Space1).Gap(theme.Space2).Children(func() {
			ui.Row(c).Padding(0, 0, 0, theme.Space2).Gap(theme.Space1).AlignItems(ui.Center).Children(func() {
				theme.Caption.On(ui.Textf(c, "%d 个文件 ·", files)).TextColor(k.Muted)
				lineCounts(c, k, add, del)
				ui.Spacer(c)
				// The icon is the look it switches to.
				icon, tip := a.icons.Flat, "平铺显示"
				if ch.flat {
					icon, tip = a.icons.Nested, "按目录显示"
				}
				if denseButton(c, k, icon, tip, false).Clicked() {
					ch.flat = !ch.flat
				}
			})
			ui.Column(c).Children(func() {
				for i, row := range rows {
					if row.kind == changeRepo && i > 0 {
						ui.Box(c).Height(theme.RowDense)
					}
					a.changeRowView(c.Key(row.key), k, s, ps, row, len(d.Repos) > 1)
				}
			})
		})
	})
}

// changeKeys lets the column's rows be chosen and opened from the
// keyboard.
func (a *App) changeKeys(col ui.Element, s proto.Session, ps *panelState, rows []changeRow) {
	ch := &ps.ch
	at := -1
	var pick []changeRow // the rows the keys go between: directories and files
	for _, r := range rows {
		if r.kind == changeDir || r.kind == changeFile {
			if r.key == ch.chosen {
				at = len(pick)
			}
			pick = append(pick, r)
		}
	}
	if len(pick) == 0 {
		return
	}
	move := func(i int) { ch.chosen = pick[min(max(i, 0), len(pick)-1)].key }
	switch {
	case col.Shortcut(0, ui.KeyDown):
		move(at + 1)
	case col.Shortcut(0, ui.KeyUp):
		if at < 0 {
			at = len(pick)
		}
		move(at - 1)
	case at >= 0 && col.Shortcut(0, ui.KeyRight):
		if r := pick[at]; r.kind == changeDir {
			delete(ch.closed, r.key)
		}
	case at >= 0 && col.Shortcut(0, ui.KeyLeft):
		if r := pick[at]; r.kind == changeDir {
			ch.close(r.key)
		}
	case at >= 0 && col.Shortcut(0, ui.KeyEnter):
		if r := pick[at]; r.kind == changeFile {
			a.showChange(s, r.key)
		}
	}
}

// close closes a directory of the column.
func (ch *changesTab) close(key string) {
	if ch.closed == nil {
		ch.closed = map[string]bool{}
	}
	ch.closed[key] = true
}

// changeRowView draws a row of the changes column.
func (a *App) changeRowView(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState, row changeRow, repos bool) {
	ch := &ps.ch
	indent := func() {
		for range row.depth {
			ui.Box(c).Width(theme.Icon).Shrink(0)
		}
	}
	switch row.kind {
	case changeRepo:
		head := ui.Row(c).Height(theme.RowGroup).Padding(0, theme.Space2).Gap(theme.Space1Half).AlignItems(ui.Center).Radius(theme.RadiusRow)
		// With more than one, a repository with changes is looked at
		// alone from its heading.
		if repos && !row.clean && repoNote(*row.repo) == "" {
			head.Label(row.label).Transition(hoverMotion)
			switch {
			case ch.repo == row.key:
				head.Background(k.Selected)
			case head.Hovered():
				head.Background(k.Hover)
			}
			if head.Clicked() {
				if ch.repo == row.key {
					ch.setRepo("")
				} else {
					ch.setRepo(row.key)
				}
			}
		}
		head.Children(func() {
			ui.Icon(c, a.icons.Repos).FontSize(theme.IconRow).TextColor(k.Fg3)
			theme.UIMedium.On(ui.Text(c, row.label)).TextColor(k.Fg).SingleLine().Shrink(0)
			if row.repo.Branch != "" {
				ui.Icon(c, a.icons.Task).FontSize(theme.IconSmall).TextColor(k.Muted)
				theme.CodeCaption.On(ui.Text(c, row.repo.Branch)).TextColor(k.Muted).SingleLine().Shrink(1)
			}
			ui.Spacer(c)
			if row.clean && repoNote(*row.repo) == "" {
				theme.Caption.On(ui.Text(c, "无改动")).TextColor(k.Muted)
			} else if !row.clean {
				lineCounts(c, k, row.add, row.del)
			}
		})
		return
	case changeNote:
		ui.Row(c).Height(theme.RowDense).Padding(0, theme.Space2).Gap(theme.Space1Half).AlignItems(ui.Center).Children(func() {
			indent()
			theme.Secondary.On(ui.Text(c, row.label)).TextColor(k.Muted).SingleLine().Shrink(1)
		})
		return
	}
	r := ui.Row(c).Height(theme.RowDense).Padding(0, theme.Space2).Gap(theme.Space1Half).AlignItems(ui.Center).
		Radius(theme.RadiusRow).Label(row.key).Transition(hoverMotion)
	switch {
	case row.key == ch.chosen:
		r.Background(k.Selected)
	case r.Hovered():
		r.Background(k.Hover)
	}
	r.Children(func() {
		indent()
		if row.kind == changeDir {
			chev := a.icons.Expand
			if row.open {
				chev = a.icons.Menu
			}
			ui.Icon(c, chev).FontSize(theme.IconSmall).TextColor(k.Faint)
			ui.Image(c, a.icons.FolderType()).Size(theme.IconRow, theme.IconRow)
			theme.UI.On(ui.Text(c, row.label)).TextColor(k.Fg).SingleLine().Shrink(1)
			return
		}
		mark := statusMark(row.file.Status)
		theme.CodeCaption.On(ui.Text(c, mark)).TextColor(markColor(k, mark)).Width(theme.IconSmall).Shrink(0)
		name := theme.Secondary.On(ui.Text(c, row.label)).TextColor(k.Fg).SingleLine().Shrink(1)
		if mark == "D" {
			name.Strikethrough()
		}
		theme.Secondary.On(ui.Text(c, row.dir)).TextColor(k.Muted).SingleLine().Shrink(1).Grow(1)
		lineCounts(c, k, row.add, row.del)
	})
	if row.kind == changeFile && r.DoubleClicked() {
		// The changes tab looks at this file alone.
		a.showChange(s, row.key)
		ch.only = row.key
		return
	}
	if r.Clicked() {
		ch.chosen = row.key
		if row.kind == changeDir {
			if row.open {
				ch.close(row.key)
			} else {
				delete(ch.closed, row.key)
			}
			return
		}
		a.showChange(s, row.key)
	}
}

// markColor colors a status letter: added green, deleted red, the others
// a neutral gray.
func markColor(k theme.Colors, mark string) ui.Color {
	switch mark {
	case "A":
		return k.Add
	case "D":
		return k.Del
	}
	return k.Muted
}

// lineCounts shows lines added and removed, +24 −9, in code captions;
// either is left out when there is none.
func lineCounts(c *ui.Context, k theme.Colors, add, del int) {
	ui.Row(c).Gap(theme.Space1).Shrink(0).Children(func() {
		if add > 0 {
			theme.CodeCaption.On(ui.Textf(c, "+%d", add)).TextColor(k.Add)
		}
		if del > 0 {
			theme.CodeCaption.On(ui.Textf(c, "−%d", del)).TextColor(k.Del)
		}
	})
}

// changesView is the changes tab: a line with how many files, folding
// them all and side by side; then every changed file, its heading over
// its lines, one after the other in the column's order.
func (a *App) changesView(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState) {
	ch := &ps.ch
	d := a.patches(s, ps)
	files := ch.shown(d, s.Directory)
	allFolded := len(files) > 0
	for _, f := range files {
		allFolded = allFolded && ch.folded[f.key]
	}
	ui.Row(c).Height(theme.RowStep).Padding(0, theme.Space1).Gap(theme.SpaceHalf).AlignItems(ui.Center).Background(k.Pane).Children(func() {
		a.scopeChip(c, k, s, ch)
		theme.Secondary.On(ui.Textf(c, "· %d 个文件", len(files))).TextColor(k.Muted).SingleLine().Shrink(0)
		if ch.repo != "" && d != nil && len(d.Repos) > 1 {
			label := ch.repo
			for _, r := range d.Repos {
				if r.Path == ch.repo {
					label = repoLabel(r, s.Directory)
				}
			}
			repoIcon := func() { ui.Icon(c, a.icons.Repos).FontSize(theme.IconRow).TextColor(k.Fg3) }
			if a.filterChip(c, k, repoIcon, label, "显示全部仓库") {
				ch.setRepo("")
			}
		}
		if ch.only != "" {
			name := path.Base(ch.only)
			fileIcon := func() {
				if img := a.icons.FileType(name); img != nil {
					ui.Image(c, img).Size(theme.IconRow, theme.IconRow)
				}
			}
			if a.filterChip(c, k, fileIcon, name, "显示全部文件") {
				ch.only = ""
			}
		}
		ui.Spacer(c)
		fold, tip := a.icons.Fold, "全部折叠"
		if allFolded {
			fold, tip = a.icons.Unfold, "全部展开"
		}
		if denseButton(c, k, fold, tip, false).Clicked() {
			ch.folded = map[string]bool{}
			if !allFolded {
				for _, f := range files {
					ch.folded[f.key] = true
				}
			}
		}
		if denseButton(c, k, a.icons.SideBySide, "左右对比", ch.split).Clicked() {
			ch.split = !ch.split
		}
	})
	if ch.heads == nil {
		ch.heads = map[string]*ui.Handle{}
	}
	ct := codeTheme(c, k)
	// The editors lay their own pane, which their tints mix with; the
	// rest of the tab lays the pane under itself, so that the pane is
	// never laid twice.
	// The list is as tall as its files, up to the tab's height; the pane
	// fills what it leaves below.
	ui.Scroll(c.Key("diffs")).Shrink(1).MinHeight(0).Scrollbars(false).TrackScroll(&ch.scroll).Children(func() {
		for _, f := range files {
			a.fileChangeView(c.Key(f.key), k, s, ps, f, ct)
		}
	})
	ui.Box(c).Grow(1).Background(k.Pane)
	a.scrollToTarget(c, ch)
}

// shown lists the files the changes tab shows, in the column's order:
// those of the repository and the file it looks at, or all.
func (ch *changesTab) shown(d *proto.SessionDiff, dir string) []changeRow {
	var out []changeRow
	for _, row := range changeList(d, dir, nil, ch.flat) {
		if row.kind != changeFile || ch.repo != "" && row.repo.Path != ch.repo || ch.only != "" && row.key != ch.only {
			continue
		}
		out = append(out, row)
	}
	return out
}

// scrollToTarget brings the file the column chose to the top of the tab
// once it is laid out.
func (a *App) scrollToTarget(c *ui.Context, ch *changesTab) {
	if ch.target == "" {
		return
	}
	h, ok := ch.heads[ch.target]
	if !ok {
		ch.target = ""
		return
	}
	at, top := h.Bounds(c), ch.scroll.Bounds(c)
	if at.H == 0 || top.H == 0 {
		c.AnimationFrame() // laid out in the next frame
		return
	}
	ch.scroll.ScrollTo(0, ch.scroll.Y+at.Y-top.Y, theme.MotionBase)
	ch.target = ""
}

// fileChangeView draws a changed file in the changes tab: its heading,
// then its lines or why none show.
func (a *App) fileChangeView(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState, row changeRow, ct *editor.Theme) {
	ch := &ps.ch
	fc := ch.files[row.key]
	folded := ch.folded[row.key]
	h, ok := ch.heads[row.key]
	if !ok {
		h = &ui.Handle{}
		ch.heads[row.key] = h
	}
	chev, tip := a.icons.Menu, "折叠 "+row.label
	if folded {
		chev, tip = a.icons.Expand, "展开 "+row.label
	}
	// The whole heading folds the file; its open button only opens it.
	head := ui.Row(c).Height(theme.RowStep).Padding(0, theme.Space1, 0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Background(k.Pane).
		Label(tip).Bind(h)
	if head.Clicked() {
		if ch.folded == nil {
			ch.folded = map[string]bool{}
		}
		ch.folded[row.key] = !folded
	}
	head.Children(func() {
		ui.Icon(c, chev).FontSize(theme.IconSmall).TextColor(k.Fg3).Shrink(0)
		mark := statusMark(row.file.Status)
		bg, fg := k.Hover, k.Fg3
		switch mark {
		case "A":
			bg, fg = k.AddBg, k.Add
		case "D":
			bg, fg = k.DelBg, k.Del
		}
		ui.Row(c).Size(theme.Icon, theme.Icon).Center().Radius(theme.RadiusXS).Background(bg).Shrink(0).Children(func() {
			theme.CodeCaption.On(ui.Text(c, mark)).FontWeight(theme.UIStrong.Weight()).TextColor(fg)
		})
		if img := a.icons.FileType(row.label); img != nil {
			ui.Image(c, img).Size(theme.IconRow, theme.IconRow)
		}
		theme.UIStrong.On(ui.Text(c, row.label)).TextColor(k.Fg).SingleLine().Shrink(0)
		dir := path.Dir(row.key)
		if dir == "." {
			dir = ""
		}
		theme.Secondary.On(ui.Text(c, dir)).TextColor(k.Muted).SingleLine().Grow(1).Shrink(1)
		// The counts end the heading; under the pointer its tools take
		// their place, the heading as wide as before.
		if !head.Hovered() {
			ui.Row(c).Padding(0, theme.Space1, 0, 0).Shrink(0).Children(func() { lineCounts(c, k, row.add, row.del) })
			return
		}
		if denseButton(c, k, a.icons.Open, "打开文件", false).Clicked() {
			a.openTab(ps, panelTab{path: row.key})
		}
	})
	if folded || fc == nil {
		return
	}
	if fc.diff == nil {
		ui.Row(c).Height(theme.RowDense).Padding(0, theme.Space3, 0, theme.Space8).AlignItems(ui.Center).Background(k.Pane).Children(func() {
			theme.Secondary.On(ui.Text(c, fc.note)).TextColor(k.Muted).SingleLine().Shrink(1)
		})
		return
	}
	st := fc.diff
	st.Theme, st.RenderFindBar = ct, a.findBar(k)
	st.Layout = editor.DiffUnified
	if ch.split {
		st.Layout = editor.DiffSplit
	}
	if fc.whole {
		a.wholeSeparators(k, fc)
	} else {
		a.patchSeparators(k, s, ch, row, fc)
	}
	editor.DiffView(c, st).FillWidth()
}

// patchSeparators draws the bars of a diff made from a patch, one above
// each change: how many unchanged lines it folds and the function it sits
// in, or, with none before it, where the change is. A bar that folds
// lines reads the file's two sides whole and shows them in place.
func (a *App) patchSeparators(k theme.Colors, s proto.Session, ch *changesTab, row changeRow, fc *fileChange) {
	gaps := map[string]gap{}
	for i, hk := range fc.hunks {
		header := "@@ -" + strconv.Itoa(hk.OldStart) + "," + strconv.Itoa(hk.OldLines) + " +" + strconv.Itoa(hk.NewStart) + "," + strconv.Itoa(hk.NewLines) + " @@"
		if hk.Section != "" {
			header += " " + hk.Section
		}
		g := gap{lines: hunkLines(hk), section: hk.Section, old: -1}
		if n := unchangedBefore(fc.hunks, i); n > 0 {
			// The old line right above the change's first.
			g.lines, g.old = strconv.Itoa(n)+" 行未改动", max(0, hk.OldStart-2)
		}
		gaps[header] = g
	}
	fc.diff.RenderSeparator = func(c *ui.Context, sep ui.Element, sp *editor.DiffSeparator) {
		g := gaps[sp.Header]
		if foldBar(c, k, a, sep, g).Clicked() && g.old >= 0 && !fc.loading {
			fc.reveal = g.old
			a.readWhole(s, ch, row, fc)
		}
	}
}

// wholeSeparators draws the bars of a diff of whole texts, one over each
// run of unchanged lines it folds; a click shows the run in place, as
// does the run the click that read the texts was on.
func (a *App) wholeSeparators(k theme.Colors, fc *fileChange) {
	gaps := wholeGaps(fc.diff, fc.opened)
	at := 0 // the separators come in order, each laid every frame
	fc.diff.RenderSeparator = func(c *ui.Context, sep ui.Element, sp *editor.DiffSeparator) {
		if sp.Lines == 0 { // the patch's header, laid once more as the texts arrive
			foldBar(c, k, a, sep, gap{lines: sp.Header, old: -1})
			return
		}
		if at >= len(gaps) {
			foldBar(c, k, a, sep, gap{lines: strconv.Itoa(sp.Lines) + " 行未改动", old: -1})
			return
		}
		wg := gaps[at]
		at++
		g := gap{lines: strconv.Itoa(sp.Lines) + " 行未改动", old: wg.a}
		if !wg.last {
			g.section = sectionAbove(fc.diff.New, wg.hideB+wg.hidden-1)
		}
		clicked := foldBar(c, k, a, sep, g).Clicked()
		if clicked || fc.reveal >= wg.hideA && fc.reveal < wg.hideA+wg.hidden {
			fc.reveal = -1
			if fc.opened == nil {
				fc.opened = map[int]bool{}
			}
			fc.opened[wg.a] = true
			sp.ExpandAll()
			c.AnimationFrame()
		}
	}
}

// foldBar fills a separator of a diff: the unfold mark on one that folds
// lines, the lines or where the change is, and the function on the end.
func foldBar(c *ui.Context, k theme.Colors, a *App, sep ui.Element, g gap) ui.Element {
	var bar ui.Element
	sep.Padding(theme.Space1, 0).Children(func() { // on the editor's pane
		bar = ui.Row(c).FillWidth().Height(theme.RowDense).Padding(0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
			if g.old >= 0 {
				ui.Icon(c, a.icons.Unfold).FontSize(theme.IconSmall).TextColor(k.Muted)
			}
			theme.Caption.On(ui.Text(c, g.lines)).TextColor(k.Muted)
			ui.Spacer(c)
			theme.CodeCaption.On(ui.Text(c, g.section)).TextColor(k.Muted).SingleLine().Shrink(1)
		})
		if g.old >= 0 {
			bar.Label("展开 " + g.lines).Cursor(ui.CursorPointer)
		}
	})
	return bar
}

// readWhole reads both sides of a changed file in the tab's scope, then
// shows its diff of the whole texts in place of its patch's.
func (a *App) readWhole(s proto.Session, ch *changesTab, row changeRow, fc *fileChange) {
	fc.loading = true
	sc, gen := ch.scope, ch.gen
	oldPath := row.key
	if row.file.OldPath != "" {
		oldPath = inRepo(row.repo.Path, row.file.OldPath)
	}
	var old, now *proto.FileContent
	done := func() {
		if old == nil || now == nil {
			return
		}
		fc.loading = false
		if gen != ch.gen || ch.files[row.key] != fc || old.Binary || old.TooLarge || now.Binary || now.TooLarge {
			return
		}
		fc.diff.Old, fc.diff.New = editor.NewBuffer(old.Text), editor.NewBuffer(now.Text)
		fc.whole = true
	}
	read := func(p proto.FilesReadParams, into **proto.FileContent) {
		a.host.ReadFile(p, func(f proto.FileContent, err error) {
			if err != nil {
				fc.loading = false
				return
			}
			*into = &f
			done()
		})
	}
	read(proto.FilesReadParams{Session: s.ID, Path: oldPath, Baseline: true, Range: sc.rng, Commit: sc.commit.SHA}, &old)
	read(proto.FilesReadParams{Session: s.ID, Path: row.key, Range: sc.rng, Commit: sc.commit.SHA}, &now)
}

// wholeGap is a run of unchanged lines a diff of whole texts folds: its
// first old line, where its folded lines start on each side and how many
// there are, and whether it follows the last change.
type wholeGap struct {
	a, hideA, hideB, hidden int
	last                    bool
}

// wholeGaps finds the runs of unchanged lines the diff folds, as the
// viewer folds them: all but the three lines on each side of a change,
// and not a single line; opened holds the runs shown, by first old line.
func wholeGaps(st *editor.DiffState, opened map[int]bool) []wholeGap {
	hunks := st.Hunks()
	if len(hunks) == 0 {
		return nil
	}
	const context = 3
	nOld, nNew := st.Old.LineCount(), st.New.LineCount()
	var out []wholeGap
	pa, pb := 0, 0
	for i := 0; i <= len(hunks); i++ {
		na, nb := nOld, nNew
		if i < len(hunks) {
			na, nb = hunks[i].OldStart, hunks[i].NewStart
		}
		n := min(nb-pb, na-pa)
		top, bottom := context, context
		if i == 0 {
			top = 0
		}
		if i == len(hunks) {
			bottom = 0
		}
		if hidden := n - top - bottom; hidden > 1 && !opened[pa] {
			out = append(out, wholeGap{a: pa, hideA: pa + top, hideB: pb + top, hidden: hidden, last: i == len(hunks)})
		}
		if i < len(hunks) {
			pa, pb = hunks[i].OldEnd, hunks[i].NewEnd
		}
	}
	return out
}

// sectionAbove is the function a line sits in, as git names a change's:
// the nearest line at or above it that starts with a letter, _ or $.
func sectionAbove(b *editor.Buffer, line int) string {
	for i := min(line, b.LineCount()-1); i >= 0; i-- {
		l := b.Line(i)
		if l != "" && (l[0] == '_' || l[0] == '$' || l[0] >= 'a' && l[0] <= 'z' || l[0] >= 'A' && l[0] <= 'Z') {
			return strings.TrimRight(l, " \t\r")
		}
	}
	return ""
}

// gap is the bar above a change of a file: its lines, the function it
// sits in, and whether the lines are unchanged ones folded away.
type gap struct {
	lines, section string
	old            int // the old line it unfolds at, -1 for none
}

// hunkLines is where a change is, on the new side, or on the old one
// for a change that only removes lines: "第 3–8 行".
func hunkLines(h editor.PatchHunk) string {
	start, n := h.NewStart, h.NewLines
	if n == 0 {
		start, n = h.OldStart, h.OldLines
	}
	if n <= 1 {
		return "第 " + strconv.Itoa(start) + " 行"
	}
	return "第 " + strconv.Itoa(start) + "–" + strconv.Itoa(start+n-1) + " 行"
}

// denseButton is a dense row's icon button, its icon on hover or when on.
func denseButton(c *ui.Context, k theme.Colors, icon *ui.SVG, tip string, on bool) ui.Element {
	b := ui.Button(c.Key(tip), "").Label(tip).Tooltip(tip).Size(theme.RowDense, theme.RowDense).Padding(0).
		Radius(theme.RadiusControl).Background(ui.Transparent).Border(0, ui.Transparent).Transition(hoverMotion)
	if on || b.Hovered() {
		b.Background(k.Hover)
	}
	return b.Children(func() {
		ui.Icon(c, icon).FontSize(theme.IconRow).TextColor(k.Fg3)
	})
}
