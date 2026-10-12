// Package provider holds what the agent knows of a vendor's sessions without knowing the vendor (ADR 0012,
// ADR 0014): the Source a sync client reads, the Provider that scans for sources, and the Store a native
// vendor offers for handoff. Each vendor implements them in provider/<vendor>.
package provider

import (
	"context"
	"errors"
	"fmt"
)

// ErrBeyondEnd reports a source whose history shrank below the sync cursor (a rewritten file, a compacted
// database): the session is warned about once and skipped.
var ErrBeyondEnd = errors.New("source shrank below the sync cursor")

// MaxLineBytes includes the terminating LF; it is independent of a caller's read window.
const MaxLineBytes int64 = 32 << 20

var ErrLineTooLarge = errors.New("JSONL line exceeds supported limit")

// LineTooLargeError reports the offset of a line above MaxLineBytes. It matches ErrLineTooLarge and carries
// no session content.
type LineTooLargeError struct {
	Offset int64
}

func (e *LineTooLargeError) Error() string {
	return fmt.Sprintf("%s: offset %d, limit %d bytes including LF", ErrLineTooLarge, e.Offset, MaxLineBytes)
}

func (e *LineTooLargeError) Is(target error) bool { return target == ErrLineTooLarge }

// Source is one session's append-only byte stream. Implementations must be safe for the sequential use the
// sync client makes of them.
type Source interface {
	// ID is the stable session id used in the wire protocol.
	ID() string
	// Dir is the vendor's name for the place the session is kept, for display and exclusion.
	Dir() string
	// Cwd is the directory the session runs in; empty when the session does not say.
	Cwd() string
	// InProject reports whether the session belongs to the project directory path (cleaned): it runs there,
	// or the vendor keeps it with that directory, as it does a session handed off from another machine.
	InProject(path string) bool
	// Size returns the stream's byte length as of the last scan when known cheaply; -1 when it requires
	// reading. The sync client uses it to skip sessions with nothing new.
	Size(ctx context.Context) (int64, error)
	// Read returns the stream bytes in [offset, offset+max) cut at the last complete line. An empty result
	// means nothing new right now. A first line longer than max, up to MaxLineBytes, is returned alone.
	// ErrBeyondEnd when the source shrank below offset; ErrLineTooLarge at an unsupported
	// first line. A supported incomplete tail is deferred.
	Read(ctx context.Context, offset int64, max int64) ([]byte, error)
}

// Provider turns one vendor's storage into session sources (docs/spec.md#session-sync).
type Provider interface {
	// Name is the vendor's `provider` value on the wire (shared/src/session.ts).
	Name() string
	Scan(ctx context.Context) ([]Source, error)
	Finder
}

// Store is implemented by the provider of a native vendor (ADR 0014): one whose own session files Hoplet
// resumes in place and installs on another machine during handoff.
type Store interface {
	// SessionFile is the path of the file the vendor writes for the new session sessionID run in dir.
	SessionFile(dir, sessionID string) string
	// Install writes the staged session file into the vendor's store as the session sessionID of the
	// working directory cwd, byte for byte, and returns its path. It never replaces a file already there.
	Install(cwd, sessionID, staged string) (string, error)
	// Remove removes a file Install wrote; a path outside the store is left alone.
	Remove(path string)
}
