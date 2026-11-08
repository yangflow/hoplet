package simulator

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	mu    sync.Mutex
	calls []call
	out   map[string]string // keyed by joined args
	err   error
	// hook, when set, runs after the call is recorded and may return a result.
	hook func(name string, args []string) ([]byte, error, bool)
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call{name, args})
	hook, err := f.hook, f.err
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	// The hook runs unlocked so it can call back into the service.
	if hook != nil {
		if out, err, ok := hook(name, args); ok {
			return out, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return []byte(f.out[strings.Join(args, " ")]), nil
}

const listJSON = `{"devices":{
 "com.apple.CoreSimulator.SimRuntime.iOS-18-0":[
  {"name":"Phone A","udid":"UDID-A18","state":"Booted","isAvailable":true,"deviceTypeIdentifier":"type.phone-a"},
  {"name":"Phone Twin","udid":"UDID-T1","state":"Shutdown","isAvailable":true,"deviceTypeIdentifier":"type.phone-twin"},
  {"name":"Phone Gone","udid":"UDID-G","state":"Shutdown","isAvailable":false,"deviceTypeIdentifier":"type.phone-a"}],
 "com.apple.CoreSimulator.SimRuntime.iOS-17-5":[
  {"name":"Phone A","udid":"UDID-A17","state":"Shutdown","isAvailable":true,"deviceTypeIdentifier":"type.phone-a"},
  {"name":"Phone Twin","udid":"UDID-T2","state":"Shutdown","isAvailable":true,"deviceTypeIdentifier":"type.phone-twin"}],
 "com.apple.CoreSimulator.SimRuntime.watchOS-11-0":[
  {"name":"Watch","udid":"UDID-W","state":"Shutdown","isAvailable":true,"deviceTypeIdentifier":"type.watch"}]}}`

const deviceTypesJSON = `{"devicetypes":[
 {"name":"Phone A","identifier":"type.phone-a","bundlePath":"/types/PhoneA.simdevicetype"},
 {"name":"Phone Twin","identifier":"type.phone-twin","bundlePath":"/types/PhoneTwin.simdevicetype"},
 {"name":"Watch","identifier":"type.watch","bundlePath":"/types/Watch.simdevicetype"}]}`

// capabilitiesJSON is plutil's JSON rendering of a device type's
// capabilities.plist, reduced to the keys the service reads. Width and height
// are pixels.
func capabilitiesJSON(scale, w, h int) string {
	return fmt.Sprintf(`{"capabilities":{"ScreenDimensionsCapability":{"main-screen-scale":%d,"main-screen-width":%d,"main-screen-height":%d}}}`, scale, w, h)
}

func plutilKey(bundle string) string {
	return "-convert json -o - " + bundle + "/Contents/Resources/capabilities.plist"
}

// testNow is the fixed clock tests inject.
var testNow = time.Unix(1_000_000, 0)

// fakeClock is a clock tests advance by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newSvc(f *fakeRunner) *Service {
	return New(f, func() time.Time { return testNow }, func(context.Context, proto.SessionID) bool { return true })
}

func listRunner() *fakeRunner {
	return &fakeRunner{out: map[string]string{
		"simctl list -j devices":                    listJSON,
		"simctl list -j devicetypes":                deviceTypesJSON,
		plutilKey("/types/PhoneA.simdevicetype"):    capabilitiesJSON(3, 1179, 2556),
		plutilKey("/types/PhoneTwin.simdevicetype"): capabilitiesJSON(2, 750, 1334),
	}}
}

func TestDevices(t *testing.T) {
	f := listRunner()
	s := newSvc(f)
	s.holders["UDID-A18"] = holderEntry{session: "ses_1", lastInput: testNow}
	got, err := s.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Device{
		{Name: "Phone A", UDID: "UDID-A17", State: "Shutdown", Runtime: "iOS-17-5", Width: 393, Height: 852, Scale: 3},
		{Name: "Phone Twin", UDID: "UDID-T2", State: "Shutdown", Runtime: "iOS-17-5", Width: 375, Height: 667, Scale: 2},
		{Name: "Phone A", UDID: "UDID-A18", State: "Booted", Runtime: "iOS-18-0", Holder: "ses_1", Width: 393, Height: 852, Scale: 3},
		{Name: "Phone Twin", UDID: "UDID-T1", State: "Shutdown", Runtime: "iOS-18-0", Width: 375, Height: 667, Scale: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("devices = %+v\nwant %+v", got, want)
	}
	if c := f.calls[0]; c.name != "xcrun" {
		t.Fatalf("command = %q", c.name)
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name, caller, device string
		held                 map[string]proto.SessionID
		wantUDID, wantCode   string
		wantInMsg            []string
	}{
		{name: "by udid", device: "UDID-A17", wantUDID: "UDID-A17"},
		{name: "by udid beats name", device: "UDID-T1", wantUDID: "UDID-T1"},
		{name: "unavailable device", device: "Phone Gone", wantCode: CodeNotFound},
		{name: "duplicate name", device: "Phone Twin", wantCode: CodeNotFound, wantInMsg: []string{"UDID-T1", "UDID-T2"}},
		{name: "duplicate across runtimes", device: "Phone A", wantCode: CodeNotFound, wantInMsg: []string{"UDID-A17", "UDID-A18"}},
		{name: "missing", device: "Nope", wantCode: CodeNotFound},
		{name: "omitted without hold", caller: "ses_1", wantCode: CodeNotFound, wantInMsg: []string{"specify"}},
		{name: "omitted without caller", wantCode: CodeNotFound, wantInMsg: []string{"specify"}},
		{name: "omitted with hold", caller: "ses_1", held: map[string]proto.SessionID{"UDID-A18": "ses_1", "UDID-T1": "ses_2"}, wantUDID: "UDID-A18"},
		{name: "omitted other holds", caller: "ses_1", held: map[string]proto.SessionID{"UDID-T1": "ses_2"}, wantCode: CodeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSvc(listRunner())
			for u, h := range tt.held {
				s.holders[u] = holderEntry{session: h, lastInput: testNow}
			}
			d, err := s.resolve(context.Background(), proto.SessionID(tt.caller), tt.device)
			if tt.wantCode != "" {
				var se *Error
				if !errors.As(err, &se) || se.Code != tt.wantCode {
					t.Fatalf("err = %v, want code %s", err, tt.wantCode)
				}
				for _, m := range tt.wantInMsg {
					if !strings.Contains(se.Message, m) {
						t.Fatalf("message %q lacks %q", se.Message, m)
					}
				}
				return
			}
			if err != nil || d.UDID != tt.wantUDID {
				t.Fatalf("d = %+v, err = %v, want %s", d, err, tt.wantUDID)
			}
		})
	}
}

func TestXcrunMissingIsUnavailable(t *testing.T) {
	f := &fakeRunner{err: &exec.Error{Name: "xcrun", Err: exec.ErrNotFound}}
	s := newSvc(f)
	for name, fn := range map[string]func() error{
		"devices": func() error { _, err := s.Devices(context.Background()); return err },
		"boot":    func() error { return s.Boot(context.Background(), "UDID-A17") },
	} {
		var se *Error
		if err := fn(); !errors.As(err, &se) || se.Code != CodeUnavailable {
			t.Fatalf("%s: err = %v, want %s", name, err, CodeUnavailable)
		}
	}
}

func TestBoot(t *testing.T) {
	f := listRunner()
	s := newSvc(f)
	if err := s.Boot(context.Background(), "Phone A "); err == nil {
		t.Fatal("duplicate name should fail")
	}
	f.calls = nil
	if err := s.Boot(context.Background(), "UDID-A17"); err != nil {
		t.Fatal(err)
	}
	last := f.calls[len(f.calls)-1]
	want := call{"xcrun", []string{"simctl", "bootstatus", "UDID-A17", "-b"}}
	if !reflect.DeepEqual(last, want) {
		t.Fatalf("last call = %+v, want %+v", last, want)
	}
	for _, c := range f.calls[:len(f.calls)-1] {
		if c.args[1] == "boot" {
			t.Fatal("unexpected boot call")
		}
	}
	var se *Error
	if err := s.Boot(context.Background(), ""); !errors.As(err, &se) || se.Code != CodeNotFound {
		t.Fatalf("blank device err = %v", err)
	}
}

// blockingRunner waits for the context on the commands block selects, the way
// a hung simctl does until it is killed.
type blockingRunner struct {
	*fakeRunner
	block func(name string, args []string) bool
}

func (b *blockingRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if b.block(name, args) {
		<-ctx.Done()
		return nil, fmt.Errorf("%s: %w", name, errors.New("signal: killed"))
	}
	return b.fakeRunner.Run(ctx, name, args...)
}

func simctlVerb(verb string) func(string, []string) bool {
	return func(_ string, args []string) bool { return len(args) > 1 && args[0] == "simctl" && args[1] == verb }
}

func TestOperationsAreBounded(t *testing.T) {
	ctx := context.Background()
	screenshot := func(s *Service) error { _, err := s.Screenshot(ctx, "ses_1", "UDID-A18"); return err }
	devices := func(s *Service) error { _, err := s.Devices(ctx); return err }
	boot := func(s *Service) error { return s.Boot(ctx, "UDID-A17") }
	launch := func(s *Service) error { return s.Launch(ctx, "ses_1", "UDID-A18", "b", "/work/App.app") }
	openURL := func(s *Service) error { return s.OpenURL(ctx, "ses_1", "UDID-A18", "myapp://x") }
	tests := []struct {
		name  string
		block func(string, []string) bool
		op    func(*Service) error
		limit func(*Service) *time.Duration
		want  string
	}{
		{"list", simctlVerb("list"), devices, func(s *Service) *time.Duration { return &s.opLimit }, "simctl list: timed out after 1ms"},
		{"boot", simctlVerb("bootstatus"), boot, func(s *Service) *time.Duration { return &s.bootLimit }, "simctl bootstatus: timed out after 1ms"},
		{"install", simctlVerb("install"), launch, func(s *Service) *time.Duration { return &s.launchLimit }, "simctl install: timed out after 1ms"},
		{"launch", simctlVerb("launch"), launch, func(s *Service) *time.Duration { return &s.launchLimit }, "simctl launch: timed out after 1ms"},
		{"screenshot", simctlVerb("io"), screenshot, func(s *Service) *time.Duration { return &s.opLimit }, "simctl io: timed out after 1ms"},
		{"open url", simctlVerb("openurl"), openURL, func(s *Service) *time.Duration { return &s.opLimit }, "simctl openurl: timed out after 1ms"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(&blockingRunner{fakeRunner: listRunner(), block: tt.block}, func() time.Time { return testNow }, func(context.Context, proto.SessionID) bool { return true })
			*tt.limit(s) = time.Millisecond
			err := tt.op(s)
			if err == nil || !strings.Contains(err.Error(), tt.want) || strings.Contains(err.Error(), "signal: killed") {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestGeometryIsBounded(t *testing.T) {
	for name, block := range map[string]func(string, []string) bool{
		"devicetypes": func(_ string, args []string) bool { return strings.Join(args, " ") == "simctl list -j devicetypes" },
		"plutil":      func(name string, _ []string) bool { return name == "plutil" },
	} {
		t.Run(name, func(t *testing.T) {
			s := New(&blockingRunner{fakeRunner: listRunner(), block: block}, func() time.Time { return testNow }, func(context.Context, proto.SessionID) bool { return true })
			s.opLimit = time.Millisecond
			devices, err := s.Devices(context.Background())
			if err != nil || len(devices) != 4 {
				t.Fatalf("devices = %+v, err = %v", devices, err)
			}
			for _, d := range devices {
				if d.Scale != 0 {
					t.Fatalf("device %+v has geometry", d)
				}
			}
		})
	}
}

func TestDefaultLimits(t *testing.T) {
	s := newSvc(listRunner())
	if s.bootLimit != 120*time.Second || s.launchLimit != 300*time.Second || s.opLimit != 60*time.Second {
		t.Fatalf("limits = %v, %v, %v", s.bootLimit, s.launchLimit, s.opLimit)
	}
}

// A deadline set by the caller is reported as a timeout too, without claiming
// one of the service's own limits.
func TestCallerDeadlineReadsAsTimeout(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	s := New(&blockingRunner{fakeRunner: listRunner(), block: simctlVerb("list")}, func() time.Time { return testNow }, func(context.Context, proto.SessionID) bool { return true })
	_, err := s.Devices(ctx)
	if err == nil || err.Error() != "simctl list: timed out" || strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnavailable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"xcrun missing", &exec.Error{Name: "xcrun", Err: exec.ErrNotFound}, true},
		{"no simctl utility", errors.New("xcrun: exit status 72: xcrun: error: unable to find utility \"simctl\", not a developer tool or in PATH"), true},
		{"no active developer directory", errors.New("xcrun: exit status 1: xcode-select: error: tool 'xcrun' requires Xcode, but active developer directory '/Library/Developer/CommandLineTools' is a command line tools instance"), true},
		{"invalid active developer path", errors.New("xcrun: exit status 1: xcrun: error: invalid active developer path (/Library/Developer/CommandLineTools), missing xcrun"), true},
		{"other failure", errors.New("xcrun: exit status 149: An error was encountered processing the command"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unavailable(tt.err); got != tt.want {
				t.Fatalf("unavailable = %v, want %v", got, tt.want)
			}
		})
	}
}
