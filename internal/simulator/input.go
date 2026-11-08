package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// Limits on input. A helper command gets inputTimeout; a helper idle for more
// than helperIdle is closed by ReapIdle.
const (
	inputTimeout = 30 * time.Second
	helperIdle   = 60 * time.Second
	// maxGesture bounds the duration of a tap or swipe.
	maxGesture = 10 * time.Second
)

type probeResult struct {
	ok  bool
	msg string
}

type helperEntry struct {
	ready    chan struct{} // closed once the start has finished
	h        Helper
	err      error // set before ready closes when the start failed
	busy     int   // commands using or waiting for the helper
	lastUsed time.Time
}

func unsupportedInput(msg string) error {
	return &Error{Code: CodeInputUnsupported, Message: msg}
}

// probeInput reports nil when the input helper exists and its probe passes.
// The probe result is cached per Xcode path, so a change of the selected
// Xcode probes again. A missing file or an unreadable Xcode path is not
// cached.
func (s *Service) probeInput(ctx context.Context) error {
	path := ""
	if s.helperFile != nil {
		path = s.helperFile()
	}
	if path == "" {
		return unsupportedInput("the simulator input helper is not available on this host")
	}
	if _, err := os.Stat(path); err != nil {
		return unsupportedInput("the simulator input helper is missing: " + filepath.Base(path))
	}
	ctx, cancel := withLimit(ctx, s.opLimit)
	defer cancel()
	out, err := s.run.Run(ctx, "xcode-select", "-p")
	xcode := strings.TrimSpace(string(out))
	if err != nil || xcode == "" {
		return unsupportedInput("cannot read the selected Xcode path")
	}
	s.pmu.Lock()
	res, ok := s.probes[xcode]
	s.pmu.Unlock()
	if !ok {
		res = s.runProbe(ctx, path)
		if ctx.Err() != nil {
			return unsupportedInput("the simulator input probe did not finish: " + context.Cause(ctx).Error())
		}
		s.pmu.Lock()
		s.probes[xcode] = res
		s.pmu.Unlock()
	}
	if !res.ok {
		return unsupportedInput(res.msg)
	}
	return nil
}

func (s *Service) runProbe(ctx context.Context, path string) probeResult {
	out, err := s.run.Run(ctx, path, "--probe")
	var r wireResponse
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &r); jerr == nil {
		if err == nil && r.OK {
			return probeResult{ok: true}
		}
		return probeResult{msg: probeMessage(r)}
	}
	if err != nil {
		return probeResult{msg: "the simulator input probe failed: " + err.Error()}
	}
	return probeResult{msg: "the simulator input probe printed no result"}
}

// start launches a helper for a device.
func (s *Service) start(ctx context.Context, udid string) (Helper, error) {
	if s.starter != nil {
		return s.starter(ctx, udid)
	}
	return execStarter(s.helperFile())(ctx, udid)
}

// helperFor returns the helper of a device, starting one when there is none,
// and counts the caller as using it; each success must be ended with
// helperDone.
func (s *Service) helperFor(ctx context.Context, udid string) (*helperEntry, error) {
	s.hmu.Lock()
	if s.closed {
		s.hmu.Unlock()
		return nil, errors.New("simulator service is closed")
	}
	e, ok := s.helpers[udid]
	if ok {
		e.busy++
		s.hmu.Unlock()
		select {
		case <-e.ready:
		case <-ctx.Done():
			s.helperDone(e)
			return nil, fmt.Errorf("start simulator helper: %w", context.Cause(ctx))
		}
		if e.err != nil {
			s.helperDone(e)
			return nil, e.err
		}
		return e, nil
	}
	e = &helperEntry{ready: make(chan struct{}), busy: 1, lastUsed: s.now()}
	s.helpers[udid] = e
	s.hmu.Unlock()

	h, err := s.start(ctx, udid)

	s.hmu.Lock()
	if err == nil && s.closed {
		// Close ran during the start and cannot see this helper.
		defer func(h Helper) { _ = h.Close() }(h)
		h, err = nil, errors.New("simulator service is closed")
	}
	if err != nil {
		e.err = err
		if s.helpers[udid] == e {
			delete(s.helpers, udid)
		}
	} else {
		e.h = h
	}
	close(e.ready)
	s.hmu.Unlock()
	if err != nil {
		s.helperDone(e)
		return nil, err
	}
	return e, nil
}

// helperDone ends a use counted by helperFor and restarts the idle window.
func (s *Service) helperDone(e *helperEntry) {
	s.hmu.Lock()
	defer s.hmu.Unlock()
	e.busy--
	e.lastUsed = s.now()
}

// drop forgets a helper and closes it, so the next command starts a new one.
func (s *Service) drop(udid string, e *helperEntry) {
	s.hmu.Lock()
	if s.helpers[udid] == e {
		delete(s.helpers, udid)
	}
	s.hmu.Unlock()
	_ = e.h.Close()
}

// helperBroken reports whether a failed command leaves the helper unusable:
// anything but an ordinary failure response, since the helper ends itself
// after a timeout or cancellation.
func helperBroken(err error) bool {
	var se *Error
	if !errors.As(err, &se) {
		return true
	}
	return se.Code == "timeout" || se.Code == "cancelled"
}

// helperDo sends one command to the device's helper under the command limit.
// The command is not retried: a crash fails it and the next command starts a
// new process.
func (s *Service) helperDo(ctx context.Context, udid string, req HelperRequest) error {
	ctx, cancel := withLimit(ctx, s.inputLimit)
	defer cancel()
	e, err := s.helperFor(ctx, udid)
	if err != nil {
		return err
	}
	defer s.helperDone(e)
	if err := e.h.Do(ctx, req); err != nil {
		if helperBroken(err) {
			s.drop(udid, e)
		}
		return fmt.Errorf("simulator %s: %w", req.Op, err)
	}
	return nil
}

// ReapIdle closes the helpers that have been idle for more than a minute
// before now and returns how many it closed. A helper idle for exactly a
// minute stays, and one with a command in flight is never closed.
func (s *Service) ReapIdle(now time.Time) int {
	var stale []Helper
	s.hmu.Lock()
	for udid, e := range s.helpers {
		if e.h != nil && e.busy == 0 && now.Sub(e.lastUsed) > helperIdle {
			stale = append(stale, e.h)
			delete(s.helpers, udid)
		}
	}
	s.hmu.Unlock()
	for _, h := range stale {
		_ = h.Close()
	}
	return len(stale)
}

// Close closes every helper and refuses to start new ones.
func (s *Service) Close() error {
	s.hmu.Lock()
	s.closed = true
	var all []Helper
	for udid, e := range s.helpers {
		if e.h != nil {
			all = append(all, e.h)
			delete(s.helpers, udid)
		}
	}
	s.hmu.Unlock()
	var errs []error
	for _, h := range all {
		errs = append(errs, h.Close())
	}
	return errors.Join(errs...)
}

// withInput runs an input operation on a booted device: it checks the
// arguments, takes the lease, requires a working helper and ends by restarting
// the idle window whether or not the operation succeeded.
func (s *Service) withInput(ctx context.Context, caller proto.SessionID, device string, check func(Device) error, run func(Device) error) error {
	d, err := s.resolve(ctx, caller, device)
	if err != nil {
		return err
	}
	if err := requireBooted(d); err != nil {
		return err
	}
	if check != nil {
		if err := check(d); err != nil {
			return err
		}
	}
	if err := s.acquire(ctx, caller, d.UDID, 1); err != nil {
		return err
	}
	defer s.finishOp(caller, d.UDID)
	if err := s.probeInput(ctx); err != nil {
		return err
	}
	return run(d)
}

func invalid(format string, args ...any) error {
	return &Error{Code: CodeInvalidArgument, Message: fmt.Sprintf(format, args...)}
}

func checkDuration(d time.Duration) error {
	if d < 0 || d > maxGesture {
		return invalid("duration %s is outside 0 to %s", d, maxGesture)
	}
	return nil
}

// ratio converts a coordinate in device points to a 0 to 1 ratio of size.
func ratio(axis string, v float64, size int) (float64, error) {
	if size <= 0 {
		return 0, fmt.Errorf("screen size of the device is unavailable, so a %s point cannot be converted", axis)
	}
	if !(v >= 0 && v <= float64(size)) {
		return 0, invalid("%s %g is outside the screen (0 to %d points)", axis, v, size)
	}
	return v / float64(size), nil
}

func ratios(d Device, x, y float64) (rx, ry float64, err error) {
	if rx, err = ratio("x", x, d.Width); err != nil {
		return 0, 0, err
	}
	if ry, err = ratio("y", y, d.Height); err != nil {
		return 0, 0, err
	}
	return rx, ry, nil
}

// Tap touches the screen at (x, y) in device points, holding for duration;
// zero uses the helper's default. It needs the device lease.
func (s *Service) Tap(ctx context.Context, caller proto.SessionID, device string, x, y float64, duration time.Duration) error {
	var req HelperRequest
	check := func(d Device) error {
		if err := checkDuration(duration); err != nil {
			return err
		}
		rx, ry, err := ratios(d, x, y)
		req = HelperRequest{Op: "tap", X: rx, Y: ry, Duration: duration}
		return err
	}
	return s.withInput(ctx, caller, device, check, func(d Device) error {
		return s.helperDo(ctx, d.UDID, req)
	})
}

// Swipe drags from (x, y) to (x2, y2) in device points over duration; zero
// uses the helper's default. edge is empty or "bottom"; any other value is
// unsupported. It needs the device lease.
func (s *Service) Swipe(ctx context.Context, caller proto.SessionID, device string, x, y, x2, y2 float64, duration time.Duration, edge string) error {
	var req HelperRequest
	check := func(d Device) error {
		if edge != "" && edge != "bottom" {
			return &Error{Code: CodeUnsupported, Message: fmt.Sprintf("edge %q is not supported; use bottom or leave it out", edge)}
		}
		if err := checkDuration(duration); err != nil {
			return err
		}
		rx, ry, err := ratios(d, x, y)
		if err != nil {
			return err
		}
		rx2, ry2, err := ratios(d, x2, y2)
		if err != nil {
			return err
		}
		req = HelperRequest{Op: "swipe", X: rx, Y: ry, X2: rx2, Y2: ry2, Duration: duration, Edge: edge}
		return nil
	}
	return s.withInput(ctx, caller, device, check, func(d Device) error {
		return s.helperDo(ctx, d.UDID, req)
	})
}

// Text types text into the focused field. Text that maps wholly onto the US
// keyboard (newline and tab included) is typed as key presses; anything else
// is written to the device clipboard and pasted. It needs the device lease.
func (s *Service) Text(ctx context.Context, caller proto.SessionID, device, text string) error {
	check := func(Device) error {
		if text == "" {
			return invalid("text is empty")
		}
		return nil
	}
	return s.withInput(ctx, caller, device, check, func(d Device) error {
		if chunks, ok := keyChunks(text); ok {
			for _, c := range chunks {
				if err := s.helperDo(ctx, d.UDID, HelperRequest{Op: "keys", Usages: c}); err != nil {
					return err
				}
			}
			return nil
		}
		if _, err := s.simctlStdinWithin(ctx, s.opLimit, []byte(text), "pbcopy", d.UDID); err != nil {
			return err
		}
		return s.helperDo(ctx, d.UDID, HelperRequest{Op: "paste"})
	})
}

func (s *Service) simctlStdinWithin(ctx context.Context, d time.Duration, stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := withLimit(ctx, d)
	defer cancel()
	return s.simctlStdin(ctx, stdin, args...)
}

// Button presses a hardware button; only "home" is supported. It needs the
// device lease.
func (s *Service) Button(ctx context.Context, caller proto.SessionID, device, name string) error {
	check := func(Device) error {
		if name != "home" {
			return &Error{Code: CodeUnsupported, Message: fmt.Sprintf("button %q is not supported; only home is", name)}
		}
		return nil
	}
	return s.withInput(ctx, caller, device, check, func(d Device) error {
		return s.helperDo(ctx, d.UDID, HelperRequest{Op: "button", Name: name})
	})
}
