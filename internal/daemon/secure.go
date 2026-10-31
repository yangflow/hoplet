package daemon

import (
	"fmt"
	"os"
)

// secureHome makes the home directory private to its owner, whatever mode
// it was created with. It lives alone in this file so that the lint
// exclusion for a directory's 0700 mode covers nothing else.
func secureHome(dir string) error {
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("daemon: secure home: %w", err)
	}
	return nil
}
