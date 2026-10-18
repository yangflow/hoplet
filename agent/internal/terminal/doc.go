// Package terminal runs the restricted terminal of ADR 0026: a shell on a pseudo-terminal, in the working
// directory of one session, with its own process group.
//
// The package knows nothing of the wire: a Manager is given Events and the daemon turns them into
// terminal.* messages. A terminal is credit-controlled (the PTY is read only while less than WindowBytes
// are unacknowledged), rate-limited on input, bounded in number, age and idle time, and its whole process
// tree is killed when it closes. A journal under <hoplet_home>/terminal/ lets the next start-up kill what a
// crashed agent left. No input or output is logged or stored.
package terminal

import (
	"regexp"
	"time"
)

// Why a terminal was refused or ended; the values are the reasons of the wire (shared/src/wire).
const (
	ReasonDisabled       = "disabled"
	ReasonBusy           = "terminal-busy"
	ReasonTurnRunning    = "turn-running"
	ReasonExternal       = "external"
	ReasonHandoffRunning = "handoff-running"
	ReasonOwnerOffline   = "owner-offline"
	ReasonNotADirectory  = "not-a-directory"
	ReasonIdle           = "idle"
	ReasonMaxAge         = "max-age"
	ReasonDetached       = "detached"
	ReasonTurnStarted    = "turn-started"
	ReasonHandoff        = "handoff"
	ReasonDeleted        = "deleted"
	ReasonClosed         = "closed"
	ReasonInputFlood     = "input-flood"
	ReasonExit           = "exit"
)

// Limits of ADR 0026.
const (
	ChunkBytes    = 4096      // largest input or output message, decoded
	WindowBytes   = 64 * 1024 // output sent and not yet acknowledged
	InputRate     = 64 * 1024 // input bytes per second
	MaxCols       = 500
	MaxRows       = 200
	PerMachine    = 3
	DefaultIdle   = 60 * time.Minute
	DefaultMaxAge = 8 * time.Hour
	DefaultGrace  = 3 * time.Second
	DefaultFlush  = 16 * time.Millisecond

	// drainWait bounds the wait for the reader after the shell is gone.
	drainWait = 500 * time.Millisecond

	defaultCols = 80
	defaultRows = 24
)

// idPattern is the opaque-ID rule of the wire (shared/src/ids.ts); the ID names the journal file.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// OpenError is a refusal to open a terminal; Reason is a wire reason.
type OpenError struct{ Reason string }

func (e *OpenError) Error() string { return e.Reason }

// Clock is the time the terminal's timers run on; tests replace it.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
