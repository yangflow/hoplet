package simulator

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type listOutput struct {
	Devices map[string][]struct {
		Name        string `json:"name"`
		UDID        string `json:"udid"`
		State       string `json:"state"`
		IsAvailable bool   `json:"isAvailable"`
		TypeID      string `json:"deviceTypeIdentifier"`
	} `json:"devices"`
}

// list returns available devices sorted by runtime, name and UDID.
func (s *Service) list(ctx context.Context) ([]Device, error) {
	out, err := s.simctl(ctx, "list", "-j", "devices")
	if err != nil {
		return nil, err
	}
	var parsed listOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("decode simctl device list: %w", err)
	}
	var devices []Device
	typeOf := map[string]string{} // UDID -> device type identifier
	for runtime, entries := range parsed.Devices {
		if !strings.HasPrefix(runtime, iosRuntimePrefix) {
			continue
		}
		for _, e := range entries {
			if !e.IsAvailable {
				continue
			}
			devices = append(devices, Device{
				Name:    e.Name,
				UDID:    e.UDID,
				State:   e.State,
				Runtime: strings.TrimPrefix(runtime, runtimePrefix),
				Holder:  s.holderOf(e.UDID),
			})
			typeOf[e.UDID] = e.TypeID
		}
	}
	s.fillGeometry(ctx, devices, typeOf)
	sort.Slice(devices, func(i, j int) bool {
		a, b := devices[i], devices[j]
		if a.Runtime != b.Runtime {
			return a.Runtime < b.Runtime
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.UDID < b.UDID
	})
	return devices, nil
}

const iosRuntimePrefix = runtimePrefix + "iOS-"

type geometry struct {
	scale, pxW, pxH int
}

type devicetypesOutput struct {
	DeviceTypes []struct {
		Identifier string `json:"identifier"`
		BundlePath string `json:"bundlePath"`
	} `json:"devicetypes"`
}

type capabilitiesOutput struct {
	Capabilities struct {
		Screen struct {
			Scale  int `json:"main-screen-scale"`
			Width  int `json:"main-screen-width"`
			Height int `json:"main-screen-height"`
		} `json:"ScreenDimensionsCapability"`
	} `json:"capabilities"`
}

// fillGeometry sets Scale, Width and Height (device points) from each device
// type's capabilities. A device type that cannot be read keeps zeros and is
// retried on the next call; successful reads are cached per device type.
func (s *Service) fillGeometry(ctx context.Context, devices []Device, typeOf map[string]string) {
	geo := map[string]geometry{}
	var bundles map[string]string
	bundlesLoaded := false
	for i := range devices {
		id := typeOf[devices[i].UDID]
		if id == "" {
			continue
		}
		g, ok := geo[id]
		if !ok {
			s.mu.Lock()
			g, ok = s.geometry[id]
			s.mu.Unlock()
		}
		if !ok {
			if !bundlesLoaded {
				bundles = s.bundlePaths(ctx)
				bundlesLoaded = true
			}
			g, ok = s.readGeometry(ctx, bundles[id])
			if !ok {
				continue
			}
			s.mu.Lock()
			s.geometry[id] = g
			s.mu.Unlock()
		}
		geo[id] = g
		devices[i].Scale = g.scale
		devices[i].Width = g.pxW / g.scale
		devices[i].Height = g.pxH / g.scale
	}
}

func (s *Service) bundlePaths(ctx context.Context) map[string]string {
	out, err := s.simctl(ctx, "list", "-j", "devicetypes")
	if err != nil {
		return nil
	}
	var parsed devicetypesOutput
	if json.Unmarshal(out, &parsed) != nil {
		return nil
	}
	paths := make(map[string]string, len(parsed.DeviceTypes))
	for _, t := range parsed.DeviceTypes {
		paths[t.Identifier] = t.BundlePath
	}
	return paths
}

func (s *Service) readGeometry(ctx context.Context, bundle string) (geometry, bool) {
	if bundle == "" {
		return geometry{}, false
	}
	out, err := s.run.Run(ctx, "plutil", "-convert", "json", "-o", "-", filepath.Join(bundle, "Contents", "Resources", "capabilities.plist"))
	if err != nil {
		return geometry{}, false
	}
	var parsed capabilitiesOutput
	if json.Unmarshal(out, &parsed) != nil {
		return geometry{}, false
	}
	sc := parsed.Capabilities.Screen
	if sc.Scale <= 0 || sc.Width <= 0 || sc.Height <= 0 {
		return geometry{}, false
	}
	return geometry{scale: sc.Scale, pxW: sc.Width, pxH: sc.Height}, true
}
