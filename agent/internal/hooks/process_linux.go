package hooks

import (
	"os"
	"strconv"
	"strings"
)

// startTime is the start time of pid in clock ticks since boot (field 22 of /proc/<pid>/stat).
func startTime(pid int) (string, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	s := string(raw)
	end := strings.LastIndexByte(s, ')') // the command name may hold spaces and parentheses
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(s[end+1:])
	if len(fields) < 20 {
		return "", false
	}
	return fields[19], true
}
