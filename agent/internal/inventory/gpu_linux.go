//go:build linux

package inventory

import "context"

// detectGPUs asks nvidia-smi, the one source that names the model and the memory without a graphics stack; a
// machine without it, or whose driver fails, has no GPUs here.
func detectGPUs(ctx context.Context, env *Env) []GPU {
	path, err := env.LookPath("nvidia-smi")
	if err != nil {
		return []GPU{}
	}
	cctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, _, err := env.Run(cctx, path, "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	if err != nil {
		return []GPU{}
	}
	return parseNvidiaSMI(out)
}
