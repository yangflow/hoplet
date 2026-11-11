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
	"github.com/egoist/mygo/ui/shader"

	"github.com/yangflow/hoplet/desktop/internal/convo"
	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/proto"
)

// edgeFade fades what it draws to nothing over its first Top and last
// Bottom DIPs, by its own opacity rather than a color laid over it, as the
// pane under it shows the desktop through.
var edgeFade = shader.MustCompile(`package main

var Top float
var Bottom float

func ColorEffect(pos vec2, color vec4) vec4 {
	t := min(pos.y/Top, (rectSize().y-pos.y)/Bottom)
	if t >= 1 {
		return color
	}
	return color * smoothstep(0, 1, t)
}
`)

// conversation draws a session's conversation over its composer. The
// column follows new content while it is scrolled to the end.
func (a *App) conversation(c *ui.Context, k theme.Colors, s proto.Session) {
	cv := a.conv(s.ID)
	personal := s.Mode == proto.ModePersonal
	width := float32(theme.CodeColumn)
	if personal {
		width = theme.PersonalColumn
	}
	sc := &a.s.scroll
	atEnd := sc.Y >= sc.MaxY-4
	ui.Box(c).Grow(1).Children(func() {
		// What scrolls fades out under the top row and above the composer,
		// over the column's top and bottom padding, so at either end
		// nothing it shows fades. A conversation that fits reaches neither
		// edge, and goes without the effect's offscreen layer.
		var fade ui.Effect
		if sc.MaxY > 0 {
			fade = edgeFade.Effect(shader.Options{Uniforms: map[string]any{"Top": float32(theme.Space6), "Bottom": float32(theme.Space8)}})
		}
		ui.Scroll(c).Fill().Scrollbars(false).Effect(fade).TrackScroll(sc).Children(func() {
			ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).MaxWidth(width).Padding(theme.Space6, theme.Space8, theme.Space8, theme.Space8).Children(func() {
					if more, first := cv.More(); more && a.older(c, k) {
						a.host.Older(proto.EventsParams{Session: s.ID, BeforeSeq: first}, func(p proto.EventsPage) { cv.Prepend(p) })
					}
					if personal {
						a.chat(c, k, s, cv.Items(), cv.Running() || cv.Starting())
						return
					}
					a.items(c, k, s, cv)
				})
			})
		})
		if atEnd {
			sc.Y = math.MaxFloat32
		} else if a.toBottom(c, k, cv.Running(), 8) {
			sc.Y = math.MaxFloat32
		}
	})
	if c.Shortcut(ui.Cmd, ui.KeyDown) {
		sc.Y = math.MaxFloat32
	}
	a.modeKeys(c, s)
	// The composer narrows with the window, as the details column beside
	// the conversation may leave it less room than its width.
	ui.Row(c).FillWidth().Justify(ui.Center).Padding(0, 0, theme.Space4, 0).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).MaxWidth(width-2*theme.Space8).Margin(0, theme.Space4).Children(func() { a.composer(c, k, s, cv) })
	})
}

// older is the quiet button above a conversation that loads its earlier
// messages: an up arrow and its name in muted text, centered, with a light
// fill only under the pointer. It reports whether it was clicked.
func (a *App) older(c *ui.Context, k theme.Colors) bool {
	b := ui.Button(c, "").Label("更早的消息").AlignSelf(ui.Center).Height(theme.RowGroup).Padding(0, theme.Space3).Gap(theme.Space1Half).AlignItems(ui.Center).
		Radius(theme.RadiusPill).Background(ui.Transparent).Border(0, ui.Transparent)
	if b.Hovered() {
		b.Background(k.Fill)
	}
	b.Children(func() {
		ui.Icon(c, a.icons.Older).FontSize(theme.IconSmall).TextColor(k.Muted)
		theme.Secondary.On(ui.Text(c, "更早的消息")).TextColor(k.Muted)
	})
	return b.Clicked()
}

// items draws a Code conversation, a turn's steps in one work group.
// Turns are not marked: your messages part them, and only a turn on
// another day than the one before it gets a day line. While a turn runs, a
// working row ends it. A turn starts 24 below the one before, what answers
// your message 16 below it, and the blocks of an answer stand 12 apart;
// the working row is 8 under a work group.
func (a *App) items(c *ui.Context, k theme.Colors, s proto.Session, cv *convo.Convo) {
	items := cv.Grouped()
	var day string
	var turn time.Time
	prev := convo.Kind(-1)
	space := func(key string, next convo.Kind, steer bool) {
		g := float32(theme.Space3)
		switch {
		case prev < 0:
			g = 0
		case prev == convo.KindTurn: // under a day line
			g = theme.Space4
		case next == convo.KindMine && !steer:
			g = theme.Space6
		case prev == convo.KindMine:
			g = theme.Space4
		case next < 0 && prev == convo.KindWork:
			g = theme.Space2
		}
		if g > 0 {
			ui.Box(c.Key("gap:" + key)).Height(g).Shrink(0)
		}
		prev = next
	}
	for _, it := range items {
		if it.Kind == convo.KindTurn {
			d := dayLabel(c.Now(), it.Time)
			if day != "" && d != day {
				space(it.Key, convo.KindMine, false)
				prev = convo.KindTurn
				dayLine(c.Key("day:"+it.Key), k, d)
			}
			day, turn = d, it.Time
			continue
		}
		space(it.Key, it.Kind, it.Steer)
		ui.Column(c.Key("in:" + it.Key)).FillWidth().Transition(enterMotion).Children(func() { a.item(c.Key(it.Key), k, s, it) })
	}
	_, _, why, unsent := cv.Failed()
	switch {
	case cv.Running():
		space("working", -1, false)
		a.working(c.Key("working"), k, items, len(cv.Pending()) > 0, turn)
	case cv.Starting():
		space("working", -1, false)
		a.startingRow(c.Key("working"), k, s)
	case unsent:
		space("working", -1, false)
		a.unsentBar(c.Key("unsent"), k, s, why)
	}
}

// unsentBar stands under a message that did not go out: why, and 重试,
// which sends it again.
func (a *App) unsentBar(c *ui.Context, k theme.Colors, s proto.Session, why string) {
	ui.Row(c).FillWidth().MinHeight(theme.RowStep).Padding(0, theme.Space2, 0, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusCard).Border(theme.LineWidth, k.Line).Background(k.Surface).Children(func() {
		ui.Icon(c, a.icons.Failed).FontSize(theme.Icon).TextColor(k.Del)
		theme.UI.On(ui.Text(c, "没能启动 "+harnessName(s.Harness.Kind)+"："+why)).TextColor(k.Fg2).Grow(1).Shrink(1)
		b := ui.Button(c, "").Label("重试").Height(theme.Row).Padding(0, theme.Space3).Gap(theme.Space1Half).AlignItems(ui.Center).Radius(theme.RadiusControl).Background(k.Fill).Border(0, ui.Transparent)
		if b.Clicked() {
			a.resend(s)
		}
		b.Children(func() {
			ui.Icon(c, a.icons.Reconnect).FontSize(theme.IconRow).TextColor(k.Fg)
			ui.Text(c, "重试").FontSize(theme.CodeInline.Size()).TextColor(k.Fg)
		})
	})
}

// working is the row that ends a running turn: three squares lighting in
// turn, what the harness is doing now, and how long the turn has run, its
// changed digits rolling in. Waiting on an approval, it says so in the
// needs-you color and its squares rest.
func (a *App) working(c *ui.Context, k theme.Colors, items []convo.Item, approval bool, since time.Time) {
	label, color := "思考中", k.Fg3
	if approval {
		label, color = "等你审批", k.AttnFg
	} else if n := len(items); n > 0 {
		switch last := items[n-1]; {
		case last.Kind == convo.KindText && last.Streaming:
			label = "回复中"
		case last.Kind == convo.KindWork && len(last.Work.Entries) > 0:
			if e := last.Work.Entries[len(last.Work.Entries)-1]; e.Tool != nil && e.Tool.Status == proto.ToolRunning {
				label = a.doing(*e.Tool)
			} else if e.Agent != nil && e.Agent.Status == proto.SubagentRunning {
				label = fmt.Sprintf("子代理运行中 · %d 个", runningAgents(last.Work, false))
			}
		}
	}
	still := approval || c.Preferences().ReduceMotion
	ui.Row(c).Height(theme.RowDense).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		squares(c, k, still)
		theme.UI.On(ui.Text(c, label)).TextColor(color)
		if !since.IsZero() {
			rolling(c, k, duration(c.Now().Sub(since)), c.Preferences().ReduceMotion)
			c.After(time.Second)
		}
	})
}

// startingRow ends a conversation whose message waits for its harness to
// start: the working row's squares and 正在启动 and the harness.
func (a *App) startingRow(c *ui.Context, k theme.Colors, s proto.Session) {
	ui.Row(c).Height(theme.RowDense).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		squares(c, k, c.Preferences().ReduceMotion)
		theme.UI.On(ui.Text(c, "正在启动 "+harnessName(s.Harness.Kind))).TextColor(k.Fg3)
	})
}

// doing names what a running tool is doing, for the working row.
func (a *App) doing(tc proto.ToolCall) string {
	switch _, verb := a.action(tc); verb {
	case "运行":
		return "运行命令"
	case "读取":
		return "读取文件"
	case "编辑":
		return "编辑文件"
	case "写入":
		return "写入文件"
	case "搜索", "抓取网页":
		return verb
	}
	return "调用工具"
}

// rolling draws a time whose characters roll when they change: each is
// keyed by its place from the end and its value, so only a changed one
// leaves and comes. Its words stand a space of the mono font apart, as a
// text of one space would lay out empty.
func rolling(c *ui.Context, k theme.Colors, text string, still bool) {
	size := theme.CodeCaption.Size()
	words := strings.Fields(text)
	place := len([]rune(text))
	ui.Row(c).Gap(0.6 * size).AlignItems(ui.Center).Children(func() {
		for _, w := range words {
			ui.Row(c.Key(place)).Children(func() {
				for _, r := range w {
					t := theme.CodeCaption.On(ui.Text(c.Key(fmt.Sprintf("%d:%c", place, r)), string(r))).TextColor(k.Faint).FontFeatures("tnum")
					if !still {
						t.Transition(ui.ElementTransition{Animation: &theme.MotionRoll, Enter: &ui.Motion{Y: theme.MotionRise}, Exit: &ui.Motion{Y: -theme.MotionRise}})
					}
					place--
				}
			})
			place-- // the space
		}
	})
}

// squares draws three small squares in the accent that light in turn, a
// third of a 1.2-second cycle apart, from the element's Loop, which keeps
// frames coming while it is built; still, they rest at full, 60% and 30%.
func squares(c *ui.Context, k theme.Colors, still bool) {
	b := ui.Box(c).Size(3*theme.Square+2*theme.SquareGap, theme.Square)
	var f float64
	if !still {
		f = float64(b.Loop("lit", theme.MotionLoop.Duration, theme.MotionLoop.Ease))
	}
	b.Draw(func(p *ui.Painter, r ui.Rect) {
		for i := range 3 {
			lit := float32([3]float64{1, 0.6, 0.3}[i])
			if !still {
				at := math.Mod(f-float64(i)/3+1, 1)
				lit = float32(0.3 + 0.7*(0.5+0.5*math.Cos(2*math.Pi*at)))
			}
			p.Fill(ui.Rect{X: r.X + float32(i)*7, Y: r.Y, W: 4, H: 4}, k.AccentFg.Alpha(lit), 1)
		}
	})
}

// toBottom is the button over the composer that scrolls to the end; while
// the harness is answering, the working row's squares light in place of
// its arrow. It reports whether it was clicked.
func (a *App) toBottom(c *ui.Context, k theme.Colors, running bool, bottom float32) bool {
	b := ui.Button(c, "").Label("回到底部").Tooltip("回到底部 ⌘↓").Size(theme.RowStep, theme.RowStep).Padding(0).
		Absolute().Bottom(bottom).Left(0).Right(0).Margin(0, ui.Auto)
	k.ShadowPopover.On(b.Radius(theme.RadiusPill).Background(k.SurfaceFloat).Border(theme.LineWidth, k.Line2)).
		Transition(ui.ElementTransition{Animation: &theme.MotionBase, Enter: &ui.Motion{Scale: theme.MotionScalePop}, Exit: &ui.Motion{}})
	hovered := b.Hovered()
	b.Children(func() {
		in := ui.Box(c).Fill().Radius(theme.RadiusPill).AlignItems(ui.Center).Justify(ui.Center)
		if hovered {
			in.Background(k.Hover)
		}
		in.Children(func() {
			if !running {
				ui.Icon(c, a.icons.ToBottom).FontSize(theme.IconRow).TextColor(k.Fg2)
				return
			}
			squares(c, k, c.Preferences().ReduceMotion)
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
		a.work(c, k, s, it)
	case convo.KindError:
		ui.Row(c).FillWidth().Padding(theme.Space2, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusCard).Background(k.Fill).Children(func() {
			ui.Icon(c, a.icons.Warning).FontSize(theme.Icon).TextColor(k.AttnFg)
			theme.UI.On(ui.Text(c, it.Text)).TextColor(k.Fg).Grow(1)
		})
	case convo.KindStopped:
		separator(c, k, "已停止 · "+clock(it.Time))
	case convo.KindChanges:
		a.changes(c, k, s, it)
	case convo.KindEvent:
		a.memoryEventRow(c, k, s, it)
	}
}

// separator is a thin line with a label in its middle.
func separator(c *ui.Context, k theme.Colors, label string) {
	ui.Row(c).FillWidth().Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Height(theme.LineWidth).Grow(1).Background(k.Line)
		ui.Text(c, label).Font(theme.Mono).FontSize(theme.Caption.Size()).TextColor(k.Faint).FontFeatures("tnum")
		ui.Box(c).Height(theme.LineWidth).Grow(1).Background(k.Line)
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
	col := ui.Column(c).AlignSelf(ui.End).AlignItems(ui.End).MaxWidth(0.75 * theme.CodeColumn).Gap(theme.Space1)
	if it.Unsent {
		col.Opacity(theme.OpacityFaded)
	}
	hovered := col.Hovered()
	col.Children(func() {
		if it.Steer {
			ui.Row(c).Gap(theme.Space1).AlignItems(ui.Center).Tooltip("运行中插话").Children(func() {
				ui.Icon(c, a.icons.Steer).FontSize(theme.IconSmall).TextColor(k.Muted)
				theme.Caption.On(ui.Text(c, clock(it.Time))).TextColor(k.Muted)
			})
		}
		ui.Box(c).Children(func() {
			b := ui.Box(c).Padding(theme.Space2, theme.Space3).Radius(theme.RadiusComposer).Background(k.Bubble)
			if it.Steer {
				b.Background(ui.Transparent).Border(theme.LineWidth, k.Line2).BorderStyle(ui.BorderDashed)
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
	ui.Row(c).Height(theme.RowDense).Gap(theme.SpaceHalf).AlignItems(ui.Center).Attach(ui.AnchorBottomStart, ui.AnchorBottomEnd).Left(-6).Children(func() {
		theme.Caption.On(ui.Text(c, clock(it.Time))).TextColor(k.Muted).SingleLine()
		a.copyButton(c, k, it.Key, it.Text, "复制")
	})
}

// text draws the harness's text as Markdown, streaming while it comes.
func (a *App) text(c *ui.Context, k theme.Colors, it convo.Item) {
	a.s.md.Theme = mdTheme(c, k)
	a.s.md.CodeBlock = a.codeBlock(k, it.Key)
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

// mdTheme is the Markdown look: reading size, the code font; what stands
// out (links, code spans) takes the accent, code spans on a faint accent
// tint.
func mdTheme(c *ui.Context, k theme.Colors) *markdown.Theme {
	th := markdown.DefaultTheme(c.Theme())
	th.FontSize = theme.Reading.Size()
	th.LineHeight = 22 / theme.Reading.Size()
	th.CodeFont = theme.Mono
	th.Text, th.Muted, th.Link = k.Fg, k.Muted, k.AccentFg
	th.Code, th.CodeBackground = k.AccentFg, k.AccentFg.Alpha(0.08)
	th.CodeBlockBackground, th.CodeBlockBorder, th.CodeBlockRadius = k.Surface, k.Line, theme.RadiusCard
	return th
}

// codeLines folds a code block longer than this.
const codeLines = 30

// codeBlock draws a code block of the text item: a 30-point header with
// the language, then expand (for a long block) and copy as icon buttons.
// Its state is kept by the item and the block's place in it, as the view
// of a block is made anew on each build.
func (a *App) codeBlock(k theme.Colors, item string) func(c *ui.Context, b *markdown.CodeBlockView) {
	return func(c *ui.Context, b *markdown.CodeBlockView) {
		code := b.Code()
		long := strings.Count(code, "\n") > codeLines
		id := fmt.Sprintf("code:%s:%d", item, b.Index)
		open := a.s.open[id]
		ui.Column(c).AlignSelf(ui.Stretch).Radius(theme.RadiusCard).Border(theme.LineWidth, k.Line).Background(k.Surface).Clip().Children(func() {
			ui.Row(c).Height(theme.Row).Padding(0, theme.Space1, 0, theme.Space3).AlignItems(ui.Center).BorderWidth(0, 0, theme.LineWidth, 0).BorderColor(k.Line).Children(func() {
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
					if iconButton(c, k, icon, tip, false).Size(theme.RowDense, theme.RowDense).Clicked() {
						a.s.open[id] = !open
					}
				}
				a.copyButton(c, k, id, code, "复制")
			})
			body := b.Body(c)
			if long && !open {
				body.MaxHeight(codeLines * theme.Code.Line())
			}
		})
	}
}

// copyButton copies text; its icon shows a check for 1.5 seconds after.
func (a *App) copyButton(c *ui.Context, k theme.Colors, id, text, tip string) {
	icon := a.icons.Copy
	if at, ok := a.s.copied[id]; ok && c.Now().Sub(at) < theme.MotionFlash.Duration {
		icon, tip = a.icons.Check, "已复制"
	}
	if iconButton(c, k, icon, tip, false).Size(theme.RowDense, theme.RowDense).Clicked() {
		c.WriteClipboard(text)
		a.s.copied[id] = c.Now()
		c.After(theme.MotionFlash.Duration)
	}
}

// staggered is how many steps arriving together are put one after another;
// the rest come with the fifth.
const staggered = 5

// work draws a turn's work group: a 26-point header with what it did,
// failures and its time, over one 32-point row a step on a rail. A running
// group is open with only its latest step open; a finished one folds to
// its header. A row opens to its detail when clicked.
func (a *App) work(c *ui.Context, k theme.Colors, s proto.Session, it convo.Item) {
	w := it.Work
	key := "work:" + string(s.ID) + ":" + it.Key
	open, set := a.s.open[key]
	if !set {
		open = w.Running
	}
	ui.Column(c).FillWidth().Children(func() {
		h := ui.Row(c).Height(theme.RowGroup).Gap(theme.Space1Half).AlignItems(ui.Center).Label(summary(w))
		if h.Clicked() {
			a.s.open[key] = !open
		}
		h.Children(func() {
			icon := a.icons.Expand
			if open {
				icon = a.icons.Menu
			}
			ui.Icon(c, icon).FontSize(theme.UI.Size()).TextColor(k.Fg3)
			theme.UI.On(ui.Text(c, summary(w))).TextColor(k.Fg2).SingleLine().Shrink(1)
			if w.Failed > 0 {
				ui.Textf(c, "✕ %d 个失败", w.Failed).Font(theme.Mono).FontSize(theme.Caption.Size()).FontWeight(theme.UIStrong.Weight()).TextColor(k.Fg).Margin(0, 0, 0, theme.Space1).SingleLine().Shrink(0)
			}
			ui.Spacer(c)
			if !w.Running {
				ui.Text(c, duration(w.End.Sub(w.Start))).FontSize(theme.Caption.Size()).TextColor(k.Faint).SingleLine().Shrink(0)
			}
		})
		if !open {
			return
		}
		var shown []convo.Entry
		for _, e := range w.Entries {
			if e.Approval != nil && e.Approval.Outcome == "" {
				continue // still open: it waits in the composer
			}
			shown = append(shown, e)
		}
		// Steps that come while the group is open rise in; several at once
		// come one after another, motion-stagger apart, the first five.
		seen, ok := a.s.steps[it.Key]
		if !ok {
			seen = len(shown)
		}
		a.s.steps[it.Key] = len(shown)
		ui.Column(c).FillWidth().Margin(0, 0, 0, theme.Space1Half).Children(func() {
			for i, e := range shown {
				latest := w.Running && i == len(shown)-1
				in := theme.MotionBase
				in.Delay = time.Duration(min(max(i-seen, 0), staggered-1)) * theme.MotionStagger
				ui.Column(c.Key("in:" + e.Key)).FillWidth().Transition(ui.ElementTransition{Animation: &in, Enter: &ui.Motion{Y: theme.MotionRise}}).Children(func() {
					if e.Agent != nil {
						a.agentStep(c.Key(e.Key), k, s, e, i == len(shown)-1)
						return
					}
					a.workStep(c.Key(e.Key), k, s, e, latest, i == len(shown)-1)
				})
			}
		})
	})
}

// rail draws a step's part of the rail its group's steps hang on: a line
// down the left, to the row's middle on the last step, and a tick across
// to the step.
func rail(c *ui.Context, k theme.Colors, last bool) { railAt(c, k, last, theme.RowStep/2) }

// railAt is rail for a row whose middle is mid below its top.
func railAt(c *ui.Context, k theme.Colors, last bool, mid float32) {
	ui.Box(c).Width(theme.ControlXS).AlignSelf(ui.Stretch).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		h := r.H
		if last {
			h = mid
		}
		p.Fill(ui.Rect{X: r.X, Y: r.Y, W: theme.LineWidth, H: h}, k.Line2, 0)
		p.Fill(ui.Rect{X: r.X, Y: r.Y + mid, W: theme.IconRow, H: theme.LineWidth}, k.Line2, 0)
	})
}

// agentStep draws a subagent of a work group on a 24-point row that is a
// link: a spinner while it runs, else its mark; its type and title; ✕ when
// it failed; how long it ran, or 已停止. A click opens its record in the
// viewer.
func (a *App) agentStep(c *ui.Context, k theme.Colors, s proto.Session, e convo.Entry, last bool) {
	g := *e.Agent
	kind := g.Type
	if kind == "" {
		kind = "子代理"
	}
	ui.Row(c).FillWidth().Children(func() {
		railAt(c, k, last, theme.RowDense/2)
		r := ui.Row(c).Grow(1).Shrink(1).Height(theme.RowDense).Padding(0, theme.Space1).Margin(0, 0, 0, -theme.Space1).Gap(theme.Space2).AlignItems(ui.Center).Radius(theme.RadiusRow).Label(kind + " " + g.Title)
		if r.Hovered() {
			r.Background(k.Fill)
		}
		if r.Clicked() {
			a.openAgent(s, g.ID)
		}
		r.Children(func() {
			if g.Status == proto.SubagentRunning {
				ui.Spinner(c).Size(theme.Icon, theme.Icon)
			} else {
				ui.Icon(c, a.icons.Agent).FontSize(theme.Icon).TextColor(k.Fg3)
			}
			ui.Text(c, kind).FontSize(theme.CodeInline.Size()).TextColor(k.Fg).SingleLine().Shrink(0)
			ui.Text(c, g.Title).FontSize(theme.CodeInline.Size()).TextColor(k.Fg3).SingleLine().Shrink(1)
			if g.Status == proto.SubagentFailed {
				ui.Text(c, "✕").Font(theme.Mono).FontSize(theme.Caption.Size()).FontWeight(theme.UIStrong.Weight()).TextColor(k.Fg)
			}
			ui.Spacer(c)
			ui.Text(c, agentTime(c, convo.Subagent{Subagent: g, Start: e.Start, End: e.End})).FontSize(theme.Caption.Size()).TextColor(k.Faint).SingleLine().Shrink(0)
			ui.Icon(c, a.icons.Expand).FontSize(theme.IconSmall).TextColor(k.Faint)
		})
	})
}

// agentTime is a subagent's time at the end of its row: 48 秒… while it
// runs, 后台 · 2 分 10 秒… in the background, 已停止 once stopped.
func agentTime(c *ui.Context, g convo.Subagent) string {
	switch g.Status {
	case proto.SubagentStopped:
		return "已停止"
	case proto.SubagentRunning:
		c.After(time.Second)
		t := duration(g.Took(c.Now())) + "…"
		if g.Background {
			t = "后台 · " + t
		}
		return t
	}
	return duration(g.Took(c.Now()))
}

// openAgent opens a subagent's record in the viewer.
func (a *App) openAgent(s proto.Session, id string) {
	a.openTab(a.panelFor(s.ID), panelTab{agent: id})
	if !a.panelOpen || a.fit.panel == 0 {
		a.bringPanel()
	}
}

// workStep draws one step of a work group on its row, and its detail when
// open: thinking's summary, a note's text, a command in full with its
// output. latest says it is the newest step of a running group, which
// opens by itself.
func (a *App) workStep(c *ui.Context, k theme.Colors, s proto.Session, e convo.Entry, latest, last bool) {
	key := "step:" + string(s.ID) + ":" + e.Key
	var (
		icon               *ui.SVG
		verb, object, tail string
		file, failed       bool
		running, unfinish  bool
		opens              bool
		tc                 proto.ToolCall
	)
	switch {
	case e.Tool != nil:
		tc = *e.Tool
		icon, verb = a.action(tc)
		object = firstLine(tc.Summary)
		file = tc.Kind == proto.ToolRead || tc.Kind == proto.ToolEdit
		if file {
			object = relPath(s.Directory, object)
		}
		failed = tc.Status == proto.ToolFailed
		// A tool still running after its turn ended will not be reported done.
		unfinish = tc.Status == proto.ToolRunning && !a.conv(s.ID).Running()
		running = tc.Status == proto.ToolRunning && !unfinish
		long := utf8.RuneCountInString(tc.Summary) > inputRunes || strings.Contains(tc.Summary, "\n")
		opens = tc.Output != nil || failed || long || tc.Kind == proto.ToolExec && tc.Summary != ""
	case e.Approval != nil:
		ap := e.Approval
		icon, verb = a.icons.Allowed, "已允许"
		switch ap.Outcome {
		case proto.OutcomeDenied:
			icon, verb = a.icons.Denied, "已拒绝"
		case proto.OutcomeExpired:
			icon, verb = a.icons.Denied, "未答复"
		}
		object, tail = ap.Request.Title, clock(ap.At)
	case e.Note != "":
		icon, verb, object = a.icons.Note, "说明", firstLine(e.Note)
		opens = true
	default:
		icon, verb = a.icons.Thinking, "思考过程"
		opens = e.Thinking != ""
	}
	open, set := a.s.open[key]
	if !set {
		open = latest
	}
	open = open && opens
	if open && tc.Output != nil {
		a.loadOutput(s, tc, "out:"+string(s.ID)+":"+tc.ID)
	}
	ui.Row(c).FillWidth().Children(func() {
		rail(c, k, last)
		ui.Column(c).Grow(1).Shrink(1).Children(func() {
			r := ui.Row(c).Height(theme.RowStep).Gap(theme.Space2).AlignItems(ui.Center).Label(verb + " " + object)
			hovered := r.Hovered()
			if opens && r.Clicked() {
				a.s.open[key] = !open
			}
			r.Children(func() {
				if running {
					ui.Spinner(c).Size(theme.Icon, theme.Icon)
				} else {
					ui.Icon(c, icon).FontSize(theme.Icon).TextColor(k.Fg3)
				}
				ui.Text(c, verb).FontSize(theme.CodeInline.Size()).TextColor(k.Fg).SingleLine().Shrink(0)
				if file {
					ui.Image(c, a.icons.FileType(tc.Summary)).Size(theme.IconRow, theme.IconRow)
				}
				obj := ui.Text(c, object).TextColor(k.Fg3).SingleLine().Shrink(1)
				if e.Note != "" {
					obj.FontSize(theme.CodeInline.Size())
				} else {
					obj.Font(theme.Mono).FontSize(theme.Secondary.Size()).FontFeatures("liga=0")
				}
				if file {
					obj.TextColor(k.AccentFg)
				}
				if e.Approval != nil {
					obj.Tooltip(object)
				}
				if failed {
					// "✕ 退出码 N" once harnesses report the exit code.
					ui.Text(c, "✕").Font(theme.Mono).FontSize(theme.Caption.Size()).FontWeight(theme.UIStrong.Weight()).TextColor(k.Fg)
				}
				ui.Spacer(c)
				switch {
				case unfinish:
					ui.Text(c, "未完成").FontSize(theme.Caption.Size()).TextColor(k.Faint)
				case running:
					ui.Text(c, duration(c.Now().Sub(e.Start))+"…").FontSize(theme.Caption.Size()).TextColor(k.Faint)
					c.After(time.Second)
				case hovered && tail != "":
					ui.Text(c, tail).FontSize(theme.Caption.Size()).TextColor(k.Faint)
				case hovered && e.Tool != nil && !open:
					ui.Text(c, duration(e.Duration())).FontSize(theme.Caption.Size()).TextColor(k.Faint)
				}
				if open {
					ui.Icon(c, a.icons.Menu).FontSize(theme.UI.Size()).TextColor(k.Fg3)
				}
			})
			if open {
				a.stepDetail(c, k, s, e, tc, failed)
			}
		})
	})
}

// stepDetail is what an open step shows under its row, in from its icon.
func (a *App) stepDetail(c *ui.Context, k theme.Colors, s proto.Session, e convo.Entry, tc proto.ToolCall, failed bool) {
	prose := func(t string) {
		theme.SecondaryBlock.On(ui.Text(c, t)).TextColor(k.Fg2).Margin(0, 0, theme.Space2, theme.Space6).Selectable()
	}
	switch {
	case e.Note != "":
		prose(e.Note)
	case e.Tool == nil:
		prose(e.Thinking)
	default:
		if tc.Summary != "" {
			theme.Code.On(ui.Text(c, tc.Summary)).TextColor(k.Fg2).
				Margin(0, 0, theme.Space1Half, theme.Space6).Selectable()
		}
		key := "out:" + string(s.ID) + ":" + tc.ID
		switch {
		case tc.Output != nil:
			a.output(c, k, key, func() { a.loadOutput(s, tc, key) })
		case failed:
			ui.Row(c).Margin(0, 0, theme.Space2, theme.Space6).Gap(theme.Space1Half).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, a.icons.Failed).FontSize(theme.UI.Size()).TextColor(k.Muted)
				theme.Secondary.On(ui.Text(c, "失败，harness 没给出原因")).TextColor(k.Muted)
			})
		}
	}
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
	box := ui.Column(c).Margin(0, 0, theme.Space2, theme.Space6).Radius(theme.RadiusCard).Border(theme.LineWidth, k.Line).Background(k.Surface).Clip()
	hovered := box.Hovered()
	box.Children(func() {
		if why, ok := a.s.outputErr[key]; ok {
			ui.Row(c).Padding(theme.Space1, theme.Space1, theme.Space1, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, a.icons.Failed).FontSize(theme.UI.Size()).TextColor(k.Muted)
				theme.Secondary.On(ui.Text(c, "没取到输出："+why)).TextColor(k.Muted).Grow(1).Shrink(1)
				if iconButton(c, k, a.icons.Reconnect, "重试", false).Size(theme.RowDense, theme.RowDense).Clicked() {
					delete(a.s.outputs, key)
					retry()
				}
			})
			return
		}
		if o == nil {
			ui.Box(c).Padding(theme.Space2, theme.Space3).Children(func() { ui.Spinner(c).Size(theme.IconSmall, theme.IconSmall) })
			return
		}
		lines := strings.Split(strings.TrimRight(o.Text, "\n"), "\n")
		all := a.s.open[key+":all"]
		shown := lines
		if len(shown) > outputLines && !all {
			shown = shown[:outputLines]
		}
		theme.Code.On(ui.Text(c, strings.Join(shown, "\n"))).TextColor(k.Fg2).
			Padding(theme.Space2, theme.Space3).Selectable()
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
		ui.Row(c).Padding(theme.SpaceHalf, theme.Space1, theme.SpaceHalf, theme.Space3).Gap(theme.SpaceHalf).AlignItems(ui.Center).BorderWidth(theme.LineWidth, 0, 0, 0).BorderColor(k.Line).Children(func() {
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
				if iconButton(c, k, a.icons.Unfold, "展开全部", false).Size(theme.RowDense, theme.RowDense).Clicked() {
					a.s.open[key+":all"] = true
				}
			case len(lines) > outputLines:
				if iconButton(c, k, a.icons.Fold, "折叠", false).Size(theme.RowDense, theme.RowDense).Clicked() {
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

// summary is a work group's one line: what it did, counted, in the order
// it first did each: 思考 N 次 · 运行 N 条命令 · … · 写了 N 段说明.
func summary(w *convo.Work) string {
	counts := map[string]int{}
	var order []string
	for _, e := range w.Entries {
		var part string
		switch {
		case e.Tool != nil:
			part = toolVerb(*e.Tool)
		case e.Agent != nil:
			part = "派出 %d 个子代理"
		case e.Approval != nil:
			part = "审批 %d 次"
		case e.Note != "":
			part = "写了 %d 段说明"
		default:
			part = "思考 %d 次"
		}
		if counts[part] == 0 {
			order = append(order, part)
		}
		counts[part]++
	}
	// Approvals count only in a group of nothing else.
	if len(order) > 1 {
		order = slices.DeleteFunc(order, func(v string) bool { return v == "审批 %d 次" })
	}
	parts := make([]string, len(order))
	for i, v := range order {
		parts[i] = fmt.Sprintf(v, counts[v])
	}
	if n := runningAgents(w, true); n > 0 && !w.Running {
		parts = append(parts, fmt.Sprintf("%d 个在后台运行", n))
	}
	return strings.Join(parts, " · ")
}

// runningAgents counts a group's subagents still running; background
// says only those the turn did not wait for.
func runningAgents(w *convo.Work, background bool) int {
	n := 0
	for _, e := range w.Entries {
		if g := e.Agent; g != nil && g.Status == proto.SubagentRunning && (!background || g.Background) {
			n++
		}
	}
	return n
}

// toolVerb is a tool call's part of a summary, with %d for the count.
func toolVerb(tc proto.ToolCall) string {
	switch tc.Name {
	case "Grep", "Glob", "search_files":
		return "搜索 %d 次"
	case "Write", "write_file":
		return "写入 %d 个文件"
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

// changes draws the files a turn changed, at the end of its reply: a
// 36-point header with the count and the lines added and removed, then
// each file with its status, path and lines, from the session's diff. A
// row opens the right panel's changes on its file; undoing comes with
// checkpoints.
func (a *App) changes(c *ui.Context, k theme.Colors, s proto.Session, it convo.Item) {
	d := a.diff(s)
	var add, del int
	counted := false // line counts come only from git repositories
	stats := make([]*proto.FileDiff, len(it.Files))
	for i, f := range it.Files {
		if st, ok := fileStats(d, s.Directory, f.Path); ok {
			stats[i] = &st
			add, del, counted = add+st.Additions, del+st.Deletions, true
		}
	}
	ui.Column(c).FillWidth().Radius(theme.RadiusCard).Border(theme.LineWidth, k.Line).Clip().Children(func() {
		ui.Row(c).Height(theme.RowStep).Padding(0, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).BorderWidth(0, 0, theme.LineWidth, 0).BorderColor(k.Line).Children(func() {
			theme.UIStrong.On(ui.Textf(c, "改了 %d 个文件", len(it.Files))).TextColor(k.Fg)
			if counted {
				lines(c, k, add, del)
			}
		})
		for i, f := range it.Files {
			key := relPath(s.Directory, f.Path)
			r := ui.Row(c.Key(f.Path)).Padding(theme.Space1Half, theme.Space3).Gap(theme.Space2).AlignItems(ui.Center).Label(key)
			if r.Hovered() {
				r.Background(k.Fill)
			}
			if r.Clicked() {
				a.showChange(s, key)
			}
			r.Children(func() {
				ui.Text(c, opLetter(f.Op)).Font(theme.Mono).FontSize(theme.Secondary.Size()).TextColor(k.Muted).Width(theme.IconSmall)
				ui.Image(c, a.icons.FileType(f.Path)).Size(theme.IconRow, theme.IconRow)
				ui.Text(c, relPath(s.Directory, f.Path)).Font(theme.Mono).FontSize(theme.Secondary.Size()).TextColor(k.AccentFg).SingleLine().Grow(1).Shrink(1)
				if st := stats[i]; st != nil {
					lines(c, k, st.Additions, st.Deletions)
				}
			})
		}
	})
}

// lines shows lines added and removed, +24 −9, in the diff colors.
func lines(c *ui.Context, k theme.Colors, add, del int) {
	ui.Row(c).Gap(theme.Space1).Children(func() {
		if add > 0 || del == 0 {
			ui.Textf(c, "+%d", add).Font(theme.Mono).FontSize(theme.Secondary.Size()).TextColor(k.Add)
		}
		if del > 0 {
			ui.Textf(c, "−%d", del).Font(theme.Mono).FontSize(theme.Secondary.Size()).TextColor(k.Del)
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
