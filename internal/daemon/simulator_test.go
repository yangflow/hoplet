package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/simulator"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/internal/tools"
	"github.com/yangflow/hoplet/proto"
)

const (
	fakeDeviceName = "Fake Phone"
	fakeDeviceArgs = `{"device":"` + fakeDeviceName + `"}`
)

// fakeScreenshot is 1 MiB of a repeating byte pattern, the largest a
// screenshot is expected to be.
func fakeScreenshot() []byte {
	b := make([]byte, 1<<20)
	for i := range b {
		b[i] = byte(i*7 + i>>8)
	}
	return b
}

// fakeSimulator stands in for simctl in every daemon test, so none reaches
// a real simulator on a macOS host.
type fakeSimulator struct{}

func (fakeSimulator) Devices(context.Context) ([]simulator.Device, error) {
	return []simulator.Device{{Name: fakeDeviceName, UDID: "FAKE-UDID", State: "Booted", Width: 402, Height: 874}}, nil
}

func (fakeSimulator) Boot(context.Context, string) error { return nil }

func (fakeSimulator) Launch(context.Context, proto.SessionID, string, string, string) error {
	return nil
}

func (fakeSimulator) Screenshot(context.Context, proto.SessionID, string) (simulator.Shot, error) {
	return simulator.Shot{PNG: fakeScreenshot(), Width: 402, Height: 874}, nil
}

func (fakeSimulator) OpenURL(context.Context, proto.SessionID, string, string) error { return nil }

func (fakeSimulator) Tap(context.Context, proto.SessionID, string, float64, float64, time.Duration) error {
	return nil
}

func (fakeSimulator) Swipe(context.Context, proto.SessionID, string, float64, float64, float64, float64, time.Duration, string) error {
	return nil
}

func (fakeSimulator) Text(context.Context, proto.SessionID, string, string) error { return nil }

func (fakeSimulator) Button(_ context.Context, _ proto.SessionID, _, name string) error {
	if name != "home" {
		return &simulator.Error{Code: simulator.CodeUnsupported, Message: "button " + name}
	}
	return nil
}

// TestSimulatorToolsThroughBridge has a harness's MCP client call the
// simulator tools through `hoplet mcp`, as a Code session's harness does.
func TestSimulatorToolsThroughBridge(t *testing.T) {
	h, script := newHome(t, fake.Script{})
	d := start(t, testConfig(h, script))
	c := dial(t, h, "test")
	sess := create(t, c, h)
	if err := d.store.SetSessionToken(timeout(t), sess.ID, store.TokenHash("token-sim"), time.Now()); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(hopletExe, "mcp", "--home", h)
	cmd.Env = append(os.Environ(), tools.TokenEnv+"=token-sim")
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "fake-harness", Version: "1"}, nil).
		Connect(timeout(t), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	r, err := cs.CallTool(timeout(t), &mcp.CallToolParams{Name: "simulator_list", Arguments: map[string]any{}})
	if err != nil || r.IsError || len(r.Content) != 1 {
		t.Fatalf("simulator_list = %+v, %v", r, err)
	}
	if text := r.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, `"udid":"FAKE-UDID"`) {
		t.Fatalf("simulator_list text = %s", text)
	}

	r, err = cs.CallTool(timeout(t), &mcp.CallToolParams{Name: "simulator_screenshot", Arguments: map[string]any{"device": fakeDeviceName}})
	if err != nil || r.IsError || len(r.Content) != 2 {
		t.Fatalf("simulator_screenshot = %+v, %v", r, err)
	}
	var size struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := json.Unmarshal([]byte(r.Content[0].(*mcp.TextContent).Text), &size); err != nil || size.Width != 402 || size.Height != 874 {
		t.Fatalf("simulator_screenshot size = %+v, %v", size, err)
	}
	img := r.Content[1].(*mcp.ImageContent)
	if img.MIMEType != "image/png" || !bytes.Equal(img.Data, fakeScreenshot()) {
		t.Fatalf("image: %s, %d bytes", img.MIMEType, len(img.Data))
	}

	for name, args := range map[string]map[string]any{
		"simulator_tap":    {"device": fakeDeviceName, "x": 100, "y": 200, "duration": 0.8},
		"simulator_swipe":  {"x": 10, "y": 300, "x2": 10, "y2": 100},
		"simulator_text":   {"text": "hello"},
		"simulator_button": {"name": "home"},
	} {
		r, err = cs.CallTool(timeout(t), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || r.IsError || len(r.Content) != 1 {
			t.Fatalf("%s = %+v, %v", name, r, err)
		}
		if text := r.Content[0].(*mcp.TextContent).Text; text != `{"ok":true}` {
			t.Fatalf("%s text = %s", name, text)
		}
	}
	r, err = cs.CallTool(timeout(t), &mcp.CallToolParams{Name: "simulator_button", Arguments: map[string]any{"name": "lock"}})
	if err != nil || !r.IsError || r.Content[0].(*mcp.TextContent).Text != "unsupported: button lock" {
		t.Fatalf("simulator_button lock = %+v, %v", r, err)
	}
}

// unusedRunner fails the test if a command is run.
type unusedRunner struct{ t *testing.T }

func (r unusedRunner) Run(_ context.Context, name string, _ ...string) ([]byte, error) {
	r.t.Errorf("ran %s", name)
	return nil, nil
}

func TestHostSimulator(t *testing.T) {
	alive := func(context.Context, proto.SessionID) bool { return true }
	r := unusedRunner{t}
	for goos, supported := range map[string]bool{"darwin": true, "linux": false, "windows": false} {
		// A typed nil pointer in the interface would compare non-nil here.
		if got := hostSimulator(goos, r, time.Now, alive); (got != nil) != supported {
			t.Errorf("%s: simulator = %v, want supported %v", goos, got, supported)
		}
	}
}

func TestSessionAlive(t *testing.T) {
	h, script := newHome(t, fake.Script{})
	d := start(t, testConfig(h, script))
	c := dial(t, h, "test")
	live, archived := create(t, c, h), create(t, c, h)
	if err := c.Archive(timeout(t), archived.ID); err != nil {
		t.Fatal(err)
	}
	alive := sessionAlive(d.store)

	if !alive(timeout(t), live.ID) {
		t.Error("a live session is not alive")
	}
	if alive(timeout(t), archived.ID) {
		t.Error("an archived session is alive")
	}
	if alive(timeout(t), "no-such-session") {
		t.Error("a missing session is alive")
	}
	// A failed lookup says nothing about the session, so a live lease must
	// not be released on it.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !alive(ctx, archived.ID) {
		t.Error("a failed lookup reads as not alive")
	}
}
