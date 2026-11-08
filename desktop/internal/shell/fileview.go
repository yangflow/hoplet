package shell

import (
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// fileShow is how a file tab shows its file.
type fileShow int

// Ways to show a file.
const (
	showText    fileShow = iota // the file as it is
	showChanges                 // with its changes since the baseline marked in place
	showSplit                   // the baseline on the left, the file on the right
)

// fileView is an open file of the right panel: its text as last read, and
// its baseline once its changes were asked for.
type fileView struct {
	content *proto.FileContent
	err     string
	waiting bool
	asked   uint64 // the conversation's last change when it was read

	base     *proto.FileContent
	baseErr  string
	baseWait bool

	show    fileShow
	source  bool // a Markdown file shows its source rather than its preview
	md      markdown.Options
	ed      editor.State
	diff    editor.DiffState
	opening bool // the open-with popover shows
}

// viewFor returns the view of an open file, made on first use.
func (ps *panelState) viewFor(p string) *fileView {
	if ps.views == nil {
		ps.views = map[string]*fileView{}
	}
	v, ok := ps.views[p]
	if !ok {
		v = &fileView{}
		ps.views[p] = v
	}
	return v
}

// readFile reads an open file, again once the session changed its files.
func (a *App) readFile(s proto.Session, p string, v *fileView) {
	at := a.conv(s.ID).Changed()
	if !a.connected || v.waiting || v.content != nil && at <= v.asked || v.err != "" && at <= v.asked {
		return
	}
	v.waiting, v.asked = true, at
	a.host.ReadFile(proto.FilesReadParams{Session: s.ID, Path: p}, func(fc proto.FileContent, err error) {
		v.waiting = false
		if err != nil {
			v.err = err.Error()
			return
		}
		v.err = ""
		if v.content == nil || v.content.Text != fc.Text {
			buf := editor.NewBuffer(fc.Text)
			if l := editor.LanguageFor(p); l != nil {
				buf.SetLanguage(l)
			}
			v.ed.Buffer = buf
			v.diff.New = buf
		}
		v.content = &fc
	})
}

// readBase reads a file as the session's baseline recorded it, once.
func (a *App) readBase(s proto.Session, p string, v *fileView) {
	if !a.connected || v.base != nil || v.baseErr != "" || v.baseWait {
		return
	}
	v.baseWait = true
	a.host.ReadFile(proto.FilesReadParams{Session: s.ID, Path: p, Baseline: true}, func(fc proto.FileContent, err error) {
		v.baseWait = false
		if err != nil {
			v.baseErr = err.Error()
			return
		}
		buf := editor.NewBuffer(fc.Text) // a file the baseline lacks is new: empty
		if l := editor.LanguageFor(p); l != nil {
			buf.SetLanguage(l)
		}
		v.diff.Old = buf
		v.base = &fc
	})
}

// isMarkdown reports a file that previews as Markdown.
func isMarkdown(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// contentNote says why a file shows no text, "" when it does.
func contentNote(fc *proto.FileContent, err string) string {
	switch {
	case err != "":
		return "读不了：" + err
	case fc == nil:
		return ""
	case fc.Missing:
		return "文件已删除"
	case fc.TooLarge:
		return "文件超过 2 MiB，不显示内容"
	case fc.Binary:
		return "二进制文件，不显示内容"
	}
	return ""
}

// fileTab is a file's tab: the path bar over the file, read only, or its
// changes; a line in place of a file that cannot show.
func (a *App) fileTab(c *ui.Context, k theme.Colors, s proto.Session, ps *panelState, p string) {
	v := ps.viewFor(p)
	a.readFile(s, p, v)
	marks, _ := changedMarks(a.diff(s))
	changed := marks[p] != ""
	if !changed {
		v.show = showText
	}
	a.pathBar(c, k, s, p, v, changed)
	note := contentNote(v.content, v.err)
	if v.show != showText {
		a.readBase(s, p, v)
		if note == "" && v.baseErr != "" {
			note = "读不了基线：" + v.baseErr
		}
	}
	switch {
	case note != "":
		ui.Column(c).Grow(1).Padding(theme.Space8, theme.Space4).AlignItems(ui.Center).Background(k.Pane).Children(func() {
			theme.Secondary.On(ui.Text(c, note)).TextColor(k.Muted)
		})
	case v.content == nil || v.show != showText && v.base == nil:
		ui.Box(c).Grow(1).Background(k.Pane) // read in a moment
	case v.show == showText && isMarkdown(p) && !v.source:
		v.md.Theme = mdTheme(c, k)
		ui.Scroll(c.Key("preview")).Grow(1).MinHeight(0).Background(k.Pane).Children(func() {
			ui.Column(c).FillWidth().Padding(theme.Space4, theme.Space6).Children(func() {
				markdown.View(c, v.content.Text, &v.md).FillWidth()
			})
		})
	case v.show == showText:
		st := &v.ed
		st.ReadOnly, st.NoActiveLine, st.Label = true, true, path.Base(p)
		st.Font, st.LineHeight = codeFont(), theme.Code.Line()/theme.Code.Size()
		st.Theme, st.FitContent, st.SoftWrap, st.RenderFindBar = codeTheme(c, k), true, a.wrap, a.findBar(k)
		quietScroll(c, k, func() { editor.View(c, st).FillWidth() })
	default:
		d := &v.diff
		d.Font, d.LineHeight, d.Theme, d.Indicators = codeFont(), theme.Code.Line()/theme.Code.Size(), codeTheme(c, k), editor.DiffBars
		d.Layout, d.Wrap, d.RenderFindBar = editor.DiffUnified, a.wrap, a.findBar(k)
		if v.show == showSplit {
			d.Layout = editor.DiffSplit
		}
		d.RenderSeparator = func(c *ui.Context, sep ui.Element, sp *editor.DiffSeparator) {
			sep.Padding(theme.Space1, 0).Children(func() { // on the editor's pane
				r := ui.Row(c).FillWidth().Height(theme.RowDense).Padding(0, theme.Space2).Gap(theme.Space2).AlignItems(ui.Center).
					Radius(theme.RadiusRow).Label("展开 " + strconv.Itoa(sp.Lines) + " 行").Transition(hoverMotion)
				if r.Hovered() {
					r.Background(k.Hover)
				}
				r.Children(func() {
					ui.Icon(c, a.icons.Unfold).FontSize(theme.IconSmall).TextColor(k.Muted)
					theme.Caption.On(ui.Textf(c, "%d 行未改动", sp.Lines)).TextColor(k.Muted)
				})
				if r.Clicked() {
					sp.ExpandAll()
				}
			})
		}
		d.FitContent = true
		quietScroll(c, k, func() { editor.DiffView(c, d).FillWidth() })
	}
}

// quietScroll scrolls an editor as high as its lines without a scroll
// bar, the pane filling what it leaves below.
func quietScroll(c *ui.Context, k theme.Colors, view func()) {
	ui.Scroll(c.Key("text")).Shrink(1).MinHeight(0).Scrollbars(false).Children(view)
	ui.Box(c).Grow(1).Background(k.Pane)
}

// codeFont is the code font at the code style's size, its ligatures off.
func codeFont() ui.Font {
	return ui.Font{Family: theme.Mono, Size: theme.Code.Size(), Features: "liga=0"}
}

// pathBar is the line over a file: where it is, then a Markdown file's
// source, wrapping lines, showing its changes, side by side, revealing it
// in Finder and opening it elsewhere.
func (a *App) pathBar(c *ui.Context, k theme.Colors, s proto.Session, p string, v *fileView, changed bool) {
	ui.Row(c).Height(theme.RowStep).Padding(0, theme.Space1, 0, theme.Space3).Gap(theme.SpaceHalf).AlignItems(ui.Center).Background(k.Pane).Children(func() {
		ui.Row(c).Grow(1).Shrink(1).MinWidth(0).Gap(theme.Space1).AlignItems(ui.Center).Clip().Children(func() {
			parts := append([]string{filepath.Base(s.Directory)}, strings.Split(path.Dir(p), "/")...)
			for _, part := range parts {
				if part == "." {
					continue
				}
				theme.Secondary.On(ui.Text(c, part)).TextColor(k.Muted).SingleLine().Shrink(1)
				theme.Secondary.On(ui.Text(c, "›")).TextColor(k.Faint).Shrink(0)
			}
			theme.UIMedium.On(ui.Text(c, path.Base(p))).TextColor(k.Fg).SingleLine().Shrink(0)
		})
		if isMarkdown(p) {
			source := v.source || v.show != showText
			tip := "显示源码"
			if source {
				tip = "显示预览"
			}
			if denseButton(c.Key("source"), k, a.icons.Source, tip, source).Clicked() {
				v.show, v.source = showText, !source
			}
		}
		// Every file tab wraps its lines, or none; a preview has none.
		if !isMarkdown(p) || v.source || v.show != showText {
			if denseButton(c, k, a.icons.Wrap, "自动换行", a.wrap).Clicked() {
				a.wrap = !a.wrap
			}
		}
		if changed {
			// A Markdown file shows its changes in its source, and keeps
			// to it after.
			if denseButton(c, k, a.icons.Changes, "显示更改", v.show == showChanges).Clicked() {
				v.toggle(showChanges)
				v.source = true
			}
			if denseButton(c, k, a.icons.SideBySide, "左右对比", v.show == showSplit).Clicked() {
				v.toggle(showSplit)
				v.source = true
			}
		}
		abs := filepath.Join(s.Directory, filepath.FromSlash(p))
		if denseButton(c, k, a.icons.Reveal, "在 Finder 中显示", false).Clicked() {
			a.host.Reveal(abs)
		}
		chip := ui.Button(c.Key("open with"), "").Label("打开方式").Tooltip("打开方式").Height(theme.Row).Padding(0, theme.Space1Half).Gap(theme.SpaceHalf).
			Radius(theme.RadiusControl).Border(0, ui.Transparent).Background(ui.Transparent).Transition(hoverMotion)
		if v.opening || chip.Hovered() {
			chip.Background(k.Hover)
		}
		chip.Children(func() {
			ui.Icon(c, a.icons.Open).FontSize(theme.IconRow).TextColor(k.Fg3)
			ui.Icon(c, a.icons.Menu).FontSize(theme.IconSmall).TextColor(k.Faint)
		})
		if chip.Clicked() {
			v.opening = !v.opening
		}
		popover(c, k, chip, &v.opening, true, theme.MenuWidth, func() {
			item := menuItem(c.Key("default app"), k, false).Label("用默认应用打开")
			item.Children(func() {
				ui.Icon(c, a.icons.Open).FontSize(theme.IconRow).TextColor(k.Fg3)
				theme.UI.On(ui.Text(c, "用默认应用打开")).TextColor(k.Fg).Grow(1)
			})
			if item.Clicked() {
				v.opening = false
				a.host.Open(abs)
			}
			item = menuItem(c.Key("copy path"), k, false).Label("复制路径")
			item.Children(func() {
				ui.Icon(c, a.icons.Copy).FontSize(theme.IconRow).TextColor(k.Fg3)
				theme.UI.On(ui.Text(c, "复制路径")).TextColor(k.Fg).Grow(1)
				theme.CodeCaption.On(ui.Text(c, "⌥⌘C")).TextColor(k.Muted)
			})
			if item.Clicked() {
				v.opening = false
				a.host.CopyPath(abs)
			}
		})
	})
}

// toggle shows the file one way, or as it is when it shows that way.
func (v *fileView) toggle(way fileShow) {
	if v.show == way {
		v.show = showText
		return
	}
	v.show = way
}

// CopyPath copies the path of the file the right panel shows, from the
// menu bar.
func (a *App) CopyPath() {
	s, ok := a.codePanelSession()
	if !ok || !a.panelOpen {
		return
	}
	ps := a.panelFor(s.ID)
	if ps.active < 0 || ps.active >= len(ps.tabs) || ps.tabs[ps.active].path == "" {
		return
	}
	a.host.CopyPath(filepath.Join(s.Directory, filepath.FromSlash(ps.tabs[ps.active].path)))
}
