package shell

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/theme"
	"github.com/yangflow/hoplet/desktop/internal/voicemedia"
	"github.com/yangflow/hoplet/proto"
)

// callPhase is how far the call page's call has got.
type callPhase int

const (
	callIdle callPhase = iota
	callConnecting
	callLive
	callFailed
)

// voiceState is the call and its page.
type voiceState struct {
	consent bool // the first-call consent dialog is open
	phase   callPhase
	call    int // the current attempt; events of older ones are dropped

	muted, speaking bool
	since           time.Time // when the call connected, set by the page

	failTitle, failNote string
}

// OpenCall answers the sidebar's microphone: it asks for consent on this
// device's first call and otherwise starts one.
func (a *App) OpenCall() {
	switch {
	case a.v.phase != callIdle:
	case !a.connected:
		a.Fail(errors.New("没有连上守护进程"), nil)
	case !a.host.VoiceConsented():
		a.v.consent = true
	default:
		a.startCall()
	}
}

func (a *App) startCall() {
	a.v.call++
	call := a.v.call
	a.v.phase, a.v.muted, a.v.speaking, a.v.since = callConnecting, false, false, time.Time{}
	current := func(fn func()) {
		if a.v.call == call {
			fn()
		}
	}
	a.host.StartCall(voicemedia.Events{
		Connected: func() { current(func() { a.v.phase = callLive }) },
		Speaking:  func(b bool) { current(func() { a.v.speaking = b }) },
		Ended: func(err error) {
			current(func() {
				if err == nil {
					a.v.phase = callIdle
					return
				}
				a.v.phase = callFailed
				a.v.failTitle, a.v.failNote = callFailure(err, a.machine)
			})
		},
	})
}

// hangUp leaves the page at once; the call winds down behind it.
func (a *App) hangUp() {
	a.v.call++
	a.v.phase = callIdle
	a.host.HangUp()
}

func (a *App) toggleMute() {
	if a.v.phase == callLive {
		a.v.muted = !a.v.muted
		a.host.MuteCall(a.v.muted)
	}
}

// callFailure is the fixed title and remedy for why a call failed. What
// the service said never reaches the page.
func callFailure(err error, machine string) (title, note string) {
	const (
		mediaTitle = "语音连接失败"
		mediaNote  = "网络可能挡住了 UDP。检查代理，或打开 TUN 模式后重试。"
	)
	if errors.Is(err, voicemedia.ErrMicrophone) {
		return "没有麦克风权限", "在系统设置的隐私与安全性里允许 Hoplet 使用麦克风。"
	}
	var api *proto.APIError
	if !errors.As(err, &api) {
		return mediaTitle, mediaNote
	}
	switch api.Code {
	case proto.CodeVoiceNoHarness:
		return "这台机器没有可用的 Codex", "安装独立版 Codex，并用 ChatGPT 账号登录后再试。"
	case proto.CodeVoiceNotSubscribed:
		return "语音需要 ChatGPT 账号登录", "当前 Codex 用的是 API Key 或自定义服务。"
	case proto.CodeVoiceNoComponent:
		return "Codex 缺少语音组件", "换成官方独立安装包后再试。"
	case proto.CodeVoiceBusy:
		return machine + " 正在通话", "通话在 " + api.Message + " 上，先在那里挂断。"
	case proto.CodeVoiceQuota:
		return "Codex 额度用完了", "额度恢复后再试。"
	case proto.CodeVoiceNotEnabled:
		return "这个账号暂时用不了语音", "当前账号或地区可能没有开通 Codex 语音。"
	}
	return mediaTitle, mediaNote
}

// voicePage covers the content area with the call.
func (a *App) voicePage(c *ui.Context, k theme.Colors) {
	failed := a.v.phase == callFailed
	ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Background(k.Pane).AlignItems(ui.Center).Justify(ui.Center).
		Padding(0, 0, callPageLift, 0).DragWindow().Children(func() {
		ui.Box(c).Padding(0, 0, theme.Space2, 0).Children(func() { callDots(c, k, a.dotsState()) })
		if failed {
			a.callFailed(c, k)
			return
		}
		caption := ui.Box(c).Padding(theme.Space4, 0, 0, 0).MinHeight(callCaptionHeight)
		caption.Children(func() {
			text := ""
			switch {
			case a.v.phase == callConnecting:
				text = "正在连接…"
			case a.v.muted:
				text = "已静音"
			}
			if text != "" {
				theme.Reading.On(ui.Text(c, text)).TextColor(k.Muted)
			}
		})
		ui.Row(c).Absolute().Left(0).Right(0).Bottom(theme.Space6).Justify(ui.Center).Children(func() { a.callBar(c, k) })
	})
}

func (a *App) callFailed(c *ui.Context, k theme.Colors) {
	ui.Column(c).Padding(theme.Space4, 0, 0, 0).Gap(theme.Space1Half).AlignItems(ui.Center).MaxWidth(callFailureWidth).Children(func() {
		theme.Title.On(ui.Text(c, a.v.failTitle)).TextColor(k.Fg)
		theme.Reading.On(ui.Text(c, a.v.failNote)).TextColor(k.Muted).TextAlign(ui.Center)
	})
	ui.Row(c).Padding(theme.Space4, 0, 0, 0).Gap(theme.Space2).Children(func() {
		if secondaryButton(c, k, "关闭").Clicked() {
			a.v.phase = callIdle
		}
		if primaryButton(c, k, "重试").Clicked() {
			a.startCall()
		}
	})
}

func secondaryButton(c *ui.Context, k theme.Colors, text string) ui.Element {
	return theme.UI.On(ui.Button(c, text)).FontWeight(theme.UIMedium.Weight()).Height(theme.Row).Padding(0, theme.Space3).
		Radius(theme.RadiusControl).Background(k.Fill).Border(0, ui.Transparent).TextColor(k.Fg)
}

func primaryButton(c *ui.Context, k theme.Colors, text string) ui.Element {
	return theme.UIStrong.On(ui.Button(c, text)).Height(theme.Row).Padding(0, theme.Space3).
		Radius(theme.RadiusControl).Background(k.Accent).Border(0, ui.Transparent).TextColor(k.OnAccent)
}

// callBar shows how the call is going and holds its controls.
func (a *App) callBar(c *ui.Context, k theme.Colors) {
	connecting := a.v.phase == callConnecting
	dot, clock := k.Accent, "--:--"
	if connecting || a.v.muted {
		dot = k.Faint
	}
	if !connecting {
		if a.v.since.IsZero() {
			a.v.since = c.Now()
		}
		elapsed := c.Now().Sub(a.v.since)
		clock = callClock(elapsed)
		c.After(time.Second - elapsed%time.Second)
	}
	bar := ui.Row(c).Height(theme.CallBar).Padding(0, theme.Space1Half, 0, theme.Space4).Gap(theme.Space1).AlignItems(ui.Center).
		Radius(theme.RadiusPill).Background(k.Raised).Border(theme.LineWidth, k.Line).Label("通话")
	k.ShadowComposer.On(bar).Children(func() {
		ui.Box(c).Size(theme.Dot, theme.Dot).Radius(theme.RadiusPill).Background(dot)
		ui.Box(c).Padding(0, 0, 0, theme.Space2).Children(func() {
			theme.CodeCaption.On(ui.Text(c, clock)).TextColor(k.Fg2)
		})
		ui.Box(c).Padding(0, theme.Space2).Children(func() {
			theme.Secondary.On(ui.Text(c, a.machine)).TextColor(k.Muted).SingleLine()
		})
		ui.Box(c).Size(theme.LineWidth, theme.RowDense).Background(k.Line)
		icon, tip := a.icons.Mic, "静音"
		if a.v.muted {
			icon, tip = a.icons.MicOff, "取消静音"
		}
		mute := ui.Button(c, "").Label(tip).Tooltip(tip).Size(theme.BarCompact, theme.BarCompact).Padding(0).
			Radius(theme.RadiusPill).Background(ui.Transparent).Border(0, ui.Transparent)
		tone := k.Fg3
		if a.v.muted {
			mute.Background(k.Fill)
			tone = k.Fg
		}
		mute.Children(func() { ui.Icon(c, icon).FontSize(theme.IconCall).TextColor(tone) })
		if mute.Clicked() {
			a.toggleMute()
		}
		hang := ui.Button(c, "").Label("挂断").Tooltip("挂断").Size(callHangupWidth, theme.BarCompact).Padding(0).
			Radius(theme.RadiusPill).Background(k.Del).Border(0, ui.Transparent)
		hang.Children(func() {
			ui.Box(c).Rotate(callHangupTurn).Children(func() {
				ui.Icon(c, a.icons.Phone).FontSize(theme.IconCall).TextColor(k.OnInverse)
			})
		})
		if hang.Clicked() {
			a.hangUp()
		}
	})
}

// callClock is a call's length as minutes and seconds; past an hour the
// minutes run on.
func callClock(d time.Duration) string {
	s := int(d / time.Second)
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// consentDialog asks once per device before the first call.
func (a *App) consentDialog(c *ui.Context, k theme.Colors) {
	if !a.v.consent {
		return
	}
	ui.DialogBase(c, &a.v.consent, func(_, panel ui.Element) {
		panel.Width(theme.DialogWidth).Padding(theme.Space4).Gap(theme.Space3).Radius(theme.RadiusPanel).Background(k.SurfaceFloat).
			Border(theme.LineWidth, k.Line2).Label("开始语音通话").
			Transition(ui.ElementTransition{Animation: &theme.MotionBase, Enter: &ui.Motion{Scale: theme.MotionScaleDialog}, Exit: &ui.Motion{}})
		k.ShadowPopover.On(panel)
		panel.Children(func() {
			theme.UIStrong.On(ui.Text(c, "开始语音通话")).TextColor(k.Fg)
			theme.Reading.On(ui.Text(c, "通话时，你的声音会发送给 OpenAI，并使用你的 Codex 额度。Hoplet 只保存文字转录，不保存声音。")).TextColor(k.Fg2)
			ui.Row(c).Gap(theme.Space2).Justify(ui.End).Children(func() {
				if secondaryButton(c, k, "取消").Clicked() {
					a.v.consent = false
				}
				if primaryButton(c, k, "开始通话").Clicked() {
					a.v.consent = false
					a.host.ConsentVoice()
					a.startCall()
				}
			})
		})
	})
}

// Layout of the call page, as the design draws it.
const (
	callPageLift      = 96 // the column's centre sits above the bar
	callCaptionHeight = 48
	callFailureWidth  = 480
	callHangupWidth   = 72
	callHangupTurn    = 135
)

// dotsState is one state of the call's dots: each dot's scale and opacity
// run between lo and hi, its phase lagging by lag of a cycle per cell of
// distance from the centre.
type dotsState struct {
	period               time.Duration
	scaleLo, scaleHi     float64
	opacityLo, opacityHi float64
	lag                  float64
	muted                bool
}

func (a *App) dotsState() dotsState {
	switch {
	case a.v.phase == callConnecting:
		return dotsState{period: theme.MotionCallBreathe, scaleLo: .62, scaleHi: .62, opacityLo: .2, opacityHi: .55}
	case a.v.phase == callFailed || a.v.muted:
		return dotsState{scaleLo: .55, scaleHi: .55, opacityLo: .55, opacityHi: .55, muted: true}
	case a.v.speaking:
		return dotsState{period: theme.MotionCallSpeak, scaleLo: .45, scaleHi: 1, opacityLo: .3, opacityHi: 1, lag: 150.0 / 1200}
	}
	return dotsState{period: theme.MotionCallListen, scaleLo: .62, scaleHi: .86, opacityLo: .45, opacityHi: .9, lag: 180.0 / 2200}
}

// callGrid is how many cells the dots span each way, cut to a circle of
// callGridRadius cells.
const (
	callGrid       = 7
	callGridRadius = 3.4
	callDotSize    = 0.72 // of a cell
	callDotRadius  = 0.22 // of a cell
)

// callDots draws the dots from one Loop, which also keeps frames coming.
// A state with no period, or a desktop asking for less motion, rests at
// the low end of the wave.
func callDots(c *ui.Context, k theme.Colors, st dotsState) {
	b := ui.Box(c).Size(theme.CallDots, theme.CallDots).Label("通话状态")
	still := st.period == 0 || c.Preferences().ReduceMotion
	var f float64
	if !still {
		f = float64(b.Loop(st.period, st.period, ui.Linear))
	}
	color := k.LogoPiece
	if st.muted {
		color = k.Faint
	}
	b.Draw(func(p *ui.Painter, r ui.Rect) {
		cell := r.W / callGrid
		mid := float64(callGrid-1) / 2
		for row := range callGrid {
			for col := range callGrid {
				x, y := float64(col)-mid, float64(row)-mid
				dist := math.Hypot(x, y)
				if dist > callGridRadius {
					continue
				}
				wave := 0.0
				if !still {
					phase := math.Mod(f-dist*st.lag+1, 1)
					u := 1 - math.Abs(2*phase-1)
					wave = u * u * (3 - 2*u)
				}
				side := cell * callDotSize * float32(st.scaleLo+(st.scaleHi-st.scaleLo)*wave)
				cx, cy := r.X+cell*(float32(col)+.5), r.Y+cell*(float32(row)+.5)
				radius := side * (callDotRadius / callDotSize)
				p.Fill(ui.Rect{X: cx - side/2, Y: cy - side/2, W: side, H: side}, color.Alpha(float32(st.opacityLo+(st.opacityHi-st.opacityLo)*wave)), radius)
			}
		}
	})
}
