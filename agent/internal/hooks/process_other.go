//go:build !linux

package hooks

import (
	"os/exec"
	"strconv"
	"strings"
)

// startTime is the start time of pid as ps prints it, to the second.
func startTime(pid int) (string, bool) {
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = []string{"LC_ALL=C", "PATH=/bin:/usr/bin"}
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(out))
	return s, s != ""
}
