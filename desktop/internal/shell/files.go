package shell

import (
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// fileTree is a session's file tree as the right panel shows it: each
// directory's listing once it was opened, which are open, whether the
// ignored files show, and the filter with every file it searches.
type fileTree struct {
	dirs    map[string]*dirListing // by path relative to the working directory, "" for it
	open    map[string]bool
	ignored bool   // the files .gitignore leaves out show too
	asked   uint64 // the conversation's last change when the listings were read
	query   string // the filter
	all     *proto.FileList
	allAt   uint64 // the conversation's last change when all was read
	allWait bool
	chosen  string // the row the keys are on
}

// dirListing is a directory's entries as last read.
type dirListing struct {
	entries   []proto.FileEntry
	truncated bool
	loaded    bool
	loading   bool
	err       string
}

// retry forgets what the panel could not read, so that it is asked for
// again: the directories, the files and their baselines.
func (ps *panelState) retry() {
	for dir, l := range ps.tree.dirs {
		if l.err != "" && !l.loaded {
			delete(ps.tree.dirs, dir)
		}
	}
	for _, v := range ps.views {
		v.err, v.baseErr = "", ""
	}
}

// treeRow is a row of the file tree.
type treeRow struct {
	entry   proto.FileEntry
	depth   int
	open    bool
	loading bool
	note    string // in place of an entry: an empty directory, a listing cut short
}

// listDir reads one directory of the tree, keeping what it held until the
// answer comes.
func (a *App) listDir(s proto.Session, t *fileTree, dir string) {
	if t.dirs == nil {
		t.dirs = map[string]*dirListing{}
	}
	l, ok := t.dirs[dir]
	if !ok {
		l = &dirListing{}
		t.dirs[dir] = l
	}
	if l.loading {
		return
	}
	l.loading = true
	ignored := t.ignored
	a.host.Files(proto.FilesListParams{Session: s.ID, Path: dir, Ignored: ignored}, func(fl proto.FileList, err error) {
		l.loading = false
		if ignored != t.ignored {
			return // asked before the ignored files were shown or hidden
		}
		if err != nil {
			l.err = err.Error()
			return
		}
		l.entries, l.truncated, l.loaded, l.err = fl.Entries, fl.Truncated, true, ""
	})
}

// refreshTree reads again, after the session changed its files, every
// directory the tree has read.
func (a *App) refreshTree(s proto.Session, t *fileTree) {
	at := a.conv(s.ID).Changed()
	if !a.connected {
		return
	}
	if _, ok := t.dirs[""]; !ok {
		t.asked = at
		a.listDir(s, t, "")
		return
	}
	if at <= t.asked {
		return
	}
	t.asked = at
	for dir := range t.dirs {
		a.listDir(s, t, dir)
	}
}

// treeRows lays out the tree from its root: each directory's entries
// under it while it is open, a line for an empty or cut-short listing.
func treeRows(t *fileTree) []treeRow {
	var rows []treeRow
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		l := t.dirs[dir]
		if l == nil || !l.loaded {
			return
		}
		if len(l.entries) == 0 && dir != "" {
			rows = append(rows, treeRow{depth: depth, note: "空目录"})
		}
		for _, e := range l.entries {
			r := treeRow{entry: e, depth: depth}
			if e.Kind == proto.FileKindDir {
				r.open = t.open[e.Path]
				sub := t.dirs[e.Path]
				r.loading = r.open && sub != nil && sub.loading && !sub.loaded
			}
			rows = append(rows, r)
			if r.open {
				walk(e.Path, depth+1)
			}
		}
		if l.truncated {
			rows = append(rows, treeRow{depth: depth, note: "只列出前 " + strconv.Itoa(len(l.entries)) + " 项"})
		}
	}
	walk("", 0)
	return rows
}

// matchRows lays out the files whose path holds the filter, as a tree of
// the directories they are in, all open.
func matchRows(all []proto.FileEntry, query string) []treeRow {
	q := strings.ToLower(query)
	dirs := map[string]bool{}
	var files []proto.FileEntry
	for _, e := range all {
		if e.Kind == proto.FileKindDir || !strings.Contains(strings.ToLower(e.Path), q) {
			continue
		}
		files = append(files, e)
		for d := path.Dir(e.Path); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	// Each directory, then its files, under the directory it is in.
	children := map[string][]proto.FileEntry{}
	for d := range dirs {
		parent := path.Dir(d)
		if parent == "." {
			parent = ""
		}
		children[parent] = append(children[parent], proto.FileEntry{Path: d, Kind: proto.FileKindDir})
	}
	for _, f := range files {
		parent := path.Dir(f.Path)
		if parent == "." {
			parent = ""
		}
		children[parent] = append(children[parent], f)
	}
	for _, list := range children {
		sort.Slice(list, func(i, j int) bool {
			if (list[i].Kind == proto.FileKindDir) != (list[j].Kind == proto.FileKindDir) {
				return list[i].Kind == proto.FileKindDir
			}
			return path.Base(list[i].Path) < path.Base(list[j].Path)
		})
	}
	var rows []treeRow
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		for _, e := range children[dir] {
			rows = append(rows, treeRow{entry: e, depth: depth, open: e.Kind == proto.FileKindDir})
			if e.Kind == proto.FileKindDir {
				walk(e.Path, depth+1)
			}
		}
	}
	walk("", 0)
	return rows
}

// changedMarks returns each changed file's status letter, and how many
// files changed under each directory, by path relative to the working
// directory.
func changedMarks(d *proto.SessionDiff) (map[string]string, map[string]int) {
	marks, counts := map[string]string{}, map[string]int{}
	if d == nil {
		return marks, counts
	}
	for _, r := range d.Repos {
		for _, f := range r.Files {
			p := inRepo(r.Path, f.Path)
			marks[p] = statusMark(f.Status)
			for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
				counts[dir]++
			}
		}
	}
	return marks, counts
}

// fileColumn is the panel's column on the files: a filter and the switch
// for ignored files over the tree of the working directory, read a
// directory at a time as it opens.
func (a *App) fileColumn(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState) {
	t := &ps.tree
	a.refreshTree(s, t)
	ui.Row(c).Height(theme.Row).Padding(0, theme.Space1, 0, theme.Space2).Gap(theme.Space1).AlignItems(ui.Center).Children(func() {
		ui.SearchField(c, &t.query).Placeholder("筛选文件").Label("筛选文件").Grow(1).Height(theme.Row).
			FontSize(theme.Secondary.Size()).Radius(theme.RadiusControl).Background(k.Fill).Border(0, ui.Transparent)
		// The eye says whether the ignored files show; the tip, what a
		// click does.
		eye, tip := a.icons.Ignored, "显示被忽略的文件"
		if t.ignored {
			eye, tip = a.icons.Shown, "隐藏被忽略的文件"
		}
		if iconButton(c, k, eye, tip, t.ignored).Clicked() {
			t.ignored = !t.ignored
			t.dirs, t.all = nil, nil // listed again, with or without them
			a.refreshTree(s, t)
			for dir, open := range t.open {
				if open {
					a.listDir(s, t, dir)
				}
			}
		}
	})
	rows := treeRows(t)
	if t.query != "" {
		rows = a.filteredRows(s, t)
		if rows == nil {
			ui.Column(c).Grow(1).Padding(theme.Space4, theme.Space3).Children(func() {
				if t.all != nil {
					theme.SecondaryBlock.On(ui.Text(c, "没有匹配的文件")).TextColor(k.Muted)
				}
			})
			return
		}
	}
	marks, counts := changedMarks(a.diff(s))
	current := ""
	if ps.active >= 0 && ps.active < len(ps.tabs) {
		current = ps.tabs[ps.active].path
	}
	col := ui.Scroll(c.Key("tree")).Grow(1).Focusable().FocusRing(false).Label("文件树")
	a.treeKeys(col, s, ps, rows)
	col.Children(func() {
		ui.Column(c).Padding(theme.Space1).Children(func() {
			for i, r := range rows {
				key := r.entry.Path
				if r.note != "" {
					key = "note:" + strconv.Itoa(i)
				}
				a.treeRowView(c.Key(key), k, s, ps, r, marks, counts, current)
			}
		})
	})
}

// filteredRows reads every file once to filter them, and lays out those
// that match; nil while they are read or when none does.
func (a *App) filteredRows(s proto.Session, t *fileTree) []treeRow {
	at := a.conv(s.ID).Changed()
	if a.connected && !t.allWait && (t.all == nil || at > t.allAt) {
		t.allWait, t.allAt = true, at
		ignored := t.ignored
		a.host.Files(proto.FilesListParams{Session: s.ID, All: true, Ignored: ignored}, func(fl proto.FileList, err error) {
			t.allWait = false
			if err == nil && ignored == t.ignored {
				t.all = &fl
			}
		})
	}
	if t.all == nil {
		return nil
	}
	return matchRows(t.all.Entries, t.query)
}

// treeKeys lets the tree's rows be chosen and opened from the keyboard:
// ↑↓ move, ← → close and open a directory, ↩ opens a file.
func (a *App) treeKeys(col ui.Element, s proto.Session, ps *panelState, rows []treeRow) {
	t := &ps.tree
	at := -1
	var pick []treeRow
	for _, r := range rows {
		if r.note == "" {
			if r.entry.Path == t.chosen {
				at = len(pick)
			}
			pick = append(pick, r)
		}
	}
	if len(pick) == 0 {
		return
	}
	move := func(i int) { t.chosen = pick[min(max(i, 0), len(pick)-1)].entry.Path }
	switch {
	case col.Shortcut(0, ui.KeyDown):
		move(at + 1)
	case col.Shortcut(0, ui.KeyUp):
		if at < 0 {
			at = len(pick)
		}
		move(at - 1)
	case at >= 0 && col.Shortcut(0, ui.KeyRight):
		if r := pick[at]; r.entry.Kind == proto.FileKindDir {
			a.openDir(s, t, r.entry.Path)
		}
	case at >= 0 && col.Shortcut(0, ui.KeyLeft):
		if r := pick[at]; r.entry.Kind == proto.FileKindDir {
			delete(t.open, r.entry.Path)
		}
	case at >= 0 && col.Shortcut(0, ui.KeyEnter):
		if r := pick[at]; r.entry.Kind != proto.FileKindDir {
			a.openFile(ps, r.entry.Path)
		}
	}
}

// openDir opens a directory of the tree, reading it the first time.
func (a *App) openDir(s proto.Session, t *fileTree, dir string) {
	if t.open == nil {
		t.open = map[string]bool{}
	}
	t.open[dir] = true
	if l, ok := t.dirs[dir]; !ok || !l.loaded {
		a.listDir(s, t, dir)
	}
}

// openFile opens a file in a tab of its own, or shows its tab.
func (a *App) openFile(ps *panelState, p string) {
	ps.tree.chosen = p
	a.openTab(ps, panelTab{path: p})
}

// treeRowView draws a row of the file tree: a directory with its
// chevron and how many files changed under it, a file with its type's
// icon, in bold with its status letter when it changed.
func (a *App) treeRowView(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState, r treeRow, marks map[string]string, counts map[string]int, current string) {
	t := &ps.tree
	indent := func(n int) {
		for range n {
			ui.Box(c).Width(theme.Icon).Shrink(0)
		}
	}
	if r.note != "" {
		ui.Row(c).Height(theme.RowDense).Padding(0, theme.Space2).Gap(theme.Space1Half).AlignItems(ui.Center).Children(func() {
			indent(r.depth)
			ui.Box(c).Width(theme.IconSmall).Shrink(0)
			theme.Secondary.On(ui.Text(c, r.note)).TextColor(k.Muted).SingleLine()
		})
		return
	}
	e := r.entry
	dir := e.Kind == proto.FileKindDir
	row := ui.Row(c).Height(theme.RowDense).Padding(0, theme.Space2).Gap(theme.Space1Half).AlignItems(ui.Center).
		Radius(theme.RadiusRow).Label(e.Path).Transition(hoverMotion)
	switch {
	case !dir && e.Path == current, e.Path == t.chosen:
		row.Background(k.Selected)
	case row.Hovered():
		row.Background(k.Hover)
	}
	name := path.Base(e.Path)
	row.Children(func() {
		indent(r.depth)
		if dir {
			chev := a.icons.Expand
			if r.open {
				chev = a.icons.Menu
			}
			ui.Icon(c, chev).FontSize(theme.IconSmall).TextColor(k.Faint)
			ui.Image(c, a.icons.FolderType()).Size(theme.IconRow, theme.IconRow)
		} else {
			ui.Box(c).Width(theme.IconSmall).Shrink(0)
			if img := a.icons.FileType(name); img != nil {
				ui.Image(c, img).Size(theme.IconRow, theme.IconRow)
			}
		}
		style, color := theme.UI, k.Fg
		mark := marks[e.Path]
		if mark != "" {
			style = theme.UIStrong
		}
		if e.Ignored {
			color = k.Muted
		}
		style.On(ui.Text(c, name)).TextColor(color).SingleLine().Shrink(1).Grow(1)
		switch {
		case r.loading:
			ui.Spinner(c).Size(theme.IconSmall, theme.IconSmall).Label("读取中")
		case dir && counts[e.Path] > 0 && t.query == "":
			theme.CodeCaption.On(ui.Text(c, strconv.Itoa(counts[e.Path]))).TextColor(k.Muted)
		case mark != "":
			theme.CodeCaption.On(ui.Text(c, mark)).TextColor(markColor(k, mark))
		}
	})
	if row.Clicked() {
		t.chosen = e.Path
		switch {
		case dir && r.open && t.query == "":
			delete(t.open, e.Path)
		case dir && t.query == "":
			a.openDir(s, t, e.Path)
		case !dir:
			a.openFile(ps, e.Path)
		}
	}
}
