package voicemedia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

const fakeName = "codex-voice-host"

// TestMain turns this binary into a fake component when it runs under the
// component's name.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == fakeName {
		fakeComponent()
		return
	}
	os.Exit(m.Run())
}

// scenario is how a fake component misbehaves.
type scenario struct {
	DieOn   string `json:"dieOn,omitempty"`
	HangOn  string `json:"hangOn,omitempty"`
	WrongOn string `json:"wrongOn,omitempty"`

	SpeakerPeak int `json:"speakerPeak,omitempty"`
}

var fakeReplies = map[string]string{
	"hello": "ready", "initializeRuntime": "runtimeReady", "startTransport": "offer",
	"applyAnswer": "transportReady", "openDevices": "devicesOpened",
	"setAudioControls": "audioControlsApplied", "inspectAudio": "audioState",
}

func appendLog(line string) {
	f, err := os.OpenFile("log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(2)
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, line)
}

func fakeComponent() {
	if slices.Contains(os.Args[1:], "--build-commit") {
		fmt.Println("abc123")
		return
	}
	// Reading these from the working directory stands in for the real
	// component's need to start from its own directory.
	b, err := os.ReadFile("scenario.json")
	if err != nil {
		os.Exit(23)
	}
	var s scenario
	if json.Unmarshal(b, &s) != nil {
		os.Exit(2)
	}
	_ = os.WriteFile("pid", []byte(strconv.Itoa(os.Getpid())), 0o600)
	_ = os.WriteFile("env", []byte(strings.Join(os.Environ(), "\n")), 0o600)
	for {
		frame, err := readFrame(os.Stdin)
		if err != nil {
			return
		}
		var m message
		if json.Unmarshal(frame, &m) != nil {
			os.Exit(2)
		}
		switch m.Type {
		case "setAudioControls":
			appendLog(fmt.Sprintf("audio mic-muted=%v speaker-suppressed=%v", m.Controls.MicrophoneMuted, m.Controls.SpeakerSuppressed))
		case "applyAnswer":
			appendLog("applyAnswer " + m.SDP)
		case "openDevices":
			appendLog(fmt.Sprintf("openDevices selection=%v", m.Selection != nil))
		case "hello":
			appendLog(fmt.Sprintf("hello %d %s", m.Protocol, m.BuildCommit))
		default:
			appendLog(m.Type)
		}
		if m.Type == s.DieOn {
			os.Exit(1)
		}
		if m.Type == s.HangOn {
			_, _ = io.Copy(io.Discard, os.Stdin)
			return
		}
		r := map[string]any{"type": fakeReplies[m.Type]}
		if m.Type == s.WrongOn {
			r["type"] = "error"
		}
		if m.Type == "startTransport" {
			r["sdp"] = "the-offer"
		}
		if m.Type == "inspectAudio" {
			r["state"] = map[string]int{"microphonePeak": 7, "speakerPeak": max(9, s.SpeakerPeak)}
		}
		out, _ := json.Marshal(r)
		if writeFrame(os.Stdout, out) != nil {
			os.Exit(2)
		}
	}
}

// install puts a fake component in its own directory and returns its path.
func install(t *testing.T, s scenario) string {
	t.Helper()
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fakeName)
	if err := os.Symlink(self, path); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(dir, "scenario.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readLog(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "log"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// assertGone checks the component that ran from path has been reaped.
func assertGone(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("component %d still exists: %v", pid, err)
	}
}

// signal is a fake host that records into the component's log, so one list
// shows the order across both.
type signal struct {
	path       string
	startErr   error
	confirmErr error
	stopErr    error
	stops      int
}

func (s *signal) note(line string) {
	f, err := os.OpenFile(filepath.Join(filepath.Dir(s.path), "log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, line)
}

func (s *signal) Start(_ context.Context, offer string) (proto.VoiceStartResult, error) {
	s.note("host.start " + offer)
	if s.startErr != nil {
		return proto.VoiceStartResult{}, s.startErr
	}
	return proto.VoiceStartResult{Call: "call-1", Answer: "the-answer"}, nil
}

func (s *signal) Confirm(_ context.Context, id proto.VoiceCallID) error {
	s.note("host.confirm " + string(id))
	return s.confirmErr
}

func (s *signal) Stop(_ context.Context, id proto.VoiceCallID) error {
	s.stops++
	s.note("host.stop " + string(id))
	return s.stopErr
}

var connected = []string{
	"hello 1 abc123",
	"initializeRuntime",
	"startTransport",
	"host.start the-offer",
	"applyAnswer the-answer",
	"openDevices selection=true",
	"audio mic-muted=true speaker-suppressed=true",
	"host.confirm call-1",
	"audio mic-muted=false speaker-suppressed=false",
}

func TestCallKeepsAudioOrder(t *testing.T) {
	path := install(t, scenario{})
	sig := &signal{path: path}
	c, err := Dial(t.Context(), path, sig)
	if err != nil {
		t.Fatal(err)
	}
	if got := readLog(t, path); !slices.Equal(got, connected) {
		t.Fatalf("connect order:\n got %q\nwant %q", got, connected)
	}
	if mic, spk, err := c.Levels(t.Context()); err != nil || mic != 7 || spk != 9 {
		t.Fatalf("Levels = %d, %d, %v", mic, spk, err)
	}
	if err := c.Hangup(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := slices.Concat(connected, []string{
		"inspectAudio",
		"audio mic-muted=true speaker-suppressed=true",
		"host.stop call-1",
	})
	if got := readLog(t, path); !slices.Equal(got, want) {
		t.Fatalf("hangup order:\n got %q\nwant %q", got, want)
	}
	assertGone(t, path)
	if err := c.Hangup(t.Context()); err != nil || sig.stops != 1 {
		t.Fatalf("second Hangup: %v, %d stops", err, sig.stops)
	}
}

func TestDialFailureLeavesNothing(t *testing.T) {
	refused := &proto.APIError{Code: proto.CodeVoiceBusy, Message: "other"}
	tests := []struct {
		name      string
		scenario  scenario
		sig       signal
		wantStops int
		wantAudio bool // whether the microphone was ever opened
		code      proto.ErrorCode
	}{
		{name: "dies at handshake", scenario: scenario{DieOn: "hello"}},
		{name: "runtime fails", scenario: scenario{DieOn: "initializeRuntime"}},
		{name: "wrong reply", scenario: scenario{WrongOn: "startTransport"}},
		{name: "host refuses", sig: signal{startErr: refused}, code: proto.CodeVoiceBusy},
		{name: "dies applying answer", scenario: scenario{DieOn: "applyAnswer"}, wantStops: 1},
		{name: "devices fail", scenario: scenario{WrongOn: "openDevices"}, wantStops: 1},
		{name: "host does not confirm", sig: signal{confirmErr: refused}, wantStops: 1, code: proto.CodeVoiceBusy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := install(t, tt.scenario)
			sig := tt.sig
			sig.path = path
			_, err := Dial(t.Context(), path, &sig)
			if err == nil {
				t.Fatal("Dial succeeded")
			}
			if tt.code != "" {
				var api *proto.APIError
				if !errors.As(err, &api) || api.Code != tt.code {
					t.Fatalf("err = %v, want code %s", err, tt.code)
				}
			}
			if sig.stops != tt.wantStops {
				t.Fatalf("stops = %d, want %d", sig.stops, tt.wantStops)
			}
			if slices.Contains(readLog(t, path), "audio mic-muted=false speaker-suppressed=false") {
				t.Fatal("microphone opened on a failed call")
			}
			assertGone(t, path)
			if strings.Contains(err.Error(), "the-offer") || strings.Contains(err.Error(), "the-answer") {
				t.Fatalf("error quotes a session description: %v", err)
			}
		})
	}
}

func TestDialMissingComponent(t *testing.T) {
	_, err := Dial(t.Context(), filepath.Join(t.TempDir(), fakeName), &signal{})
	var api *proto.APIError
	if !errors.As(err, &api) || api.Code != proto.CodeVoiceNoComponent {
		t.Fatalf("err = %v", err)
	}
}

func TestComponentNeedsItsOwnDirectory(t *testing.T) {
	path := install(t, scenario{})
	if err := os.Remove(filepath.Join(filepath.Dir(path), "scenario.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Dial(t.Context(), path, &signal{path: path}); err == nil {
		t.Fatal("Dial succeeded without the component's files")
	}
}

func TestComponentExitDuringCall(t *testing.T) {
	path := install(t, scenario{})
	sig := &signal{path: path}
	c, err := Dial(t.Context(), path, sig)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(b))
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-c.Done()
	if _, _, err := c.Levels(t.Context()); err == nil {
		t.Fatal("Levels succeeded after the component exited")
	}
	if err := c.Hangup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if sig.stops != 1 {
		t.Fatalf("stops = %d", sig.stops)
	}
	assertGone(t, path)
}

func TestHangupWithHostUnreachable(t *testing.T) {
	path := install(t, scenario{})
	sig := &signal{path: path, stopErr: errors.New("connection lost")}
	c, err := Dial(t.Context(), path, sig)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Hangup(t.Context()); err == nil {
		t.Fatal("Hangup hid the host's failure")
	}
	assertGone(t, path)
}

func TestStalledRequestEndsComponent(t *testing.T) {
	path := install(t, scenario{HangOn: "startTransport"})
	h, err := open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.offer(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	<-h.done
	assertGone(t, path)
}

func TestHostEnvIsAllowlist(t *testing.T) {
	got := hostEnv([]string{
		"HOME=/h", "OPENAI_API_KEY=secret", "CODEX_HOME=/c", "DYLD_INSERT_LIBRARIES=/x",
		"GST_PLUGIN_PATH=/evil", "https_proxy=http://p", "PATH=/bin",
	})
	for _, want := range []string{"HOME=/h", "https_proxy=http://p", "GST_PLUGIN_PATH=", "GST_REGISTRY=/dev/null"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %q", want, got)
		}
	}
	for _, kv := range got {
		for _, banned := range []string{"OPENAI", "CODEX", "DYLD", "PATH=/bin", "/evil"} {
			if strings.Contains(kv, banned) {
				t.Errorf("leaked %s", kv)
			}
		}
	}
}

func TestFrameLimits(t *testing.T) {
	if err := writeFrame(new(strings.Builder), make([]byte, maxFrame+1)); err == nil {
		t.Fatal("wrote an oversized frame")
	}
	for name, in := range map[string]string{
		"zero":     "\x00\x00\x00\x00",
		"too long": "\x00\x02\x00\x01",
		"short":    "\x00\x00\x00\x05ab",
	} {
		if _, err := readFrame(strings.NewReader(in)); err == nil {
			t.Errorf("%s: read a bad frame", name)
		}
	}
}
