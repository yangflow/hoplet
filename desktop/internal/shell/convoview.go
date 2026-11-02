package shell

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/yangflow/hoplet/desktop/internal/convo"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// Widths of the conversation column.
const (
	codeColumn     = 720
	personalColumn = 640
)

// conversation draws a session's conversation over its composer. The
// column follows new content while it is scrolled to the end.
func (a *App) conversation(c *ui.Context, k theme.Colors, s proto.Session) {
	cv := a.conv(s.ID)
	personal := s.Mode == proto.ModePersonal
	width, gap := float32(codeColumn), float32(14)
	if personal {
		width, gap = personalColumn, 10
	}
	sc := &a.s.scroll
	atEnd := sc.Y >= sc.MaxY-4
	ui.Box(c).Grow(1).Children(func() {
		ui.Scroll(c).Fill().TrackScroll(sc).Children(func() {
			ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
				ui.Column(c).Width(width).Padding(20, 32, 24, 32).Gap(gap).Children(func() {
					if more, first := cv.More(); more {
						if ui.Button(c, "更早的消息").Clicked() {
							a.host.Older(s.ID, first, func(p proto.EventsPage) { cv.Prepend(p) })
						}
					}
					items := cv.Items()
					if personal {
						a.chat(c, k, s, items, cv.Running())
						return
					}
					a.items(c, k, s, items, cv.Running())
				})
			})
		})
		if atEnd {
			sc.Y = math.MaxFloat32
		} else if a.toBottom(c, k, cv.Running()) {
			sc.Y = math.MaxFloat32
		}
	})
	if c.Shortcut(ui.Cmd, ui.KeyDown) {
		sc.Y = math.MaxFloat32
	}
	a.modeKeys(c, s)
	ui.Row(c).FillWidth().Justify(ui.Center).Padding(0, 0, 16, 0).Children(func() {
		ui.Column(c).Width(width - 64).Children(func() { a.composer(c, k, s, cv) })
	})
}

// items draws a Code conversation. Turns are not marked: your messages part
// them, and only a turn on another day than the one before it gets a day
// line. While the harness has not answered your last message yet, a
// thinking row stands in for its answer.
func (a *App) items(c *ui.Context, k theme.Colors, s proto.Session, items []convo.Item, running bool) {
	var day string
	var waiting *convo.Item
	for i, it := range items {
		switch it.Kind {
		case convo.KindTurn:
			d := dayLabel(c.Now(), it.Time)
			if day != "" && d != day {
				dayLine(c.Key("day:"+it.Key), k, d)
			}
			day = d
			continue
		case convo.KindMine:
			waiting = &items[i]
		default:
			waiting = nil
		}
		a.item(c.Key(it.Key), k, s, it)
	}
	if running && waiting != nil {
		ui.Row(c.Key("thinking")).Height(24).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, a.icons.Thinking).FontSize(theme.IconSizeInRow).TextColor(k.Muted)
			theme.UI.On(ui.Text(c, "思考中")).TextColor(k.Muted)
			ui.Text(c, duration(c.Now().Sub(waiting.Time))+"…").Font(theme.Mono).FontSize(11).TextColor(k.Faint)
			c.After(time.Second)
		})
	}
}

// toBottom is the button over the composer that scrolls to the end; while
// the harness is answering, three dots light in turn in place of its
// arrow. It reports whether it was clicked.
func (a *App) toBottom(c *ui.Context, k theme.Colors, running bool) bool {
	b := ui.Button(c, "").Label("回到底部").Tooltip("回到底部 ⌘↓").Size(32, 32).Padding(0).Radius(16).
		Background(k.Popover).Border(1, k.Line2).Shadow(0, 6, 18, 0, k.Shadow).
		Absolute().Bottom(8).Left(0).Right(0).Margin(0, ui.Auto)
	hovered := b.Hovered()
	b.Children(func() {
		in := ui.Box(c).Fill().Radius(16).AlignItems(ui.Center).Justify(ui.Center)
		if hovered {
			in.Background(k.Fill2)
		}
		in.Children(func() {
			if !running {
				ui.Icon(c, a.icons.ToBottom).FontSize(14).TextColor(k.Fg2)
				return
			}
			still := c.Preferences().ReduceMotion
			ui.Box(c).Size(18, 4).Draw(func(p *ui.Painter, r ui.Rect) {
				t := p.Now().Sub(time.Time{}).Seconds()
				for i := range 3 {
					lit := float32([3]float64{1, 0.6, 0.3}[i])
					if !still {
						// Each dot falls from full to 30% and back in
						// 1.2 seconds, a third of the way after the one
						// before it.
						f := math.Mod(t/1.2-float64(i)/3, 1)
						lit = float32(0.3 + 0.7*(0.5+0.5*math.Cos(2*math.Pi*f)))
					}
					p.Fill(ui.Rect{X: r.X + float32(i)*7, Y: r.Y, W: 4, H: 4}, k.AccentFg.Alpha(lit), 2)
				}
				if !still {
					p.AnimationFrame()
				}
			})
		})
	})
	return b.Clicked()
}

// item draws one item of a Code conversation.
func (a *App) item(c *ui.Context, k theme.Colors, s proto.Session, it convo.Item) {
	switch it.Kind {
	case convo.KindMine:
		a.mine(c, k, it)
	case convo.KindText:
		a.text(c, k, it)
	case convo.KindWork:
		a.work(c, k, s, it, false)
	case convo.KindApproval:
		a.record(c, k, it.Approval)
	case convo.KindError:
		ui.Row(c).FillWidth().Padding(8, 12).Gap(8).AlignItems(ui.Center).Radius(theme.RadiusCard).Background(k.Fill).Children(func() {
			ui.Icon(c, a.icons.Warning).FontSize(theme.IconSize).TextColor(k.AttnFg)
			theme.UI.On(ui.Text(c, it.Text)).TextColor(k.Fg).Grow(1)
		})
	case convo.KindStopped:
		separator(c, k, "已停止 · "+clock(it.Time))
	case convo.KindChanges:
		a.changes(c, k, s, it)
	}
}

// separator is a thin line with a label in its middle.
func separator(c *ui.Context, k theme.Colors, label string) {
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Height(1).Grow(1).Background(k.Line)
		ui.Text(c, label).Font(theme.Mono).FontSize(11).TextColor(k.Faint).FontFeatures("tnum")
		ui.Box(c).Height(1).Grow(1).Background(k.Line)
	})
}

// modeKeys are the composer's mode shortcuts: ⇧⇥ plan mode, ⇧⌘1–4 the
// permission modes the harness has.
func (a *App) modeKeys(c *ui.Context, s proto.Session) {
	h, _ := a.harness(s.Harness.Kind)
	if h.Capabilities.PlanMode && c.Shortcut(ui.Shift, ui.KeyTab) {
		a.host.SetPlan(proto.SetPlanParams{Session: s.ID, Plan: !s.Plan})
	}
	if !h.Capabilities.SetMode {
		return
	}
	for i, key := range []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4} {
		m := permissionModes()[i].mode
		if c.Shortcut(ui.Shift|ui.Cmd, key) && slices.Contains(h.Capabilities.Modes, m) {
			a.host.SetPermission(proto.SetPermissionParams{Session: s.ID, Permission: m})
		}
	}
}

// mine draws a message of mine: a bubble on the right, its time and copy
// on hover; a steer is dashed, under an insert arrow and the time it
// reached the turn.
func (a *App) mine(c *ui.Context, k theme.Colors, it convo.Item) {
	col := ui.Column(c).AlignSelf(ui.End).AlignItems(ui.End).MaxWidth(0.75 * codeColumn).Gap(4)
	hovered := col.Hovered()
	col.Children(func() {
		if it.Steer {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Tooltip("运行中插话").Children(func() {
				ui.Icon(c, a.icons.Steer).FontSize(theme.IconSizeSmall).TextColor(k.Muted)
				theme.Caption.On(ui.Text(c, clock(it.Time))).TextColor(k.Muted)
			})
		}
		ui.Box(c).Children(func() {
			b := ui.Box(c).Padding(9, 14).Radius(14).Background(k.Bubble)
			if it.Steer {
				b.Background(ui.Transparent).Border(1, k.Line2).BorderStyle(ui.BorderDashed)
			}
			b.Children(func() {
				theme.Reading.On(ui.Text(c, it.Text)).TextColor(k.Fg).Selectable()
			})
			if hovered {
				a.mineActions(c, k, it)
			}
		})
	})
}

// mineActions are a message of mine's time and copy, shown on hover left
// of the bubble, its bottom on the bubble's: out of the flow, they take
// no room, and the bubble keeps the left of it free.
func (a *App) mineActions(c *ui.Context, k theme.Colors, it convo.Item) {
	ui.Row(c).Height(24).Gap(2).AlignItems(ui.Center).Attach(ui.AnchorBottomStart, ui.AnchorBottomEnd).Left(-6).Children(func() {
		theme.Caption.On(ui.Text(c, clock(it.Time))).TextColor(k.Muted).SingleLine()
		a.copyButton(c, k, it.Key, it.Text, "复制")
	})
}

// text draws the harness's text as Markdown, streaming while it comes.
func (a *App) text(c *ui.Context, k theme.Colors, it convo.Item) {
	a.s.md.Theme = mdTheme(c, k)
	a.s.md.CodeBlock = a.codeBlock(k)
	renderMarkdown(c, it, &a.s.md).FillWidth()
}

// cursor ends text that is still streaming: a solid bar after the last
// character.
const cursor = "\u258D"

// renderMarkdown draws an item's text as Markdown; text still streaming
// reveals as it comes and ends with the cursor.
func renderMarkdown(c *ui.Context, it convo.Item, o *markdown.Options) ui.Element {
	if it.Streaming {
		return markdown.Stream(c, it.Text+cursor, o)
	}
	return markdown.View(c, it.Text, o)
}

// mdTheme is the Markdown look: reading size, the code font, the accent
// only for links.
func mdTheme(c *ui.Context, k theme.Colors) *markdown.Theme {
	th := markdown.DefaultTheme(c.Theme())
	th.FontSize = theme.Reading.Size()
	th.LineHeight = 22 / theme.Reading.Size()
	th.CodeFont = theme.Mono
	th.Text, th.Muted, th.Link = k.Fg, k.Muted, k.AccentFg
	th.CodeBackground = k.Fill
	th.CodeBlockBackground, th.CodeBlockBorder, th.CodeBlockRadius = k.Bg, k.Line, theme.RadiusCard
	return th
}

// codeLines folds a code block longer than this.
const codeLines = 30

// codeBlock draws a code block: a 30-point header with the language, then
// expand (for a long block) and copy as icon buttons.
func (a *App) codeBlock(k theme.Colors) func(c *ui.Context, b *markdown.CodeBlockView) {
	return func(c *ui.Context, b *markdown.CodeBlockView) {
		code := b.Code()
		long := strings.Count(code, "\n") > codeLines
		id := fmt.Sprintf("code:%p:%d", b, b.Index)
		open := a.s.open[id]
		ui.Column(c).AlignSelf(ui.Stretch).Radius(theme.RadiusCard).Border(1, k.Line).Background(k.Bg).Clip().Children(func() {
			ui.Row(c).Height(30).Padding(0, 4, 0, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Children(func() {
				theme.Caption.On(ui.Text(c, b.Language)).TextColor(k.Muted).Grow(1)
				if long {
					tip := "展开"
					if open {
						tip = "折叠"
					}
					icon := a.icons.Unfold
					if open {
						icon = a.icons.Fold
					}
					if iconButton(c, k, icon, tip, false).Size(24, 24).Clicked() {
						a.s.open[id] = !open
					}
				}
				a.copyButton(c, k, id, code, "复制")
			})
			body := b.Body(c)
			if long && !open {
				body.MaxHeight(codeLines * 19)
			}
		})
	}
}

// copyButton copies text; its icon shows a check for 1.5 seconds after.
func (a *App) copyButton(c *ui.Context, k theme.Colors, id, text, tip string) {
	icon := a.icons.Copy
	if at, ok := a.s.copied[id]; ok && c.Now().Sub(at) < 1500*time.Millisecond {
		icon, tip = a.icons.Check, "已复制"
	}
	if iconButton(c, k, icon, tip, false).Size(24, 24).Clicked() {
		c.WriteClipboard(text)
		a.s.copied[id] = c.Now()
		c.After(1500 * time.Millisecond)
	}
}

// work draws a work group. A finished group folds into one line unless a
// step failed; a running one shows its last three steps. folded keeps it
// one line whatever happens (Personal).
func (a *App) work(c *ui.Context, k theme.Colors, s proto.Session, it convo.Item, folded bool) {
	w := it.Work
	key := "work:" + string(s.ID) + ":" + it.Key
	open, set := a.s.open[key]
	if !set {
		open = w.Running || w.Failed > 0
	}
	if folded {
		open = false
	}
	ui.Column(c).FillWidth().Children(func() {
		h := ui.Row(c).Height(26).Gap(6).AlignItems(ui.Center)
		if !folded && h.Clicked() {
			a.s.open[key] = !open
		}
		h.Children(func() {
			chev := ui.Icon(c, a.icons.Expand).FontSize(theme.IconSizeSmall).TextColor(k.Faint)
			if open {
				chev.Rotate(90)
			}
			theme.UI.On(ui.Text(c, summary(w))).TextColor(k.Fg3).SingleLine().Shrink(1)
			ui.Spacer(c)
			// Failures sit at the end, before the time.
			if w.Failed > 0 {
				theme.Caption.On(ui.Textf(c, "✕ %d 个失败", w.Failed)).TextColor(k.Fg).FontWeight(600)
				if !w.Running {
					ui.Text(c, "·").FontSize(11).TextColor(k.Faint)
				}
			}
			if !w.Running {
				ui.Text(c, duration(w.End.Sub(w.Start))).Font(theme.Mono).FontSize(11).TextColor(k.Faint)
			}
		})
		if !open {
			return
		}
		ui.Column(c).Margin(2, 0, 0, 4).Padding(0, 0, 0, 14).Gap(2).BorderWidth(0, 0, 0, 1).BorderColor(k.Line).Children(func() {
			entries := w.Entries
			if w.Running && len(entries) > 3 {
				theme.Caption.On(ui.Textf(c, "更早的 %d 条", len(entries)-3)).TextColor(k.Muted)
				entries = entries[len(entries)-3:]
			}
			for i, run := range runs(entries) {
				if len(run) == 1 {
					a.workStep(c.Key(i), k, s, run[0])
					continue
				}
				a.reads(c.Key(i), k, s, it.Key, i, run)
			}
		})
	})
}

// runs splits a group's steps into runs: consecutive finished reads go
// together, every other step alone.
func runs(entries []convo.Entry) [][]convo.Entry {
	var out [][]convo.Entry
	for _, e := range entries {
		read := e.Tool != nil && e.Tool.Kind == proto.ToolRead && e.Tool.Status == proto.ToolDone
		if n := len(out); read && n > 0 {
			prev := out[n-1][0]
			if prev.Tool != nil && prev.Tool.Kind == proto.ToolRead && prev.Tool.Status == proto.ToolDone {
				out[n-1] = append(out[n-1], e)
				continue
			}
		}
		out = append(out, []convo.Entry{e})
	}
	return out
}

// reads draws consecutive reads as one row, 读取了 N 个文件 ›, which opens
// to the reads.
func (a *App) reads(c *ui.Context, k theme.Colors, s proto.Session, group string, i int, run []convo.Entry) {
	key := fmt.Sprintf("reads:%s:%s:%d", s.ID, group, i)
	open := a.s.open[key]
	ui.Column(c).Children(func() {
		r := ui.Row(c).Height(24).Gap(8).AlignItems(ui.Center).Label(fmt.Sprintf("读取了 %d 个文件", len(run)))
		if r.Clicked() {
			a.s.open[key] = !open
		}
		r.Children(func() {
			ui.Icon(c, a.icons.Read).FontSize(theme.IconSizeInRow).TextColor(k.Fg3)
			theme.UI.On(ui.Textf(c, "读取了 %d 个文件", len(run))).TextColor(k.Fg).FontWeight(500)
			icon := a.icons.Expand
			if open {
				icon = a.icons.Menu
			}
			ui.Icon(c, icon).FontSize(theme.IconSizeSmall).TextColor(k.Faint)
		})
		if open {
			for j, e := range run {
				a.workStep(c.Key(j), k, s, e)
			}
		}
	})
}

// workStep draws one step of a work group: thinking, or a tool row that opens
// its output.
func (a *App) workStep(c *ui.Context, k theme.Colors, s proto.Session, e convo.Entry) {
	if e.Tool == nil {
		ui.Column(c).Gap(2).Children(func() {
			ui.Row(c).Height(24).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, a.icons.Thinking).FontSize(theme.IconSizeInRow).TextColor(k.Fg3)
				theme.UI.On(ui.Text(c, "思考")).TextColor(k.Fg).FontWeight(500)
			})
			theme.Secondary.On(ui.Text(c, e.Thinking)).TextColor(k.Muted).Padding(0, 0, 0, 22).Selectable()
		})
		return
	}
	tc := *e.Tool
	key := "out:" + string(s.ID) + ":" + tc.ID
	// A tool still running after its turn ended will not be reported done.
	unfinished := tc.Status == proto.ToolRunning && !a.conv(s.ID).Running()
	running := tc.Status == proto.ToolRunning && !unfinished
	failed := tc.Status == proto.ToolFailed
	long := utf8.RuneCountInString(tc.Summary) > inputRunes || strings.Contains(tc.Summary, "\n")
	opens := tc.Output != nil || failed || long
	open, set := a.s.open[key]
	if !set {
		open = failed
	}
	open = open && opens
	if open && tc.Output != nil {
		a.loadOutput(s, tc, key)
	}
	ui.Column(c).Children(func() {
		r := ui.Row(c).Height(24).Gap(8).AlignItems(ui.Center)
		hovered := r.Hovered()
		if opens && r.Clicked() {
			a.s.open[key] = !open
		}
		r.Children(func() {
			icon, verb := a.action(tc)
			if running {
				ui.Spinner(c).Size(14, 14)
			} else {
				ui.Icon(c, icon).FontSize(theme.IconSizeInRow).TextColor(k.Fg3)
			}
			theme.UI.On(ui.Text(c, verb)).TextColor(k.Fg).FontWeight(500)
			ui.Text(c, firstLine(tc.Summary)).Font(theme.Mono).FontSize(12).TextColor(k.Fg3).FontFeatures("liga=0").SingleLine().Shrink(1)
			if failed {
				// Right after what failed; "✕ 退出码 N" once harnesses
				// report the exit code.
				ui.Text(c, "✕").Font(theme.Mono).FontSize(11).FontWeight(600).TextColor(k.Fg)
			}
			ui.Spacer(c)
			switch {
			case unfinished:
				ui.Text(c, "未完成").Font(theme.Mono).FontSize(11).TextColor(k.Faint)
			case running:
				ui.Text(c, duration(c.Now().Sub(e.Start))+"…").Font(theme.Mono).FontSize(11).TextColor(k.Faint)
				c.After(time.Second)
			case hovered:
				ui.Text(c, duration(e.Duration())).Font(theme.Mono).FontSize(11).TextColor(k.Faint)
			}
		})
		if !open {
			return
		}
		if long {
			ui.Text(c, tc.Summary).Font(theme.Mono).FontSize(12).LineHeight(18.0/12).TextColor(k.Fg2).
				FontFeatures("liga=0").Margin(2, 0, 4, 22).Selectable()
		}
		switch {
		case tc.Output != nil:
			a.output(c, k, key, func() { a.loadOutput(s, tc, key) })
		case failed:
			ui.Row(c).Margin(2, 0, 4, 22).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, a.icons.Failed).FontSize(13).TextColor(k.Muted)
				theme.Secondary.On(ui.Text(c, "失败，harness 没给出原因")).TextColor(k.Muted)
			})
		}
	})
}

// inputRunes is how long a tool's input may be and still show whole in
// its row; a longer one is written out when the row opens.
const inputRunes = 48

// firstLine is s up to its first line end.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// loadOutput asks the daemon once for a tool's output; why it could not
// be read is kept for its box.
func (a *App) loadOutput(s proto.Session, tc proto.ToolCall, key string) {
	if _, asked := a.s.outputs[key]; asked {
		return
	}
	a.s.outputs[key] = nil
	delete(a.s.outputErr, key)
	a.host.Output(proto.OutputParams{Session: s.ID, ToolCall: tc.ID}, func(o proto.ToolOutput, err error) {
		if err != nil {
			a.s.outputErr[key] = err.Error()
			return
		}
		a.s.outputs[key] = &o
	})
}

// outputLines is how many lines of an output show before showing all.
const outputLines = 12

// output draws a tool's output in a box, or why it could not be read
// with retry: its first lines, then a bar
// saying how many remain or that the daemon kept only the start, with
// show all or fold back, and copy. A short output that is all there has no
// bar; its copy button shows on hover.
func (a *App) output(c *ui.Context, k theme.Colors, key string, retry func()) {
	o := a.s.outputs[key]
	box := ui.Column(c).Margin(4, 0, 4, 22).Radius(8).Border(1, k.Line).Background(k.Bg2).Clip()
	hovered := box.Hovered()
	box.Children(func() {
		if why, ok := a.s.outputErr[key]; ok {
			ui.Row(c).Padding(4, 4, 4, 12).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, a.icons.Failed).FontSize(13).TextColor(k.Muted)
				theme.Secondary.On(ui.Text(c, "没取到输出："+why)).TextColor(k.Muted).Grow(1).Shrink(1)
				if iconButton(c, k, a.icons.Reconnect, "重试", false).Size(24, 24).Clicked() {
					delete(a.s.outputs, key)
					retry()
				}
			})
			return
		}
		if o == nil {
			ui.Box(c).Padding(8, 12).Children(func() { ui.Spinner(c).Size(12, 12) })
			return
		}
		lines := strings.Split(strings.TrimRight(o.Text, "\n"), "\n")
		all := a.s.open[key+":all"]
		shown := lines
		if len(shown) > outputLines && !all {
			shown = shown[:outputLines]
		}
		ui.Text(c, strings.Join(shown, "\n")).Font(theme.Mono).FontSize(12).LineHeight(18.0/12).TextColor(k.Fg2).
			FontFeatures("liga=0").Padding(8, 12).Selectable()
		copyTip := "复制"
		if o.Truncated {
			copyTip = "复制已显示的部分"
		}
		if len(lines) <= outputLines && !o.Truncated {
			if hovered {
				ui.Box(c).Absolute().Top(4).Right(4).Children(func() { a.copyButton(c, k, key, o.Text, copyTip) })
			}
			return
		}
		rest := len(lines) - len(shown)
		ui.Row(c).Padding(2, 4, 2, 12).Gap(2).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(k.Line).Children(func() {
			label := ""
			switch {
			case rest > 0:
				label = fmt.Sprintf("还有 %d 行", rest)
			case o.Truncated:
				label = "输出超过 64 KB，只显示开头"
			}
			theme.Caption.On(ui.Text(c, label)).TextColor(k.Muted).Grow(1)
			switch {
			case rest > 0:
				if iconButton(c, k, a.icons.Unfold, "展开全部", false).Size(24, 24).Clicked() {
					a.s.open[key+":all"] = true
				}
			case len(lines) > outputLines:
				if iconButton(c, k, a.icons.Fold, "折叠", false).Size(24, 24).Clicked() {
					a.s.open[key+":all"] = false
				}
			}
			a.copyButton(c, k, key, o.Text, copyTip)
		})
	})
}

// action is a tool call's icon and verb.
func (a *App) action(tc proto.ToolCall) (*ui.SVG, string) {
	switch tc.Name {
	case "Grep", "Glob", "search_files":
		return a.icons.Search, "搜索"
	case "Task", "Agent":
		return a.icons.Task, "子任务"
	case "Write", "write_file":
		return a.icons.Write, "写入"
	}
	switch tc.Kind {
	case proto.ToolRead:
		return a.icons.Read, "读取"
	case proto.ToolEdit:
		return a.icons.Edit, "编辑"
	case proto.ToolExec:
		return a.icons.Run, "运行"
	case proto.ToolWeb:
		return a.icons.Web, "抓取网页"
	}
	return a.icons.Tool, tc.Name
}

// summary is a work group's one line: thinking · read N files · …, with
// the running one saying so.
func summary(w *convo.Work) string {
	var think bool
	counts := map[string]int{}
	var order []string
	for _, e := range w.Entries {
		if e.Tool == nil {
			think = true
			continue
		}
		verb := toolVerb(*e.Tool)
		if counts[verb] == 0 {
			order = append(order, verb)
		}
		counts[verb]++
	}
	var parts []string
	if think {
		parts = append(parts, "思考")
	}
	for _, v := range order {
		parts = append(parts, fmt.Sprintf(v, counts[v]))
	}
	out := strings.Join(parts, " · ")
	if w.Running {
		out += " · 运行中"
	}
	return out
}

// toolVerb is a tool call's part of a summary, with %d for the count.
func toolVerb(tc proto.ToolCall) string {
	switch tc.Name {
	case "Grep", "Glob", "search_files":
		return "搜索 %d 次"
	case "Task", "Agent":
		return "%d 个子任务"
	}
	switch tc.Kind {
	case proto.ToolRead:
		return "读取 %d 个文件"
	case proto.ToolEdit:
		return "编辑 %d 个文件"
	case proto.ToolExec:
		return "运行 %d 条命令"
	case proto.ToolWeb:
		return "抓取 %d 个网页"
	}
	return "调用 %d 个工具"
}

// record draws a decided approval: a shield, the outcome, what it was
// about; who decided and when show on hover.
func (a *App) record(c *ui.Context, k theme.Colors, ap *convo.Approval) {
	if ap.Outcome == "" {
		return // still open: it waits in the composer
	}
	r := ui.Row(c).Height(24).Gap(8).AlignItems(ui.Center)
	hovered := r.Hovered()
	r.Children(func() {
		icon, label := a.icons.Allowed, "已允许"
		switch ap.Outcome {
		case proto.OutcomeDenied:
			icon, label = a.icons.Denied, "已拒绝"
		case proto.OutcomeExpired:
			icon, label = a.icons.Denied, "未答复"
		}
		ui.Icon(c, icon).FontSize(theme.IconSizeInRow).TextColor(k.Fg3)
		theme.UI.On(ui.Text(c, label)).TextColor(k.Fg).FontWeight(500)
		ui.Text(c, ap.Request.Title).Font(theme.Mono).FontSize(12).TextColor(k.Fg3).SingleLine().Shrink(1)
		ui.Spacer(c)
		if hovered {
			who := ap.DecidedBy
			if who == "" {
				who = "harness"
			}
			theme.Caption.On(ui.Text(c, who+" · "+clock(ap.At))).TextColor(k.Muted)
		}
	})
}

// changes draws the files a turn changed, at the end of its reply: a
// 36-point header with the count and the lines added and removed, then
// each file with its status, path and lines, from the session's diff. The
// rows open the file's diff in the right panel once there is one; undoing
// comes with checkpoints.
func (a *App) changes(c *ui.Context, k theme.Colors, s proto.Session, it convo.Item) {
	d := a.diff(s)
	var add, del int
	stats := make([]*proto.FileDiff, len(it.Files))
	for i, f := range it.Files {
		if st, ok := fileStats(d, s.Directory, f.Path); ok {
			stats[i] = &st
			add, del = add+st.Additions, del+st.Deletions
		}
	}
	ui.Column(c).FillWidth().Radius(theme.RadiusCard).Border(1, k.Line).Clip().Children(func() {
		ui.Row(c).Height(36).Padding(0, 12).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Children(func() {
			theme.UIStrong.On(ui.Textf(c, "改了 %d 个文件", len(it.Files))).TextColor(k.Fg)
			if d != nil {
				lines(c, k, add, del)
			}
		})
		for i, f := range it.Files {
			r := ui.Row(c.Key(f.Path)).Padding(6, 12).Gap(8).AlignItems(ui.Center)
			if r.Hovered() {
				r.Background(k.Fill)
			}
			r.Children(func() {
				ui.Text(c, opLetter(f.Op)).Font(theme.Mono).FontSize(12).TextColor(k.Muted).Width(12)
				ui.Text(c, relPath(s.Directory, f.Path)).Font(theme.Mono).FontSize(12).TextColor(k.Fg).SingleLine().Grow(1).Shrink(1)
				if st := stats[i]; st != nil {
					lines(c, k, st.Additions, st.Deletions)
				}
			})
		}
	})
}

// lines shows lines added and removed, +24 −9, in the diff colors.
func lines(c *ui.Context, k theme.Colors, add, del int) {
	ui.Row(c).Gap(4).Children(func() {
		if add > 0 || del == 0 {
			ui.Textf(c, "+%d", add).Font(theme.Mono).FontSize(12).TextColor(k.Add)
		}
		if del > 0 {
			ui.Textf(c, "−%d", del).Font(theme.Mono).FontSize(12).TextColor(k.Del)
		}
	})
}

// relPath is path relative to dir when it lies inside it.
func relPath(dir, path string) string {
	if dir == "" || !filepath.IsAbs(path) {
		return path
	}
	if r, err := filepath.Rel(dir, path); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}

func opLetter(op proto.FileOp) string {
	switch op {
	case proto.FileCreated:
		return "A"
	case proto.FileDeleted:
		return "D"
	}
	return "M"
}

// clock is a local time of day, 14:20.
func clock(t time.Time) string { return t.Local().Format("15:04") }

// duration is a short length of time: 8 秒, 3 分 12 秒, 1 小时 5 分.
func duration(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%d 秒", s)
	case s < 3600:
		if s%60 == 0 {
			return fmt.Sprintf("%d 分", s/60)
		}
		return fmt.Sprintf("%d 分 %d 秒", s/60, s%60)
	}
	return fmt.Sprintf("%d 小时 %d 分", s/3600, s%3600/60)
}
