package claude

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/yangflow/hoplet/internal/harness/verified"
)

// Hoplet keeps, next to its copy of claude, the versions that passed the
// self-check on this machine and the outcome of the last automatic
// follow-up (package verified). Install and Follow write them.

// stateDir is where they live: the directory of the managed binary.
func stateDir(binary string) string { return filepath.Dir(binary) }

// localVerified reports whether version v passed the self-check here.
func localVerified(binary, v string) bool { return verified.Has(stateDir(binary), v) }

// Update is the outcome of the last automatic follow-up.
type Update = verified.Outcome

// lastUpdate reads the outcome of the last automatic follow-up of the
// managed binary, if any.
func lastUpdate(binary string) (Update, bool) { return verified.Last(stateDir(binary)) }

// notice is what doctor says about u while Hoplet runs version current:
// a follow-up to current, or a newer version that failed (a problem).
func notice(u Update, current string) (string, bool) {
	day := u.At.Format(time.DateOnly)
	switch {
	case u.Error != "" && u.Version != current && u.Attempts == 0:
		// Not a self-check failure (those count): Follow retries it.
		return fmt.Sprintf("Hoplet could not move to claude %s on %s: %s; it will try again", u.Version, day, u.Error), true
	case u.Error != "" && u.Version != current:
		again := "Hoplet will try again"
		if u.Attempts >= verified.MaxAttempts {
			again = "Hoplet will not try this version again"
		}
		return fmt.Sprintf("claude %s on your PATH failed the self-check on %s (attempt %d of %d), so Hoplet stays on %s: %s; %s",
			u.Version, day, u.Attempts, verified.MaxAttempts, current, u.Error, again), true
	case u.Error == "" && u.Version == current:
		return fmt.Sprintf("followed from %s on %s after a self-check", u.From, day), false
	}
	return "", false
}
