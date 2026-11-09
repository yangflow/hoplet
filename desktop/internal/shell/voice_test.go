package shell

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/desktop/internal/voicemedia"
	"github.com/yangflow/hoplet/proto"
)

func click(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if err := tt.Click(name); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
}

func TestCallConsentAsksOnce(t *testing.T) {
	a, h, tt, _ := newApp(t)
	click(t, tt, "语音通话")
	if !tt.HasText("开始通话") || h.voiceStarts != 0 {
		t.Fatalf("no consent first: %q, %d starts", tt.Texts(), h.voiceStarts)
	}
	click(t, tt, "取消")
	if a.v.phase != callIdle || h.voiceConsented || h.voiceStarts != 0 {
		t.Fatalf("cancel started something: %v %v %d", a.v.phase, h.voiceConsented, h.voiceStarts)
	}
	click(t, tt, "语音通话")
	click(t, tt, "开始通话")
	if !h.voiceConsented || h.voiceStarts != 1 {
		t.Fatalf("consented %v, %d starts", h.voiceConsented, h.voiceStarts)
	}
	h.voice.Ended(nil)
	tt.Frame()
	click(t, tt, "语音通话")
	if tt.HasText("开始通话") || h.voiceStarts != 2 {
		t.Fatalf("asked twice: %q, %d starts", tt.Texts(), h.voiceStarts)
	}
}

func TestCallPageStates(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.voiceConsented = true
	click(t, tt, "语音通话")
	for _, want := range []string{"正在连接…", "--:--", "studio"} {
		if !tt.HasText(want) {
			t.Fatalf("connecting: no %q in %q", want, tt.Texts())
		}
	}
	click(t, tt, "静音")
	if a.v.muted {
		t.Fatal("muted before the call connected")
	}

	h.voice.Connected()
	tt.Frame()
	if tt.HasText("正在连接…") || !tt.HasText("00:00") {
		t.Fatalf("listening: %q", tt.Texts())
	}
	tt.Advance(65 * time.Second)
	tt.Frame()
	if !tt.HasText("01:05") {
		t.Fatalf("clock: %q", tt.Texts())
	}
	h.voice.Speaking(true)
	tt.Frame()
	if a.dotsState().period == 0 || !a.v.speaking {
		t.Fatal("speaking not shown")
	}

	click(t, tt, "静音")
	if !h.voiceMuted || !tt.HasText("已静音") {
		t.Fatalf("muted: %v %q", h.voiceMuted, tt.Texts())
	}
	if _, ok := tt.Find("取消静音"); !ok {
		t.Fatal("no unmute")
	}
	click(t, tt, "取消静音")
	if h.voiceMuted || tt.HasText("已静音") {
		t.Fatalf("unmuted: %v %q", h.voiceMuted, tt.Texts())
	}

	click(t, tt, "挂断")
	if a.v.phase != callIdle || h.voiceHangups != 1 {
		t.Fatalf("hangup: %v, %d", a.v.phase, h.voiceHangups)
	}
	if tt.HasText("01:05") {
		t.Fatalf("page stayed: %q", tt.Texts())
	}
	// The ended call's late report must not disturb what comes next.
	h.voice.Ended(errors.New("late"))
	tt.Frame()
	if a.v.phase != callIdle {
		t.Fatalf("late report: %v", a.v.phase)
	}
}

func TestCallFailureCopy(t *testing.T) {
	api := func(code proto.ErrorCode, msg string) error { return &proto.APIError{Code: code, Message: msg} }
	for _, tc := range []struct {
		name        string
		err         error
		title, note string
	}{
		{"no codex", api(proto.CodeVoiceNoHarness, "x"), "这台机器没有可用的 Codex", "安装独立版 Codex，并用 ChatGPT 账号登录后再试。"},
		{"api key", api(proto.CodeVoiceNotSubscribed, "x"), "语音需要 ChatGPT 账号登录", "当前 Codex 用的是 API Key 或自定义服务。"},
		{"component", api(proto.CodeVoiceNoComponent, "x"), "Codex 缺少语音组件", "换成官方独立安装包后再试。"},
		{"microphone", voicemedia.ErrMicrophone, "没有麦克风权限", "在系统设置的隐私与安全性里允许 Hoplet 使用麦克风。"},
		{"busy", api(proto.CodeVoiceBusy, "macbook"), "studio 正在通话", "通话在 macbook 上，先在那里挂断。"},
		{"media", api(proto.CodeVoiceMedia, "raw service text"), "语音连接失败", "网络可能挡住了 UDP。检查代理，或打开 TUN 模式后重试。"},
		{"quota", api(proto.CodeVoiceQuota, "x"), "Codex 额度用完了", "额度恢复后再试。"},
		{"not enabled", api(proto.CodeVoiceNotEnabled, "x"), "这个账号暂时用不了语音", "当前账号或地区可能没有开通 Codex 语音。"},
		{"anything else", errors.New("raw service text"), "语音连接失败", "网络可能挡住了 UDP。检查代理，或打开 TUN 模式后重试。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, h, tt, _ := newApp(t)
			h.voiceConsented = true
			click(t, tt, "语音通话")
			h.voice.Ended(tc.err)
			tt.Frame()
			if !tt.HasText(tc.title) || !tt.HasText(tc.note) {
				t.Fatalf("texts %q", tt.Texts())
			}
			if tt.HasText("raw service text") || tt.HasText("--:--") {
				t.Fatalf("failure shows too much: %q", tt.Texts())
			}
		})
	}
}

func TestCallFailureRetryAndClose(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.voiceConsented = true
	click(t, tt, "语音通话")
	h.voice.Ended(errors.New("x"))
	tt.Frame()
	click(t, tt, "重试")
	if h.voiceStarts != 2 || a.v.phase != callConnecting {
		t.Fatalf("retry: %d, %v", h.voiceStarts, a.v.phase)
	}
	h.voice.Ended(errors.New("x"))
	tt.Frame()
	click(t, tt, "关闭")
	if a.v.phase != callIdle || tt.HasText("语音连接失败") {
		t.Fatalf("close: %v %q", a.v.phase, tt.Texts())
	}
}

func TestCallNeedsDaemon(t *testing.T) {
	a, h, tt, _ := newApp(t)
	h.voiceConsented = true
	a.Lost(errors.New("gone"))
	tt.Frame()
	click(t, tt, "语音通话")
	if h.voiceStarts != 0 || a.v.phase != callIdle {
		t.Fatalf("%d starts, %v", h.voiceStarts, a.v.phase)
	}
}
