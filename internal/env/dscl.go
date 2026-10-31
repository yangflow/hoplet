package env

import (
	"context"
	"os/exec"
	"time"
)

// dsclShell asks the macOS directory service for the account's login shell
// with dscl. It lives alone in this file so that the lint exclusion for
// running a command with the account name covers nothing else.
func dsclShell(ctx context.Context, username string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/dscl", ".", "-read", "/Users/"+username, "UserShell").Output()
	if err != nil {
		return ""
	}
	return parseDscl(out)
}
