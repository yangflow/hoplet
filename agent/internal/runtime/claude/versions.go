package claude

import (
	"fmt"
	"strconv"
	"strings"
)

// MinVersion is the oldest Claude Code version the driver runs; an older one is refused.
const MinVersion = "2.1.289"

// TestedVersions lists the Claude Code versions whose streaming JSON format and control protocol the
// driver was verified against (ADR 0008). A newer version than the minimum runs too and is reported
// as untested.
var TestedVersions = []string{"2.1.289", "2.1.295"}

// CheckVersion accepts every version from MinVersion on and reports whether it is one of
// TestedVersions.
func CheckVersion(version string) (tested bool, err error) {
	got, ok := parseVersion(version)
	if !ok {
		return false, fmt.Errorf("unrecognized Claude Code version %q (minimum %s)", version, MinVersion)
	}
	min, _ := parseVersion(MinVersion)
	if compareVersions(got, min) < 0 {
		return false, fmt.Errorf("unsupported Claude Code version %q (minimum %s)", version, MinVersion)
	}
	for _, v := range TestedVersions {
		if v == version {
			return true, nil
		}
	}
	return false, nil
}

// parseVersion reads a dotted numeric version such as 2.1.289; a suffix after the third number is ignored.
func parseVersion(s string) ([3]int, bool) {
	var out [3]int
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		end := 0
		for end < len(p) && p[end] >= '0' && p[end] <= '9' {
			end++
		}
		n, err := strconv.Atoi(p[:end])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func compareVersions(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}
