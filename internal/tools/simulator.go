package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yangflow/hoplet/internal/simulator"
	"github.com/yangflow/hoplet/proto"
)

// Simulator is what the simulator tools call. The daemon's implementation
// is simulator.Service.
type Simulator interface {
	Devices(ctx context.Context) ([]simulator.Device, error)
	Boot(ctx context.Context, device string) error
	Launch(ctx context.Context, caller proto.SessionID, device, bundleID, appPath string) error
	Screenshot(ctx context.Context, caller proto.SessionID, device string) (simulator.Shot, error)
	OpenURL(ctx context.Context, caller proto.SessionID, device, url string) error
}

// simulatorDevice is one row of simulator_list.
type simulatorDevice struct {
	Name           string          `json:"name"`
	UDID           string          `json:"udid"`
	State          string          `json:"state"`
	Runtime        string          `json:"runtime"`
	Width          int             `json:"width"`
	Height         int             `json:"height"`
	Holder         proto.SessionID `json:"holder,omitempty"`
	InputAvailable bool            `json:"input_available"`
}

// simulatorSize is the text of a simulator_screenshot result, in device
// points; the image has the same size in pixels.
type simulatorSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// simulatorDone is what a simulator tool with nothing to report returns.
type simulatorDone struct {
	OK bool `json:"ok"`
}

// optionalDevice is the schema of a device argument that may be omitted.
const optionalDevice = `"device":{"type":"string","description":"Device name or UDID; omit to use the device this session holds"}`

// simulatorFailure makes any simulator error a Failure the model reads: a
// simulator.Error keeps its code prefix, anything else keeps its own text.
func simulatorFailure(err error) error {
	var e *simulator.Error
	if errors.As(err, &e) {
		return &Failure{e.Error()}
	}
	return &Failure{err.Error()}
}

// simulatorTools are for Code sessions on a host with simulators; src's
// Simulator is nil on any other host.
func simulatorTools(src Source) []tool {
	sim := src.Simulator()
	visible := func(caller proto.Session) bool { return caller.Mode == proto.ModeCode && sim != nil }
	return []tool{
		{
			info: proto.ToolInfo{
				Name:        "simulator_list",
				Description: "List the iOS simulators on this host. Prefer a device with no holder.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			},
			visible: visible,
			call: func(ctx context.Context, _ proto.Session, args json.RawMessage) (any, error) {
				if err := decode(args, &struct{}{}); err != nil {
					return nil, err
				}
				devices, err := sim.Devices(ctx)
				if err != nil {
					return nil, simulatorFailure(err)
				}
				out := make([]simulatorDevice, 0, len(devices))
				for _, d := range devices {
					out = append(out, simulatorDevice{
						Name: d.Name, UDID: d.UDID, State: d.State, Runtime: d.Runtime,
						Width: d.Width, Height: d.Height, Holder: d.Holder, InputAvailable: d.InputAvailable,
					})
				}
				return out, nil
			},
		},
		{
			info: proto.ToolInfo{
				Name:        "simulator_boot",
				Description: "Boot an existing iOS simulator and wait until it is usable.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"device":{"type":"string","description":"Device name or UDID"}},"required":["device"],"additionalProperties":false}`),
			},
			visible: visible,
			call: func(ctx context.Context, _ proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					Device string `json:"device"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				if strings.TrimSpace(p.Device) == "" {
					return nil, failf("device is required")
				}
				if err := sim.Boot(ctx, p.Device); err != nil {
					return nil, simulatorFailure(err)
				}
				return simulatorDone{OK: true}, nil
			},
		},
		{
			info: proto.ToolInfo{
				Name:        "simulator_launch",
				Description: "Launch an app on a booted iOS simulator, installing it first when app_path is given. Holds the device for this session.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{` + optionalDevice + `,` +
					`"bundle_id":{"type":"string","description":"The app's bundle identifier"},` +
					`"app_path":{"type":"string","description":"A built .app to install, inside your working directory"}` +
					`},"required":["bundle_id"],"additionalProperties":false}`),
			},
			visible: visible,
			call: func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					Device   string `json:"device"`
					BundleID string `json:"bundle_id"`
					AppPath  string `json:"app_path"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				if strings.TrimSpace(p.BundleID) == "" {
					return nil, failf("bundle_id is required")
				}
				appPath := ""
				if p.AppPath != "" {
					dir, err := src.WorkDir(ctx, caller.ID)
					if err != nil {
						return nil, err
					}
					if appPath, err = inside(dir, p.AppPath); err != nil {
						return nil, err
					}
				}
				if err := sim.Launch(ctx, caller.ID, p.Device, p.BundleID, appPath); err != nil {
					return nil, simulatorFailure(err)
				}
				return simulatorDone{OK: true}, nil
			},
		},
		{
			info: proto.ToolInfo{
				Name:        "simulator_screenshot",
				Description: "Take a screenshot of a booted iOS simulator. The image's pixels are device points.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{` + optionalDevice + `},"additionalProperties":false}`),
			},
			visible: visible,
			call: func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					Device string `json:"device"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				shot, err := sim.Screenshot(ctx, caller.ID, p.Device)
				if err != nil {
					return nil, simulatorFailure(err)
				}
				return imageResult{
					Value: simulatorSize{Width: shot.Width, Height: shot.Height},
					Image: proto.ToolImage{MIME: "image/png", Data: shot.PNG},
				}, nil
			},
		},
		{
			info: proto.ToolInfo{
				Name:        "simulator_open_url",
				Description: "Open a URL or deep link on a booted iOS simulator.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{` + optionalDevice + `,` +
					`"url":{"type":"string","description":"The URL or deep link"}` +
					`},"required":["url"],"additionalProperties":false}`),
			},
			visible: visible,
			call: func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					Device string `json:"device"`
					URL    string `json:"url"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				if strings.TrimSpace(p.URL) == "" {
					return nil, failf("url is required")
				}
				if err := sim.OpenURL(ctx, caller.ID, p.Device, p.URL); err != nil {
					return nil, simulatorFailure(err)
				}
				return simulatorDone{OK: true}, nil
			},
		},
	}
}
