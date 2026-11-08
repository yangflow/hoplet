package simulator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// fakeHelper records the commands sent to it.
type fakeHelper struct {
	mu     sync.Mutex
	reqs   []HelperRequest
	err    error
	closed bool
	// hook, when set, replaces err and runs for each command.
	hook func(ctx context.Context, r HelperRequest) error
}

func (f *fakeHelper) Do(ctx context.Context, r HelperRequest) error {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	hook, err := f.hook, f.err
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx, r)
	}
	return err
}

func (f *fakeHelper) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeHelper) requests() []HelperRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]HelperRequest(nil), f.reqs...)
}

func (f *fakeHelper) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// stdinRunner is a fakeRunner that also records standard input.
type stdinRunner struct {
	*fakeRunner
	mu     sync.Mutex
	stdins []string
}

func (r *stdinRunner) RunStdin(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.stdins = append(r.stdins, string(stdin))
	r.mu.Unlock()
	return r.Run(ctx, name, args...)
}

type inputEnv struct {
	s      *Service
	f      *stdinRunner
	clock  *fakeClock
	file   string
	mu     sync.Mutex
	made   []*fakeHelper
	onMake func(*fakeHelper)
}

const bootedUDID = "UDID-A18" // Phone A, 393x852 points, booted

func newInputEnv(t *testing.T) *inputEnv {
	t.Helper()
	file := filepath.Join(t.TempDir(), helperName)
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := &stdinRunner{fakeRunner: listRunner()}
	r.out["-p"] = "/Xcode-A"
	r.out["--probe"] = `{"ok":true,"xcode":"16.0","missing":[]}`
	clock := &fakeClock{now: testNow}
	env := &inputEnv{f: r, clock: clock, file: file}
	env.s = New(r, clock.Now, func(context.Context, proto.SessionID) bool { return true })
	env.s.helperFile = func() string { return file }
	env.s.starter = func(context.Context, string) (Helper, error) {
		h := &fakeHelper{}
		env.mu.Lock()
		env.made = append(env.made, h)
		fn := env.onMake
		env.mu.Unlock()
		if fn != nil {
			fn(h)
		}
		return h, nil
	}
	return env
}

func (e *inputEnv) helpers() []*fakeHelper {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]*fakeHelper(nil), e.made...)
}

func (e *inputEnv) countCalls(name string, args ...string) int {
	e.f.mu.Lock()
	defer e.f.mu.Unlock()
	n := 0
	for _, c := range e.f.calls {
		if c.name == name && (len(args) == 0 || reflect.DeepEqual(c.args, args)) {
			n++
		}
	}
	return n
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var se *Error
	if !errors.As(err, &se) || se.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

func TestTap(t *testing.T) {
	tests := []struct {
		name     string
		x, y     float64
		duration time.Duration
		want     HelperRequest
		wantCode string
	}{
		{name: "center", x: 196.5, y: 426, want: HelperRequest{Op: "tap", X: 0.5, Y: 0.5}},
		{name: "origin", want: HelperRequest{Op: "tap"}},
		{name: "far corner", x: 393, y: 852, want: HelperRequest{Op: "tap", X: 1, Y: 1}},
		{name: "long press", x: 98.25, y: 213, duration: 2 * time.Second, want: HelperRequest{Op: "tap", X: 0.25, Y: 0.25, Duration: 2 * time.Second}},
		{name: "max duration", x: 1, y: 1, duration: 10 * time.Second, want: HelperRequest{Op: "tap", X: 1.0 / 393, Y: 1.0 / 852, Duration: 10 * time.Second}},
		{name: "x too large", x: 393.1, y: 1, wantCode: CodeInvalidArgument},
		{name: "y too large", x: 1, y: 852.5, wantCode: CodeInvalidArgument},
		{name: "negative x", x: -1, y: 1, wantCode: CodeInvalidArgument},
		{name: "duration too long", x: 1, y: 1, duration: 10*time.Second + time.Nanosecond, wantCode: CodeInvalidArgument},
		{name: "negative duration", x: 1, y: 1, duration: -time.Second, wantCode: CodeInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newInputEnv(t)
			err := env.s.Tap(context.Background(), "ses_1", bootedUDID, tc.x, tc.y, tc.duration)
			if tc.wantCode != "" {
				wantCode(t, err, tc.wantCode)
				if len(env.helpers()) != 0 {
					t.Fatal("a helper was started for an invalid request")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			hs := env.helpers()
			if len(hs) != 1 {
				t.Fatalf("%d helpers started", len(hs))
			}
			got := hs[0].requests()
			if len(got) != 1 || !reflect.DeepEqual(got[0], tc.want) {
				t.Fatalf("requests = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSwipe(t *testing.T) {
	tests := []struct {
		name     string
		edge     string
		x2       float64
		duration time.Duration
		want     HelperRequest
		wantCode string
	}{
		{name: "plain", x2: 98.25, want: HelperRequest{Op: "swipe", X: 0.5, Y: 0.5, X2: 0.25, Y2: 0.25}},
		{name: "bottom edge", edge: "bottom", x2: 98.25, duration: time.Second, want: HelperRequest{Op: "swipe", X: 0.5, Y: 0.5, X2: 0.25, Y2: 0.25, Duration: time.Second, Edge: "bottom"}},
		{name: "left edge", edge: "left", x2: 1, wantCode: CodeUnsupported},
		{name: "end outside", x2: 400, wantCode: CodeInvalidArgument},
		{name: "duration", x2: 1, duration: 11 * time.Second, wantCode: CodeInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newInputEnv(t)
			err := env.s.Swipe(context.Background(), "ses_1", bootedUDID, 196.5, 426, tc.x2, 213, tc.duration, tc.edge)
			if tc.wantCode != "" {
				wantCode(t, err, tc.wantCode)
				if len(env.helpers()) != 0 {
					t.Fatal("a helper was started for an invalid request")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := env.helpers()[0].requests(); len(got) != 1 || !reflect.DeepEqual(got[0], tc.want) {
				t.Fatalf("requests = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestButton(t *testing.T) {
	env := newInputEnv(t)
	if err := env.s.Button(context.Background(), "ses_1", bootedUDID, "home"); err != nil {
		t.Fatal(err)
	}
	if got := env.helpers()[0].requests(); !reflect.DeepEqual(got, []HelperRequest{{Op: "button", Name: "home"}}) {
		t.Fatalf("requests = %+v", got)
	}
	for _, name := range []string{"lock", "Home", ""} {
		wantCode(t, env.s.Button(context.Background(), "ses_1", bootedUDID, name), CodeUnsupported)
	}
	if n := len(env.helpers()[0].requests()); n != 1 {
		t.Fatalf("%d commands, want 1", n)
	}
}

func TestInputNeedsBootedDevice(t *testing.T) {
	env := newInputEnv(t)
	wantCode(t, env.s.Tap(context.Background(), "ses_1", "UDID-A17", 1, 1, 0), CodeNotBooted)
	wantCode(t, env.s.Text(context.Background(), "ses_1", "UDID-A17", "x"), CodeNotBooted)
	if len(env.helpers()) != 0 {
		t.Fatal("helper started for a device that is not booted")
	}
}

func TestInputBusySendsNothing(t *testing.T) {
	env := newInputEnv(t)
	env.s.holders[bootedUDID] = holderEntry{session: "ses_other", lastInput: testNow}
	ctx := context.Background()
	calls := map[string]error{
		"tap":    env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0),
		"swipe":  env.s.Swipe(ctx, "ses_1", bootedUDID, 1, 1, 2, 2, 0, ""),
		"text":   env.s.Text(ctx, "ses_1", bootedUDID, "é"),
		"button": env.s.Button(ctx, "ses_1", bootedUDID, "home"),
	}
	for name, err := range calls {
		var se *Error
		if !errors.As(err, &se) || se.Code != CodeBusy || !strings.Contains(se.Message, "ses_other") {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if len(env.helpers()) != 0 {
		t.Fatal("a helper was started for a busy device")
	}
	if n := env.countCalls("xcrun", "simctl", "pbcopy", bootedUDID); n != 0 {
		t.Fatalf("pbcopy ran %d times for a busy device", n)
	}
}

func TestInputTakesLeaseAndRestartsWindow(t *testing.T) {
	for _, failing := range []bool{false, true} {
		env := newInputEnv(t)
		env.onMake = func(h *fakeHelper) {
			h.hook = func(context.Context, HelperRequest) error {
				// While a command runs the lease is held in flight.
				env.s.mu.Lock()
				e := env.s.holders[bootedUDID]
				env.s.mu.Unlock()
				if e.session != "ses_1" || e.inFlight != 1 {
					t.Errorf("during command: %+v", e)
				}
				env.clock.Advance(5 * time.Second)
				if failing {
					return &Error{Code: CodeInputFailed, Message: "no"}
				}
				return nil
			}
		}
		err := env.s.Tap(context.Background(), "ses_1", bootedUDID, 1, 1, 0)
		if (err != nil) != failing {
			t.Fatalf("failing=%v err=%v", failing, err)
		}
		e := env.s.holders[bootedUDID]
		if e.session != "ses_1" || e.inFlight != 0 || !e.lastInput.Equal(env.clock.Now()) {
			t.Fatalf("failing=%v: after command: %+v, clock %v", failing, e, env.clock.Now())
		}
	}
}

func TestText(t *testing.T) {
	long := strings.Repeat("aB1!", 40) // 160 characters, 80 shifted
	tests := []struct {
		name     string
		text     string
		want     [][]int
		paste    bool
		wantCode string
	}{
		{name: "lower", text: "hi", want: [][]int{{11, 12}}},
		{name: "upper uses shift", text: "A", want: [][]int{{225, 4}}},
		{name: "digits", text: "1234567890", want: [][]int{{30, 31, 32, 33, 34, 35, 36, 37, 38, 39}}},
		{name: "shifted symbols", text: "!@#$%^&*()_+{}|:\"~<>?", want: [][]int{{
			225, 30, 225, 31, 225, 32, 225, 33, 225, 34, 225, 35, 225, 36, 225, 37, 225, 38, 225, 39,
			225, 0x2D, 225, 0x2E, 225, 0x2F, 225, 0x30, 225, 0x31, 225, 0x33, 225, 0x34, 225, 0x35, 225, 0x36, 225, 0x37, 225, 0x38}}},
		{name: "newline and tab", text: "a\nb\tc", want: [][]int{{4, 0x28, 5, 0x2B, 6}}},
		{name: "non-ascii", text: "héllo", paste: true},
		{name: "emoji", text: "ok 👍", paste: true},
		{name: "control char", text: "a\x01b", paste: true},
		{name: "carriage return", text: "a\r\nb", paste: true},
		{name: "delete char", text: "a\x7f", paste: true},
		{name: "empty", text: "", wantCode: CodeInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newInputEnv(t)
			err := env.s.Text(context.Background(), "ses_1", bootedUDID, tc.text)
			if tc.wantCode != "" {
				wantCode(t, err, tc.wantCode)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := env.helpers()[0].requests()
			if tc.paste {
				if !reflect.DeepEqual(got, []HelperRequest{{Op: "paste"}}) {
					t.Fatalf("requests = %+v", got)
				}
				if env.f.stdins[0] != tc.text || len(env.f.stdins) != 1 {
					t.Fatalf("clipboard text = %q", env.f.stdins)
				}
				if n := env.countCalls("xcrun", "simctl", "pbcopy", bootedUDID); n != 1 {
					t.Fatalf("pbcopy ran %d times", n)
				}
				return
			}
			var usages [][]int
			for _, r := range got {
				if r.Op != "keys" {
					t.Fatalf("op = %s", r.Op)
				}
				usages = append(usages, r.Usages)
			}
			if !reflect.DeepEqual(usages, tc.want) {
				t.Fatalf("usages = %v, want %v", usages, tc.want)
			}
			if len(env.f.stdins) != 0 {
				t.Fatal("clipboard used for plain ASCII")
			}
		})
	}
	t.Run("long text is split without separating shifts", func(t *testing.T) {
		env := newInputEnv(t)
		if err := env.s.Text(context.Background(), "ses_1", bootedUDID, long); err != nil {
			t.Fatal(err)
		}
		var all []int
		for _, r := range env.helpers()[0].requests() {
			if len(r.Usages) > maxUsagesPerCommand || len(r.Usages) == 0 {
				t.Fatalf("command has %d usages", len(r.Usages))
			}
			if last := r.Usages[len(r.Usages)-1]; last >= 0xE0 {
				t.Fatalf("command ends with held modifier %d", last)
			}
			all = append(all, r.Usages...)
		}
		want, _ := keyChunks(long)
		var flat []int
		for _, c := range want {
			flat = append(flat, c...)
		}
		if len(env.helpers()[0].requests()) < 3 || !reflect.DeepEqual(all, flat) {
			t.Fatalf("%d commands, usages differ from the mapping", len(env.helpers()[0].requests()))
		}
	})
}

func TestPlainSymbols(t *testing.T) {
	chunks, ok := keyChunks("-=[]\\;',./` ")
	want := []int{0x2D, 0x2E, 0x2F, 0x30, 0x31, 0x33, 0x34, 0x36, 0x37, 0x38, 0x35, 0x2C}
	if !ok || len(chunks) != 1 || !reflect.DeepEqual(chunks[0], want) {
		t.Fatalf("chunks = %v ok=%v", chunks, ok)
	}
	// Every printable ASCII character has a key.
	for r := rune(0x20); r < 0x7F; r++ {
		if _, _, found := asciiUsage(r); !found {
			t.Fatalf("no key for %q", r)
		}
	}
}

func TestTextStopsAtFirstFailedChunk(t *testing.T) {
	env := newInputEnv(t)
	env.onMake = func(h *fakeHelper) { h.err = &Error{Code: CodeInputFailed, Message: "no"} }
	err := env.s.Text(context.Background(), "ses_1", bootedUDID, strings.Repeat("a", 200))
	wantCode(t, err, CodeInputFailed)
	if n := len(env.helpers()[0].requests()); n != 1 {
		t.Fatalf("%d commands sent after the first failed", n)
	}
	if !env.helpers()[0].isClosed() {
		t.Fatal("helper kept after an input_failed response")
	}
}

func TestInputFailedReplacesHelperOnNextCommand(t *testing.T) {
	env := newInputEnv(t)
	ctx := context.Background()
	env.onMake = func(h *fakeHelper) {
		if len(env.made) == 1 {
			h.err = &Error{Code: CodeInputFailed, Message: "stale client"}
		}
	}
	wantCode(t, env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0), CodeInputFailed)
	if err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	hs := env.helpers()
	if len(hs) != 2 || !hs[0].isClosed() || len(hs[0].requests()) != 1 || len(hs[1].requests()) != 1 {
		t.Fatalf("%d helpers; the failed command must not be retried and the next must run on a new helper", len(hs))
	}
}

func TestFailedProbeIsNotCached(t *testing.T) {
	env := newInputEnv(t)
	ctx := context.Background()
	env.f.out["--probe"] = `{"ok":false,"missing":["x"]}`
	wantCode(t, env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0), CodeInputUnsupported)
	env.f.out["--probe"] = `{"ok":true,"xcode":"16.0","missing":[]}`
	if err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
}

func TestInputUnsupported(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*inputEnv)
	}{
		{"file missing", func(e *inputEnv) { _ = os.Remove(e.file) }},
		{"no helper on host", func(e *inputEnv) { e.s.helperFile = func() string { return "" } }},
		{"probe not ok", func(e *inputEnv) {
			e.f.out["--probe"] = `{"ok":false,"xcode":"16.0","missing":["SimServiceContext"]}`
		}},
		{"probe exits non-zero", func(e *inputEnv) {
			e.f.hook = func(_ string, args []string) ([]byte, error, bool) {
				if len(args) == 1 && args[0] == "--probe" {
					return nil, errors.New("exit status 1"), true
				}
				return nil, nil, false
			}
		}},
		{"probe prints junk", func(e *inputEnv) { e.f.out["--probe"] = "oops" }},
		{"xcode path unreadable", func(e *inputEnv) { e.f.out["-p"] = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newInputEnv(t)
			tc.setup(env)
			ctx := context.Background()
			wantCode(t, env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0), CodeInputUnsupported)
			wantCode(t, env.s.Button(ctx, "ses_1", bootedUDID, "home"), CodeInputUnsupported)
			if len(env.helpers()) != 0 {
				t.Fatal("helper started without a working probe")
			}
			// simctl operations are unaffected.
			if err := env.s.OpenURL(ctx, "ses_1", bootedUDID, "https://example.com"); err != nil {
				t.Fatal(err)
			}
			if err := env.s.Launch(ctx, "ses_1", bootedUDID, "com.example.app", ""); err != nil {
				t.Fatal(err)
			}
			devs, err := env.s.Devices(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range devs {
				if d.InputAvailable {
					t.Fatalf("%s reports input available", d.UDID)
				}
			}
		})
	}
}

func TestDevicesInputAvailable(t *testing.T) {
	env := newInputEnv(t)
	devs, err := env.s.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) == 0 {
		t.Fatal("no devices")
	}
	for _, d := range devs {
		if !d.InputAvailable {
			t.Fatalf("%s: input unavailable", d.UDID)
		}
	}
}

func TestProbeCachedPerXcodePath(t *testing.T) {
	env := newInputEnv(t)
	ctx := context.Background()
	for range 3 {
		if err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.s.Devices(ctx); err != nil {
		t.Fatal(err)
	}
	if n := env.countCalls(env.file, "--probe"); n != 1 {
		t.Fatalf("probe ran %d times, want 1", n)
	}
	// A different selected Xcode probes again; a failing probe there is not
	// cached, so it probes on every call, and the first answer stays cached.
	env.f.out["-p"] = "/Xcode-B"
	env.f.out["--probe"] = `{"ok":false,"missing":["x"]}`
	wantCode(t, env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0), CodeInputUnsupported)
	wantCode(t, env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0), CodeInputUnsupported)
	if n := env.countCalls(env.file, "--probe"); n != 3 {
		t.Fatalf("probe ran %d times, want 3", n)
	}
	env.f.out["-p"] = "/Xcode-A"
	if err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if n := env.countCalls(env.file, "--probe"); n != 3 {
		t.Fatalf("probe ran %d times after switching back, want 3", n)
	}
}

func TestOneHelperPerDevice(t *testing.T) {
	env := newInputEnv(t)
	ctx := context.Background()
	for range 3 {
		if err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0); err != nil {
			t.Fatal(err)
		}
	}
	if len(env.helpers()) != 1 || len(env.helpers()[0].requests()) != 3 {
		t.Fatalf("%d helpers", len(env.helpers()))
	}
}

func TestCrashFailsCommandAndNextRestarts(t *testing.T) {
	env := newInputEnv(t)
	ctx := context.Background()
	crash := &Error{Code: CodeInputFailed, Message: "simulator helper exited: signal: killed"}
	env.onMake = func(h *fakeHelper) {
		if len(env.made) == 1 {
			h.err = crash
		}
	}
	err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0)
	wantCode(t, err, CodeInputFailed)
	if !strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("err = %v", err)
	}
	hs := env.helpers()
	if len(hs) != 1 || len(hs[0].requests()) != 1 || !hs[0].isClosed() {
		t.Fatalf("after crash: %d helpers, closed=%v; the command must not be retried", len(hs), hs[0].isClosed())
	}
	if err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	hs = env.helpers()
	if len(hs) != 2 || len(hs[1].requests()) != 1 {
		t.Fatalf("%d helpers after restart", len(hs))
	}
}

func TestStartFailureIsReportedAndRetried(t *testing.T) {
	env := newInputEnv(t)
	boom := &Error{Code: CodeInputUnsupported, Message: "probe failed"}
	fail := true
	inner := env.s.starter
	env.s.starter = func(ctx context.Context, udid string) (Helper, error) {
		if fail {
			return nil, boom
		}
		return inner(ctx, udid)
	}
	wantCode(t, env.s.Tap(context.Background(), "ses_1", bootedUDID, 1, 1, 0), CodeInputUnsupported)
	fail = false
	if err := env.s.Tap(context.Background(), "ses_1", bootedUDID, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
}

func TestCommandLimit(t *testing.T) {
	env := newInputEnv(t)
	env.s.inputLimit = 10 * time.Millisecond
	env.onMake = func(h *fakeHelper) {
		h.hook = func(ctx context.Context, _ HelperRequest) error {
			<-ctx.Done()
			return context.Cause(ctx)
		}
	}
	err := env.s.Tap(context.Background(), "ses_1", bootedUDID, 1, 1, 0)
	if err == nil || !strings.Contains(err.Error(), "timed out after 10ms") {
		t.Fatalf("err = %v", err)
	}
	if !env.helpers()[0].isClosed() {
		t.Fatal("helper kept after a timed out command")
	}
	if e := env.s.holders[bootedUDID]; e.inFlight != 0 {
		t.Fatalf("lease still in flight: %+v", e)
	}
}

func TestDefaultLimitIs30Seconds(t *testing.T) {
	if s := newSvc(&fakeRunner{}); s.inputLimit != 30*time.Second {
		t.Fatalf("limit = %s", s.inputLimit)
	}
}

func TestReapIdle(t *testing.T) {
	env := newInputEnv(t)
	if err := env.s.Tap(context.Background(), "ses_1", bootedUDID, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	h := env.helpers()[0]
	if n := env.s.ReapIdle(testNow.Add(60 * time.Second)); n != 0 || h.isClosed() {
		t.Fatalf("helper idle for exactly 60s: reaped %d, closed %v", n, h.isClosed())
	}
	if n := env.s.ReapIdle(testNow.Add(60*time.Second + time.Nanosecond)); n != 1 || !h.isClosed() {
		t.Fatalf("helper idle for over 60s: reaped %d, closed %v", n, h.isClosed())
	}
	// The next command starts a fresh helper.
	if err := env.s.Tap(context.Background(), "ses_1", bootedUDID, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if len(env.helpers()) != 2 {
		t.Fatalf("%d helpers", len(env.helpers()))
	}
}

func TestReapIdleCountsFromLastCommand(t *testing.T) {
	env := newInputEnv(t)
	ctx := context.Background()
	if err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	env.clock.Advance(50 * time.Second)
	if err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if n := env.s.ReapIdle(testNow.Add(100 * time.Second)); n != 0 {
		t.Fatalf("reaped %d helpers 50s after the last command", n)
	}
}

func TestReapIdleSparesRunningCommand(t *testing.T) {
	env := newInputEnv(t)
	entered, release := make(chan struct{}), make(chan struct{})
	env.onMake = func(h *fakeHelper) {
		h.hook = func(context.Context, HelperRequest) error {
			close(entered)
			<-release
			return nil
		}
	}
	done := make(chan error, 1)
	go func() { done <- env.s.Tap(context.Background(), "ses_1", bootedUDID, 1, 1, 0) }()
	<-entered
	if n := env.s.ReapIdle(testNow.Add(time.Hour)); n != 0 {
		t.Fatalf("reaped %d helpers with a command running", n)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCloseClosesHelpers(t *testing.T) {
	env := newInputEnv(t)
	ctx := context.Background()
	if err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := env.s.Close(); err != nil {
		t.Fatal(err)
	}
	if !env.helpers()[0].isClosed() {
		t.Fatal("helper not closed by Service.Close")
	}
	if err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0); err == nil {
		t.Fatal("command accepted after Close")
	}
	if len(env.helpers()) != 1 {
		t.Fatal("helper started after Close")
	}
}

func TestCloseDuringStart(t *testing.T) {
	env := newInputEnv(t)
	inner := env.s.starter
	started, release := make(chan struct{}), make(chan struct{})
	env.s.starter = func(ctx context.Context, udid string) (Helper, error) {
		close(started)
		<-release
		return inner(ctx, udid)
	}
	done := make(chan error, 1)
	go func() { done <- env.s.Tap(context.Background(), "ses_1", bootedUDID, 1, 1, 0) }()
	<-started
	if err := env.s.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("command succeeded on a closed service")
	}
	if !env.helpers()[0].isClosed() {
		t.Fatal("helper started during Close was left running")
	}
}

func TestDefaultHelperFileBesideExecutable(t *testing.T) {
	got := defaultHelperFile()
	if got == "" {
		t.Skip("no helper on this host")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(exe)
	if want := filepath.Join(filepath.Dir(real), helperName); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestHelperDiedWhileIdleIsRestarted(t *testing.T) {
	env := newInputEnv(t)
	ctx := context.Background()
	if err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	first := env.helpers()[0]
	// The helper dies while idle: it rejects the next command unsent.
	first.mu.Lock()
	first.err = errors.Join(errHelperNotRunning, errors.New("signal: killed"))
	first.mu.Unlock()
	if err := env.s.Tap(ctx, "ses_1", bootedUDID, 2, 2, 0); err != nil {
		t.Fatal(err)
	}
	hs := env.helpers()
	if len(hs) != 2 || !first.isClosed() {
		t.Fatalf("%d helpers, first closed = %v", len(hs), first.isClosed())
	}
	if got := hs[1].requests(); len(got) != 1 || got[0].X != 2.0/393 {
		t.Fatalf("new helper got %+v", got)
	}
}

func TestRestartHappensOnce(t *testing.T) {
	env := newInputEnv(t)
	env.onMake = func(h *fakeHelper) { h.err = errHelperNotRunning }
	err := env.s.Tap(context.Background(), "ses_1", bootedUDID, 1, 1, 0)
	if !errors.Is(err, errHelperNotRunning) || len(env.helpers()) != 2 {
		t.Fatalf("err = %v, %d helpers", err, len(env.helpers()))
	}
}

func TestStdioIdleDeathRejectsUnsent(t *testing.T) {
	c, h, _ := started(t, false)
	c.say(t, `{"ok":true}`)
	if _, err := h.awaitProbe(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.exit()
	<-h.exited
	if err := h.Do(context.Background(), HelperRequest{Op: "paste"}); !errors.Is(err, errHelperNotRunning) {
		t.Fatalf("err = %v", err)
	}
	if len(c.reqs) != 0 {
		t.Fatal("a command was written to a dead helper")
	}
}

func TestProbeWithoutAnswerIsNotCached(t *testing.T) {
	env := newInputEnv(t)
	probeErr := errors.New("exit status 1")
	env.f.hook = func(_ string, args []string) ([]byte, error, bool) {
		if len(args) == 1 && args[0] == "--probe" {
			return nil, probeErr, true
		}
		return nil, nil, false
	}
	ctx := context.Background()
	wantCode(t, env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0), CodeInputUnsupported)
	env.f.mu.Lock()
	env.f.hook = nil
	env.f.mu.Unlock()
	if err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0); err != nil {
		t.Fatalf("transient probe failure was cached: %v", err)
	}
}

func TestProbeCanceledReturnsCause(t *testing.T) {
	env := newInputEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	env.f.hook = func(_ string, args []string) ([]byte, error, bool) {
		if len(args) == 1 && args[0] == "--probe" {
			cancel()
			return nil, ctx.Err(), true
		}
		return nil, nil, false
	}
	err := env.s.Tap(ctx, "ses_1", bootedUDID, 1, 1, 0)
	var se *Error
	if !errors.Is(err, context.Canceled) || errors.As(err, &se) {
		t.Fatalf("err = %v", err)
	}
	if env.s.holders[bootedUDID].inFlight != 0 {
		t.Fatal("lease left in flight")
	}
}

func TestUnknownGeometryHasStableCode(t *testing.T) {
	env := newInputEnv(t)
	delete(env.f.out, "simctl list -j devicetypes")
	wantCode(t, env.s.Tap(context.Background(), "ses_1", bootedUDID, 1, 1, 0), CodeInputFailed)
}

func TestTextPartialFailureCountsCharacters(t *testing.T) {
	env := newInputEnv(t)
	calls := 0
	env.onMake = func(h *fakeHelper) {
		h.hook = func(context.Context, HelperRequest) error {
			calls++
			if calls == 2 {
				return &Error{Code: CodeInputFailed, Message: "no"}
			}
			return nil
		}
	}
	// 100 plain characters: chunks of 64 and 36; the second fails.
	err := env.s.Text(context.Background(), "ses_1", bootedUDID, strings.Repeat("a", 100))
	wantCode(t, err, CodeInputFailed)
	if !strings.Contains(err.Error(), "64 of 100 characters were typed") {
		t.Fatalf("err = %v", err)
	}
}
