//go:build linux

package resources

// NewReader reads /proc.
func NewReader() Reader { return newProcReader("/proc") }
