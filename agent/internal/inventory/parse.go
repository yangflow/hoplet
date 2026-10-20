package inventory

import (
	"encoding/json"
	"strconv"
	"strings"
)

// parseNvidiaSMI reads the output of
// `nvidia-smi --query-gpu=name,memory.total --format=csv,noheader,nounits`: one `name, MiB` line per GPU. A
// memory that is not a number (`[N/A]`) is left out.
func parseNvidiaSMI(out string) []GPU {
	gpus := []GPU{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, mem := line, ""
		if i := strings.LastIndex(line, ","); i >= 0 {
			name, mem = strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		}
		if name == "" {
			continue
		}
		g := GPU{Name: clip(name, nameMax)}
		if mib, err := strconv.ParseUint(mem, 10, 64); err == nil && mib <= 1<<40 {
			bytes := mib << 20
			g.Memory = &bytes
		}
		gpus = append(gpus, g)
	}
	return capGPUs(gpus)
}

// parseSystemProfiler reads the output of `system_profiler SPDisplaysDataType -json`: the model of each display
// adapter. unifiedMemory is the machine's memory, given to an Apple silicon chip, which has no memory of its own.
func parseSystemProfiler(out string, unifiedMemory uint64) []GPU {
	var doc struct {
		Displays []struct {
			Name  string `json:"_name"`
			Model string `json:"sppci_model"`
			Vram  string `json:"spdisplays_vram"`
		} `json:"SPDisplaysDataType"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return []GPU{}
	}
	gpus := []GPU{}
	for _, d := range doc.Displays {
		name := strings.TrimSpace(d.Model)
		if name == "" {
			name = strings.TrimSpace(d.Name)
		}
		if name == "" {
			continue
		}
		g := GPU{Name: clip(name, nameMax)}
		switch {
		case strings.HasPrefix(name, "Apple") && unifiedMemory > 0:
			m := unifiedMemory
			g.Memory = &m
		default:
			if m, ok := parseVRAM(d.Vram); ok {
				g.Memory = &m
			}
		}
		gpus = append(gpus, g)
	}
	return capGPUs(gpus)
}

// parseVRAM reads "8 GB", "1536 MB" or "4 GB" as bytes.
func parseVRAM(s string) (uint64, bool) {
	fields := strings.Fields(s)
	if len(fields) != 2 {
		return 0, false
	}
	n, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || n > 1<<30 {
		return 0, false
	}
	switch strings.ToUpper(fields[1]) {
	case "MB":
		return n << 20, true
	case "GB":
		return n << 30, true
	}
	return 0, false
}

func capGPUs(g []GPU) []GPU {
	if len(g) > MaxGPUs {
		return g[:MaxGPUs]
	}
	return g
}
