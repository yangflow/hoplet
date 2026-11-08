package simulator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// helperName is the file name of the input helper, installed beside the
// hoplet executable.
const helperName = "hoplet-simulator-helper"

// helperCloseWait is how long Close waits for a helper to exit after its
// standard input is closed before killing it.
const helperCloseWait = 3 * time.Second

// stderrTail is how much of a helper's standard error is kept for errors.
const stderrTail = 4096

// errHelperNotRunning marks a command that was not sent because its helper
// had already exited. Nothing reached the helper, so a fresh one may take the
// command.
var errHelperNotRunning = errors.New("simulator helper is not running")

// HelperRequest is one command for the input helper. Coordinates are ratios
// of the screen, 0 to 1.
type HelperRequest struct {
	// Op is tap, swipe, keys, paste or button.
	Op           string
	X, Y, X2, Y2 float64
	// Duration is left out of the command when zero, so the helper's default
	// applies.
	Duration time.Duration
	// Edge is left out of the command when empty.
	Edge string
	// Usages are HID keyboard usages for keys.
	Usages []int
	// Name is the hardware button for button.
	Name string
}

// Helper is a running input helper for one device.
type Helper interface {
	// Do sends one command and waits for its response. A response that
	// reports failure comes back as an *Error carrying the helper's code.
	Do(ctx context.Context, req HelperRequest) error
	// Close ends the helper and waits until it has exited.
	Close() error
}

// HelperStarter starts a helper for a device. The helper's lifetime is not
// tied to ctx, which only bounds the start.
type HelperStarter func(ctx context.Context, udid string) (Helper, error)

// defaultHelperFile finds the helper beside the running executable, following
// symlinks. It is empty when the executable cannot be located or the host is
// not macOS.
func defaultHelperFile() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Join(filepath.Dir(exe), helperName)
}

// encodeRequest renders one command line, newline included.
func encodeRequest(id int64, r HelperRequest) ([]byte, error) {
	m := map[string]any{"id": id, "op": r.Op}
	switch r.Op {
	case "tap":
		m["x"], m["y"] = r.X, r.Y
	case "swipe":
		m["x"], m["y"], m["x2"], m["y2"] = r.X, r.Y, r.X2, r.Y2
		if r.Edge != "" {
			m["edge"] = r.Edge
		}
	case "keys":
		m["usages"] = r.Usages
	case "button":
		m["name"] = r.Name
	case "paste":
	default:
		return nil, fmt.Errorf("unknown helper op %q", r.Op)
	}
	if (r.Op == "tap" || r.Op == "swipe") && r.Duration > 0 {
		m["duration"] = r.Duration.Seconds()
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode helper command: %w", err)
	}
	return append(b, '\n'), nil
}

// wireResponse is one line from the helper: a command response, or the probe
// line that opens the stream (no id).
type wireResponse struct {
	ID      *int64   `json:"id"`
	OK      bool     `json:"ok"`
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Xcode   string   `json:"xcode"`
	Missing []string `json:"missing"`
}

// tailBuffer keeps the last max bytes written to it. It is safe for
// concurrent use.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.TrimSpace(t.buf))
}

// process is the part of a child process the client controls.
type process interface {
	// Wait returns once the process has exited.
	Wait() error
	// Kill ends the process.
	Kill() error
}

// stdioHelper is the production Helper: it speaks the helper's line protocol
// over a pipe pair and pairs responses with requests by id.
type stdioHelper struct {
	stdin     io.WriteCloser
	proc      process
	tail      *tailBuffer
	closeWait time.Duration

	wmu sync.Mutex // serializes command writes

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan wireResponse

	ready  chan wireResponse // the probe line
	exited chan struct{}     // closed after exitErr is set
	// exitErr says why the process ended; set before exited closes.
	exitErr error

	closeOnce sync.Once
}

// newStdioHelper starts reading stdout; the helper ends when stdout does and
// the process has been waited for.
func newStdioHelper(stdin io.WriteCloser, stdout io.Reader, proc process, tail *tailBuffer, closeWait time.Duration) *stdioHelper {
	h := &stdioHelper{
		stdin: stdin, proc: proc, tail: tail, closeWait: closeWait,
		pending: map[int64]chan wireResponse{},
		ready:   make(chan wireResponse, 1),
		exited:  make(chan struct{}),
	}
	go h.run(stdout)
	return h
}

func (h *stdioHelper) run(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	probed := false
	for sc.Scan() {
		var r wireResponse
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if r.ID == nil {
			// The first line without an id is the probe result. Any later
			// line without one cannot be paired and is dropped.
			if !probed {
				probed = true
				h.ready <- r
			}
			continue
		}
		h.mu.Lock()
		ch, ok := h.pending[*r.ID]
		delete(h.pending, *r.ID)
		h.mu.Unlock()
		if ok {
			ch <- r
		}
	}
	err := h.proc.Wait()
	h.exitErr = h.exitError(err)
	close(h.exited)
}

func (h *stdioHelper) exitError(waitErr error) error {
	msg := "simulator helper exited"
	if waitErr != nil {
		msg = fmt.Sprintf("simulator helper exited: %v", waitErr)
	}
	if s := h.tail.String(); s != "" {
		msg += ": " + s
	}
	return errors.New(msg)
}

// awaitProbe waits for the helper's opening probe line. A helper that fails
// its probe or exits first is closed and reported as input_unsupported.
func (h *stdioHelper) awaitProbe(ctx context.Context) (Helper, error) {
	var r wireResponse
	select {
	case r = <-h.ready:
	case <-h.exited:
		select {
		case r = <-h.ready:
		default:
			return nil, &Error{Code: CodeInputUnsupported, Message: h.exitErr.Error()}
		}
	case <-ctx.Done():
		_ = h.Close()
		return nil, fmt.Errorf("start simulator helper: %w", context.Cause(ctx))
	}
	if !r.OK {
		_ = h.Close()
		code := CodeInputUnsupported
		// A message without missing symbols means the device could not be
		// opened (not booted, unknown, not connectable), which is a failure
		// of this command, not of the Xcode.
		if r.Message != "" && len(r.Missing) == 0 {
			code = CodeInputFailed
		}
		return nil, &Error{Code: code, Message: probeMessage(r)}
	}
	return h, nil
}

func probeMessage(r wireResponse) string {
	if r.Message != "" {
		return r.Message
	}
	msg := "the simulator input helper reported that this Xcode is not supported"
	if len(r.Missing) > 0 {
		msg += "; missing: " + strings.Join(r.Missing, ", ")
	}
	if r.Xcode != "" {
		msg += " (Xcode " + r.Xcode + ")"
	}
	return msg
}

// Do implements Helper.
func (h *stdioHelper) Do(ctx context.Context, req HelperRequest) error {
	select {
	case <-h.exited:
		return fmt.Errorf("%w: %w", errHelperNotRunning, h.exitErr)
	default:
	}
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	ch := make(chan wireResponse, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	forget := func() {
		h.mu.Lock()
		delete(h.pending, id)
		h.mu.Unlock()
	}

	line, err := encodeRequest(id, req)
	if err != nil {
		forget()
		return err
	}
	h.wmu.Lock()
	_, err = h.stdin.Write(line)
	h.wmu.Unlock()
	if err != nil {
		forget()
		select {
		case <-h.exited:
			return fmt.Errorf("%w: %w", errHelperNotRunning, h.exitErr)
		default:
		}
		return fmt.Errorf("send simulator helper command: %w", err)
	}
	select {
	case r := <-ch:
		return responseError(r)
	case <-h.exited:
		select {
		case r := <-ch:
			return responseError(r)
		default:
			// The command was sent, so it is not retried.
			return &Error{Code: CodeInputFailed, Message: h.exitErr.Error()}
		}
	case <-ctx.Done():
		forget()
		return context.Cause(ctx)
	}
}

func responseError(r wireResponse) error {
	if r.OK {
		return nil
	}
	code := r.Code
	if code == "" {
		code = CodeInputFailed
	}
	return &Error{Code: code, Message: r.Message}
}

// Close closes the helper's standard input, which makes it lift any touch and
// exit, and kills it if it has not exited within the close wait.
func (h *stdioHelper) Close() error {
	h.closeOnce.Do(func() {
		_ = h.stdin.Close()
		t := time.NewTimer(h.closeWait)
		defer t.Stop()
		select {
		case <-h.exited:
		case <-t.C:
			_ = h.proc.Kill()
		}
	})
	<-h.exited
	return nil
}

// cmdProcess adapts an exec.Cmd to process.
type cmdProcess struct{ cmd *exec.Cmd }

func (p cmdProcess) Wait() error { return p.cmd.Wait() }
func (p cmdProcess) Kill() error { return p.cmd.Process.Kill() }

// execStarter starts the helper binary at path for a device and waits for its
// probe line.
func execStarter(path string) HelperStarter {
	return func(ctx context.Context, udid string) (Helper, error) {
		cmd := exec.Command(path, "--device", udid) //nolint:gosec // path is the helper installed beside the executable
		tail := &tailBuffer{max: stderrTail}
		cmd.Stderr = tail
		cmd.WaitDelay = killGrace
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("start simulator helper: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("start simulator helper: %w", err)
		}
		if err := cmd.Start(); err != nil {
			return nil, &Error{Code: CodeInputUnsupported, Message: fmt.Sprintf("start simulator helper: %v", err)}
		}
		return newStdioHelper(stdin, stdout, cmdProcess{cmd}, tail, helperCloseWait).awaitProbe(ctx)
	}
}
