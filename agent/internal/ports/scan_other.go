//go:build !linux && !darwin

package ports

import (
	"context"
	"errors"
)

// NewScanner finds nothing on a platform the agent does not support for ports.
func NewScanner() Scanner { return unsupported{} }

type unsupported struct{}

func (unsupported) Scan(context.Context) (Snapshot, error) {
	return Snapshot{}, errors.New("port scan is not supported on this platform")
}
