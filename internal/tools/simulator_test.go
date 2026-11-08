package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

var (
	codeCaller = proto.Session{ID: "code0001", Mode: proto.ModeCode}
	simNames   = []string{"simulator_list", "simulator_boot", "simulator_launch", "simulator_screenshot", "simulator_open_url"}
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

func TestSimulatorErrors(t *testing.T) {
	calls := map[string]string{
		"simulator_list":       `{}`,
		"simulator_boot":       `{"device":"x"}`,
		"simulator_launch":     `{"bundle_id":"b"}`,
		"simulator_screenshot": `{}`,
		"simulator_open_url":   `{"url":"u"}`,
	}
	busy := &simulator.Error{Code: simulator.CodeBusy, Message: "held by session code0002"}
	for name, args := range calls {
		set := New(fakeSource{sim: &fakeSim{err: busy}, got: &changes{}})
		r := simCallTool(t, set, name, args)
		if !r.IsError || r.Text != "simulator_busy: held by session code0002" || r.Images != nil {
			t.Errorf("%s = %+v", name, r)
		}

		other := errors.New("disk on fire")
		set = New(fakeSource{sim: &fakeSim{err: other}, got: &changes{}})
		if _, err := set.Call(context.Background(), codeCaller, name, json.RawMessage(args)); !errors.Is(err, other) {
			t.Errorf("%s: err = %v, want the failure path of other errors", name, err)
		}
	}
}
