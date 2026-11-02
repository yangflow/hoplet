package shell

import (
	"fmt"
	"math"
	"path"
	"slices"
	"strings"
	"time"

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
	width := float32(codeColumn)
	if personal {
		width = personalColumn
	}
	sc := &a.s.scroll
	atEnd := sc.Y >= sc.MaxY-4
	ui.Box(c).Grow(1).Children(func() {
		ui.Scroll(c).Fill().TrackScroll(sc).Children(func() {
			ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
				ui.Column(c).Width(width).Padding(20, 32, 24, 32).Gap(14).Children(func() {
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
					for _, it := range items {
						a.item(c.Key(it.Key), k, s, it)
					}
				})
			})
		})
		if atEnd {
			sc.Y = math.MaxFloat32
		} else if iconButton(c, k, a.icons.ToBottom, "回到底部 ⌘↓", false).Absolute().Bottom(8).Left(0).Right(0).Margin(0, ui.Auto).
			Background(k.Raised).Radius(theme.RadiusPill).Clicked() {
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

// item draws one item of a Code conversation.
func (a *App) item(c *ui.Context, k theme.Colors, s proto.Session, it convo.Item) {
	switch it.Kind {
	case convo.KindTurn:
		label := clock(it.Time)
		if it.Duration > 0 {
			label += " · " + duration(it.Duration)
		}
		separator(c, k, label)
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
		a.changes(c, k, it)
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
		b := ui.Box(c).Padding(9, 14).Radius(14).Background(k.Bubble)
		if it.Steer {
			b.Background(ui.Transparent).Border(1, k.Line2).BorderStyle(ui.BorderDashed)
		}
		b.Children(func() {
			theme.Reading.On(ui.Text(c, it.Text)).TextColor(k.Fg).Selectable()
		})
		row := ui.Row(c).Height(24).Gap(2).AlignItems(ui.Center)
		if !hovered {
			row.Opacity(0)
		}
		row.Children(func() {
			theme.Caption.On(ui.Text(c, clock(it.Time))).TextColor(k.Muted)
			a.copyButton(c, k, it.Key, it.Text)
		})
	})
}

// text draws the harness's text as Markdown, streaming while it comes.
func (a *App) text(c *ui.Context, k theme.Colors, it convo.Item) {
	a.s.md.Theme = mdTheme(c, k)
	a.s.md.CodeBlock = a.codeBlock(k)
	if it.Streaming {
		markdown.Stream(c, it.Text, &a.s.md).FillWidth()
		return
	}
	markdown.View(c, it.Text, &a.s.md).FillWidth()
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
	th.CodeBlockBackground, th.CodeBlockBorder, th.CodeBlockRadius = k.Fill, k.Line, theme.RadiusCard
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
		ui.Column(c).AlignSelf(ui.Stretch).Radius(theme.RadiusCard).Border(1, k.Line).Background(k.Fill).Clip().Children(func() {
			ui.Row(c).Height(30).Padding(0, 4, 0, 12).AlignItems(ui.Center).Children(func() {
				theme.Caption.On(ui.Text(c, b.Language)).TextColor(k.Muted).Grow(1)
				if long {
					tip := "展开"
					if open {
						tip = "折叠"
					}
					if iconButton(c, k, a.icons.Expand, tip, false).Size(24, 24).Clicked() {
						a.s.open[id] = !open
					}
				}
				a.copyButton(c, k, id, code)
			})
			body := b.Body(c)
			if long && !open {
				body.MaxHeight(codeLines * 19)
			}
		})
	}
}

// copyButton copies text; its icon shows a check for 1.5 seconds after.
func (a *App) copyButton(c *ui.Context, k theme.Colors, id, text string) {
	icon, tip := a.icons.Copy, "复制"
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
			if w.Failed > 0 {
				theme.Caption.On(ui.Textf(c, "✕ %d 个失败", w.Failed)).TextColor(k.Del)
			}
			ui.Spacer(c)
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
			for i, e := range entries {
				a.workStep(c.Key(i), k, s, e)
			}
		})
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
	open, set := a.s.open[key]
	if !set {
		open = tc.Status == proto.ToolFailed
	}
	if open && tc.Output != nil {
		if _, ok := a.s.outputs[key]; !ok {
			a.s.outputs[key] = nil // asked
			a.host.Output(proto.OutputParams{Session: s.ID, ToolCall: tc.ID}, func(o proto.ToolOutput) { a.s.outputs[key] = &o })
		}
	}
	ui.Column(c).Children(func() {
		r := ui.Row(c).Height(24).Gap(8).AlignItems(ui.Center)
		hovered := r.Hovered()
		if tc.Output != nil && r.Clicked() {
			a.s.open[key] = !open
		}
		r.Children(func() {
			icon, verb := a.action(tc)
			if tc.Status == proto.ToolRunning {
				ui.Spinner(c).Size(14, 14)
			} else {
				ui.Icon(c, icon).FontSize(theme.IconSizeInRow).TextColor(k.Fg3)
			}
			theme.UI.On(ui.Text(c, verb)).TextColor(k.Fg).FontWeight(500)
			ui.Text(c, tc.Summary).Font(theme.Mono).FontSize(12).TextColor(k.Fg3).FontFeatures("liga=0").SingleLine().Shrink(1)
			ui.Spacer(c)
			switch {
			case tc.Status == proto.ToolRunning:
				ui.Text(c, duration(c.Now().Sub(e.Start))+"…").Font(theme.Mono).FontSize(11).TextColor(k.Faint)
				c.After(time.Second)
			case tc.Status == proto.ToolFailed:
				ui.Text(c, "✕ "+duration(e.Duration())).Font(theme.Mono).FontSize(11).TextColor(k.Del)
			case hovered:
				ui.Text(c, duration(e.Duration())).Font(theme.Mono).FontSize(11).TextColor(k.Faint)
			}
		})
		if open {
			a.output(c, k, key)
		}
	})
}

// outputLines shows at most this many lines of an output.
const outputLines = 20

// output draws a tool's output: its first 20 lines, then how many remain.
func (a *App) output(c *ui.Context, k theme.Colors, key string) {
	o := a.s.outputs[key]
	ui.Column(c).Margin(2, 0, 4, 22).Padding(8, 10).Radius(theme.RadiusRow).Background(k.Fill).Children(func() {
		if o == nil {
			ui.Spinner(c).Size(12, 12)
			return
		}
		lines := strings.Split(strings.TrimRight(o.Text, "\n"), "\n")
		shown := lines
		if len(shown) > outputLines && !a.s.open[key+":all"] {
			shown = shown[:outputLines]
		}
		ui.Text(c, strings.Join(shown, "\n")).Font(theme.Mono).FontSize(12).LineHeight(19.0 / 12).TextColor(k.Fg2).FontFeatures("liga=0").Selectable()
		if rest := len(lines) - len(shown); rest > 0 || o.Truncated {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				label := fmt.Sprintf("还有 %d 行", rest)
				if rest == 0 {
					label = "输出过长，只显示了开头"
				}
				theme.Caption.On(ui.Text(c, label)).TextColor(k.Muted).Grow(1)
				if rest > 0 && iconButton(c, k, a.icons.Expand, "展开", false).Size(22, 22).Clicked() {
					a.s.open[key+":all"] = true
				}
				a.copyButton(c, k, key, o.Text)
			})
		}
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

// changes draws the files a turn changed, at the end of its reply. The
// rows open the file's diff in the right panel once there is one.
func (a *App) changes(c *ui.Context, k theme.Colors, it convo.Item) {
	ui.Column(c).FillWidth().Padding(6, 6).Radius(theme.RadiusCard).Border(1, k.Line).Children(func() {
		theme.UI.On(ui.Textf(c, "改了 %d 个文件", len(it.Files))).TextColor(k.Fg3).FontWeight(500).Padding(4, 6)
		for _, f := range it.Files {
			ui.Row(c).Height(26).Padding(0, 6).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, opLetter(f.Op)).Font(theme.Mono).FontSize(11).FontWeight(600).TextColor(opColor(k, f.Op))
				theme.UI.On(ui.Text(c, path.Base(f.Path))).TextColor(k.Fg).SingleLine()
				theme.Caption.On(ui.Text(c, path.Dir(f.Path))).TextColor(k.Faint).SingleLine().Shrink(1)
			})
		}
	})
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

func opColor(k theme.Colors, op proto.FileOp) ui.Color {
	switch op {
	case proto.FileCreated:
		return k.Add
	case proto.FileDeleted:
		return k.Del
	}
	return k.Fg3
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
