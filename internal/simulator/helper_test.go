package simulator

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeChild stands in for the helper process: the test reads the commands the
// client wrote and writes responses by hand.
type fakeChild struct {
	stdin    *io.PipeWriter // client side of the helper's stdin
	reqs     chan string    // command lines the client wrote
	outW     *io.PipeWriter // the helper's stdout
	done     chan struct{}
	once     sync.Once
	killed   atomic.Bool
	stubborn bool // ignore end of stdin; only Kill ends it
}

func newFakeChild(stubborn bool) (*fakeChild, *stdioHelper, *tailBuffer) {
	reqR, stdin := io.Pipe()
	outR, outW := io.Pipe()
	c := &fakeChild{stdin: stdin, reqs: make(chan string, 16), outW: outW, done: make(chan struct{}), stubborn: stubborn}
	tail := &tailBuffer{max: stderrTail}
	// Only a helper that ignores end of input should reach the kill path;
	// the others get all the time they need.
	closeWait := time.Minute
	if stubborn {
		closeWait = 5 * time.Millisecond
	}
	go func() {
		sc := bufio.NewScanner(reqR)
		for sc.Scan() {
			c.reqs <- sc.Text()
		}
		if !c.stubborn {
			c.exit()
		}
	}()
	return c, newStdioHelper(stdin, outR, c, tail, closeWait), tail
}

func (c *fakeChild) exit() {
	c.once.Do(func() {
		_ = c.outW.Close()
		close(c.done)
	})
}

func (c *fakeChild) Wait() error { <-c.done; return nil }
func (c *fakeChild) Kill() error { c.killed.Store(true); c.exit(); return nil }

func (c *fakeChild) say(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(c.outW, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

// next returns the next command the client wrote, decoded.
func (c *fakeChild) next(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(<-c.reqs), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func started(t *testing.T, stubborn bool) (*fakeChild, *stdioHelper, *tailBuffer) {
	t.Helper()
	c, h, tail := newFakeChild(stubborn)
	t.Cleanup(func() { _ = h.Close() })
	return c, h, tail
}

func TestEncodeRequest(t *testing.T) {
	tests := []struct {
		name string
		req  HelperRequest
		want string
	}{
		{"tap default duration", HelperRequest{Op: "tap", X: 0, Y: 0.5}, `{"id":7,"op":"tap","x":0,"y":0.5}`},
		{"tap duration", HelperRequest{Op: "tap", X: 0.1, Y: 0.2, Duration: 1500 * time.Millisecond}, `{"duration":1.5,"id":7,"op":"tap","x":0.1,"y":0.2}`},
		{"swipe edge", HelperRequest{Op: "swipe", X: 0.5, Y: 1, X2: 0.5, Y2: 0.5, Edge: "bottom"}, `{"edge":"bottom","id":7,"op":"swipe","x":0.5,"x2":0.5,"y":1,"y2":0.5}`},
		{"keys", HelperRequest{Op: "keys", Usages: []int{225, 4}}, `{"id":7,"op":"keys","usages":[225,4]}`},
		{"paste", HelperRequest{Op: "paste"}, `{"id":7,"op":"paste"}`},
		{"button", HelperRequest{Op: "button", Name: "home"}, `{"id":7,"name":"home","op":"button"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encodeRequest(7, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(got)) != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
	if _, err := encodeRequest(1, HelperRequest{Op: "nope"}); err == nil {
		t.Fatal("unknown op accepted")
	}
}

func TestStdioProbe(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantCode string
		wantMsg  string
	}{
		{"ok", `{"ok":true,"xcode":"16.0","missing":[]}`, "", ""},
		{"failed", `{"ok":false,"xcode":"16.0","missing":["SimServiceContext"]}`, CodeInputUnsupported, "SimServiceContext"},
		{"missing with message", `{"ok":false,"xcode":"16.0","missing":["SimServiceContext"],"message":"symbols gone"}`, CodeInputUnsupported, "symbols gone"},
		{"device not booted", `{"ok":false,"xcode":"16.0","missing":[],"message":"device is not booted"}`, CodeInputFailed, "device is not booted"},
		{"device unknown", `{"ok":false,"xcode":"16.0","missing":[],"message":"unknown device"}`, CodeInputFailed, "unknown device"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, h, _ := newFakeChild(false)
			go func() { _, _ = io.WriteString(c.outW, tc.line+"\n") }()
			got, err := h.awaitProbe(context.Background())
			if tc.wantCode == "" {
				if err != nil || got == nil {
					t.Fatalf("err = %v", err)
				}
				_ = h.Close()
				return
			}
			var se *Error
			if !errors.As(err, &se) || se.Code != tc.wantCode || !strings.Contains(se.Message, tc.wantMsg) {
				t.Fatalf("err = %v", err)
			}
			select {
			case <-h.exited:
			default:
				t.Fatal("helper left running after a failed probe")
			}
		})
	}
}

func TestStdioExitBeforeProbe(t *testing.T) {
	c, h, tail := newFakeChild(false)
	_, _ = tail.Write([]byte("code signature invalid\n"))
	c.exit()
	_, err := h.awaitProbe(context.Background())
	var se *Error
	if !errors.As(err, &se) || se.Code != CodeInputUnsupported || !strings.Contains(se.Message, "code signature invalid") {
		t.Fatalf("err = %v", err)
	}
}

func TestStdioProbeCanceled(t *testing.T) {
	_, h, _ := newFakeChild(false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.awaitProbe(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	<-h.exited
}

func TestStdioPairsResponsesByID(t *testing.T) {
	c, h, _ := started(t, false)
	c.say(t, `{"ok":true}`)
	if _, err := h.awaitProbe(context.Background()); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	go func() { errs <- h.Do(context.Background(), HelperRequest{Op: "button", Name: "home"}) }()
	first := c.next(t)
	go func() { errs <- h.Do(context.Background(), HelperRequest{Op: "paste"}) }()
	second := c.next(t)
	if first["id"] == second["id"] {
		t.Fatalf("ids repeat: %v", first["id"])
	}
	// Answer out of order: the second command fails, the first succeeds.
	c.say(t, `{"id":`+itoa(second["id"])+`,"ok":false,"code":"input_failed","message":"no focus"}`)
	err := <-errs
	var se *Error
	if !errors.As(err, &se) || se.Code != "input_failed" || se.Message != "no focus" {
		t.Fatalf("second command err = %v", err)
	}
	c.say(t, `{"id":`+itoa(first["id"])+`,"ok":true}`)
	if err := <-errs; err != nil {
		t.Fatalf("first command err = %v", err)
	}
}

func itoa(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestStdioCrashFailsInFlightAndLater(t *testing.T) {
	c, h, tail := started(t, false)
	c.say(t, `{"ok":true}`)
	if _, err := h.awaitProbe(context.Background()); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 1)
	go func() { errs <- h.Do(context.Background(), HelperRequest{Op: "paste"}) }()
	c.next(t)
	_, _ = tail.Write([]byte("fatal: boom"))
	c.exit()
	err := <-errs
	var se *Error
	if !errors.As(err, &se) || se.Code != CodeInputFailed || !strings.Contains(se.Message, "fatal: boom") || errors.Is(err, errHelperNotRunning) {
		t.Fatalf("in-flight err = %v", err)
	}
	if !helperBroken(err) {
		t.Fatal("an exit during a command must count as broken")
	}
	if err := h.Do(context.Background(), HelperRequest{Op: "paste"}); err == nil {
		t.Fatal("command after exit succeeded")
	}
}

func TestStdioDoCanceled(t *testing.T) {
	c, h, _ := started(t, false)
	c.say(t, `{"ok":true}`)
	if _, err := h.awaitProbe(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	errs := make(chan error, 1)
	go func() { errs <- h.Do(ctx, HelperRequest{Op: "paste"}) }()
	c.next(t)
	cause := errors.New("timed out after 30s")
	cancel(cause)
	if err := <-errs; !errors.Is(err, cause) {
		t.Fatalf("err = %v", err)
	}
	h.mu.Lock()
	n := len(h.pending)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d pending after cancel", n)
	}
}

func TestStdioCloseEndsProcess(t *testing.T) {
	for _, stubborn := range []bool{false, true} {
		c, h, _ := newFakeChild(stubborn)
		c.say(t, `{"ok":true}`)
		if _, err := h.awaitProbe(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-c.done:
		default:
			t.Fatalf("stubborn=%v: process still running after Close", stubborn)
		}
		if c.killed.Load() != stubborn {
			t.Fatalf("stubborn=%v: killed = %v", stubborn, c.killed.Load())
		}
		if err := h.Close(); err != nil { // idempotent
			t.Fatal(err)
		}
	}
}

func TestTailBufferKeepsEnd(t *testing.T) {
	b := &tailBuffer{max: 5}
	_, _ = b.Write([]byte("abc"))
	_, _ = b.Write([]byte("defgh\n"))
	if got := b.String(); got != "efgh" {
		t.Fatalf("tail = %q", got)
	}
}
