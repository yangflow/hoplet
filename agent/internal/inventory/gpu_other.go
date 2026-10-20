//go:build !linux && !darwin

package inventory

import "context"

// detectGPUs finds none on a platform the agent does not inspect.
func detectGPUs(context.Context, *Env) []GPU { return []GPU{} }
