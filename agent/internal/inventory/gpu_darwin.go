//go:build darwin

package inventory

import (
	"context"
	"strconv"
	"strings"
)

// detectGPUs reads the display adapters from system_profiler; an Apple silicon chip gets the machine's memory,
// which it shares. A failure leaves the machine without GPUs here.
func detectGPUs(ctx context.Context, env *Env) []GPU {
	cctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, _, err := env.Run(cctx, "system_profiler", "SPDisplaysDataType", "-json")
	if err != nil {
		return []GPU{}
	}
	var mem uint64
	mctx, mcancel := context.WithTimeout(ctx, commandTimeout)
	defer mcancel()
	if raw, _, err := env.Run(mctx, "sysctl", "-n", "hw.memsize"); err == nil {
		mem, _ = strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	}
	return parseSystemProfiler(out, mem)
}
