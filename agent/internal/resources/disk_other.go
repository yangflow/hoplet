//go:build !linux && !darwin

package resources

import "errors"

func statDisk(string) (uint64, uint64, error) {
	return 0, 0, errors.New("disk usage is not supported on this platform")
}
