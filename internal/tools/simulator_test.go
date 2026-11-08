package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/simulator"
	"github.com/yangflow/hoplet/proto"
)

// fakeSim records the calls the simulator tools make.
type fakeSim struct {
	err     error
	devices []simulator.Device
	shot    simulator.Shot
	calls   []simCall
}

type simCall struct {
	method                         string
	caller                         proto.SessionID
	device, bundleID, appPath, url string
	// input holds the arguments of an input call: Tap and Swipe take the
	// points and duration, Text the text, Button the name and Swipe the edge.
	input string
}

func (f *fakeSim) Devices(context.Context) ([]simulator.Device, error) {
	f.calls = append(f.calls, simCall{method: "devices"})
	return f.devices, f.err
}

func (f *fakeSim) Boot(_ context.Context, device string) error {
	f.calls = append(f.calls, simCall{method: "boot", device: device})
	return f.err
}

func (f *fakeSim) Launch(_ context.Context, caller proto.SessionID, device, bundleID, appPath string) error {
	f.calls = append(f.calls, simCall{method: "launch", caller: caller, device: device, bundleID: bundleID, appPath: appPath})
	return f.err
}

func (f *fakeSim) Screenshot(_ context.Context, caller proto.SessionID, device string) (simulator.Shot, error) {
	f.calls = append(f.calls, simCall{method: "screenshot", caller: caller, device: device})
	return f.shot, f.err
}

func (f *fakeSim) OpenURL(_ context.Context, caller proto.SessionID, device, url string) error {
	f.calls = append(f.calls, simCall{method: "open_url", caller: caller, device: device, url: url})
	return f.err
}

func (f *fakeSim) Tap(_ context.Context, caller proto.SessionID, device string, x, y float64, d time.Duration) error {
	f.calls = append(f.calls, simCall{method: "tap", caller: caller, device: device, input: fmt.Sprintf("%v %v %v", x, y, d)})
	return f.err
}

func (f *fakeSim) Swipe(_ context.Context, caller proto.SessionID, device string, x, y, x2, y2 float64, d time.Duration, edge string) error {
	f.calls = append(f.calls, simCall{method: "swipe", caller: caller, device: device, input: fmt.Sprintf("%v %v %v %v %v %s", x, y, x2, y2, d, edge)})
	return f.err
}

func (f *fakeSim) Text(_ context.Context, caller proto.SessionID, device, text string) error {
	f.calls = append(f.calls, simCall{method: "text", caller: caller, device: device, input: text})
	return f.err
}

func (f *fakeSim) Button(_ context.Context, caller proto.SessionID, device, name string) error {
	f.calls = append(f.calls, simCall{method: "button", caller: caller, device: device, input: name})
	return f.err
}

var (
	codeCaller = proto.Session{ID: "code0001", Mode: proto.ModeCode}
	simNames   = []string{"simulator_list", "simulator_boot", "simulator_launch", "simulator_screenshot", "simulator_tap", "simulator_swipe", "simulator_text", "simulator_button", "simulator_open_url"}
)

func simCallTool(t *testing.T, set *Set, name, args string) proto.ToolsCallResult {
	t.Helper()
	r, err := set.Call(context.Background(), codeCaller, name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return r
}

func TestSimulatorToolsVisibility(t *testing.T) {
	personal := proto.Session{ID: "pers0001", Mode: proto.ModePersonal}
	for name, tc := range map[string]struct {
		sim    Simulator
		caller proto.Session
		want   bool
	}{
		"code session on a supported host": {&fakeSim{}, codeCaller, true},
		"personal session":                 {&fakeSim{}, personal, false},
		"unsupported host":                 {nil, codeCaller, false},
	} {
		t.Run(name, func(t *testing.T) {
			set := New(fakeSource{sim: tc.sim, got: &changes{}})
			var got []string
			for _, info := range set.List(tc.caller) {
				if strings.HasPrefix(info.Name, "simulator_") {
					got = append(got, info.Name)
				}
			}
			if tc.want {
				if !slices.Equal(got, simNames) {
					t.Fatalf("tools = %v, want %v", got, simNames)
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("tools = %v, want none", got)
			}
			for _, n := range simNames {
				r, err := set.Call(context.Background(), tc.caller, n, json.RawMessage(`{"device":"x","bundle_id":"b","url":"u"}`))
				if err != nil || !r.IsError || r.Text != "no tool named "+n {
					t.Errorf("%s = %+v, %v", n, r, err)
				}
			}
			if s, ok := tc.sim.(*fakeSim); ok && len(s.calls) != 0 {
				t.Errorf("simulator called: %+v", s.calls)
			}
		})
	}
}

func TestSimulatorToolArguments(t *testing.T) {
	for name, tc := range map[string]struct {
		tool, args, want string
	}{
		"boot without device":     {"simulator_boot", `{}`, "device is required"},
		"launch without bundle":   {"simulator_launch", `{"device":"x"}`, "bundle_id is required"},
		"open_url without url":    {"simulator_open_url", `{"device":"x"}`, "url is required"},
		"list unknown":            {"simulator_list", `{"nope":1}`, "bad arguments"},
		"boot unknown":            {"simulator_boot", `{"device":"x","nope":1}`, "bad arguments"},
		"launch unknown":          {"simulator_launch", `{"bundle_id":"b","nope":1}`, "bad arguments"},
		"screenshot unknown":      {"simulator_screenshot", `{"nope":1}`, "bad arguments"},
		"open_url unknown":        {"simulator_open_url", `{"url":"u","nope":1}`, "bad arguments"},
		"tap without y":           {"simulator_tap", `{"x":1}`, "x and y are required"},
		"tap without x":           {"simulator_tap", `{"y":1,"device":"x"}`, "x and y are required"},
		"swipe without x2":        {"simulator_swipe", `{"x":1,"y":1,"y2":2}`, "x, y, x2 and y2 are required"},
		"swipe without anything":  {"simulator_swipe", `{}`, "x, y, x2 and y2 are required"},
		"text without text":       {"simulator_text", `{"device":"x"}`, "text is required"},
		"text empty":              {"simulator_text", `{"text":""}`, "text is required"},
		"button without name":     {"simulator_button", `{}`, "name is required"},
		"button blank name":       {"simulator_button", `{"name":" "}`, "name is required"},
		"tap unknown":             {"simulator_tap", `{"x":1,"y":1,"nope":1}`, "bad arguments"},
		"swipe unknown":           {"simulator_swipe", `{"x":1,"y":1,"x2":1,"y2":1,"nope":1}`, "bad arguments"},
		"text unknown":            {"simulator_text", `{"text":"a","nope":1}`, "bad arguments"},
		"button unknown":          {"simulator_button", `{"name":"home","nope":1}`, "bad arguments"},
		"tap wrong type":          {"simulator_tap", `{"x":"1","y":1}`, "bad arguments"},
		"list takes no device":    {"simulator_list", `{"device":"x"}`, "bad arguments"},
		"boot needs real device":  {"simulator_boot", `{"device":"  "}`, "device is required"},
		"launch app_path missing": {"simulator_launch", `{"bundle_id":"b","app_path":"none.app"}`, "none.app"},
	} {
		t.Run(name, func(t *testing.T) {
			sim := &fakeSim{}
			r := simCallTool(t, New(fakeSource{sim: sim, workDir: t.TempDir(), got: &changes{}}), tc.tool, tc.args)
			if !r.IsError || !strings.Contains(r.Text, tc.want) {
				t.Fatalf("result = %+v, want error with %q", r, tc.want)
			}
			if len(sim.calls) != 0 {
				t.Fatalf("simulator called: %+v", sim.calls)
			}
		})
	}
}

func TestSimulatorLaunchAppPath(t *testing.T) {
	work, outside := t.TempDir(), t.TempDir()
	for _, d := range []string{filepath.Join(work, "build", "My.app"), filepath.Join(outside, "Evil.app")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(outside, "Evil.app"), filepath.Join(work, "link.app")); err != nil {
		t.Fatal(err)
	}
	sim := &fakeSim{}
	set := New(fakeSource{sim: sim, workDir: work, got: &changes{}})

	for _, p := range []string{"../Evil.app", "link.app", filepath.Join(outside, "Evil.app")} {
		args, _ := json.Marshal(map[string]string{"bundle_id": "com.example.app", "app_path": p})
		if r := simCallTool(t, set, "simulator_launch", string(args)); !r.IsError || !strings.Contains(r.Text, "outside your working directory") {
			t.Errorf("%s: %+v", p, r)
		}
	}
	if len(sim.calls) != 0 {
		t.Fatalf("simulator called: %+v", sim.calls)
	}

	r := simCallTool(t, set, "simulator_launch", `{"device":"Phone","bundle_id":"com.example.app","app_path":"build/My.app"}`)
	want := simCall{method: "launch", caller: codeCaller.ID, device: "Phone", bundleID: "com.example.app", appPath: filepath.Join(work, "build", "My.app")}
	if r.IsError || len(sim.calls) != 1 || sim.calls[0] != want {
		t.Fatalf("result %+v, calls %+v", r, sim.calls)
	}

	// Without app_path the installed app is launched.
	sim.calls = nil
	r = simCallTool(t, set, "simulator_launch", `{"bundle_id":"com.example.app"}`)
	want = simCall{method: "launch", caller: codeCaller.ID, bundleID: "com.example.app"}
	if r.IsError || len(sim.calls) != 1 || sim.calls[0] != want {
		t.Fatalf("result %+v, calls %+v", r, sim.calls)
	}
}

func TestSimulatorToolCalls(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G'}
	sim := &fakeSim{
		devices: []simulator.Device{{Name: "Phone", UDID: "UDID-1", State: "Booted", Runtime: "iOS 26.0", Width: 402, Height: 874, Scale: 3, Holder: "code0002"}},
		shot:    simulator.Shot{PNG: png, Width: 402, Height: 874},
	}
	set := New(fakeSource{sim: sim, got: &changes{}})

	r := simCallTool(t, set, "simulator_list", "")
	if want := `[{"name":"Phone","udid":"UDID-1","state":"Booted","runtime":"iOS 26.0","width":402,"height":874,"holder":"code0002","input_available":false}]`; r.IsError || r.Text != want {
		t.Fatalf("list = %s, want %s", r.Text, want)
	}
	for _, info := range set.List(codeCaller) {
		if info.Name == "simulator_list" && !strings.Contains(info.Description, "no holder") {
			t.Fatalf("description = %q", info.Description)
		}
	}

	r = simCallTool(t, set, "simulator_screenshot", "")
	if r.IsError || r.Text != `{"width":402,"height":874}` || len(r.Images) != 1 || r.Images[0].MIME != "image/png" || string(r.Images[0].Data) != string(png) {
		t.Fatalf("screenshot = %+v", r)
	}
	if got := sim.calls[len(sim.calls)-1]; got != (simCall{method: "screenshot", caller: codeCaller.ID}) {
		t.Fatalf("screenshot call = %+v", got)
	}

	if r = simCallTool(t, set, "simulator_boot", `{"device":"Phone"}`); r.IsError || r.Text != `{"ok":true}` {
		t.Fatalf("boot = %+v", r)
	}
	if got := sim.calls[len(sim.calls)-1]; got != (simCall{method: "boot", device: "Phone"}) {
		t.Fatalf("boot call = %+v", got)
	}

	if r = simCallTool(t, set, "simulator_open_url", `{"url":"myapp://x"}`); r.IsError || r.Text != `{"ok":true}` {
		t.Fatalf("open_url = %+v", r)
	}
	if got := sim.calls[len(sim.calls)-1]; got != (simCall{method: "open_url", caller: codeCaller.ID, url: "myapp://x"}) {
		t.Fatalf("open_url call = %+v", got)
	}
}

func TestSimulatorInputToolCalls(t *testing.T) {
	for name, tc := range map[string]struct {
		tool, args string
		want       simCall
	}{
		"tap":             {"simulator_tap", `{"x":10.5,"y":20}`, simCall{method: "tap", input: "10.5 20 0s"}},
		"long press":      {"simulator_tap", `{"device":"Phone","x":0,"y":0,"duration":0.75}`, simCall{method: "tap", device: "Phone", input: "0 0 750ms"}},
		"swipe":           {"simulator_swipe", `{"x":1,"y":2,"x2":3,"y2":4}`, simCall{method: "swipe", input: "1 2 3 4 0s "}},
		"swipe from edge": {"simulator_swipe", `{"device":"Phone","x":1,"y":2,"x2":3,"y2":4,"duration":1,"edge":"bottom"}`, simCall{method: "swipe", device: "Phone", input: "1 2 3 4 1s bottom"}},
		"text":            {"simulator_text", `{"text":"héllo world"}`, simCall{method: "text", input: "héllo world"}},
		"button":          {"simulator_button", `{"device":"Phone","name":"home"}`, simCall{method: "button", device: "Phone", input: "home"}},
	} {
		t.Run(name, func(t *testing.T) {
			sim := &fakeSim{}
			r := simCallTool(t, New(fakeSource{sim: sim, got: &changes{}}), tc.tool, tc.args)
			tc.want.caller = codeCaller.ID
			if r.IsError || r.Text != `{"ok":true}` || r.Images != nil || len(sim.calls) != 1 || sim.calls[0] != tc.want {
				t.Fatalf("result %+v, calls %+v, want %+v", r, sim.calls, tc.want)
			}
		})
	}
}

func TestSimulatorInputToolDescriptions(t *testing.T) {
	want := map[string][]string{
		"simulator_tap":    {"device points", "simulator_screenshot", "no holder", "0.5 seconds", "long press"},
		"simulator_swipe":  {"device points", "no holder", "simulator_button"},
		"simulator_text":   {"no holder"},
		"simulator_button": {"no holder", "home"},
	}
	for _, info := range New(fakeSource{sim: &fakeSim{}, got: &changes{}}).List(codeCaller) {
		for _, w := range want[info.Name] {
			if !strings.Contains(info.Description, w) {
				t.Errorf("%s description %q lacks %q", info.Name, info.Description, w)
			}
		}
		delete(want, info.Name)
	}
	if len(want) != 0 {
		t.Errorf("missing tools: %v", want)
	}
}

func TestSimulatorInputErrorCodes(t *testing.T) {
	inputs := map[string]string{
		"simulator_tap":    `{"x":1,"y":1}`,
		"simulator_swipe":  `{"x":1,"y":1,"x2":2,"y2":2}`,
		"simulator_text":   `{"text":"a"}`,
		"simulator_button": `{"name":"home"}`,
	}
	for _, code := range []string{
		simulator.CodeBusy, simulator.CodeInputUnsupported, simulator.CodeUnsupported,
		simulator.CodeInvalidArgument, simulator.CodeInputFailed,
	} {
		for tool, args := range inputs {
			t.Run(code+"/"+tool, func(t *testing.T) {
				err := fmt.Errorf("simulator op: %w", &simulator.Error{Code: code, Message: "detail"})
				set := New(fakeSource{sim: &fakeSim{err: err}, got: &changes{}})
				r := simCallTool(t, set, tool, args)
				if !r.IsError || r.Text != code+": detail" || r.Images != nil {
					t.Fatalf("result = %+v, want %q", r, code+": detail")
				}
			})
		}
	}
}

func TestSimulatorErrors(t *testing.T) {
	calls := map[string]string{
		"simulator_list":       `{}`,
		"simulator_boot":       `{"device":"x"}`,
		"simulator_launch":     `{"bundle_id":"b"}`,
		"simulator_screenshot": `{}`,
		"simulator_open_url":   `{"url":"u"}`,
		"simulator_tap":        `{"x":1,"y":1}`,
		"simulator_swipe":      `{"x":1,"y":1,"x2":2,"y2":2}`,
		"simulator_text":       `{"text":"a"}`,
		"simulator_button":     `{"name":"home"}`,
	}
	busy := &simulator.Error{Code: simulator.CodeBusy, Message: "held by session code0002"}
	for name, args := range calls {
		set := New(fakeSource{sim: &fakeSim{err: busy}, got: &changes{}})
		r := simCallTool(t, set, name, args)
		if !r.IsError || r.Text != "simulator_busy: held by session code0002" || r.Images != nil {
			t.Errorf("%s = %+v", name, r)
		}

		other := errors.New("simctl launch: xcrun: exit status 1: disk on fire")
		set = New(fakeSource{sim: &fakeSim{err: other}, got: &changes{}})
		if r = simCallTool(t, set, name, args); !r.IsError || r.Text != other.Error() || r.Images != nil {
			t.Errorf("%s = %+v, want the failure's text for the model", name, r)
		}
	}
}

// scriptedRunner stands in for xcrun and plutil behind a real simulator.Service.
type scriptedRunner struct {
	verb string // the simctl verb that fails with err
	err  error
	// geometry is whether plutil reports a screen for the device type.
	geometry bool
}

func (r scriptedRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "plutil" {
		if !r.geometry {
			return []byte(`{"capabilities":{}}`), nil
		}
		return []byte(`{"capabilities":{"ScreenDimensionsCapability":{"main-screen-scale":3,"main-screen-width":1179,"main-screen-height":2556}}}`), nil
	}
	if len(args) > 1 && args[1] == r.verb {
		return nil, r.err
	}
	switch strings.Join(args, " ") {
	case "simctl list -j devices":
		return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-0":[{"name":"Phone","udid":"UDID-1","state":"Booted","isAvailable":true,"deviceTypeIdentifier":"type.phone"}]}}`), nil
	case "simctl list -j devicetypes":
		return []byte(`{"devicetypes":[{"identifier":"type.phone","bundlePath":"/types/Phone.simdevicetype"}]}`), nil
	}
	return nil, nil
}

func realSimSet(r simulator.Runner) *Set {
	sim := simulator.New(r, time.Now, func(context.Context, proto.SessionID) bool { return true })
	return New(fakeSource{sim: sim, got: &changes{}})
}

func TestSimulatorCommandFailuresReachTheModel(t *testing.T) {
	stderr := func(verb, msg string) scriptedRunner {
		return scriptedRunner{verb: verb, err: fmt.Errorf("xcrun: exit status 1: %s", msg), geometry: true}
	}
	for name, tc := range map[string]struct {
		runner     scriptedRunner
		tool, args string
		want       []string
	}{
		"bad bundle id": {
			stderr("launch", "The request to open \"com.example.nope\" failed."),
			"simulator_launch", `{"device":"UDID-1","bundle_id":"com.example.nope"}`,
			[]string{"simctl launch:", "com.example.nope"},
		},
		"bad url": {
			stderr("openurl", "The URL is invalid."),
			"simulator_open_url", `{"device":"UDID-1","url":"nope"}`,
			[]string{"simctl openurl:", "The URL is invalid."},
		},
		"toolchain missing keeps its code": {
			scriptedRunner{verb: "list", err: errors.New("xcrun: error: invalid active developer path")},
			"simulator_list", `{}`,
			[]string{"simulator_unavailable:"},
		},
		"device type without a screen": {
			scriptedRunner{},
			"simulator_screenshot", `{"device":"UDID-1"}`,
			[]string{`screen scale of device "Phone" is unavailable`, "newer device type"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := realSimSet(tc.runner).Call(context.Background(), codeCaller, tc.tool, json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("protocol error: %v", err)
			}
			if !r.IsError {
				t.Fatalf("result = %+v, want IsError", r)
			}
			for _, w := range tc.want {
				if !strings.Contains(r.Text, w) {
					t.Fatalf("text %q lacks %q", r.Text, w)
				}
			}
		})
	}
}
