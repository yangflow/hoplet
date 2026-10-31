package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Hoplet keeps two small files next to its copy of claude: the versions
// that passed the self-check on this machine, and the outcome of the last
// automatic follow-up. Install and Follow write them.

// verifiedFile lists, next to the managed binary, the versions that passed
// the self-check on this machine.
func verifiedFile(binary string) string { return filepath.Join(filepath.Dir(binary), "verified.json") }

// localVerified reports whether version v passed the self-check here.
func localVerified(binary, v string) bool {
	vs, _ := readVerified(binary)
	return slices.Contains(vs, v)
}

func readVerified(binary string) ([]string, error) {
	b, err := fs.ReadFile(os.DirFS(filepath.Dir(binary)), filepath.Base(verifiedFile(binary)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Versions []string `json:"versions"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("claude: %s: %w", verifiedFile(binary), err)
	}
	return f.Versions, nil
}

// Update is the outcome of the last automatic follow-up, kept next to the
// managed binary for doctor.
type Update struct {
	At      time.Time `json:"at"`
	From    string    `json:"from,omitempty"`
	Version string    `json:"version,omitempty"`
	Error   string    `json:"error,omitempty"`
	// Attempts counts the consecutive follow-ups to Version that failed the
	// self-check; other failures leave it zero.
	Attempts int `json:"attempts,omitempty"`
}

func updateFile(binary string) string { return filepath.Join(filepath.Dir(binary), "update.json") }

// notice is what doctor says about u while Hoplet runs version current:
// a follow-up to current, or a newer version that failed (a problem).
func (u Update) notice(current string) (string, bool) {
	day := u.At.Format(time.DateOnly)
	switch {
	case u.Error != "" && u.Version != current && u.Attempts == 0:
		// Not a self-check failure (those count): Follow retries it.
		return fmt.Sprintf("Hoplet could not move to claude %s on %s: %s; it will try again", u.Version, day, u.Error), true
	case u.Error != "" && u.Version != current:
		again := "Hoplet will try again"
		if u.Attempts >= maxFollowAttempts {
			again = "Hoplet will not try this version again"
		}
		return fmt.Sprintf("claude %s on your PATH failed the self-check on %s (attempt %d of %d), so Hoplet stays on %s: %s; %s",
			u.Version, day, u.Attempts, maxFollowAttempts, current, u.Error, again), true
	case u.Error == "" && u.Version == current:
		return fmt.Sprintf("followed from %s on %s after a self-check", u.From, day), false
	}
	return "", false
}

// lastUpdate reads the outcome of the last automatic follow-up of the
// managed binary, if any.
func lastUpdate(bin string) (Update, bool) {
	b, err := fs.ReadFile(os.DirFS(filepath.Dir(bin)), filepath.Base(updateFile(bin)))
	if err != nil {
		return Update{}, false
	}
	var u Update
	if json.Unmarshal(b, &u) != nil {
		return Update{}, false
	}
	return u, true
}
