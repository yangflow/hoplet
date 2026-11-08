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
}

// Service wraps simctl and tracks which session holds which device.
type Service struct {
	run   Runner
	now   func() time.Time
	alive func(context.Context, proto.SessionID) bool

	mu      sync.Mutex
	holders map[string]holderEntry // keyed by UDID
}

// New returns a Service. alive reports whether a session still exists and is
// not archived; now supplies the clock.
func New(r Runner, now func() time.Time, alive func(context.Context, proto.SessionID) bool) *Service {
	return &Service{run: r, now: now, alive: alive, holders: map[string]holderEntry{}}
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

func (s *Service) holderOf(udid string) proto.SessionID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.holders[udid].session
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
