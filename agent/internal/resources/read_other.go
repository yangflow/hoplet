//go:build !linux && !darwin

package resources

// NewReader is nil on a platform the agent does not sample: the sampler does not run and nothing is sent.
func NewReader() Reader { return nil }
