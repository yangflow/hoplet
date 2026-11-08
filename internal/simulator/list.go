package simulator

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type listOutput struct {
	Devices map[string][]struct {
		Name        string `json:"name"`
		UDID        string `json:"udid"`
		State       string `json:"state"`
		IsAvailable bool   `json:"isAvailable"`
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
	for runtime, entries := range parsed.Devices {
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
		}
	}
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
