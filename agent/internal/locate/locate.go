// Package locate finds a session on this machine through the providers' finders.
// runtime and workspace share this so cwd / path lookup is not duplicated.
package locate

import (
	"errors"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

// ErrNotFound means the session id is not on this device (or is excluded).
var ErrNotFound = errors.New("session-not-on-this-device")

// Session is a native session found by a provider Finder.
type Session struct {
	Cwd       string // project directory to run git and the vendor process in
	JSONLPath string // the vendor's session file; empty when the vendor has none
}

// Locate finds sessionID on the first Finder that reports it. Callers pass finders in
// priority order (a later vendor appends).
func Locate(sessionID string, finders ...provider.Finder) (Session, error) {
	if !provider.ValidSessionID(sessionID) {
		return Session{}, errors.New("invalid-session-id")
	}
	for _, f := range finders {
		if f == nil {
			continue
		}
		got, ok := f.Find(sessionID)
		if !ok {
			continue
		}
		return Session{Cwd: got.Cwd, JSONLPath: got.NativePath}, nil
	}
	return Session{}, ErrNotFound
}
