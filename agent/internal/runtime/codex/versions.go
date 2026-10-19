package codex

import (
	"fmt"
	"strings"
)

// TestedVersions lists the codex-cli versions whose app-server protocol the driver was verified against
// (ADR 0027). Only these run: the protocol is partly experimental, so a version that is not listed is
// reported and not started. Each Codex upgrade needs the driver tested and this list extended.
var TestedVersions = []string{"0.163.0"}

// CheckVersion accepts exactly the versions in TestedVersions.
func CheckVersion(version string) error {
	for _, v := range TestedVersions {
		if v == version {
			return nil
		}
	}
	return fmt.Errorf("unsupported codex-cli version %q (supported: %s)", version, strings.Join(TestedVersions, ", "))
}

// parseVersionOutput reads the version out of `codex --version`, which prints `codex-cli 0.163.0`.
func parseVersionOutput(out string) string {
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}
