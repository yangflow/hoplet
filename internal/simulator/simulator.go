// Package simulator drives iOS simulators through xcrun simctl.
package simulator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// Error codes reported to agent tools.
const (
	CodeUnavailable = "simulator_unavailable"
	CodeNotFound    = "device_not_found"
	CodeNotBooted   = "device_not_booted"
	CodeBusy        = "simulator_busy"
)

const bootTimeout = 120 * time.Second

const runtimePrefix = "com.apple.CoreSimulator.SimRuntime."

// Error is a simulator failure with a stable machine-readable code.
type Error struct {
	Code    string
	Message string
}

// Error implements error.
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Runner executes an external command and returns its standard output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner is the production Runner backed by os/exec.
type ExecRunner struct{}

// Run executes name with args; a failing command's error includes its stderr.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // the caller chooses the command; Service only passes xcrun and plutil
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if msg := string(bytes.TrimSpace(ee.Stderr)); msg != "" {
				return out, fmt.Errorf("%s: %w: %s", name, err, msg)
			}
		}
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// Device describes one available simulator device.
type Device struct {
	Name    string
	UDID    string
	State   string
	Runtime string
	// Width and Height are device points; 0 when unknown.
	Width  int
	Height int
	Scale  int
	// Holder is the session holding the device, empty when free.
	Holder proto.SessionID
	// InputAvailable reports whether input injection works for the device.
	InputAvailable bool
}

type holderEntry struct {
	session   proto.SessionID
	lastInput time.Time
	// inFlight counts running operations; the lease cannot expire while it
	// is positive.
	inFlight int
}

// Service wraps simctl and tracks which session holds which device.
type Service struct {
	run   Runner
	now   func() time.Time
	alive func(context.Context, proto.SessionID) bool

	mu       sync.Mutex
	holders  map[string]holderEntry // keyed by UDID
	geometry map[string]geometry    // keyed by device type identifier
}

// New returns a Service. alive reports whether a session still exists and is
// not archived; now supplies the clock.
func New(r Runner, now func() time.Time, alive func(context.Context, proto.SessionID) bool) *Service {
	return &Service{run: r, now: now, alive: alive, holders: map[string]holderEntry{}, geometry: map[string]geometry{}}
}

// Devices lists available simulator devices.
func (s *Service) Devices(ctx context.Context) ([]Device, error) {
	return s.list(ctx)
}

// Boot boots a device and waits until it is usable. It succeeds when the
// device is already booted.
func (s *Service) Boot(ctx context.Context, device string) error {
	d, err := s.resolve(ctx, "", device)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, bootTimeout)
	defer cancel()
	if _, err := s.simctl(ctx, "bootstatus", d.UDID, "-b"); err != nil {
		return err
	}
	return nil
}

// resolve finds a device by UDID, then by name. A blank device selects the
// device held by caller.
func (s *Service) resolve(ctx context.Context, caller proto.SessionID, device string) (Device, error) {
	devices, err := s.list(ctx)
	if err != nil {
		return Device{}, err
	}
	device = strings.TrimSpace(device)
	if device == "" {
		for _, d := range devices {
			if caller != "" && d.Holder == caller {
				return d, nil
			}
		}
		return Device{}, &Error{Code: CodeNotFound, Message: "no device specified and this session holds none; specify a device name or UDID"}
	}
	for _, d := range devices {
		if strings.EqualFold(d.UDID, device) {
			return d, nil
		}
	}
	var named []Device
	for _, d := range devices {
		if d.Name == device {
			named = append(named, d)
		}
	}
	switch len(named) {
	case 0:
		return Device{}, &Error{Code: CodeNotFound, Message: fmt.Sprintf("no available device matches %q", device)}
	case 1:
		return named[0], nil
	}
	udids := make([]string, len(named))
	for i, d := range named {
		udids[i] = d.UDID
	}
	return Device{}, &Error{Code: CodeNotFound, Message: fmt.Sprintf("%d devices are named %q; specify a UDID: %s", len(named), device, strings.Join(udids, ", "))}
}

// leaseIdle is how long a holder may go without input before others can take
// the device. Exactly leaseIdle counts as expired.
const leaseIdle = 60 * time.Second

func (s *Service) expired(e holderEntry) bool {
	return e.inFlight == 0 && s.now().Sub(e.lastInput) >= leaseIdle
}

// finishOp ends an operation started by acquire with ops 1 and restarts the
// idle window. It leaves the entry alone if another session has since taken
// the device.
func (s *Service) finishOp(caller proto.SessionID, udid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.holders[udid]
	if !ok || e.session != caller || e.inFlight == 0 {
		return
	}
	e.inFlight--
	e.lastInput = s.now()
	s.holders[udid] = e
}

// holderOf returns the live holder of a device: expired holds and holds of
// sessions that are no longer alive are dropped and read as free.
func (s *Service) holderOf(ctx context.Context, udid string) proto.SessionID {
	s.mu.Lock()
	e, ok := s.holders[udid]
	if ok && s.expired(e) {
		delete(s.holders, udid)
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		return ""
	}
	// alive may be slow, so it runs without the lock; the entry is only
	// dropped if it is still the one that was checked.
	if s.alive(ctx, e.session) {
		return e.session
	}
	s.mu.Lock()
	if s.holders[udid] == e {
		delete(s.holders, udid)
	}
	s.mu.Unlock()
	return ""
}

// acquire gives caller the lease on a device, or fails with CodeBusy naming
// the holder. The alive check runs outside the lock; after it the table is
// re-checked and the decision is retried if another caller changed the entry,
// so two callers never both win. Entries compare by value, so a change to the
// in-flight count also forces a retry. ops is added to the caller's in-flight
// count; each one must be ended with finishOp.
func (s *Service) acquire(ctx context.Context, caller proto.SessionID, udid string, ops int) error {
	for {
		s.mu.Lock()
		e, held := s.holders[udid]
		if !held || e.session == caller || s.expired(e) {
			if held && e.session == caller {
				ops += e.inFlight
			}
			s.holders[udid] = holderEntry{session: caller, lastInput: s.now(), inFlight: ops}
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()

		alive := s.alive(ctx, e.session)

		s.mu.Lock()
		if s.holders[udid] != e {
			s.mu.Unlock()
			continue
		}
		if !alive {
			s.holders[udid] = holderEntry{session: caller, lastInput: s.now(), inFlight: ops}
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()
		return &Error{Code: CodeBusy, Message: fmt.Sprintf("device %s is held by session %s; use another device or wait until it is idle", udid, e.session)}
	}
}

// Launch installs the app at appPath (when given) and launches bundleID on a
// booted device. It takes the device lease first; a refused lease runs
// nothing, and a failed install or launch keeps the lease. The lease cannot
// expire while Launch runs, and its idle window restarts when Launch ends.
func (s *Service) Launch(ctx context.Context, caller proto.SessionID, device, bundleID, appPath string) error {
	d, err := s.resolve(ctx, caller, device)
	if err != nil {
		return err
	}
	if err := requireBooted(d); err != nil {
		return err
	}
	if err := s.acquire(ctx, caller, d.UDID, 1); err != nil {
		return err
	}
	defer s.finishOp(caller, d.UDID)
	if appPath != "" {
		if _, err := s.simctl(ctx, "install", d.UDID, appPath); err != nil {
			return err
		}
	}
	_, err = s.simctl(ctx, "launch", d.UDID, bundleID)
	return err
}

// OpenURL opens a URL or deep link on a booted device. It needs no lease.
func (s *Service) OpenURL(ctx context.Context, caller proto.SessionID, device, url string) error {
	d, err := s.resolve(ctx, caller, device)
	if err != nil {
		return err
	}
	if err := requireBooted(d); err != nil {
		return err
	}
	_, err = s.simctl(ctx, "openurl", d.UDID, url)
	return err
}

func requireBooted(d Device) error {
	if d.State != "Booted" {
		return &Error{Code: CodeNotBooted, Message: fmt.Sprintf("device %q is %s; boot it first", d.Name, d.State)}
	}
	return nil
}

// simctl runs xcrun simctl and maps a missing toolchain to CodeUnavailable.
func (s *Service) simctl(ctx context.Context, args ...string) ([]byte, error) {
	out, err := s.run.Run(ctx, "xcrun", append([]string{"simctl"}, args...)...)
	if err != nil {
		if unavailable(err) {
			return nil, &Error{Code: CodeUnavailable, Message: "xcrun simctl is not available: " + err.Error()}
		}
		return nil, fmt.Errorf("simctl %s: %w", args[0], err)
	}
	return out, nil
}

func unavailable(err error) bool {
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "unable to find utility") || strings.Contains(msg, "active developer directory")
}
