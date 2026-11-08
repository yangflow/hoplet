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
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{name, args})
	if f.err != nil {
		return nil, f.err
	}
	if f.hook != nil {
		if out, err, ok := f.hook(name, args); ok {
			return out, err
		}
	}
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
